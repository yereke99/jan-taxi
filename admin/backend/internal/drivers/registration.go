package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/iin"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicles"
)

// FileMeta is the image metadata shown with a file reference.
type FileMeta struct {
	ID        uuid.UUID
	Width     int
	Height    int
	CreatedAt time.Time
}

// UserInfo is the Telegram account behind a driver.
type UserInfo struct {
	ID                 uuid.UUID
	TelegramUserID     int64
	Username           *string
	IsBlocked          bool
	DriverBotStartedAt *time.Time
	DriverBotBlockedAt *time.Time
	PhoneE164          *string
	PhoneVerifiedAt    *time.Time
}

// AdminRef identifies the administrator behind a decision.
type AdminRef struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	FullName string    `json:"full_name"`
}

// Application is one submission round.
type Application struct {
	ID              uuid.UUID
	Round           int
	Status          string
	Snapshot        json.RawMessage
	Corrections     []CorrectionItem
	DecisionComment *string
	SubmittedAt     time.Time
	ReviewedAt      *time.Time
	ReviewedBy      *AdminRef
}

// Event is a driver_moderation_events row.
type Event struct {
	ID            int64
	ApplicationID *uuid.UUID
	Action        string
	ActorType     string
	Admin         *AdminRef
	FromStatus    *string
	ToStatus      *string
	Reasons       []CorrectionItem
	Comment       *string
	CreatedAt     time.Time
}

// Registration is everything known about a driver's application. Profile is
// nil before the first save. IDFront, IDBack and Photos are only filled for
// drivers who uploaded them before ID scans and vehicle photos were retired.
type Registration struct {
	User         UserInfo
	Profile      *Profile
	City         *cities.City
	Avatar       *FileMeta
	IDFront      *FileMeta
	IDBack       *FileMeta
	Vehicle      *vehicles.Vehicle
	Photos       []vehicles.Photo
	Applications []Application // newest first
	Events       []Event       // newest first
}

// LoadRegistrationByUser loads the registration of the user's driver profile,
// or an empty draft registration when the user has none yet.
func LoadRegistrationByUser(ctx context.Context, q db.Querier, userID uuid.UUID) (Registration, error) {
	user, err := loadUser(ctx, q, userID)
	if err != nil {
		return Registration{}, err
	}
	p, err := ProfileByUser(ctx, q, userID)
	if errors.Is(err, ErrNotFound) {
		return Registration{User: user}, nil
	}
	if err != nil {
		return Registration{}, err
	}
	return loadRest(ctx, q, user, p)
}

// LoadRegistration loads a driver's registration by driver id (404 if unknown).
func LoadRegistration(ctx context.Context, q db.Querier, driverID uuid.UUID) (Registration, error) {
	p, err := GetProfile(ctx, q, driverID)
	if err != nil {
		return Registration{}, err
	}
	user, err := loadUser(ctx, q, p.UserID)
	if err != nil {
		return Registration{}, err
	}
	return loadRest(ctx, q, user, p)
}

func loadRest(ctx context.Context, q db.Querier, user UserInfo, p Profile) (Registration, error) {
	reg := Registration{User: user, Profile: &p}
	city, err := cities.Get(ctx, q, p.CityID)
	if err != nil {
		return Registration{}, fmt.Errorf("drivers: city: %w", err)
	}
	reg.City = &city
	if p.AvatarFileID != nil {
		if reg.Avatar, err = fileMeta(ctx, q, *p.AvatarFileID); err != nil {
			return Registration{}, err
		}
	}
	if reg.IDFront, reg.IDBack, err = loadDocuments(ctx, q, p.ID); err != nil {
		return Registration{}, err
	}
	v, err := vehicles.Current(ctx, q, p.ID)
	switch {
	case err == nil:
		reg.Vehicle = &v
		if reg.Photos, err = vehicles.Photos(ctx, q, v.ID); err != nil {
			return Registration{}, err
		}
	case !errors.Is(err, vehicles.ErrNotFound):
		return Registration{}, err
	}
	if reg.Applications, err = LoadApplications(ctx, q, p.ID); err != nil {
		return Registration{}, err
	}
	if reg.Events, err = LoadEvents(ctx, q, p.ID); err != nil {
		return Registration{}, err
	}
	return reg, nil
}

func loadUser(ctx context.Context, q db.Querier, userID uuid.UUID) (UserInfo, error) {
	var u UserInfo
	err := q.QueryRow(ctx, `
		SELECT id, telegram_user_id, username, is_blocked, driver_bot_started_at, driver_bot_blocked_at,
		       phone_e164, phone_verified_at
		FROM users WHERE id = $1`, userID).
		Scan(&u.ID, &u.TelegramUserID, &u.Username, &u.IsBlocked, &u.DriverBotStartedAt, &u.DriverBotBlockedAt,
			&u.PhoneE164, &u.PhoneVerifiedAt)
	if db.IsNoRows(err) {
		return UserInfo{}, ErrNotFound
	}
	if err != nil {
		return UserInfo{}, fmt.Errorf("drivers: load user: %w", err)
	}
	return u, nil
}

func fileMeta(ctx context.Context, q db.Querier, id uuid.UUID) (*FileMeta, error) {
	m := FileMeta{ID: id}
	err := q.QueryRow(ctx, `SELECT width, height, created_at FROM files WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&m.Width, &m.Height, &m.CreatedAt)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("drivers: file meta: %w", err)
	}
	return &m, nil
}

func loadDocuments(ctx context.Context, q db.Querier, driverID uuid.UUID) (front, back *FileMeta, err error) {
	rows, err := q.Query(ctx, `
		SELECT dd.kind, f.id, f.width, f.height, f.created_at
		FROM driver_documents dd JOIN files f ON f.id = dd.file_id AND f.deleted_at IS NULL
		WHERE dd.driver_id = $1 AND dd.is_current`, driverID)
	if err != nil {
		return nil, nil, fmt.Errorf("drivers: documents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var m FileMeta
		if err := rows.Scan(&kind, &m.ID, &m.Width, &m.Height, &m.CreatedAt); err != nil {
			return nil, nil, fmt.Errorf("drivers: documents: %w", err)
		}
		switch kind {
		case FieldIDFront:
			front = &m
		case FieldIDBack:
			back = &m
		}
	}
	return front, back, rows.Err()
}

// LoadApplications returns the driver's submission rounds, newest first.
func LoadApplications(ctx context.Context, q db.Querier, driverID uuid.UUID) ([]Application, error) {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.round, a.status, a.snapshot, a.corrections, a.decision_comment, a.submitted_at, a.reviewed_at,
		       au.id, au.email, au.full_name
		FROM driver_applications a LEFT JOIN admin_users au ON au.id = a.reviewed_by
		WHERE a.driver_id = $1
		ORDER BY a.round DESC`, driverID)
	if err != nil {
		return nil, fmt.Errorf("drivers: applications: %w", err)
	}
	defer rows.Close()
	out := []Application{}
	for rows.Next() {
		var (
			a           Application
			corrections []byte
			adminID     *uuid.UUID
			email, name *string
		)
		if err := rows.Scan(&a.ID, &a.Round, &a.Status, &a.Snapshot, &corrections, &a.DecisionComment, &a.SubmittedAt,
			&a.ReviewedAt, &adminID, &email, &name); err != nil {
			return nil, fmt.Errorf("drivers: applications: %w", err)
		}
		a.Corrections = ParseCorrections(corrections)
		a.ReviewedBy = adminRef(adminID, email, name)
		out = append(out, a)
	}
	return out, rows.Err()
}

// LoadEvents returns the driver's moderation history, newest first.
func LoadEvents(ctx context.Context, q db.Querier, driverID uuid.UUID) ([]Event, error) {
	rows, err := q.Query(ctx, `
		SELECT e.id, e.application_id, e.action, e.actor_type, au.id, au.email, au.full_name, e.from_status, e.to_status,
		       e.reasons, e.comment, e.created_at
		FROM driver_moderation_events e LEFT JOIN admin_users au ON au.id = e.admin_id
		WHERE e.driver_id = $1
		ORDER BY e.created_at DESC, e.id DESC`, driverID)
	if err != nil {
		return nil, fmt.Errorf("drivers: events: %w", err)
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var (
			e           Event
			reasons     []byte
			adminID     *uuid.UUID
			email, name *string
		)
		if err := rows.Scan(&e.ID, &e.ApplicationID, &e.Action, &e.ActorType, &adminID, &email, &name, &e.FromStatus,
			&e.ToStatus, &reasons, &e.Comment, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("drivers: events: %w", err)
		}
		e.Reasons = ParseCorrections(reasons)
		e.Admin = adminRef(adminID, email, name)
		out = append(out, e)
	}
	return out, rows.Err()
}

func adminRef(id *uuid.UUID, email, name *string) *AdminRef {
	if id == nil {
		return nil
	}
	r := AdminRef{ID: *id}
	if email != nil {
		r.Email = *email
	}
	if name != nil {
		r.FullName = *name
	}
	return &r
}

// Status is the registration status ("draft" before the profile exists).
func (r Registration) Status() string {
	if r.Profile == nil {
		return StatusDraft
	}
	return r.Profile.Status
}

// Editable reports whether the driver may change registration data now.
func (r Registration) Editable() bool { return r.Profile == nil || r.Profile.Editable() }

// Phone returns the phone shown on the registration and when it was verified:
// the profile's copy once it has one, otherwise the user's verified phone.
func (r Registration) Phone() (*string, *time.Time) {
	if r.Profile != nil && r.Profile.PhoneE164 != nil {
		return r.Profile.PhoneE164, r.Profile.PhoneVerifiedAt
	}
	if r.User.PhoneE164 != nil && r.User.PhoneVerifiedAt != nil {
		return r.User.PhoneE164, r.User.PhoneVerifiedAt
	}
	return nil, nil
}

// PhoneVerified reports whether a Telegram-verified phone is on file.
func (r Registration) PhoneVerified() bool {
	p, at := r.Phone()
	return p != nil && at != nil
}

// LatestApplication returns the newest submission round, if any.
func (r Registration) LatestApplication() *Application {
	if len(r.Applications) == 0 {
		return nil
	}
	return &r.Applications[0]
}

// OpenCorrections are the moderator's remarks while the status is
// changes_requested; empty otherwise.
func (r Registration) OpenCorrections() ([]CorrectionItem, *string) {
	a := r.LatestApplication()
	if r.Status() != StatusChangesRequested || a == nil || a.Status != StatusChangesRequested {
		return []CorrectionItem{}, nil
	}
	return a.Corrections, a.DecisionComment
}

// RejectionReason is the reason shown to a rejected driver (the comment of
// the latest rejection event).
func (r Registration) RejectionReason() *string {
	if r.Status() != StatusRejected {
		return nil
	}
	for _, e := range r.Events {
		if e.Action == ActionRejected {
			return e.Comment
		}
	}
	return nil
}

// EditableFields returns the registration fields the driver may change now:
// all of them in draft, only the flagged ones in changes_requested (plus the
// IIN while none is stored, so a driver whose application predates the IIN is
// never stuck), none otherwise.
func (r Registration) EditableFields() map[string]bool {
	out := map[string]bool{}
	switch r.Status() {
	case StatusDraft:
		for _, f := range RegistrationFields {
			out[f] = true
		}
	case StatusChangesRequested:
		items, _ := r.OpenCorrections()
		for _, f := range CorrectionFields(items) {
			out[f] = true
		}
		if r.IIN() == "" {
			out[FieldIdentity] = true
		}
	}
	return out
}

// IIN returns the stored IIN, or "" when there is none (or no profile yet).
func (r Registration) IIN() string {
	if r.Profile == nil {
		return ""
	}
	return r.Profile.IINValue()
}

// MissingFields lists what is still needed before submission, in form order:
// profile photo, names, a valid IIN, the phone (only when a Telegram-verified
// phone is required), vehicle class, make and model, colour and plate. ID
// scans and vehicle photos are no longer asked for.
func (r Registration) MissingFields(requirePhone bool) []string {
	missing := map[string]bool{}
	if r.Avatar == nil {
		missing[FieldAvatar] = true
	}
	if r.Profile == nil || r.Profile.FirstName == "" {
		missing[FieldFirstName] = true
	}
	if r.Profile == nil || r.Profile.LastName == "" {
		missing[FieldLastName] = true
	}
	if !iin.Valid(r.IIN()) {
		missing[FieldIdentity] = true
	}
	if requirePhone && !r.PhoneVerified() {
		missing[FieldPhone] = true
	}
	v := r.Vehicle
	if v == nil || !v.HasClass() {
		missing[FieldVehicleClass] = true
	}
	if v == nil || v.Make == "" || v.Model == "" {
		missing[FieldVehicleMakeModel] = true
	}
	if v == nil || v.Color == "" {
		missing[FieldVehicleColor] = true
	}
	if v == nil || v.PlateNormalized == "" {
		missing[FieldVehiclePlate] = true
	}
	out := []string{}
	for _, f := range RequiredFields {
		if missing[f] {
			out = append(out, f)
		}
	}
	return out
}

// FileIDs lists every file the registration currently references.
func (r Registration) FileIDs() []uuid.UUID {
	var ids []uuid.UUID
	for _, m := range []*FileMeta{r.Avatar, r.IDFront, r.IDBack} {
		if m != nil {
			ids = append(ids, m.ID)
		}
	}
	for _, p := range r.Photos {
		ids = append(ids, p.FileID)
	}
	return ids
}
