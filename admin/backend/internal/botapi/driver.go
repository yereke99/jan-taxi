package botapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/users"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicles"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

func errUserBlocked() *httpx.Error {
	return httpx.Forbidden().WithDetails(map[string]any{"reason": "user_blocked"})
}

func errNotRegistered() *httpx.Error {
	return httpx.NewError(http.StatusForbidden, "driver_not_registered", "driver profile required")
}

func isNotFound(err error) bool {
	var he *httpx.Error
	return errors.As(err, &he) && he.Status == http.StatusNotFound
}

// resolveDriver maps a Telegram user to their driver profile: 404 for an
// unknown user, 403 for an admin-blocked user or a user without a profile.
func (h *Handler) resolveDriver(ctx context.Context, tgID int64) (drivers.Profile, error) {
	u, err := users.GetByTelegramID(ctx, h.pool, tgID)
	if err != nil {
		return drivers.Profile{}, err
	}
	if u.IsBlocked {
		return drivers.Profile{}, errUserBlocked()
	}
	p, err := drivers.ProfileByUser(ctx, h.pool, u.ID)
	if isNotFound(err) {
		return drivers.Profile{}, errNotRegistered()
	}
	return p, err
}

// walletView is the OpenAPI Wallet.
type walletView struct {
	Balance          int64     `json:"balance"`
	Reserved         int64     `json:"reserved"`
	Available        int64     `json:"available"`
	Currency         string    `json:"currency"`
	MinOnlineBalance int64     `json:"min_online_balance"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func loadWallet(ctx context.Context, q db.Querier, driverID uuid.UUID) (*walletView, error) {
	wl, err := wallets.Get(ctx, q, driverID)
	if errors.Is(err, wallets.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &walletView{
		Balance: wl.Balance, Reserved: wl.Reserved, Available: wl.Available(), Currency: wl.Currency,
		MinOnlineBalance: settings.MinOnlineBalance(ctx, q), UpdatedAt: wl.UpdatedAt.UTC(),
	}, nil
}

// driverSummary is the OpenAPI BotDriverSummary plus display fields for the
// bot's profile reply (first_name, last_name, vehicle, rating, completed_trips).
type driverSummary struct {
	Registered      bool                     `json:"registered"`
	DriverID        *uuid.UUID               `json:"driver_id"`
	Code            *string                  `json:"code"`
	Status          *string                  `json:"status"`
	IsOnline        bool                     `json:"is_online"`
	PhoneVerified   bool                     `json:"phone_verified"`
	Language        string                   `json:"language"`
	Wallet          *walletView              `json:"wallet"`
	ActiveOrder     *orderSummary            `json:"active_order"`
	Corrections     []drivers.CorrectionItem `json:"corrections"`
	AvailableOrders int                      `json:"available_orders"`

	FirstName      string               `json:"first_name"`
	LastName       string               `json:"last_name"`
	Vehicle        *drivers.VehicleView `json:"vehicle"`
	Rating         *float64             `json:"rating"`
	CompletedTrips int                  `json:"completed_trips"`
}

// driverSummary answers the driver bot's menu: registration state, wallet,
// current trip, open corrections and how many orders are available now.
// Unknown users and users without a profile get registered=false.
func (h *Handler) driverSummary(w http.ResponseWriter, r *http.Request) error {
	tgID, err := tgIDParam(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	s := driverSummary{Language: h.opts.DefaultLanguage, Corrections: []drivers.CorrectionItem{}}
	u, err := users.GetByTelegramID(ctx, h.pool, tgID)
	if errors.Is(err, users.ErrNotFound) {
		return httpx.OK(w, s)
	}
	if err != nil {
		return err
	}
	s.Language = u.Language
	s.PhoneVerified = u.PhoneVerifiedAt != nil

	p, err := drivers.ProfileByUser(ctx, h.pool, u.ID)
	if isNotFound(err) {
		return httpx.OK(w, s)
	}
	if err != nil {
		return err
	}
	if err := h.fillDriverSummary(ctx, &s, p); err != nil {
		return err
	}
	return httpx.OK(w, s)
}

func (h *Handler) fillDriverSummary(ctx context.Context, s *driverSummary, p drivers.Profile) error {
	id, code, status := p.ID, p.Code, p.Status
	s.Registered, s.DriverID, s.Code, s.Status = true, &id, &code, &status
	s.IsOnline = p.IsOnline
	s.PhoneVerified = s.PhoneVerified || p.PhoneVerifiedAt != nil
	s.FirstName, s.LastName = p.FirstName, p.LastName
	s.Rating, s.CompletedTrips = p.Rating(), p.CompletedTrips

	v, err := vehicles.Current(ctx, h.pool, p.ID)
	switch {
	case err == nil:
		s.Vehicle = drivers.NewVehicleView(&v)
	case !isNotFound(err):
		return err
	}
	if s.Wallet, err = loadWallet(ctx, h.pool, p.ID); err != nil {
		return err
	}
	if s.ActiveOrder, err = activeDriverOrder(ctx, h.pool, p.ID); err != nil {
		return err
	}
	if status == drivers.StatusChangesRequested {
		if s.Corrections, err = latestCorrections(ctx, h.pool, p.ID); err != nil {
			return err
		}
	}
	if s.AvailableOrders, err = dispatch.CountAvailable(ctx, h.pool, p.ID); err != nil {
		return err
	}
	return nil
}

// latestCorrections returns the moderator remarks of the driver's most recent
// application round.
func latestCorrections(ctx context.Context, q db.Querier, driverID uuid.UUID) ([]drivers.CorrectionItem, error) {
	var raw []byte
	err := q.QueryRow(ctx, `
		SELECT corrections FROM driver_applications
		WHERE driver_id = $1 ORDER BY round DESC LIMIT 1`, driverID).Scan(&raw)
	if db.IsNoRows(err) {
		return []drivers.CorrectionItem{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("botapi: load corrections: %w", err)
	}
	return drivers.ParseCorrections(raw), nil
}

type availabilityRequest struct {
	Online *bool `json:"online"`
}

// driverAvailability takes the driver online or offline with the same rules
// and errors as PATCH /driver/availability and returns the OpenAPI Availability.
func (h *Handler) driverAvailability(w http.ResponseWriter, r *http.Request) error {
	tgID, err := tgIDParam(r)
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
	ctx := r.Context()
	p, err := h.resolveDriver(ctx, tgID)
	if err != nil {
		return err
	}
	if _, err := drivers.SetAvailability(ctx, h.pool, p.ID, *req.Online); err != nil {
		return err
	}
	v, err := drivers.Availability(ctx, h.pool, p.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}

// driverOrders lists the orders the driver may accept now together with the
// eligibility verdict, exactly like GET /driver/orders.
func (h *Handler) driverOrders(w http.ResponseWriter, r *http.Request) error {
	tgID, err := tgIDParam(r)
	if err != nil {
		return err
	}
	limit, err := httpx.QueryInt(r, "limit", 5, 1, 20)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p, err := h.resolveDriver(ctx, tgID)
	if err != nil {
		return err
	}
	res, err := dispatch.ListAvailable(ctx, h.pool, p.ID, limit)
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}
