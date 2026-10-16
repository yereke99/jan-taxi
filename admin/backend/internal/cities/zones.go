package cities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// ErrZoneNotFound is returned for unknown zone ids.
var ErrZoneNotFound = httpx.NewError(http.StatusNotFound, "not_found", "zone not found")

// maxZonePoints bounds polygon complexity so ST_Covers stays cheap on the order path.
const maxZonePoints = 20000

// Zone is a service-area polygon of a city.
type Zone struct {
	ID        uuid.UUID       `json:"id"`
	CityID    uuid.UUID       `json:"city_id"`
	Name      string          `json:"name"`
	IsActive  bool            `json:"is_active"`
	Geometry  json.RawMessage `json:"geometry"` // GeoJSON MultiPolygon, WGS84
	AreaKM2   float64         `json:"area_km2"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

const zoneColumns = `z.id, z.city_id, z.name, z.is_active, ST_AsGeoJSON(z.geom, 6),
	round((ST_Area(z.geom::geography) / 1e6)::numeric, 3)::float8, z.created_at, z.updated_at`

func scanZone(s scanner) (Zone, error) {
	var z Zone
	var geom string
	err := s.Scan(&z.ID, &z.CityID, &z.Name, &z.IsActive, &geom, &z.AreaKM2, &z.CreatedAt, &z.UpdatedAt)
	z.Geometry = json.RawMessage(geom)
	return z, err
}

// GetZone loads one zone.
func GetZone(ctx context.Context, q db.Querier, id uuid.UUID) (Zone, error) {
	z, err := scanZone(q.QueryRow(ctx, `SELECT `+zoneColumns+` FROM service_zones z WHERE z.id = $1`, id))
	if db.IsNoRows(err) {
		return Zone{}, ErrZoneNotFound
	}
	if err != nil {
		return Zone{}, fmt.Errorf("load zone: %w", err)
	}
	return z, nil
}

// ListZones returns all zones of a city, active first.
func ListZones(ctx context.Context, q db.Querier, cityID uuid.UUID) ([]Zone, error) {
	rows, err := q.Query(ctx, `SELECT `+zoneColumns+` FROM service_zones z WHERE z.city_id = $1
		ORDER BY z.is_active DESC, z.name, z.created_at`, cityID)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	defer rows.Close()
	out := []Zone{}
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, fmt.Errorf("scan zone: %w", err)
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// geoJSONPolygon is the accepted input shape; foreign members such as "crs" are
// rejected because PostGIS would honour them and silently reproject.
type geoJSONPolygon struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
	BBox        json.RawMessage `json:"bbox,omitempty"`
}

// ValidateZoneGeometry checks a GeoJSON Polygon/MultiPolygon: well-formed,
// valid (ST_IsValid), WGS84 ranges, bounded size, intersecting Kazakhstan. It
// returns the canonical GeoJSON text to store. It must run outside the write
// transaction: a malformed geometry raises a database error that would abort it.
func ValidateZoneGeometry(ctx context.Context, q db.Querier, raw json.RawMessage) (string, error) {
	invalid := func(reason string) error {
		return httpx.Validation(map[string]string{"geometry": reason})
	}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return "", invalid("required")
	}
	var g geoJSONPolygon
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return "", invalid("invalid")
	}
	if g.Type != "Polygon" && g.Type != "MultiPolygon" {
		return "", invalid("unsupported_type")
	}
	if len(g.Coordinates) == 0 {
		return "", invalid("invalid")
	}
	clean, err := json.Marshal(geoJSONPolygon{Type: g.Type, Coordinates: g.Coordinates})
	if err != nil {
		return "", invalid("invalid")
	}
	var (
		valid, empty, inRange, inKZ bool
		points                      int
		canonical                   string
	)
	err = q.QueryRow(ctx, `
		WITH g AS (SELECT ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON($1), 4326)) AS geom)
		SELECT ST_IsValid(geom), ST_IsEmpty(geom), ST_NPoints(geom),
		       ST_XMin(geom) >= -180 AND ST_XMax(geom) <= 180 AND ST_YMin(geom) >= -90 AND ST_YMax(geom) <= 90,
		       EXISTS (SELECT 1 FROM country_boundaries k WHERE k.code = 'KZ' AND ST_Intersects(k.geom_check, g.geom)),
		       ST_AsGeoJSON(geom, 6)
		FROM g`, string(clean)).Scan(&valid, &empty, &points, &inRange, &inKZ, &canonical)
	if err != nil {
		if db.PgError(err) != nil {
			return "", invalid("invalid")
		}
		return "", httpx.Internal(err)
	}
	switch {
	case empty:
		return "", invalid("empty")
	case !inRange:
		return "", invalid("out_of_range")
	case points > maxZonePoints:
		return "", invalid("too_complex")
	case !valid:
		return "", invalid("invalid_geometry")
	case !inKZ:
		return "", invalid("outside_service_area")
	}
	return canonical, nil
}

// zoneInsert stores a validated zone and returns it.
func zoneInsert(ctx context.Context, q db.Querier, cityID uuid.UUID, name, geometry string, active bool) (Zone, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO service_zones (city_id, name, geom, is_active)
		VALUES ($1, $2, ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON($3), 4326)), $4)
		RETURNING id`, cityID, name, geometry, active).Scan(&id)
	if db.IsForeignKeyViolation(err, "") {
		return Zone{}, ErrNotFound
	}
	if err != nil {
		return Zone{}, fmt.Errorf("insert zone: %w", err)
	}
	return GetZone(ctx, q, id)
}
