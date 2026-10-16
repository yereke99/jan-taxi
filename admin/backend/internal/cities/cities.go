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
	ErrInvalidPoint = &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "invalid coordinates", Fields: map[string]string{"point": "invalid"}}
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

// City is a service city with its dispatch policy.
type City struct {
	ID                    uuid.UUID
	Code                  string
	Name                  Name
	Center                geo.Point
	DefaultZoom           float64
	Timezone              string
	IsActive              bool
	DispatchRadiusM       int
	RequireDriverLocation bool
	DriverLocationMaxAgeS int
	SearchTimeoutS        int
	CreatedAt             time.Time
	UpdatedAt             time.Time
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
		Timezone: c.Timezone, IsActive: c.IsActive, DispatchRadiusM: c.DispatchRadiusM,
		RequireDriverLocation: c.RequireDriverLocation, DriverLocationMaxAgeS: c.DriverLocationMaxAgeS,
		SearchTimeoutS: c.SearchTimeoutS, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	})
}

const cityColumns = `c.id, c.code, c.name_kk, c.name_ru, c.name_en, c.center_lat, c.center_lon,
	c.default_zoom::float8, c.timezone, c.is_active, c.dispatch_radius_m, c.require_driver_location,
	c.driver_location_max_age_s, c.search_timeout_s, c.created_at, c.updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanCity(s scanner) (City, error) {
	var c City
	err := s.Scan(&c.ID, &c.Code, &c.Name.KK, &c.Name.RU, &c.Name.EN, &c.Center.Lat, &c.Center.Lon,
		&c.DefaultZoom, &c.Timezone, &c.IsActive, &c.DispatchRadiusM, &c.RequireDriverLocation,
		&c.DriverLocationMaxAgeS, &c.SearchTimeoutS, &c.CreatedAt, &c.UpdatedAt)
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

// Resolve picks the city responsible for p: the active city whose active
// service zone covers p (nearest centre wins if zones overlap), otherwise the
// nearest active city centre. It does not check the Kazakhstan boundary;
// callers validate points with geo.RequireInServiceArea first.
func Resolve(ctx context.Context, q db.Querier, p geo.Point) (City, error) {
	if !validPoint(p) {
		return City{}, ErrInvalidPoint
	}
	c, err := scanCity(q.QueryRow(ctx, `
		WITH pt AS (SELECT ST_SetSRID(ST_MakePoint($2, $1), 4326) AS g)
		SELECT `+cityColumns+`
		FROM cities c, pt
		WHERE c.is_active
		ORDER BY EXISTS (
		             SELECT 1 FROM service_zones z
		             WHERE z.city_id = c.id AND z.is_active AND ST_Covers(z.geom, pt.g)
		         ) DESC,
		         ST_Distance(c.center, pt.g::geography),
		         c.code
		LIMIT 1`, p.Lat, p.Lon))
	if db.IsNoRows(err) {
		return City{}, ErrNoActiveCity
	}
	if err != nil {
		return City{}, fmt.Errorf("resolve city: %w", err)
	}
	return c, nil
}

// Default returns the configured default city: ref may be a code ("EMB") or an
// English name ("Embi"). An unknown or inactive reference falls back to the
// oldest active city.
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
	c, err := getCity(ctx, q, `c.is_active ORDER BY c.created_at, c.code LIMIT 1`)
	if errors.Is(err, ErrNotFound) {
		return City{}, ErrNoActiveCity
	}
	return c, err
}
