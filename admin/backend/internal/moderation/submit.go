package moderation

import (
	"context"
	"fmt"
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicles"
)

// submitRequest carries the personal-data consent given with «Тіркелуді
// аяқтау». Consent is required on the first submission; a resubmission may
// omit it when it was given before. The Mini App asks again whenever the
// version of its consent text changes.
type submitRequest struct {
	PDConsent        *bool   `json:"pd_consent"`
	PDConsentVersion *string `json:"pd_consent_version"`
}

var consentVersionRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

var (
	errPhoneNotVerified = httpx.Unprocessable("phone_not_verified", "a Telegram-verified phone is required")
)

func errIncomplete(missing []string) error {
	return httpx.Unprocessable("registration_incomplete", "registration incomplete").
		WithDetails(map[string]any{"missing_fields": missing})
}

// snapshot is the application data as submitted in one round. The IIN is only
// kept masked: snapshots are shown to administrators as they are.
type snapshot struct {
	Round            int               `json:"round"`
	Code             string            `json:"code"`
	CityID           uuid.UUID         `json:"city_id"`
	FirstName        string            `json:"first_name"`
	LastName         string            `json:"last_name"`
	Patronymic       string            `json:"patronymic"`
	IINMasked        *string           `json:"iin_masked"`
	Phone            *string           `json:"phone"`
	AvatarFileID     *uuid.UUID        `json:"avatar_file_id"`
	Documents        snapshotDocuments `json:"documents"`
	Vehicle          snapshotVehicle   `json:"vehicle"`
	PDConsentVersion *string           `json:"pd_consent_version"`
	FileIDs          []uuid.UUID       `json:"file_ids"`
}

// snapshotDocuments are ID scans uploaded before they were retired (nil since).
type snapshotDocuments struct {
	IDFront *uuid.UUID `json:"id_front"`
	IDBack  *uuid.UUID `json:"id_back"`
}

type snapshotVehicle struct {
	ID              uuid.UUID       `json:"id"`
	Make            string          `json:"make"`
	Model           string          `json:"model"`
	Color           string          `json:"color"`
	PlateRaw        string          `json:"plate_raw"`
	PlateNormalized string          `json:"plate_normalized"`
	VehicleClass    string          `json:"vehicle_class"`
	Seats           int             `json:"seats"`
	CargoVolumeL    int             `json:"cargo_volume_l"`
	Photos          []snapshotPhoto `json:"photos"`
}

type snapshotPhoto struct {
	Kind   string    `json:"kind"`
	FileID uuid.UUID `json:"file_id"`
}

func fileID(m *drivers.FileMeta) *uuid.UUID {
	if m == nil {
		return nil
	}
	id := m.ID
	return &id
}

func buildSnapshot(reg drivers.Registration, round int, consentVersion *string) snapshot {
	p := reg.Profile
	phoneNum, _ := reg.Phone()
	s := snapshot{
		Round: round, Code: p.Code, CityID: p.CityID, FirstName: p.FirstName, LastName: p.LastName,
		Patronymic: p.Patronymic, IINMasked: p.IINMasked(), Phone: phoneNum,
		AvatarFileID:     fileID(reg.Avatar),
		Documents:        snapshotDocuments{IDFront: fileID(reg.IDFront), IDBack: fileID(reg.IDBack)},
		PDConsentVersion: consentVersion,
		FileIDs:          reg.FileIDs(),
	}
	if v := reg.Vehicle; v != nil {
		s.Vehicle = snapshotVehicle{ID: v.ID, Make: v.Make, Model: v.Model, Color: v.Color, PlateRaw: v.PlateRaw,
			PlateNormalized: v.PlateNormalized, VehicleClass: v.ClassCode, Seats: v.Seats, CargoVolumeL: v.CargoVolumeL,
			Photos: []snapshotPhoto{}}
	}
	for _, ph := range reg.Photos {
		s.Vehicle.Photos = append(s.Vehicle.Photos, snapshotPhoto{Kind: ph.Kind, FileID: ph.FileID})
	}
	if s.FileIDs == nil {
		s.FileIDs = []uuid.UUID{}
	}
	return s
}

// submit finishes the registration (draft) or the corrected registration
// (changes_requested): when the automatic checks pass the driver is approved
// at once and may go online.
func (h *Handler) submit(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	var req submitRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	requirePhone := settings.RequirePhoneVerification(ctx, h.pool)
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		return Submit(ctx, tx, d.ID, p.UserID, req.PDConsent, req.PDConsentVersion, requirePhone)
	})
	if err != nil {
		return err
	}
	v, err := drivers.RegistrationResponse(ctx, h.pool, p.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}

// Submit runs the automatic checks on a draft or changes_requested
// registration inside tx and, when they pass, approves it in the same
// transaction (owner decision 2026-10-31: no manual review):
//
//   - everything required is there: profile photo, names, a valid IIN,
//     vehicle class, make and model, colour and plate (422
//     registration_incomplete with details.missing_fields), and a
//     Telegram-verified phone while driver.require_phone_verification is on
//     (422 phone_not_verified when it is the only gap);
//   - the personal-data consent (422 validation_failed, fields.pd_consent*);
//   - the IIN, the phone and the plate are not held by another live driver
//     (409 iin_taken, phone_taken, plate_taken). The partial unique indexes
//     make simultaneous claims end with exactly one winner; one profile per
//     Telegram account is the unique users key of driver_profiles.
//
// It records the application round (the snapshot keeps the IIN masked) and
// the submission event, then approves through the path administrators use
// (approve) with the system as the actor: the welcome bonus once per phone and
// plate, the approval event, audit rows, the moderation_approved message and
// the realtime events. Any failure rolls everything back and the registration
// stays editable.
func Submit(ctx context.Context, tx pgx.Tx, driverID, userID uuid.UUID, consent *bool, consentVersion *string, requirePhone bool) error {
	prof, err := drivers.LockProfile(ctx, tx, driverID)
	if err != nil {
		return err
	}
	if prof.UserID != userID {
		return httpx.NotFound()
	}
	from := prof.Status
	if from != drivers.StatusDraft && from != drivers.StatusChangesRequested {
		return invalidTransition(from, "submit")
	}
	giveConsent, version, err := checkConsent(prof, consent, consentVersion)
	if err != nil {
		return err
	}

	reg, err := drivers.LoadRegistration(ctx, tx, driverID)
	if err != nil {
		return err
	}
	missing := reg.MissingFields(requirePhone)
	if len(missing) == 1 && missing[0] == drivers.FieldPhone {
		return errPhoneNotVerified
	}
	if len(missing) > 0 {
		return errIncomplete(missing)
	}
	held, err := drivers.IINHeldByOther(ctx, tx, reg.IIN(), driverID)
	if err != nil {
		return err
	}
	if held {
		return drivers.ErrIINTaken
	}

	phoneNum, phoneAt := reg.Phone()
	_, err = tx.Exec(ctx, `
		UPDATE driver_profiles
		SET status = 'pending', submitted_at = now(), phone_e164 = $2, phone_verified_at = $3,
		    pd_consent_at = CASE WHEN $4 THEN now() ELSE pd_consent_at END,
		    pd_consent_version = CASE WHEN $4 THEN $5 ELSE pd_consent_version END
		WHERE id = $1`, driverID, phoneNum, phoneAt, giveConsent, version)
	switch {
	case db.IsUniqueViolation(err, drivers.PhoneUniqueIndex):
		return drivers.ErrPhoneTaken
	case db.IsUniqueViolation(err, drivers.IINUniqueIndex):
		return drivers.ErrIINTaken
	case err != nil:
		return fmt.Errorf("moderation: submit: %w", err)
	}
	if err := vehicles.SetStatus(ctx, tx, reg.Vehicle.ID, vehicles.StatusClaimed); err != nil {
		return err
	}

	var round int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(round), 0) + 1 FROM driver_applications WHERE driver_id = $1`, driverID).
		Scan(&round); err != nil {
		return fmt.Errorf("moderation: next round: %w", err)
	}
	consentInSnapshot := prof.PDConsentVersion
	if giveConsent {
		consentInSnapshot = &version
	}
	var appID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO driver_applications (driver_id, round, status, snapshot) VALUES ($1, $2, 'pending', $3) RETURNING id`,
		driverID, round, buildSnapshot(reg, round, consentInSnapshot)).Scan(&appID); err != nil {
		return fmt.Errorf("moderation: create application: %w", err)
	}

	action := drivers.ActionSubmitted
	if from == drivers.StatusChangesRequested {
		action = drivers.ActionResubmitted
	}
	if _, err := InsertEvent(ctx, tx, Event{
		DriverID: driverID, ApplicationID: &appID, ActorType: "driver", Action: action,
		FromStatus: from, ToStatus: drivers.StatusPending,
	}); err != nil {
		return err
	}
	prof.Status = drivers.StatusPending
	if err := publish(ctx, tx, realtime.ModerationSubmitted, prof, map[string]any{
		"application_id": appID, "round": round,
	}); err != nil {
		return err
	}
	return approve(ctx, tx, nil, prof, appID, auth.AdminPrincipal{}, nil)
}

// checkConsent validates the personal-data consent. It returns whether a new
// consent is recorded now and its version.
func checkConsent(prof drivers.Profile, consent *bool, version *string) (bool, string, error) {
	given := consent != nil && *consent
	if (consent != nil && !*consent) || (!given && prof.PDConsentAt == nil) {
		return false, "", httpx.Validation(map[string]string{"pd_consent": "must_be_true"})
	}
	if !given {
		return false, "", nil
	}
	if version == nil || *version == "" {
		return false, "", httpx.Validation(map[string]string{"pd_consent_version": "required"})
	}
	if !consentVersionRe.MatchString(*version) {
		return false, "", httpx.Validation(map[string]string{"pd_consent_version": "invalid"})
	}
	return true, *version, nil
}
