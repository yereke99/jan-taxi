package drivers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/plate"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicles"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// RegistrationResponse loads and renders the user's registration.
func RegistrationResponse(ctx context.Context, q db.Querier, userID uuid.UUID) (RegistrationView, error) {
	reg, err := LoadRegistrationByUser(ctx, q, userID)
	if err != nil {
		return RegistrationView{}, err
	}
	return NewRegistrationView(reg, settings.RequirePhoneVerification(ctx, q)), nil
}

func (h *Handler) getRegistration(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	v, err := RegistrationResponse(r.Context(), h.pool, p.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}

type registrationRequest struct {
	FirstName *string         `json:"first_name"`
	LastName  *string         `json:"last_name"`
	Vehicle   *vehicleRequest `json:"vehicle"`
}

type vehicleRequest struct {
	Make  *string `json:"make"`
	Model *string `json:"model"`
	Color *string `json:"color"`
	Plate *string `json:"plate"`
}

// registrationInput is a validated, normalised registrationRequest.
type registrationInput struct {
	FirstName, LastName string
	Vehicle             vehicles.Details
}

func (req registrationRequest) validate() (registrationInput, error) {
	var in registrationInput
	fields := map[string]string{}
	in.FirstName = cleanName(req.FirstName, "first_name", fields)
	in.LastName = cleanName(req.LastName, "last_name", fields)
	if req.Vehicle == nil {
		fields["vehicle"] = "required"
		return in, httpx.Validation(fields)
	}
	in.Vehicle.Make = cleanField(req.Vehicle.Make, vehicles.MaxMake, "vehicle.make", fields)
	in.Vehicle.Model = cleanField(req.Vehicle.Model, vehicles.MaxModel, "vehicle.model", fields)
	in.Vehicle.Color = cleanField(req.Vehicle.Color, vehicles.MaxColor, "vehicle.color", fields)
	raw := cleanField(req.Vehicle.Plate, vehicles.MaxPlateRaw, "vehicle.plate", fields)
	if raw != "" {
		norm, err := plate.Normalize(raw)
		if err != nil {
			fields["vehicle.plate"] = "invalid"
		}
		in.Vehicle.PlateRaw, in.Vehicle.PlateNormalized = raw, norm
	}
	if len(fields) > 0 {
		return in, httpx.Validation(fields)
	}
	return in, nil
}

func cleanField(s *string, max int, name string, fields map[string]string) string {
	if s == nil {
		fields[name] = "required"
		return ""
	}
	v, err := textutil.CleanText(*s, max)
	if err != nil {
		fields[name] = textutil.Reason(err)
		return ""
	}
	return v
}

func cleanName(s *string, name string, fields map[string]string) string {
	v := cleanField(s, MaxNameLen, name, fields)
	if v != "" && !validName(v) {
		fields[name] = "invalid"
		return ""
	}
	return v
}

// validName accepts letters of any script with spaces, hyphens, apostrophes
// and dots between them (e.g. "Әлі-Ахмет", "O'Connor").
func validName(s string) bool {
	letters := 0
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsMark(r), r == ' ', r == '-', r == '\'', r == '’', r == 'ʼ', r == '.':
		default:
			return false
		}
	}
	return letters > 0
}

func (h *Handler) putRegistration(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	var req registrationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in, err := req.validate()
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		return saveRegistration(ctx, tx, p.UserID, h.opt.DefaultCity, in)
	})
	if err != nil {
		return err
	}
	v, err := RegistrationResponse(ctx, h.pool, p.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}

// saveRegistration applies a registration update, creating the profile on the
// first call. Concurrent calls by one user are serialised on the user row.
func saveRegistration(ctx context.Context, tx pgx.Tx, userID uuid.UUID, defaultCity string, in registrationInput) error {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID); err != nil {
		return fmt.Errorf("drivers: lock user: %w", err)
	}
	prof, err := scanProfile(tx.QueryRow(ctx, `SELECT `+profileColumns+` FROM driver_profiles WHERE user_id = $1 FOR UPDATE`, userID))
	if errors.Is(err, ErrNotFound) {
		prof, err = createProfile(ctx, tx, userID, defaultCity)
	}
	if err != nil {
		return err
	}
	if !prof.Editable() {
		return ErrRegistrationLocked(prof.Status)
	}
	veh, err := vehicles.CurrentForUpdate(ctx, tx, prof.ID)
	if errors.Is(err, vehicles.ErrNotFound) {
		veh, err = vehicles.Create(ctx, tx, prof.ID, vehicles.Details{})
	}
	if err != nil {
		return err
	}

	next := in.Vehicle
	if prof.Status == StatusChangesRequested {
		editable, err := FlaggedFields(ctx, tx, prof.ID)
		if err != nil {
			return err
		}
		if err := checkLockedFields(prof, veh, in, editable); err != nil {
			return err
		}
		if !editable[FieldVehiclePlate] {
			next.PlateRaw, next.PlateNormalized = veh.PlateRaw, veh.PlateNormalized
		}
	}
	if next.PlateNormalized != "" && next.PlateNormalized != veh.PlateNormalized {
		held, err := vehicles.PlateHeldByOther(ctx, tx, next.PlateNormalized, prof.ID)
		if err != nil {
			return err
		}
		if held {
			return vehicles.ErrPlateTaken
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE driver_profiles SET first_name = $2, last_name = $3 WHERE id = $1`,
		prof.ID, in.FirstName, in.LastName); err != nil {
		return fmt.Errorf("drivers: save names: %w", err)
	}
	return vehicles.UpdateDetails(ctx, tx, veh.ID, next)
}

// checkLockedFields rejects changes to fields the moderator did not flag.
func checkLockedFields(prof Profile, veh vehicles.Vehicle, in registrationInput, editable map[string]bool) error {
	fields := map[string]string{}
	lock := func(changed bool, field string, names ...string) {
		if changed && !editable[field] {
			for _, n := range names {
				fields[n] = "not_editable"
			}
		}
	}
	lock(in.FirstName != prof.FirstName, FieldFirstName, "first_name")
	lock(in.LastName != prof.LastName, FieldLastName, "last_name")
	lock(in.Vehicle.Make != veh.Make, FieldVehicleMakeModel, "vehicle.make")
	lock(in.Vehicle.Model != veh.Model, FieldVehicleMakeModel, "vehicle.model")
	lock(in.Vehicle.Color != veh.Color, FieldVehicleColor, "vehicle.color")
	lock(in.Vehicle.PlateNormalized != veh.PlateNormalized, FieldVehiclePlate, "vehicle.plate")
	if len(fields) > 0 {
		return ErrFieldLocked(fields, editable)
	}
	return nil
}

// FlaggedFields returns the fields flagged in the driver's latest application
// (the open corrections while the status is changes_requested).
func FlaggedFields(ctx context.Context, q db.Querier, driverID uuid.UUID) (map[string]bool, error) {
	var raw []byte
	err := q.QueryRow(ctx, `
		SELECT corrections FROM driver_applications WHERE driver_id = $1 AND status = 'changes_requested'
		ORDER BY round DESC LIMIT 1`, driverID).Scan(&raw)
	if err != nil && !db.IsNoRows(err) {
		return nil, fmt.Errorf("drivers: corrections: %w", err)
	}
	out := map[string]bool{}
	for _, f := range CorrectionFields(ParseCorrections(raw)) {
		out[f] = true
	}
	return out, nil
}

// createProfile creates a draft profile in the default city with the next
// driver code of that city (the city row lock makes the sequence safe), a
// draft vehicle and an empty wallet. A phone the user already verified is copied.
func createProfile(ctx context.Context, tx pgx.Tx, userID uuid.UUID, defaultCity string) (Profile, error) {
	city, err := cities.Default(ctx, tx, defaultCity)
	if err != nil {
		return Profile{}, err
	}
	var (
		seq      int64
		cityCode string
	)
	if err := tx.QueryRow(ctx, `UPDATE cities SET driver_seq = driver_seq + 1 WHERE id = $1 RETURNING driver_seq, code`, city.ID).
		Scan(&seq, &cityCode); err != nil {
		return Profile{}, fmt.Errorf("drivers: next driver code: %w", err)
	}
	code := fmt.Sprintf("%s%03d", cityCode, seq)
	p, err := scanProfile(tx.QueryRow(ctx, `
		INSERT INTO driver_profiles (user_id, code, city_id, phone_e164, phone_verified_at)
		SELECT u.id, $2, $3, u.phone_e164, CASE WHEN u.phone_e164 IS NOT NULL THEN u.phone_verified_at END
		FROM users u WHERE u.id = $1
		RETURNING `+profileColumns, userID, code, city.ID))
	if err != nil {
		return Profile{}, err
	}
	if _, err := vehicles.Create(ctx, tx, p.ID, vehicles.Details{}); err != nil {
		return Profile{}, err
	}
	if _, err := wallets.Ensure(ctx, tx, p.ID); err != nil {
		return Profile{}, fmt.Errorf("drivers: create wallet: %w", err)
	}
	return p, nil
}

type checkRequest struct {
	Plate *string `json:"plate"`
}

type plateCheck struct {
	Input      string  `json:"input"`
	Normalized *string `json:"normalized"`
	Display    *string `json:"display"`
	Valid      bool    `json:"valid"`
	Available  *bool   `json:"available"`
}

type checkResult struct {
	Plate *plateCheck `json:"plate"`
}

// checkRegistration is the advisory plate check used while typing.
func (h *Handler) checkRegistration(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	var req checkRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res := checkResult{}
	if req.Plate == nil {
		return httpx.OK(w, res)
	}
	if textutil.Len(*req.Plate) > vehicles.MaxPlateRaw {
		return httpx.Validation(map[string]string{"plate": "too_long"})
	}
	pc := &plateCheck{Input: *req.Plate}
	res.Plate = pc
	norm, err := plate.Normalize(*req.Plate)
	if err != nil {
		return httpx.OK(w, res)
	}
	display := plate.Format(norm)
	pc.Normalized, pc.Display, pc.Valid = &norm, &display, true
	driverID := uuid.Nil
	if d, ok := auth.DriverFrom(r.Context()); ok {
		driverID = d.ID
	} else if id, err := IDForUser(r.Context(), h.pool, p.UserID); err == nil {
		driverID = id
	}
	held, err := vehicles.PlateHeldByOther(r.Context(), h.pool, norm, driverID)
	if err != nil {
		return err
	}
	available := !held
	pc.Available = &available
	return httpx.OK(w, res)
}
