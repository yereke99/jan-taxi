package cities

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

var cityCodeRe = regexp.MustCompile(`^[A-Z]{3}$`)

// Timezones lists the IANA zones valid for a Kazakhstan city. A fixed list
// keeps validation independent of the tzdata present in the container.
var Timezones = []string{"Asia/Almaty", "Asia/Aqtau", "Asia/Aqtobe", "Asia/Atyrau", "Asia/Oral", "Asia/Qostanay", "Asia/Qyzylorda"}

type cityRequest struct {
	Code                  *string     `json:"code"`
	Name                  *Name       `json:"name"`
	Center                *pointInput `json:"center"`
	DefaultZoom           *float64    `json:"default_zoom"`
	Timezone              *string     `json:"timezone"`
	IsActive              *bool       `json:"is_active"`
	DispatchRadiusM       *int        `json:"dispatch_radius_m"`
	RequireDriverLocation *bool       `json:"require_driver_location"`
	DriverLocationMaxAgeS *int        `json:"driver_location_max_age_s"`
	SearchTimeoutS        *int        `json:"search_timeout_s"`
}

// newCityDefaults mirrors the column defaults of the cities table.
func newCityDefaults() City {
	return City{DefaultZoom: 13, Timezone: "Asia/Aqtobe", IsActive: true, DispatchRadiusM: 50000,
		DriverLocationMaxAgeS: 900, SearchTimeoutS: 900}
}

// apply merges req into c and validates the result; creating requires code,
// name and center.
func (req cityRequest) apply(ctx context.Context, q db.Querier, c *City, creating bool) (map[string]string, error) {
	fields := map[string]string{}
	if req.Code != nil {
		code := strings.ToUpper(strings.TrimSpace(*req.Code))
		switch {
		case !cityCodeRe.MatchString(code):
			fields["code"] = reasonInvalid
		case !creating && code != c.Code:
			// Driver codes (EMB001) and DEFAULT_CITY embed the city code.
			fields["code"] = reasonImmutable
		default:
			c.Code = code
		}
	} else if creating {
		fields["code"] = reasonRequired
	}
	if req.Name != nil || creating {
		c.Name = cleanNames(req.Name, "name", fields)
	}
	centerChanged := false
	if req.Center != nil || creating {
		if p, ok := req.Center.point(); !ok {
			fields["center"] = reasonInvalid
		} else {
			c.Center, centerChanged = p, true
		}
	}
	if req.DefaultZoom != nil {
		if !finiteIn(*req.DefaultZoom, 3, 20) {
			fields["default_zoom"] = reasonOutOfRange
		} else {
			c.DefaultZoom = float64(int(*req.DefaultZoom*100+0.5)) / 100
		}
	}
	if req.Timezone != nil {
		if tz := strings.TrimSpace(*req.Timezone); slices.Contains(Timezones, tz) {
			c.Timezone = tz
		} else {
			fields["timezone"] = reasonInvalid
		}
	}
	if req.IsActive != nil {
		c.IsActive = *req.IsActive
	}
	if req.RequireDriverLocation != nil {
		c.RequireDriverLocation = *req.RequireDriverLocation
	}
	intField := func(name string, v *int, lo, hi int, dst *int) {
		if v == nil {
			return
		}
		if !inRange(*v, lo, hi) {
			fields[name] = reasonOutOfRange
			return
		}
		*dst = *v
	}
	intField("dispatch_radius_m", req.DispatchRadiusM, 500, 1_000_000, &c.DispatchRadiusM)
	intField("driver_location_max_age_s", req.DriverLocationMaxAgeS, 30, 86400, &c.DriverLocationMaxAgeS)
	intField("search_timeout_s", req.SearchTimeoutS, 60, 86400, &c.SearchTimeoutS)

	if centerChanged && fields["center"] == "" {
		ok, err := geo.InServiceArea(ctx, q, c.Center)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		if !ok {
			fields["center"] = reasonOutside
		}
	}
	return fields, nil
}

func (h *AdminHandler) listCities(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	items, total, err := List(r.Context(), h.pool, page)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, httpx.Page[City]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

var errCityCodeTaken = httpx.Conflict("city_code_taken", "a city with this code already exists")

func (h *AdminHandler) createCity(w http.ResponseWriter, r *http.Request) error {
	var req cityRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	c := newCityDefaults()
	fields, err := req.apply(ctx, h.pool, &c, true)
	if err != nil {
		return err
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	var created City
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO cities AS c (code, name_kk, name_ru, name_en, center_lat, center_lon, default_zoom, timezone,
			                         is_active, dispatch_radius_m, require_driver_location, driver_location_max_age_s,
			                         search_timeout_s)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			RETURNING `+cityColumns,
			c.Code, c.Name.KK, c.Name.RU, c.Name.EN, c.Center.Lat, c.Center.Lon, c.DefaultZoom, c.Timezone,
			c.IsActive, c.DispatchRadiusM, c.RequireDriverLocation, c.DriverLocationMaxAgeS, c.SearchTimeoutS)
		var err error
		if created, err = scanCity(row); err != nil {
			if db.IsUniqueViolation(err, "") {
				return errCityCodeTaken
			}
			return httpx.Internal(err)
		}
		return logAudit(ctx, tx, r, "city.create", "city", created.ID.String(), nil, created)
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, created)
}

func (h *AdminHandler) updateCity(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req cityRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	var updated City
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		before, err := getCity(ctx, tx, "c.id = $1 FOR UPDATE", id)
		if err != nil {
			return err
		}
		c := before
		fields, err := req.apply(ctx, tx, &c, false)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		row := tx.QueryRow(ctx, `
			UPDATE cities AS c SET name_kk = $2, name_ru = $3, name_en = $4, center_lat = $5, center_lon = $6,
			       default_zoom = $7, timezone = $8, is_active = $9, dispatch_radius_m = $10,
			       require_driver_location = $11, driver_location_max_age_s = $12, search_timeout_s = $13
			WHERE c.id = $1
			RETURNING `+cityColumns,
			id, c.Name.KK, c.Name.RU, c.Name.EN, c.Center.Lat, c.Center.Lon, c.DefaultZoom, c.Timezone,
			c.IsActive, c.DispatchRadiusM, c.RequireDriverLocation, c.DriverLocationMaxAgeS, c.SearchTimeoutS)
		if updated, err = scanCity(row); err != nil {
			return httpx.Internal(err)
		}
		return logAudit(ctx, tx, r, "city.update", "city", id.String(), before, updated)
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, updated)
}

func logAudit(ctx context.Context, q db.Querier, r *http.Request, action, entityType, entityID string, before, after any) error {
	if err := audit.Log(ctx, q, r, audit.Entry{Action: action, EntityType: entityType, EntityID: entityID, Before: before, After: after}); err != nil {
		return httpx.Internal(err)
	}
	return nil
}
