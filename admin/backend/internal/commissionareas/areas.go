// Package commissionareas holds the villages with their own driver commission
// (ARCHITECTURE §22.9). An administrator draws a village as a circle around
// its centre (the radius proposed from OpenStreetMap) or as a boundary, and
// sets a percentage of the fare. An order whose pickup A lies inside an active
// area is snapshotted with that percentage when it is created and charged when
// a driver takes it; where areas overlap, the village whose centre is nearest
// to A wins. Outside every area the general commission (settings) applies.
// Passenger prices are not affected and drivers never see the commission.
package commissionareas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Shapes of an area (commission_areas.shape).
const (
	ShapeCircle  = "circle"
	ShapePolygon = "polygon"
)

// Limits (commission_areas CHECK constraints).
const (
	MaxNameLen       = 120
	MaxCommissionBps = 5000 // 50 %
	MinRadiusM       = 200
	MaxRadiusM       = 50_000
)

// ErrNotFound is the 404 for an unknown area.
var ErrNotFound = httpx.NewError(http.StatusNotFound, "not_found", "commission area not found")

// Area is one village with its commission (OpenAPI CommissionArea).
type Area struct {
	ID            uuid.UUID       `json:"id"`
	Name          string          `json:"name"`
	CommissionBps int             `json:"commission_bps"`
	Shape         string          `json:"shape"`
	Center        geo.Point       `json:"center"`
	RadiusM       *int            `json:"radius_m"`
	Boundary      json.RawMessage `json:"boundary"`
	IsActive      bool            `json:"is_active"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Ref is the area an order belongs to.
type Ref struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	CommissionBps int       `json:"commission_bps"`
}

const columns = `a.id, a.name, a.commission_bps, a.shape, a.center_lat, a.center_lon, a.radius_m,
	ST_AsGeoJSON(a.boundary, 6), a.is_active, a.created_at, a.updated_at`

func scan(row pgx.Row) (Area, error) {
	var a Area
	var boundary *string
	err := row.Scan(&a.ID, &a.Name, &a.CommissionBps, &a.Shape, &a.Center.Lat, &a.Center.Lon, &a.RadiusM,
		&boundary, &a.IsActive, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return Area{}, err
	}
	a.Boundary = json.RawMessage(`null`)
	if boundary != nil {
		a.Boundary = json.RawMessage(*boundary)
	}
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return a, nil
}

// List returns every area, active or not, by name.
func List(ctx context.Context, q db.Querier) ([]Area, error) {
	rows, err := q.Query(ctx, `SELECT `+columns+` FROM commission_areas a ORDER BY lower(a.name), a.id`)
	if err != nil {
		return nil, fmt.Errorf("commission areas: list: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Area, error) { return scan(row) })
	if err != nil {
		return nil, fmt.Errorf("commission areas: list: %w", err)
	}
	if out == nil {
		out = []Area{}
	}
	return out, nil
}

// get returns one area, locked for update when lock is set.
func get(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (Area, error) {
	sql := `SELECT ` + columns + ` FROM commission_areas a WHERE a.id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	a, err := scan(q.QueryRow(ctx, sql, id))
	if db.IsNoRows(err) {
		return Area{}, ErrNotFound
	}
	if err != nil {
		return Area{}, fmt.Errorf("commission areas: get: %w", err)
	}
	return a, nil
}

// prefilterM bounds the indexed search for circles: no circle is larger.
const prefilterM = MaxRadiusM

// Resolve returns the active area an order picked up at p belongs to, with
// the distance from p to the area's centre, or nil outside every area. A
// circle covers the points within its radius of the centre, a boundary the
// points inside it; of several covering areas the one whose centre is nearest
// to p wins (equal distances: by name).
func Resolve(ctx context.Context, q db.Querier, p geo.Point) (*Ref, int, error) {
	if err := p.Valid(); err != nil {
		return nil, 0, nil
	}
	var ref Ref
	var dist float64
	err := q.QueryRow(ctx, `
		WITH pt AS (SELECT ST_SetSRID(ST_MakePoint($2, $1), 4326) AS g)
		SELECT a.id, a.name, a.commission_bps, ST_Distance(a.center, pt.g::geography)
		FROM commission_areas a, pt
		WHERE a.is_active
		  AND ((a.shape = 'circle' AND ST_DWithin(a.center, pt.g::geography, $3)
		        AND ST_DWithin(a.center, pt.g::geography, a.radius_m))
		    OR (a.shape = 'polygon' AND ST_Covers(a.boundary, pt.g)))
		ORDER BY 4, lower(a.name), a.id
		LIMIT 1`, p.Lat, p.Lon, prefilterM).Scan(&ref.ID, &ref.Name, &ref.CommissionBps, &dist)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("commission areas: resolve: %w", err)
	}
	return &ref, int(dist + 0.5), nil
}

// Neighbor is the nearest other area centre to a point.
type Neighbor struct {
	Name      string
	DistanceM float64
}

// NearestCenter returns the active area whose centre is nearest to p,
// skipping exclude (the area being edited), or nil when there is none.
func NearestCenter(ctx context.Context, q db.Querier, p geo.Point, exclude uuid.UUID) (*Neighbor, error) {
	var n Neighbor
	err := q.QueryRow(ctx, `
		SELECT a.name, ST_Distance(a.center, ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography) AS d
		FROM commission_areas a
		WHERE a.is_active AND a.id <> $3
		ORDER BY a.center <-> ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography
		LIMIT 1`, p.Lat, p.Lon, exclude).Scan(&n.Name, &n.DistanceM)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("commission areas: nearest: %w", err)
	}
	return &n, nil
}
