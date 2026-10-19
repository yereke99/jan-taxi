// Package cities owns cities, their service zones, the admin-curated places
// gazetteer and the public runtime configuration served to the apps.
package cities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Errors are *httpx.Error values so handlers can return them unchanged.
var (
	ErrNotFound     = httpx.NewError(http.StatusNotFound, "not_found", "city not found")
	ErrNoActiveCity = httpx.NewError(http.StatusUnprocessableEntity, "no_active_city", "no active city is configured")
	// ErrDefaultRegionLocked refuses deactivating the nationwide default region.
	ErrDefaultRegionLocked = httpx.Conflict("default_region_locked", "the nationwide default region cannot be deactivated or deleted")
	ErrInvalidPoint        = &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "invalid coordinates", Fields: map[string]string{"point": "invalid"}}
)

// Name is a name in the three supported languages.
type Name struct {
	KK string `json:"kk"`
	RU string `json:"ru"`
	EN string `json:"en"`
}

// Get returns the name for lang, falling back to Kazakh.
func (n Name) Get(lang string) string {
	switch lang {
	case "ru":
		return n.RU
	case "en":
		return n.EN
	default:
		return n.KK
	}
}

// DefaultRegionCode is the code of the nationwide default region seeded by
// migration 00009: its zone is all of Kazakhstan and it prices every order
// whose pickup no smaller city zone covers.
const DefaultRegionCode = "KAZ"

// City is a service city or region: the tariff area of the orders resolved
// to it (Resolve), the prefix of its drivers' codes and a map centre. It does
// not restrict dispatch, which is by distance from the driver to the pickup
// (ARCHITECTURE §7, settings dispatch.*).
type City struct {
	ID          uuid.UUID
	Code        string
	Name        Name
	Center      geo.Point
	DefaultZoom float64
	Timezone    string
	IsActive    bool
	// IsDefaultRegion marks the nationwide region (KAZ): the fallback of
	// Resolve, never deactivated through the admin API.
	IsDefaultRegion bool
	// DispatchRadiusM, RequireDriverLocation and DriverLocationMaxAgeS are
	// legacy per-city dispatch fields. They are still stored and editable
	// but nothing reads them: the global dispatch.* settings apply.
	DispatchRadiusM       int
	RequireDriverLocation bool
	DriverLocationMaxAgeS int
	// SearchTimeoutS is how long an order resolved to this city searches
	// for a driver before search_expiry cancels it.
	SearchTimeoutS int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// LatLon is the API coordinate shape {"lat":…,"lon":…}.
type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

func toLatLon(p geo.Point) LatLon { return LatLon{Lat: round6(p.Lat), Lon: round6(p.Lon)} }

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

type cityJSON struct {
	ID                    uuid.UUID `json:"id"`
	Code                  string    `json:"code"`
	Name                  Name      `json:"name"`
	Center                LatLon    `json:"center"`
	DefaultZoom           float64   `json:"default_zoom"`
	Timezone              string    `json:"timezone"`
	IsActive              bool      `json:"is_active"`
	IsDefaultRegion       bool      `json:"is_default_region"`
	DispatchRadiusM       int       `json:"dispatch_radius_m"`
	RequireDriverLocation bool      `json:"require_driver_location"`
	DriverLocationMaxAgeS int       `json:"driver_location_max_age_s"`
	SearchTimeoutS        int       `json:"search_timeout_s"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// MarshalJSON renders the API representation.
func (c City) MarshalJSON() ([]byte, error) {
	return json.Marshal(cityJSON{
		ID: c.ID, Code: c.Code, Name: c.Name, Center: toLatLon(c.Center), DefaultZoom: c.DefaultZoom,
		Timezone: c.Timezone, IsActive: c.IsActive, IsDefaultRegion: c.IsDefaultRegion, DispatchRadiusM: c.DispatchRadiusM,
		RequireDriverLocation: c.RequireDriverLocation, DriverLocationMaxAgeS: c.DriverLocationMaxAgeS,
		SearchTimeoutS: c.SearchTimeoutS, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	})
}

const cityColumns = `c.id, c.code, c.name_kk, c.name_ru, c.name_en, c.center_lat, c.center_lon,
	c.default_zoom::float8, c.timezone, c.is_active, c.dispatch_radius_m, c.require_driver_location,
	c.driver_location_max_age_s, c.search_timeout_s, c.created_at, c.updated_at, c.is_default_region`

type scanner interface{ Scan(dest ...any) error }

func scanCity(s scanner) (City, error) {
	var c City
	err := s.Scan(&c.ID, &c.Code, &c.Name.KK, &c.Name.RU, &c.Name.EN, &c.Center.Lat, &c.Center.Lon,
		&c.DefaultZoom, &c.Timezone, &c.IsActive, &c.DispatchRadiusM, &c.RequireDriverLocation,
		&c.DriverLocationMaxAgeS, &c.SearchTimeoutS, &c.CreatedAt, &c.UpdatedAt, &c.IsDefaultRegion)
	return c, err
}

func getCity(ctx context.Context, q db.Querier, where string, args ...any) (City, error) {
	c, err := scanCity(q.QueryRow(ctx, `SELECT `+cityColumns+` FROM cities c WHERE `+where, args...))
	if db.IsNoRows(err) {
		return City{}, ErrNotFound
	}
	if err != nil {
		return City{}, fmt.Errorf("load city: %w", err)
	}
	return c, nil
}

// Get loads a city by id (active or not).
func Get(ctx context.Context, q db.Querier, id uuid.UUID) (City, error) {
	return getCity(ctx, q, "c.id = $1", id)
}

// GetByCode loads a city by its three-letter code (case-insensitive input).
func GetByCode(ctx context.Context, q db.Querier, code string) (City, error) {
	return getCity(ctx, q, "c.code = $1", strings.ToUpper(strings.TrimSpace(code)))
}

func listCities(ctx context.Context, q db.Querier, sql string, args ...any) ([]City, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list cities: %w", err)
	}
	defer rows.Close()
	out := []City{}
	for rows.Next() {
		c, err := scanCity(rows)
		if err != nil {
			return nil, fmt.Errorf("scan city: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListActive returns active cities ordered by code.
func ListActive(ctx context.Context, q db.Querier) ([]City, error) {
	return listCities(ctx, q, `SELECT `+cityColumns+` FROM cities c WHERE c.is_active ORDER BY c.code`)
}

// List returns one page of all cities (active and inactive) and the total.
func List(ctx context.Context, q db.Querier, page httpx.PageParams) ([]City, int, error) {
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM cities`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count cities: %w", err)
	}
	items, err := listCities(ctx, q, `SELECT `+cityColumns+` FROM cities c ORDER BY c.code LIMIT $1 OFFSET $2`,
		page.PageSize, page.Offset())
	return items, total, err
}

func validPoint(p geo.Point) bool {
	return !math.IsNaN(p.Lat) && !math.IsNaN(p.Lon) && !math.IsInf(p.Lat, 0) && !math.IsInf(p.Lon, 0) &&
		p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180
}

// Resolve picks the city responsible for p (the tariff region of an order
// with pickup p): the active city owning the smallest active service zone
// that covers p, so a configured city wins over the nationwide region inside
// its zone; otherwise the active default region (KAZ). It never falls back to
// the nearest city. It does not check the Kazakhstan boundary; callers
// validate points with geo.RequireInServiceArea first.
func Resolve(ctx context.Context, q db.Querier, p geo.Point) (City, error) {
	if !validPoint(p) {
		return City{}, ErrInvalidPoint
	}
	c, err := scanCity(q.QueryRow(ctx, `
		WITH pt AS (SELECT ST_SetSRID(ST_MakePoint($2, $1), 4326) AS g)
		SELECT `+cityColumns+`
		FROM pt
		JOIN service_zones z ON z.is_active AND ST_Covers(z.geom, pt.g)
		JOIN cities c ON c.id = z.city_id AND c.is_active
		ORDER BY ST_Area(z.geom) ASC, c.is_default_region ASC, c.code
		LIMIT 1`, p.Lat, p.Lon))
	if err == nil {
		return c, nil
	}
	if !db.IsNoRows(err) {
		return City{}, fmt.Errorf("resolve city: %w", err)
	}
	return DefaultRegion(ctx, q)
}

// DefaultRegion returns the active nationwide default region (KAZ), or
// ErrNoActiveCity when it is missing or inactive.
func DefaultRegion(ctx context.Context, q db.Querier) (City, error) {
	c, err := getCity(ctx, q, `c.is_default_region AND c.is_active ORDER BY c.created_at LIMIT 1`)
	if errors.Is(err, ErrNotFound) {
		return City{}, ErrNoActiveCity
	}
	return c, err
}

// Region is a short tariff-region reference (openapi TariffRegion): the
// city or nationwide region whose tariff priced an order.
type Region struct {
	Code string `json:"code"`
	Name Name   `json:"name"`
}

// Ref returns the short region reference of c.
func (c City) Ref() Region { return Region{Code: c.Code, Name: c.Name} }

// Default returns the configured default city: ref may be a code ("EMB") or an
// English name ("Embi"). An unknown or inactive reference falls back to the
// oldest active city, a real city before the nationwide region.
func Default(ctx context.Context, q db.Querier, ref string) (City, error) {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		c, err := getCity(ctx, q, `c.is_active AND (c.code = upper($1) OR lower(c.name_en) = lower($1))
			ORDER BY (c.code = upper($1)) DESC LIMIT 1`, ref)
		if err == nil {
			return c, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return City{}, err
		}
	}
	c, err := getCity(ctx, q, `c.is_active ORDER BY c.is_default_region, c.created_at, c.code LIMIT 1`)
	if errors.Is(err, ErrNotFound) {
		return City{}, ErrNoActiveCity
	}
	return c, err
}
