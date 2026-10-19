package vehicleclasses

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// ErrCodeTaken is returned when a class with the code already exists.
var ErrCodeTaken = httpx.Conflict("vehicle_class_exists", "a vehicle class with this code already exists")

// AdminHandler serves /admin/vehicle-classes.
type AdminHandler struct {
	pool *pgxpool.Pool
}

// NewAdminHandler builds the admin handler.
func NewAdminHandler(pool *pgxpool.Pool) *AdminHandler { return &AdminHandler{pool: pool} }

// MountAdmin registers GET/POST /vehicle-classes and PATCH
// /vehicle-classes/{code} on the admin router (config.manage; writes audited).
func (h *AdminHandler) MountAdmin(r chi.Router) {
	r.With(auth.Permit(auth.PermConfigManage)).Get("/vehicle-classes", httpx.H(h.list))
	r.With(auth.Permit(auth.PermConfigManage)).Post("/vehicle-classes", httpx.H(h.create))
	r.With(auth.Permit(auth.PermConfigManage)).Patch("/vehicle-classes/{code}", httpx.H(h.update))
}

func (h *AdminHandler) list(w http.ResponseWriter, r *http.Request) error {
	items, err := List(r.Context(), h.pool, false)
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"items": items})
}

// classRequest is VehicleClassCreateRequest / VehicleClassUpdateRequest:
// pointers tell missing fields apart; on update a missing field keeps its value.
type classRequest struct {
	Code                *string   `json:"code"`
	Name                *Name     `json:"name"`
	Examples            *string   `json:"examples"`
	SeatsDefault        *int64    `json:"seats_default"`
	SeatsMax            *int64    `json:"seats_max"`
	CargoVolumeLDefault *int64    `json:"cargo_volume_l_default"`
	PassengerCapable    *bool     `json:"passenger_capable"`
	CargoCapable        *bool     `json:"cargo_capable"`
	Serves              *[]string `json:"serves"`
	Icon                *string   `json:"icon"`
	Sort                *int64    `json:"sort"`
	IsActive            *bool     `json:"is_active"`
}

// apply validates req onto c (a copy of the stored class, or a new one) and
// returns the per-field reasons. creating requires every mandatory field.
func (req classRequest) apply(ctx context.Context, q db.Querier, c *Class, creating bool) (map[string]string, error) {
	fields := map[string]string{}
	if creating {
		switch {
		case req.Code == nil || *req.Code == "":
			fields["code"] = "required"
		case !ValidCode(*req.Code):
			fields["code"] = "invalid"
		default:
			c.Code = *req.Code
		}
	} else if req.Code != nil && *req.Code != c.Code {
		fields["code"] = "immutable"
	}
	if req.Name != nil || creating {
		c.Name = cleanName(req.Name, fields)
	}
	if req.Examples != nil {
		v, err := textutil.CleanText(*req.Examples, MaxExamplesLen)
		if err != nil {
			fields["examples"] = textutil.Reason(err)
		}
		c.Examples = v
	}
	intField(fields, "seats_max", req.SeatsMax, creating, 1, MaxSeats, &c.SeatsMax)
	intField(fields, "seats_default", req.SeatsDefault, creating, 1, MaxSeats, &c.SeatsDefault)
	intField(fields, "cargo_volume_l_default", req.CargoVolumeLDefault, creating, 0, MaxCargoVolumeL, &c.CargoVolumeLDefault)
	if fields["seats_max"] == "" && fields["seats_default"] == "" && c.SeatsDefault > c.SeatsMax {
		fields["seats_default"] = "too_large"
	}
	boolField(fields, "passenger_capable", req.PassengerCapable, creating, &c.PassengerCapable)
	boolField(fields, "cargo_capable", req.CargoCapable, creating, &c.CargoCapable)
	if fields["passenger_capable"] == "" && fields["cargo_capable"] == "" && !c.PassengerCapable && !c.CargoCapable {
		fields["passenger_capable"] = "one_required"
		fields["cargo_capable"] = "one_required"
	}
	switch {
	case req.Icon != nil && !slices.Contains(Icons, *req.Icon):
		fields["icon"] = "invalid"
	case req.Icon != nil:
		c.Icon = *req.Icon
	case creating:
		fields["icon"] = "required"
	}
	if req.Sort != nil {
		if *req.Sort < 0 || *req.Sort > MaxSort {
			fields["sort"] = "out_of_range"
		} else {
			c.Sort = int(*req.Sort)
		}
	}
	if req.IsActive != nil {
		c.IsActive = *req.IsActive
	}
	if req.Serves != nil || creating {
		var in []string
		if req.Serves != nil {
			in = *req.Serves
		}
		serves, err := validateServes(ctx, q, c.Code, in, fields)
		if err != nil {
			return nil, err
		}
		c.Serves = serves
	}
	return fields, nil
}

func cleanName(n *Name, fields map[string]string) Name {
	if n == nil {
		fields["name"] = "required"
		return Name{}
	}
	var out Name
	for _, f := range []struct {
		lang string
		in   string
		out  *string
	}{{"kk", n.KK, &out.KK}, {"ru", n.RU, &out.RU}, {"en", n.EN, &out.EN}} {
		v, err := textutil.CleanRequired(f.in, MaxNameLen)
		if err != nil {
			fields["name."+f.lang] = textutil.Reason(err)
		}
		*f.out = v
	}
	return out
}

func intField(fields map[string]string, name string, v *int64, required bool, lo, hi int, dst *int) {
	switch {
	case v == nil && required:
		fields[name] = "required"
	case v == nil:
	case *v < int64(lo):
		fields[name] = "too_small"
	case *v > int64(hi):
		fields[name] = "too_large"
	default:
		*dst = int(*v)
	}
}

func boolField(fields map[string]string, name string, v *bool, required bool, dst *bool) {
	switch {
	case v == nil && required:
		fields[name] = "required"
	case v != nil:
		*dst = *v
	}
}

// validateServes checks that every served code is a known class (or the class
// itself) and returns the list deduplicated with the class itself first.
func validateServes(ctx context.Context, q db.Querier, self string, in []string, fields map[string]string) ([]string, error) {
	out := []string{}
	if self != "" {
		out = append(out, self)
	}
	if len(in) > MaxServes {
		fields["serves"] = "too_many"
		return out, nil
	}
	for i, code := range in {
		key := "serves." + strconv.Itoa(i)
		code = strings.TrimSpace(code)
		switch {
		case !ValidCode(code):
			fields[key] = "invalid"
			continue
		case slices.Contains(out, code):
			continue
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM vehicle_classes WHERE code = $1)`, code).Scan(&exists); err != nil {
			return nil, httpx.Internal(err)
		}
		if !exists {
			fields[key] = "not_found"
			continue
		}
		out = append(out, code)
	}
	if len(out) > MaxServes {
		fields["serves"] = "too_many"
	}
	return out, nil
}

func (h *AdminHandler) create(w http.ResponseWriter, r *http.Request) error {
	var req classRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	var created Class
	err := db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		c := Class{IsActive: true, Sort: 100}
		fields, err := req.apply(ctx, tx, &c, true)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		created, err = scan(tx.QueryRow(ctx, `
			INSERT INTO vehicle_classes AS vc (code, name_kk, name_ru, name_en, examples, seats_default, seats_max,
			                                   cargo_volume_l_default, passenger_capable, cargo_capable, serves, icon,
			                                   sort, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (code) DO NOTHING
			RETURNING `+columns,
			c.Code, c.Name.KK, c.Name.RU, c.Name.EN, c.Examples, c.SeatsDefault, c.SeatsMax, c.CargoVolumeLDefault,
			c.PassengerCapable, c.CargoCapable, c.Serves, c.Icon, c.Sort, c.IsActive))
		if db.IsNoRows(err) {
			return ErrCodeTaken
		}
		if err != nil {
			return httpx.Internal(err)
		}
		return logAudit(ctx, tx, r, "vehicle_class.create", created.Code, nil, created)
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, created)
}

func (h *AdminHandler) update(w http.ResponseWriter, r *http.Request) error {
	code := chi.URLParam(r, "code")
	var req classRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	var updated Class
	err := db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		before, err := get(ctx, tx, code, true)
		if err != nil {
			return err
		}
		c := before
		c.Serves = slices.Clone(before.Serves)
		fields, err := req.apply(ctx, tx, &c, false)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		updated, err = scan(tx.QueryRow(ctx, `
			UPDATE vehicle_classes AS vc SET name_kk = $2, name_ru = $3, name_en = $4, examples = $5,
			       seats_default = $6, seats_max = $7, cargo_volume_l_default = $8, passenger_capable = $9,
			       cargo_capable = $10, serves = $11, icon = $12, sort = $13, is_active = $14
			WHERE vc.code = $1
			RETURNING `+columns,
			c.Code, c.Name.KK, c.Name.RU, c.Name.EN, c.Examples, c.SeatsDefault, c.SeatsMax, c.CargoVolumeLDefault,
			c.PassengerCapable, c.CargoCapable, c.Serves, c.Icon, c.Sort, c.IsActive))
		if err != nil {
			return httpx.Internal(err)
		}
		return logAudit(ctx, tx, r, "vehicle_class.update", code, before, updated)
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, updated)
}

func logAudit(ctx context.Context, q db.Querier, r *http.Request, action, code string, before, after any) error {
	entry := audit.Entry{Action: action, EntityType: "vehicle_class", EntityID: code, After: after}
	if before != nil {
		entry.Before = before
	}
	if err := audit.Log(ctx, q, r, entry); err != nil {
		return httpx.Internal(err)
	}
	return nil
}
