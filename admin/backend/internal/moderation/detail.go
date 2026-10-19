package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// DriverDetail is the full driver record for administrators (OpenAPI
// AdminDriverDetail). Documents is nil without documents.view.
type DriverDetail struct {
	ID               uuid.UUID                        `json:"id"`
	Code             string                           `json:"code"`
	Status           string                           `json:"status"`
	FirstName        string                           `json:"first_name"`
	LastName         string                           `json:"last_name"`
	Phone            *string                          `json:"phone"`
	PhoneVerified    bool                             `json:"phone_verified"`
	PhoneVerifiedAt  *time.Time                       `json:"phone_verified_at"`
	City             *drivers.CityRef                 `json:"city"`
	IsOnline         bool                             `json:"is_online"`
	OnlineSince      *time.Time                       `json:"online_since"`
	LastHeartbeatAt  *time.Time                       `json:"last_heartbeat_at"`
	User             UserView                         `json:"user"`
	Avatar           *drivers.FileRef                 `json:"avatar"`
	Documents        *drivers.DocumentsView           `json:"documents"`
	Vehicle          *drivers.RegistrationVehicleView `json:"vehicle"`
	Corrections      []drivers.CorrectionItem         `json:"corrections"`
	Applications     []ApplicationView                `json:"applications"`
	ModerationEvents []EventView                      `json:"moderation_events"`
	Wallet           *WalletView                      `json:"wallet"`
	WelcomeBonus     *WelcomeBonusView                `json:"welcome_bonus"`
	// Bonuses is the driver's bonus history, newest first (§22.7).
	Bonuses          []BonusView           `json:"bonuses"`
	Stats            Stats                 `json:"stats"`
	LastLocation     *drivers.LastLocation `json:"last_location"`
	ActiveOrder      *drivers.OrderRef     `json:"active_order"`
	Rating           *float64              `json:"rating"`
	RatingCount      int                   `json:"rating_count"`
	CompletedTrips   int                   `json:"completed_trips"`
	SubmittedAt      *time.Time            `json:"submitted_at"`
	ApprovedAt       *time.Time            `json:"approved_at"`
	SuspendedAt      *time.Time            `json:"suspended_at"`
	SuspendedReason  *string               `json:"suspended_reason"`
	PDConsentAt      *time.Time            `json:"pd_consent_at"`
	PDConsentVersion *string               `json:"pd_consent_version"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

// UserView is the Telegram account behind a driver.
type UserView struct {
	ID                 uuid.UUID  `json:"id"`
	TelegramUserID     int64      `json:"telegram_user_id"`
	Username           *string    `json:"username"`
	IsBlocked          bool       `json:"is_blocked"`
	DriverBotStartedAt *time.Time `json:"driver_bot_started_at"`
	DriverBotBlockedAt *time.Time `json:"driver_bot_blocked_at"`
}

// ApplicationView is one submission round (OpenAPI DriverApplication).
type ApplicationView struct {
	ID              uuid.UUID                `json:"id"`
	Round           int                      `json:"round"`
	Status          string                   `json:"status"`
	SubmittedAt     time.Time                `json:"submitted_at"`
	ReviewedAt      *time.Time               `json:"reviewed_at"`
	ReviewedBy      *drivers.AdminRef        `json:"reviewed_by"`
	DecisionComment *string                  `json:"decision_comment"`
	Corrections     []drivers.CorrectionItem `json:"corrections"`
	Snapshot        json.RawMessage          `json:"snapshot"`
}

// EventView is one moderation event (OpenAPI ModerationEvent).
type EventView struct {
	ID            int64                    `json:"id"`
	ApplicationID *uuid.UUID               `json:"application_id"`
	Action        string                   `json:"action"`
	ActorType     string                   `json:"actor_type"`
	Admin         *drivers.AdminRef        `json:"admin"`
	FromStatus    *string                  `json:"from_status"`
	ToStatus      *string                  `json:"to_status"`
	Reasons       []drivers.CorrectionItem `json:"reasons"`
	Comment       *string                  `json:"comment"`
	CreatedAt     time.Time                `json:"created_at"`
}

// WalletView is the driver's wallet (OpenAPI Wallet).
type WalletView struct {
	Balance          int64     `json:"balance"`
	Reserved         int64     `json:"reserved"`
	Available        int64     `json:"available"`
	Currency         string    `json:"currency"`
	MinOnlineBalance int64     `json:"min_online_balance"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Stats are the driver's order statistics (OpenAPI DriverStats).
// TotalCommission is the commission the driver paid net of refunds (wallet
// ledger), since commissions are charged when an order is taken (§22.2).
type Stats struct {
	CompletedTrips    int   `json:"completed_trips"`
	AcceptedOrders    int   `json:"accepted_orders"`
	CancelledByDriver int   `json:"cancelled_by_driver"`
	TotalFares        int64 `json:"total_fares"`
	TotalCommission   int64 `json:"total_commission"`
}

// LoadDetail assembles the admin view of a driver. ID document references are
// included only for admins with documents.view.
func LoadDetail(ctx context.Context, q db.Querier, driverID uuid.UUID, admin auth.AdminPrincipal) (DriverDetail, error) {
	reg, err := drivers.LoadRegistration(ctx, q, driverID)
	if err != nil {
		return DriverDetail{}, err
	}
	p := reg.Profile
	phoneNum, phoneAt := reg.Phone()
	d := DriverDetail{
		ID: p.ID, Code: p.Code, Status: p.Status, FirstName: p.FirstName, LastName: p.LastName,
		Phone: phoneNum, PhoneVerified: reg.PhoneVerified(), PhoneVerifiedAt: drivers.UTCPtr(phoneAt),
		City: drivers.NewCityRef(reg.City), IsOnline: p.IsOnline, OnlineSince: drivers.UTCPtr(p.OnlineSince),
		LastHeartbeatAt: drivers.UTCPtr(p.LastHeartbeatAt),
		User: UserView{
			ID: reg.User.ID, TelegramUserID: reg.User.TelegramUserID, Username: reg.User.Username,
			IsBlocked: reg.User.IsBlocked, DriverBotStartedAt: drivers.UTCPtr(reg.User.DriverBotStartedAt),
			DriverBotBlockedAt: drivers.UTCPtr(reg.User.DriverBotBlockedAt),
		},
		Avatar:           drivers.NewFileRef(reg.Avatar, drivers.FieldAvatar, drivers.AdminFileURL),
		Applications:     []ApplicationView{},
		ModerationEvents: []EventView{},
		Rating:           p.Rating(), RatingCount: p.RatingCount, CompletedTrips: p.CompletedTrips,
		SubmittedAt: drivers.UTCPtr(p.SubmittedAt), ApprovedAt: drivers.UTCPtr(p.ApprovedAt),
		SuspendedAt: drivers.UTCPtr(p.SuspendedAt), SuspendedReason: p.SuspendedReason,
		PDConsentAt: drivers.UTCPtr(p.PDConsentAt), PDConsentVersion: p.PDConsentVersion,
		CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(),
	}
	d.Corrections, _ = reg.OpenCorrections()
	if admin.Can(auth.PermDocumentsView) {
		d.Documents = &drivers.DocumentsView{
			IDFront: drivers.NewFileRef(reg.IDFront, drivers.FieldIDFront, drivers.AdminFileURL),
			IDBack:  drivers.NewFileRef(reg.IDBack, drivers.FieldIDBack, drivers.AdminFileURL),
		}
	}
	if reg.Vehicle != nil {
		v := drivers.NewRegistrationVehicleView(reg.Vehicle, reg.Photos, drivers.AdminFileURL, true)
		d.Vehicle = &v
	}
	for _, a := range reg.Applications {
		d.Applications = append(d.Applications, ApplicationView{
			ID: a.ID, Round: a.Round, Status: a.Status, SubmittedAt: a.SubmittedAt.UTC(), ReviewedAt: drivers.UTCPtr(a.ReviewedAt),
			ReviewedBy: a.ReviewedBy, DecisionComment: a.DecisionComment, Corrections: a.Corrections, Snapshot: a.Snapshot,
		})
	}
	for _, e := range reg.Events {
		d.ModerationEvents = append(d.ModerationEvents, EventView{
			ID: e.ID, ApplicationID: e.ApplicationID, Action: e.Action, ActorType: e.ActorType, Admin: e.Admin,
			FromStatus: e.FromStatus, ToStatus: e.ToStatus, Reasons: e.Reasons, Comment: e.Comment, CreatedAt: e.CreatedAt.UTC(),
		})
	}
	w, err := wallets.Get(ctx, q, p.ID)
	switch {
	case err == nil:
		d.Wallet = &WalletView{
			Balance: w.Balance, Reserved: w.Reserved, Available: w.Available(), Currency: w.Currency,
			MinOnlineBalance: settings.MinOnlineBalance(ctx, q), UpdatedAt: w.UpdatedAt.UTC(),
		}
	case !errors.Is(err, wallets.ErrNotFound):
		return DriverDetail{}, err
	}
	if d.Stats, err = loadStats(ctx, q, p.ID); err != nil {
		return DriverDetail{}, err
	}
	if d.WelcomeBonus, err = loadWelcomeBonus(ctx, q, p.ID); err != nil {
		return DriverDetail{}, err
	}
	if d.Bonuses, err = loadBonuses(ctx, q, p.ID); err != nil {
		return DriverDetail{}, err
	}
	if d.LastLocation, err = drivers.GetLastLocation(ctx, q, p.ID); err != nil {
		return DriverDetail{}, err
	}
	order, active, err := drivers.FindActiveOrder(ctx, q, p.ID)
	if err != nil {
		return DriverDetail{}, err
	}
	if active {
		d.ActiveOrder = &drivers.OrderRef{ID: order.ID, Number: order.Number, Status: order.Status}
	}
	return d, nil
}

func loadStats(ctx context.Context, q db.Querier, driverID uuid.UUID) (Stats, error) {
	var s Stats
	err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'completed'),
		       count(*) FILTER (WHERE accepted_at IS NOT NULL),
		       count(*) FILTER (WHERE status = 'cancelled' AND cancelled_by = 'driver'),
		       COALESCE(sum(fare) FILTER (WHERE status = 'completed'), 0),
		       (SELECT COALESCE(-sum(l.balance_delta), 0)::bigint FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id
		         WHERE w.driver_id = $1 AND l.entry_type IN ('commission_charge', 'commission_refund'))
		FROM orders WHERE driver_id = $1`, driverID).
		Scan(&s.CompletedTrips, &s.AcceptedOrders, &s.CancelledByDriver, &s.TotalFares, &s.TotalCommission)
	if err != nil {
		return Stats{}, fmt.Errorf("moderation: stats: %w", err)
	}
	return s, nil
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	d, err := LoadDetail(r.Context(), h.pool, id, admin)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}
