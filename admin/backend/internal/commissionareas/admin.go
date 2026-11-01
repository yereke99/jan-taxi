package commissionareas

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// Validation reasons of this package.
const (
	reasonRequired   = "required"
	reasonInvalid    = "invalid"
	reasonTooLong    = "too_long"
	reasonTooShort   = "too_short"
	reasonOutOfRange = "out_of_range"
	reasonOutside    = "outside_service_area"
	reasonDuplicate  = "duplicate"
)

// Search limits of GET /admin/commission-areas/search.
const (
	searchMinChars = 2
	searchLimit    = 8
)

// AdminHandler serves /admin/commission-areas.
type AdminHandler struct {
	pool *pgxpool.Pool
	geo  Geocoder
}

// NewAdminHandler builds the handler; g may be nil (no map search).
func NewAdminHandler(pool *pgxpool.Pool, g Geocoder) *AdminHandler {
	return &AdminHandler{pool: pool, geo: g}
}

// MountAdmin registers the routes on the admin router (which already requires
// an admin session). Commissions are configuration: config.manage.
func (h *AdminHandler) MountAdmin(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.Permit(auth.PermConfigManage))
		r.Get("/commission-areas", httpx.H(h.list))
		r.Post("/commission-areas", httpx.H(h.create))
		r.Get("/commission-areas/resolve", httpx.H(h.resolve))
		r.Get("/commission-areas/suggest", httpx.H(h.suggest))
		r.Get("/commission-areas/search", httpx.H(h.search))
		r.Patch("/commission-areas/{id}", httpx.H(h.update))
		r.Delete("/commission-areas/{id}", httpx.H(h.delete))
	})
}

// areaRequest is the create/update body; absent fields keep their value.
type areaRequest struct {
	Name          *string          `json:"name"`
	CommissionBps *int64           `json:"commission_bps"`
	Shape         *string          `json:"shape"`
	Center        *geo.Point       `json:"center"`
	RadiusM       *int64           `json:"radius_m"`
	Boundary      *json.RawMessage `json:"boundary"`
	IsActive      *bool            `json:"is_active"`
}

// apply validates the request onto a (the current area, or a zero area when
// creating) and returns the field problems plus the canonical boundary
// GeoJSON to store (nil for a circle).
func (req areaRequest) apply(ctx context.Context, q db.Querier, a *Area, creating bool) (map[string]string, *string, error) {
	fields := map[string]string{}
	if req.Name != nil || creating {
		switch name, err := textutil.CleanText(deref(req.Name), MaxNameLen); {
		case errors.Is(err, textutil.ErrTooLong):
			fields["name"] = reasonTooLong
		case err != nil:
			fields["name"] = reasonInvalid
		case name == "":
			fields["name"] = reasonRequired
		default:
			a.Name = name
		}
	}
	if req.CommissionBps != nil || creating {
		switch {
		case req.CommissionBps == nil:
			fields["commission_bps"] = reasonRequired
		case *req.CommissionBps < 0 || *req.CommissionBps > MaxCommissionBps:
			fields["commission_bps"] = reasonOutOfRange
		default:
			a.CommissionBps = int(*req.CommissionBps)
		}
	}
	if req.Shape != nil || creating {
		switch {
		case req.Shape == nil:
			fields["shape"] = reasonRequired
		case *req.Shape != ShapeCircle && *req.Shape != ShapePolygon:
			fields["shape"] = reasonInvalid
		default:
			a.Shape = *req.Shape
		}
	}
	if req.Center != nil || creating {
		switch {
		case req.Center == nil:
			fields["center"] = reasonRequired
		case req.Center.Valid() != nil:
			fields["center"] = reasonInvalid
		default:
			ok, err := geo.InServiceArea(ctx, q, *req.Center)
			if err != nil {
				return nil, nil, httpx.Internal(err)
			}
			if !ok {
				fields["center"] = reasonOutside
			} else {
				a.Center = *req.Center
			}
		}
	}
	if req.IsActive != nil {
		a.IsActive = *req.IsActive
	}

	// The shape decides which of radius and boundary is kept.
	var boundary *string
	switch a.Shape {
	case ShapeCircle:
		if req.RadiusM != nil || a.RadiusM == nil {
			switch {
			case req.RadiusM == nil:
				fields["radius_m"] = reasonRequired
			case *req.RadiusM < MinRadiusM || *req.RadiusM > MaxRadiusM:
				fields["radius_m"] = reasonOutOfRange
			default:
				r := int(*req.RadiusM)
				a.RadiusM = &r
			}
		}
		a.Boundary = json.RawMessage(`null`)
	case ShapePolygon:
		a.RadiusM = nil
		raw := json.RawMessage(nil)
		switch {
		case req.Boundary != nil:
			raw = *req.Boundary
		case len(a.Boundary) > 0 && string(a.Boundary) != "null":
			raw = a.Boundary
		}
		if raw == nil {
			fields["boundary"] = reasonRequired
			break
		}
		canonical, err := cities.ValidateZoneGeometry(ctx, q, raw)
		if err != nil {
			var he *httpx.Error
			if !errors.As(err, &he) || he.Fields == nil {
				return nil, nil, err
			}
			fields["boundary"] = he.Fields["geometry"]
			break
		}
		boundary = &canonical
		a.Boundary = json.RawMessage(canonical)
	}
	return fields, boundary, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (h *AdminHandler) list(w http.ResponseWriter, r *http.Request) error {
	items, err := List(r.Context(), h.pool)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, map[string]any{"items": items})
}

func (h *AdminHandler) create(w http.ResponseWriter, r *http.Request) error {
	var req areaRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	var created Area
	err := db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		a := Area{IsActive: true}
		fields, boundary, err := req.apply(ctx, tx, &a, true)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO commission_areas AS a (name, commission_bps, shape, center_lat, center_lon, radius_m, boundary, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, ST_SetSRID(ST_GeomFromGeoJSON($7::text), 4326), $8)
			RETURNING `+columns,
			a.Name, a.CommissionBps, a.Shape, a.Center.Lat, a.Center.Lon, a.RadiusM, boundary, a.IsActive)
		if created, err = scan(row); err != nil {
			return storeError(err)
		}
		return logAudit(ctx, tx, r, "commission_area.create", created.ID.String(), nil, created)
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, created)
}

func (h *AdminHandler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req areaRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	var updated Area
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		before, err := get(ctx, tx, id, true)
		if err != nil {
			return err
		}
		a := before
		fields, boundary, err := req.apply(ctx, tx, &a, false)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		row := tx.QueryRow(ctx, `
			UPDATE commission_areas AS a SET name = $2, commission_bps = $3, shape = $4, center_lat = $5, center_lon = $6,
			       radius_m = $7, boundary = ST_SetSRID(ST_GeomFromGeoJSON($8::text), 4326),
			       is_active = $9
			WHERE a.id = $1
			RETURNING `+columns,
			id, a.Name, a.CommissionBps, a.Shape, a.Center.Lat, a.Center.Lon, a.RadiusM, boundary, a.IsActive)
		if updated, err = scan(row); err != nil {
			return storeError(err)
		}
		return logAudit(ctx, tx, r, "commission_area.update", id.String(), before, updated)
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, updated)
}

func (h *AdminHandler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		before, err := get(ctx, tx, id, true)
		if err != nil {
			return err
		}
		// Orders keep the village name they were charged under (ON DELETE SET NULL).
		if _, err := tx.Exec(ctx, `DELETE FROM commission_areas WHERE id = $1`, id); err != nil {
			return httpx.Internal(err)
		}
		return logAudit(ctx, tx, r, "commission_area.delete", id.String(), before, nil)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// resolve answers which village commission an order picked up at a point
// would get (the same rule as order creation).
func (h *AdminHandler) resolve(w http.ResponseWriter, r *http.Request) error {
	p, err := pointQuery(r)
	if err != nil {
		return err
	}
	ref, dist, err := Resolve(r.Context(), h.pool, p)
	if err != nil {
		return httpx.Internal(err)
	}
	out := map[string]any{"area": nil, "distance_m": nil}
	if ref != nil {
		out["area"], out["distance_m"] = ref, dist
	}
	return httpx.OK(w, out)
}

// suggest proposes a circle radius for a village centre (see Suggest).
func (h *AdminHandler) suggest(w http.ResponseWriter, r *http.Request) error {
	p, err := pointQuery(r)
	if err != nil {
		return err
	}
	exclude, err := httpx.QueryUUID(r, "exclude_id")
	if err != nil {
		return err
	}
	ex := uuid.Nil
	if exclude != nil {
		ex = *exclude
	}
	s, err := Suggest(r.Context(), h.pool, h.geo, p, ex)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, s)
}

// search finds villages and places by name for the map editor.
func (h *AdminHandler) search(w http.ResponseWriter, r *http.Request) error {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	switch n := len([]rune(query)); {
	case n < searchMinChars:
		return httpx.Validation(map[string]string{"q": reasonTooShort})
	case n > MaxNameLen:
		return httpx.Validation(map[string]string{"q": reasonTooLong})
	}
	items := []Place{}
	if h.geo != nil {
		found, err := h.geo.SearchPlaces(r.Context(), query, adminLang(r), searchLimit)
		if err != nil {
			return httpx.Internal(err)
		}
		items = append(items, found...)
	}
	return httpx.OK(w, map[string]any{"items": items})
}

// pointQuery reads ?lat=&lon= (422 fields.lat/lon = required|invalid).
func pointQuery(r *http.Request) (geo.Point, error) {
	fields := map[string]string{}
	coord := func(name string) float64 {
		v := strings.TrimSpace(r.URL.Query().Get(name))
		if v == "" {
			fields[name] = reasonRequired
			return 0
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			fields[name] = reasonInvalid
		}
		return f
	}
	p := geo.Point{Lat: coord("lat"), Lon: coord("lon")}
	if len(fields) == 0 && p.Valid() != nil {
		fields["lat"] = reasonInvalid
	}
	if len(fields) > 0 {
		return geo.Point{}, httpx.Validation(fields)
	}
	return p, nil
}

// adminLang is the language for place names (?lang=kk|ru|en, default ru).
func adminLang(r *http.Request) string {
	switch l := r.URL.Query().Get("lang"); l {
	case "kk", "ru", "en":
		return l
	}
	return "ru"
}

// storeError maps constraint violations of an insert or update.
func storeError(err error) error {
	switch {
	case db.IsUniqueViolation(err, "commission_areas_name_unique"):
		return httpx.Validation(map[string]string{"name": reasonDuplicate})
	case db.PgError(err) != nil:
		return httpx.Validation(map[string]string{"commission_area": reasonInvalid}).WithCause(err)
	}
	return httpx.Internal(err)
}

func logAudit(ctx context.Context, q db.Querier, r *http.Request, action, id string, before, after any) error {
	if err := audit.Log(ctx, q, r, audit.Entry{Action: action, EntityType: "commission_area", EntityID: id, Before: before, After: after}); err != nil {
		return httpx.Internal(err)
	}
	return nil
}
