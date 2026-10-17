package drivers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// Reasons a driver cannot go online (Availability.blocked_reason and the
// details.reason of the matching errors).
const (
	BlockedNotApproved         = "not_approved"
	BlockedSuspended           = "suspended"
	BlockedInsufficientBalance = "insufficient_balance"
	BlockedLocationRequired    = "location_required"
)

// AvailabilityResult is the driver's availability after SetAvailability.
type AvailabilityResult struct {
	Online      bool
	OnlineSince *time.Time
}

// eligibility is what decides whether a driver may go online.
type eligibility struct {
	status           string
	userID           uuid.UUID
	userBlocked      bool
	available        int64
	minOnlineBalance int64
	locationMaxAge   time.Duration
	// locationFresh: the last fix is younger than locationMaxAge by the
	// database clock (the same clock dispatch uses).
	locationFresh bool
}

func loadEligibility(ctx context.Context, q db.Querier, driverID uuid.UUID, lock bool) (eligibility, Profile, error) {
	var (
		p   Profile
		err error
	)
	if lock {
		p, err = LockProfile(ctx, q, driverID)
	} else {
		p, err = GetProfile(ctx, q, driverID)
	}
	if err != nil {
		return eligibility{}, Profile{}, err
	}
	e := eligibility{
		status: p.Status, userID: p.UserID, minOnlineBalance: settings.MinOnlineBalance(ctx, q),
		locationMaxAge: settings.Dispatch(ctx, q).LocationMaxAge(),
	}
	err = q.QueryRow(ctx, `
		SELECT u.is_blocked,
		       COALESCE((SELECT l.recorded_at > now() - $3::float8 * interval '1 second'
		                 FROM driver_locations l WHERE l.driver_id = $2), false)
		FROM users u
		WHERE u.id = $1`, p.UserID, p.ID, e.locationMaxAge.Seconds()).Scan(&e.userBlocked, &e.locationFresh)
	if err != nil {
		return eligibility{}, Profile{}, fmt.Errorf("drivers: eligibility: %w", err)
	}
	w, err := wallets.Get(ctx, q, p.ID)
	switch {
	case err == nil:
		e.available = w.Available()
	case !errors.Is(err, wallets.ErrNotFound):
		return eligibility{}, Profile{}, err
	}
	return e, p, nil
}

// blockedReason is "" when the driver may go online now. Dispatch is by
// distance from the driver's location to the pickup (ARCHITECTURE §7), so a
// driver without a fresh location fix cannot go online.
func (e eligibility) blockedReason() string {
	switch {
	case e.status == StatusSuspended:
		return BlockedSuspended
	case e.status != StatusApproved:
		return BlockedNotApproved
	case e.available < e.minOnlineBalance:
		return BlockedInsufficientBalance
	case !e.locationFresh:
		return BlockedLocationRequired
	}
	return ""
}

// error converts a blocked reason into the API error.
func (e eligibility) error(ctx context.Context, q db.Querier, reason string) error {
	switch reason {
	case BlockedSuspended, BlockedNotApproved:
		return httpx.NewError(http.StatusForbidden, "driver_not_approved", "driver is not approved").
			WithDetails(map[string]any{"reason": reason, "status": e.status})
	case BlockedInsufficientBalance:
		return httpx.NewError(http.StatusPaymentRequired, "insufficient_balance", "insufficient balance to go online").
			WithDetails(map[string]any{
				"required": e.minOnlineBalance, "available": e.available, "currency": "KZT",
				"topup_min_amount": settings.TopUp(ctx, q).Min,
			})
	case BlockedLocationRequired:
		return httpx.Unprocessable("location_required", "a fresh location is required to go online").
			WithDetails(map[string]any{"max_age_s": int(e.locationMaxAge / time.Second)})
	}
	return nil
}

// SetAvailability switches a driver online or offline. Going online requires
// status approved (403 driver_not_approved, details.reason "suspended" for a
// suspended driver), an available balance of at least
// settings driver.min_online_balance (402 insufficient_balance) and a location
// fix younger than settings dispatch.location_max_age_s (422
// location_required, details.max_age_s), sent by the Mini App or as a
// Telegram live location in the driver bot. Going offline is always allowed
// and does not affect an active order. Status changes publish
// driver.status_changed to the driver and the admin panel.
func SetAvailability(ctx context.Context, pool *pgxpool.Pool, driverID uuid.UUID, online bool) (AvailabilityResult, error) {
	var res AvailabilityResult
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		e, p, err := loadEligibility(ctx, tx, driverID, true)
		if err != nil {
			return err
		}
		if online {
			if e.userBlocked {
				return auth.ErrUserBlocked
			}
			if reason := e.blockedReason(); reason != "" {
				return e.error(ctx, tx, reason)
			}
		}
		if p.IsOnline == online {
			res = AvailabilityResult{Online: p.IsOnline, OnlineSince: utcPtr(p.OnlineSince)}
			if online {
				_, err := tx.Exec(ctx, `UPDATE driver_profiles SET last_heartbeat_at = now() WHERE id = $1`, driverID)
				return err
			}
			return nil
		}
		var since *time.Time
		if err := tx.QueryRow(ctx, `
			UPDATE driver_profiles
			SET is_online = $2,
			    online_since = CASE WHEN $2 THEN now() END,
			    last_heartbeat_at = CASE WHEN $2 THEN now() ELSE last_heartbeat_at END
			WHERE id = $1
			RETURNING online_since`, driverID, online).Scan(&since); err != nil {
			return fmt.Errorf("drivers: set availability: %w", err)
		}
		res = AvailabilityResult{Online: online, OnlineSince: utcPtr(since)}
		return PublishStatus(ctx, tx, driverID, p.UserID, p.Status, online)
	})
	return res, err
}

// PublishStatus publishes driver.status_changed to the driver, their user
// scope and the admin panel.
func PublishStatus(ctx context.Context, q db.Querier, driverID, userID uuid.UUID, status string, online bool) error {
	return realtime.Publish(ctx, q, realtime.Event{
		Type:   realtime.DriverStatusChanged,
		Scopes: []string{realtime.DriverScope(driverID), realtime.UserScope(userID), realtime.AdminScope},
		Data:   map[string]any{"driver_id": driverID, "status": status, "is_online": online},
	})
}

// AvailabilityView is the response of PATCH /driver/availability (OpenAPI Availability).
type AvailabilityView struct {
	IsOnline         bool       `json:"is_online"`
	OnlineSince      *time.Time `json:"online_since"`
	CanGoOnline      bool       `json:"can_go_online"`
	BlockedReason    *string    `json:"blocked_reason"`
	Available        int64      `json:"available"`
	MinOnlineBalance int64      `json:"min_online_balance"`
	Currency         string     `json:"currency"`
}

// Availability reports whether the driver is online and whether they could go online.
func Availability(ctx context.Context, q db.Querier, driverID uuid.UUID) (AvailabilityView, error) {
	e, p, err := loadEligibility(ctx, q, driverID, false)
	if err != nil {
		return AvailabilityView{}, err
	}
	v := AvailabilityView{
		IsOnline: p.IsOnline, OnlineSince: utcPtr(p.OnlineSince), Available: e.available,
		MinOnlineBalance: e.minOnlineBalance, Currency: "KZT",
	}
	if reason := e.blockedReason(); reason != "" {
		v.BlockedReason = &reason
	} else {
		v.CanGoOnline = true
	}
	return v, nil
}

type availabilityRequest struct {
	Online *bool `json:"online"`
}

func (h *Handler) patchAvailability(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	var req availabilityRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.Online == nil {
		return httpx.Validation(map[string]string{"online": "required"})
	}
	if _, err := SetAvailability(r.Context(), h.pool, d.ID, *req.Online); err != nil {
		return err
	}
	v, err := Availability(r.Context(), h.pool, d.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}
