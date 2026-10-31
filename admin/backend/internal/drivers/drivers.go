// Package drivers owns driver profiles: registration drafts and uploads, the
// driver's own profile, availability (online/offline), location reports, private
// file access for drivers, passengers and admins, and the driver housekeeping
// jobs. Moderation decisions live in package moderation.
package drivers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/iin"
)

// Driver statuses (driver_profiles.status).
const (
	StatusDraft            = "draft"
	StatusPending          = "pending"
	StatusApproved         = "approved"
	StatusChangesRequested = "changes_requested"
	StatusRejected         = "rejected"
	StatusSuspended        = "suspended"
)

// Statuses lists every driver status.
var Statuses = []string{StatusDraft, StatusPending, StatusApproved, StatusChangesRequested, StatusRejected, StatusSuspended}

// ValidStatus reports whether s is a driver status.
func ValidStatus(s string) bool {
	for _, x := range Statuses {
		if x == s {
			return true
		}
	}
	return false
}

// Moderation actions (driver_moderation_events.action).
const (
	ActionSubmitted        = "submitted"
	ActionResubmitted      = "resubmitted"
	ActionApproved         = "approved"
	ActionChangesRequested = "changes_requested"
	ActionRejected         = "rejected"
	ActionSuspended        = "suspended"
	ActionRestored         = "restored"
	ActionCityChanged      = "city_changed"
)

// ActiveOrderStatuses are the order statuses in which a driver is busy.
var ActiveOrderStatuses = []string{"accepted", "driver_arriving", "in_progress"}

// Field limits (code points).
const (
	MaxNameLen = 60
	// MaxPatronymicLen bounds the optional patronymic (Әкесінің аты).
	MaxPatronymicLen = 60
)

// Errors shared by driver endpoints.
var (
	ErrNotFound   = httpx.NotFound()
	ErrPhoneTaken = httpx.NewError(http.StatusConflict, "phone_taken", "phone is registered to another driver")
	ErrIINTaken   = httpx.NewError(http.StatusConflict, "iin_taken", "IIN is registered to another driver")
)

// Partial unique indexes that keep one phone and one IIN per live driver
// (pending, approved, changes_requested, suspended).
const (
	PhoneUniqueIndex = "driver_profiles_phone_unique"
	IINUniqueIndex   = "driver_profiles_iin_unique"
)

// liveStatuses hold a phone, an IIN and a plate; drafts and rejected profiles do not.
var liveStatuses = []string{StatusPending, StatusApproved, StatusChangesRequested, StatusSuspended}

// Profile is a driver_profiles row. IIN is the 12-digit ЖСН (nil until
// entered); outside the driver's own registration it is only shown masked.
type Profile struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	Code             string
	CityID           uuid.UUID
	FirstName        string
	LastName         string
	PhoneE164        *string
	PhoneVerifiedAt  *time.Time
	IIN              *string
	Patronymic       string
	AvatarFileID     *uuid.UUID
	Status           string
	IsOnline         bool
	OnlineSince      *time.Time
	LastHeartbeatAt  *time.Time
	SubmittedAt      *time.Time
	ApprovedAt       *time.Time
	SuspendedAt      *time.Time
	SuspendedReason  *string
	RatingSum        int
	RatingCount      int
	CompletedTrips   int
	PDConsentAt      *time.Time
	PDConsentVersion *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Rating is the average rating, or nil before the first rating.
func (p Profile) Rating() *float64 {
	return Rating(p.RatingSum, p.RatingCount)
}

// Rating returns sum/count rounded to two decimals, or nil when count is 0.
func Rating(sum, count int) *float64 {
	if count <= 0 {
		return nil
	}
	v := float64(int(float64(sum)*100/float64(count)+0.5)) / 100
	return &v
}

// PhoneVerified reports whether the profile holds a Telegram-verified phone.
func (p Profile) PhoneVerified() bool { return p.PhoneE164 != nil && p.PhoneVerifiedAt != nil }

// IINValue returns the stored IIN, or "" before the driver entered one.
func (p Profile) IINValue() string {
	if p.IIN == nil {
		return ""
	}
	return *p.IIN
}

// IINMasked returns the IIN for administrators, e.g. "********1234", or nil.
func (p Profile) IINMasked() *string {
	if p.IIN == nil || *p.IIN == "" {
		return nil
	}
	m := iin.Mask(*p.IIN)
	return &m
}

// Editable reports whether the driver may change registration data.
func (p Profile) Editable() bool {
	return p.Status == StatusDraft || p.Status == StatusChangesRequested
}

const profileColumns = `id, user_id, code, city_id, first_name, last_name, phone_e164, phone_verified_at, iin, patronymic,
	avatar_file_id, status, is_online, online_since, last_heartbeat_at, submitted_at, approved_at, suspended_at, suspended_reason,
	rating_sum, rating_count, completed_trips, pd_consent_at, pd_consent_version, created_at, updated_at`

func scanProfile(row interface{ Scan(...any) error }) (Profile, error) {
	var p Profile
	err := row.Scan(&p.ID, &p.UserID, &p.Code, &p.CityID, &p.FirstName, &p.LastName, &p.PhoneE164, &p.PhoneVerifiedAt,
		&p.IIN, &p.Patronymic, &p.AvatarFileID, &p.Status, &p.IsOnline, &p.OnlineSince, &p.LastHeartbeatAt, &p.SubmittedAt, &p.ApprovedAt,
		&p.SuspendedAt, &p.SuspendedReason, &p.RatingSum, &p.RatingCount, &p.CompletedTrips, &p.PDConsentAt,
		&p.PDConsentVersion, &p.CreatedAt, &p.UpdatedAt)
	if db.IsNoRows(err) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("drivers: scan profile: %w", err)
	}
	return p, nil
}

// GetProfile loads a driver profile by id, or ErrNotFound.
func GetProfile(ctx context.Context, q db.Querier, driverID uuid.UUID) (Profile, error) {
	return scanProfile(q.QueryRow(ctx, `SELECT `+profileColumns+` FROM driver_profiles WHERE id = $1`, driverID))
}

// LockProfile loads a driver profile FOR UPDATE (call inside a transaction).
func LockProfile(ctx context.Context, q db.Querier, driverID uuid.UUID) (Profile, error) {
	return scanProfile(q.QueryRow(ctx, `SELECT `+profileColumns+` FROM driver_profiles WHERE id = $1 FOR UPDATE`, driverID))
}

// ProfileByUser loads the driver profile owned by userID, or ErrNotFound.
func ProfileByUser(ctx context.Context, q db.Querier, userID uuid.UUID) (Profile, error) {
	return scanProfile(q.QueryRow(ctx, `SELECT `+profileColumns+` FROM driver_profiles WHERE user_id = $1`, userID))
}

// IDForUser returns the id of the driver profile owned by userID (404 if none).
func IDForUser(ctx context.Context, q db.Querier, userID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `SELECT id FROM driver_profiles WHERE user_id = $1`, userID).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("drivers: id for user: %w", err)
	}
	return id, nil
}

// ActiveOrder is the driver's current order, if any.
type ActiveOrder struct {
	ID          uuid.UUID
	Number      int64
	Status      string
	PassengerID uuid.UUID
}

// FindActiveOrder returns the driver's active order (accepted, driver_arriving
// or in_progress); found is false when the driver is free.
func FindActiveOrder(ctx context.Context, q db.Querier, driverID uuid.UUID) (o ActiveOrder, found bool, err error) {
	err = q.QueryRow(ctx, `
		SELECT id, number, status, passenger_id FROM orders
		WHERE driver_id = $1 AND status = ANY($2)
		ORDER BY accepted_at DESC NULLS LAST LIMIT 1`, driverID, ActiveOrderStatuses).
		Scan(&o.ID, &o.Number, &o.Status, &o.PassengerID)
	if db.IsNoRows(err) {
		return ActiveOrder{}, false, nil
	}
	if err != nil {
		return ActiveOrder{}, false, fmt.Errorf("drivers: active order: %w", err)
	}
	return o, true, nil
}
