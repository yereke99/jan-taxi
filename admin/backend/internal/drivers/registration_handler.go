package drivers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/iin"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/plate"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicleclasses"
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

// registrationRequest is PUT /driver/registration. Every key is optional: a
// key that is not sent keeps the stored value (older app versions do not know
// every field), an empty string clears it while drafting.
type registrationRequest struct {
	FirstName  *string         `json:"first_name"`
	LastName   *string         `json:"last_name"`
	Patronymic *string         `json:"patronymic"`
	IIN        *string         `json:"iin"`
	Vehicle    *vehicleRequest `json:"vehicle"`
	// Location is the device's latest position (optional). On the first
	// save it decides the driver's city and so the code prefix.
	Location *registrationPoint `json:"location"`
	// Identity is the object older app versions send. Its non-empty iin and
	// patronymic stand in for the top-level keys; the document fields are
	// accepted and ignored (document numbers are no longer collected).
	Identity *legacyIdentity `json:"identity"`
}

type legacyIdentity struct {
	DocumentType   *string `json:"document_type"`
	IIN            *string `json:"iin"`
	DocumentNumber *string `json:"document_number"`
	Patronymic     *string `json:"patronymic"`
}

type registrationPoint struct {
	Lat *float64 `json:"lat"`
	Lon *float64 `json:"lon"`
}

type vehicleRequest struct {
	Make  *string `json:"make"`
	Model *string `json:"model"`
	Color *string `json:"color"`
	Plate *string `json:"plate"`
	// Class fields (ARCHITECTURE §21). Omitted vehicle_class keeps the stored
	// class; omitted seats/cargo_volume_l keep the stored values, or take the
	// class defaults when the class changes.
	VehicleClass *string `json:"vehicle_class"`
	Seats        *int64  `json:"seats"`
	CargoVolumeL *int64  `json:"cargo_volume_l"`
}

// registrationInput is a validated, normalised registrationRequest; nil
// means "not sent" (the stored value stays). The class fields are resolved
// against the stored vehicle inside the transaction.
type registrationInput struct {
	FirstName, LastName, Patronymic, IIN *string
	Make, Model, Color                   *string
	Plate                                *plateInput
	Class                                classInput
	Location                             *geo.Point // nil when not sent
}

// plateInput is a plate as typed and its canonical form ("" while empty).
type plateInput struct {
	Raw, Normalized string
}

// classInput is the requested vehicle class, seats and cargo volume; nil
// means "not sent".
type classInput struct {
	Code         *string
	Seats        *int
	CargoVolumeL *int
}

func (req registrationRequest) validate() (registrationInput, error) {
	var in registrationInput
	fields := map[string]string{}
	in.FirstName = cleanName(req.FirstName, MaxNameLen, "first_name", fields)
	in.LastName = cleanName(req.LastName, MaxNameLen, "last_name", fields)
	patronymic, rawIIN := req.Patronymic, req.IIN
	if old := req.Identity; old != nil {
		if patronymic == nil && old.Patronymic != nil && strings.TrimSpace(*old.Patronymic) != "" {
			patronymic = old.Patronymic
		}
		if rawIIN == nil && old.IIN != nil && strings.TrimSpace(*old.IIN) != "" {
			rawIIN = old.IIN
		}
	}
	in.Patronymic = cleanName(patronymic, MaxPatronymicLen, "patronymic", fields)
	in.IIN = cleanIIN(rawIIN, "iin", fields)
	if v := req.Vehicle; v != nil {
		in.Make = cleanText(v.Make, vehicles.MaxMake, "vehicle.make", fields)
		in.Model = cleanText(v.Model, vehicles.MaxModel, "vehicle.model", fields)
		in.Color = cleanText(v.Color, vehicles.MaxColor, "vehicle.color", fields)
		if raw := cleanText(v.Plate, vehicles.MaxPlateRaw, "vehicle.plate", fields); raw != nil {
			p := &plateInput{Raw: *raw}
			if p.Raw != "" {
				norm, err := plate.Normalize(p.Raw)
				if err != nil {
					fields["vehicle.plate"] = "invalid"
				}
				p.Normalized = norm
			}
			in.Plate = p
		}
		in.Class = v.classInput(fields)
	}
	if l := req.Location; l != nil {
		if l.Lat == nil || l.Lon == nil || (geo.Point{Lat: *l.Lat, Lon: *l.Lon}).Valid() != nil {
			fields["location"] = "invalid"
		} else {
			in.Location = &geo.Point{Lat: *l.Lat, Lon: *l.Lon}
		}
	}
	if len(fields) > 0 {
		return in, httpx.Validation(fields)
	}
	return in, nil
}

// classInput checks the shape of the class fields; the class itself and the
// seat limit are checked against the database in resolveClass.
func (v vehicleRequest) classInput(fields map[string]string) classInput {
	var in classInput
	if v.VehicleClass != nil {
		if c := strings.TrimSpace(*v.VehicleClass); vehicleclasses.ValidCode(c) {
			in.Code = &c
		} else {
			fields["vehicle.vehicle_class"] = "invalid"
		}
	}
	if v.Seats != nil {
		switch {
		case *v.Seats < 1:
			fields["vehicle.seats"] = "too_small"
		case *v.Seats > vehicleclasses.MaxSeats:
			fields["vehicle.seats"] = "too_large"
		default:
			n := int(*v.Seats)
			in.Seats = &n
		}
	}
	if v.CargoVolumeL != nil {
		switch {
		case *v.CargoVolumeL < 0:
			fields["vehicle.cargo_volume_l"] = "too_small"
		case *v.CargoVolumeL > vehicleclasses.MaxCargoVolumeL:
			fields["vehicle.cargo_volume_l"] = "too_large"
		default:
			n := int(*v.CargoVolumeL)
			in.CargoVolumeL = &n
		}
	}
	return in
}

// resolveClass applies the requested class fields to next (which starts as
// the stored vehicle's class): a new class must exist and be active, seats
// must fit the class (1..seats_max) and missing values come from the stored
// vehicle or, when the class changes, from the class defaults.
func resolveClass(ctx context.Context, q db.Querier, cur vehicles.Vehicle, in classInput, next *vehicles.Details) error {
	next.ClassCode, next.Seats, next.CargoVolumeL = cur.ClassCode, cur.Seats, cur.CargoVolumeL
	if in.Code == nil && in.Seats == nil && in.CargoVolumeL == nil {
		return nil
	}
	fields := map[string]string{}
	code := cur.ClassCode
	if in.Code != nil {
		code = *in.Code
	}
	if code == "" {
		return httpx.Validation(map[string]string{"vehicle.vehicle_class": "required"})
	}
	class, err := vehicleclasses.Get(ctx, q, code)
	switch {
	case errors.Is(err, vehicleclasses.ErrNotFound):
		return httpx.Validation(map[string]string{"vehicle.vehicle_class": "not_found"})
	case err != nil:
		return err
	case !class.IsActive && code != cur.ClassCode:
		return httpx.Validation(map[string]string{"vehicle.vehicle_class": "not_allowed"})
	}
	changed := code != cur.ClassCode
	next.ClassCode = code
	switch {
	case in.Seats != nil:
		next.Seats = *in.Seats
	case changed:
		next.Seats = class.SeatsDefault
	}
	switch {
	case in.CargoVolumeL != nil:
		next.CargoVolumeL = *in.CargoVolumeL
	case changed:
		next.CargoVolumeL = class.CargoVolumeLDefault
	}
	if next.Seats > class.SeatsMax {
		fields["vehicle.seats"] = "too_large"
	}
	if next.Seats < 1 {
		fields["vehicle.seats"] = "too_small"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields).WithDetails(map[string]any{"seats_max": class.SeatsMax})
	}
	return nil
}

// cleanText normalises an optional text field; nil stays nil (not sent).
func cleanText(s *string, max int, name string, fields map[string]string) *string {
	if s == nil {
		return nil
	}
	v, err := textutil.CleanText(*s, max)
	if err != nil {
		fields[name] = textutil.Reason(err)
		return nil
	}
	return &v
}

func cleanName(s *string, max int, name string, fields map[string]string) *string {
	v := cleanText(s, max, name, fields)
	if v != nil && *v != "" && !validName(*v) {
		fields[name] = "invalid"
		return nil
	}
	return v
}

// cleanIIN validates an optional IIN: "" clears it, anything else must be
// twelve digits ("invalid") with a valid check digit ("checksum").
func cleanIIN(s *string, name string, fields map[string]string) *string {
	if s == nil {
		return nil
	}
	if strings.TrimSpace(*s) == "" {
		empty := ""
		return &empty
	}
	v, err := iin.Normalize(*s)
	if err != nil {
		fields[name] = iinReason(err)
		return nil
	}
	return &v
}

// iinReason is the validation reason of an iin.Normalize error.
func iinReason(err error) string {
	if errors.Is(err, iin.ErrChecksum) {
		return "checksum"
	}
	return "invalid"
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
	if err := db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		return saveRegistration(ctx, tx, p.UserID, h.opt.DefaultCity, in)
	}); err != nil {
		return err
	}
	v, err := RegistrationResponse(ctx, h.pool, p.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}

// personValues are the person fields of a profile as they will be stored.
type personValues struct {
	FirstName, LastName, Patronymic, IIN string
}

func pick(sent *string, stored string) string {
	if sent != nil {
		return *sent
	}
	return stored
}

// saveRegistration applies a registration update, creating the profile on the
// first call. Concurrent calls by one user are serialised on the user row.
func saveRegistration(ctx context.Context, tx pgx.Tx, userID uuid.UUID, defaultCity string, in registrationInput) error {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID); err != nil {
		return fmt.Errorf("drivers: lock user: %w", err)
	}
	prof, err := scanProfile(tx.QueryRow(ctx, `SELECT `+profileColumns+` FROM driver_profiles WHERE user_id = $1 FOR UPDATE`, userID))
	if errors.Is(err, ErrNotFound) {
		prof, err = createProfile(ctx, tx, userID, defaultCity, in.Location)
	}
	if err != nil {
		return err
	}
	if !prof.Editable() {
		return ErrRegistrationLocked(prof.Status)
	}
	veh, err := vehicles.CurrentForUpdate(ctx, tx, prof.ID)
	if errors.Is(err, vehicles.ErrNotFound) {
		veh, err = createVehicle(ctx, tx, prof.ID)
	}
	if err != nil {
		return err
	}

	// What was sent replaces the stored value; everything else stays.
	person := personValues{
		FirstName:  pick(in.FirstName, prof.FirstName),
		LastName:   pick(in.LastName, prof.LastName),
		Patronymic: pick(in.Patronymic, prof.Patronymic),
		IIN:        pick(in.IIN, prof.IINValue()),
	}
	next := veh.Details()
	next.Make, next.Model, next.Color = pick(in.Make, veh.Make), pick(in.Model, veh.Model), pick(in.Color, veh.Color)
	if in.Plate != nil {
		next.PlateRaw, next.PlateNormalized = in.Plate.Raw, in.Plate.Normalized
	}
	if err := resolveClass(ctx, tx, veh, in.Class, &next); err != nil {
		return err
	}
	if prof.Status == StatusChangesRequested {
		editable, err := editableFieldsOf(ctx, tx, prof)
		if err != nil {
			return err
		}
		if err := checkLockedFields(prof, veh, person, next, editable); err != nil {
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
	if person.IIN != "" && person.IIN != prof.IINValue() {
		held, err := IINHeldByOther(ctx, tx, person.IIN, prof.ID)
		if err != nil {
			return err
		}
		if held {
			return ErrIINTaken
		}
	}
	_, err = tx.Exec(ctx, `UPDATE driver_profiles SET first_name = $2, last_name = $3, patronymic = $4, iin = NULLIF($5, '') WHERE id = $1`,
		prof.ID, person.FirstName, person.LastName, person.Patronymic, person.IIN)
	if db.IsUniqueViolation(err, IINUniqueIndex) {
		return ErrIINTaken
	}
	if err != nil {
		return fmt.Errorf("drivers: save person: %w", err)
	}
	return vehicles.UpdateDetails(ctx, tx, veh.ID, next)
}

// checkLockedFields rejects changes to fields the moderator did not flag. next
// carries the resolved vehicle fields.
func checkLockedFields(prof Profile, veh vehicles.Vehicle, person personValues, next vehicles.Details, editable map[string]bool) error {
	fields := map[string]string{}
	lock := func(changed bool, field string, names ...string) {
		if changed && !editable[field] {
			for _, n := range names {
				fields[n] = "not_editable"
			}
		}
	}
	lock(person.FirstName != prof.FirstName, FieldFirstName, "first_name")
	lock(person.LastName != prof.LastName, FieldLastName, "last_name")
	lock(person.Patronymic != prof.Patronymic, FieldIdentity, "patronymic")
	lock(person.IIN != prof.IINValue(), FieldIdentity, "iin")
	lock(next.Make != veh.Make, FieldVehicleMakeModel, "vehicle.make")
	lock(next.Model != veh.Model, FieldVehicleMakeModel, "vehicle.model")
	lock(next.Color != veh.Color, FieldVehicleColor, "vehicle.color")
	lock(next.PlateNormalized != veh.PlateNormalized, FieldVehiclePlate, "vehicle.plate")
	lock(next.ClassCode != veh.ClassCode, FieldVehicleClass, "vehicle.vehicle_class")
	lock(next.Seats != veh.Seats, FieldVehicleClass, "vehicle.seats")
	lock(next.CargoVolumeL != veh.CargoVolumeL, FieldVehicleClass, "vehicle.cargo_volume_l")
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

// editableFieldsOf returns the fields the driver may change in prof's status,
// as Registration.EditableFields does for a loaded registration.
func editableFieldsOf(ctx context.Context, q db.Querier, prof Profile) (map[string]bool, error) {
	switch prof.Status {
	case StatusDraft:
		out := map[string]bool{}
		for _, f := range RegistrationFields {
			out[f] = true
		}
		return out, nil
	case StatusChangesRequested:
		flagged, err := FlaggedFields(ctx, q, prof.ID)
		if err != nil {
			return nil, err
		}
		if prof.IINValue() == "" {
			flagged[FieldIdentity] = true
		}
		return flagged, nil
	}
	return map[string]bool{}, nil
}

// RegistrationCity is the city of a new driver profile: the city whose
// smallest active zone covers the driver's latest location loc, or the
// nationwide region (KAZ) when no city zone covers it (cities.Resolve). The
// configured default city applies only when the location is unknown or
// outside Kazakhstan.
func RegistrationCity(ctx context.Context, q db.Querier, defaultCity string, loc *geo.Point) (cities.City, error) {
	if loc != nil {
		inside, err := geo.InServiceArea(ctx, q, *loc)
		if err != nil {
			return cities.City{}, err
		}
		if inside {
			c, err := cities.Resolve(ctx, q, *loc)
			if err == nil {
				return c, nil
			}
			if !errors.Is(err, cities.ErrNoActiveCity) {
				return cities.City{}, err
			}
		}
	}
	return cities.Default(ctx, q, defaultCity)
}

// createProfile creates a draft profile in the driver's city
// (RegistrationCity) with the next driver code of that city (the city row
// lock makes the sequence safe; drivers outside every configured city get
// KAZ codes), a draft vehicle and an empty wallet. A phone the user already
// verified is copied.
func createProfile(ctx context.Context, tx pgx.Tx, userID uuid.UUID, defaultCity string, loc *geo.Point) (Profile, error) {
	city, err := RegistrationCity(ctx, tx, defaultCity, loc)
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
	if _, err := createVehicle(ctx, tx, p.ID); err != nil {
		return Profile{}, err
	}
	if _, err := wallets.Ensure(ctx, tx, p.ID); err != nil {
		return Profile{}, fmt.Errorf("drivers: create wallet: %w", err)
	}
	return p, nil
}

// createVehicle creates the draft vehicle of a new registration with the
// default class (Sedan) and its seat and cargo defaults, as the registration
// form preselects it; the driver may switch the class. Without an active
// default class the vehicle starts without one.
func createVehicle(ctx context.Context, tx pgx.Tx, driverID uuid.UUID) (vehicles.Vehicle, error) {
	var d vehicles.Details
	class, err := vehicleclasses.Get(ctx, tx, vehicleclasses.Default)
	switch {
	case err == nil && class.IsActive:
		d.ClassCode, d.Seats, d.CargoVolumeL = class.Code, class.SeatsDefault, class.CargoVolumeLDefault
	case err != nil && !errors.Is(err, vehicleclasses.ErrNotFound):
		return vehicles.Vehicle{}, err
	}
	return vehicles.Create(ctx, tx, driverID, d)
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
