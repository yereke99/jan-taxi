package commissionareas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// checkRequest is a village being drawn, possibly incomplete: a name only,
// a point only, or the whole circle or boundary.
type checkRequest struct {
	Name      *string          `json:"name"`
	Shape     *string          `json:"shape"`
	Center    *geo.Point       `json:"center"`
	RadiusM   *int64           `json:"radius_m"`
	Boundary  *json.RawMessage `json:"boundary"`
	ExcludeID *uuid.UUID       `json:"exclude_id"`
}

// CheckArea is an existing village in a check answer. DistanceM is from the
// checked centre to its centre (null without a centre).
type CheckArea struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	CommissionBps int       `json:"commission_bps"`
	IsActive      bool      `json:"is_active"`
	DistanceM     *int      `json:"distance_m"`
}

// CheckResult tells the administrator whether the village being drawn is
// already in the database (OpenAPI CommissionAreaCheck): a village of the
// same name (any status), villages whose area already holds the centre,
// villages the new circle or boundary overlaps, and the nearest other one.
// Overlaps are allowed — the nearest centre decides (§22.9) — so nothing
// here refuses a save; a taken name is refused by the save itself.
type CheckResult struct {
	NameTaken   *CheckArea  `json:"name_taken"`
	Covering    []CheckArea `json:"covering"`
	Overlapping []CheckArea `json:"overlapping"`
	Nearest     *CheckArea  `json:"nearest"`
}

// newShape is the drawn area in SQL terms: a circle (Center and RadiusM) or a
// boundary (canonical GeoJSON); none while only a point or a name is known.
type newShape struct {
	circle   bool
	radiusM  int64
	boundary string
}

// parse validates the request leniently: what is present must be valid, what
// is missing is simply not checked.
func (req checkRequest) parse(ctx context.Context, q db.Querier) (name string, center *geo.Point, shape *newShape, err error) {
	fields := map[string]string{}
	if req.Name != nil {
		switch n, cerr := textutil.CleanText(*req.Name, MaxNameLen); {
		case errors.Is(cerr, textutil.ErrTooLong):
			fields["name"] = reasonTooLong
		case cerr != nil:
			fields["name"] = reasonInvalid
		default:
			name = n
		}
	}
	if req.Center != nil {
		if req.Center.Valid() != nil {
			fields["center"] = reasonInvalid
		} else {
			c := *req.Center
			center = &c
		}
	}
	if req.Shape != nil {
		switch *req.Shape {
		case ShapeCircle:
			if req.RadiusM != nil {
				if *req.RadiusM < MinRadiusM || *req.RadiusM > MaxRadiusM {
					fields["radius_m"] = reasonOutOfRange
				} else if center != nil {
					shape = &newShape{circle: true, radiusM: *req.RadiusM}
				}
			}
		case ShapePolygon:
			if req.Boundary != nil {
				canonical, verr := cities.ValidateZoneGeometry(ctx, q, *req.Boundary)
				var he *httpx.Error
				switch {
				case verr == nil:
					shape = &newShape{boundary: canonical}
				case errors.As(verr, &he) && he.Fields != nil:
					fields["boundary"] = he.Fields["geometry"]
				default:
					return "", nil, nil, verr
				}
			}
		default:
			fields["shape"] = reasonInvalid
		}
	}
	if len(fields) > 0 {
		return "", nil, nil, httpx.Validation(fields)
	}
	return name, center, shape, nil
}

// Check runs the database checks for a village being drawn (see CheckResult).
func Check(ctx context.Context, q db.Querier, name string, center *geo.Point, shape *newShape, exclude uuid.UUID) (CheckResult, error) {
	out := CheckResult{Covering: []CheckArea{}, Overlapping: []CheckArea{}}
	if name != "" {
		a, err := oneArea(ctx, q, `
			SELECT a.id, a.name, a.commission_bps, a.is_active, NULL::float8
			FROM commission_areas a WHERE lower(a.name) = lower($1) AND a.id <> $2
			LIMIT 1`, name, exclude)
		if err != nil {
			return CheckResult{}, err
		}
		out.NameTaken = a
	}
	// The distance column is from the checked centre when there is one.
	lat, lon := 0.0, 0.0
	if center != nil {
		lat, lon = center.Lat, center.Lon
	}
	if center != nil {
		covering, err := areas(ctx, q, `
			WITH pt AS (SELECT ST_SetSRID(ST_MakePoint($2, $1), 4326) AS g)
			SELECT a.id, a.name, a.commission_bps, a.is_active, ST_Distance(a.center, pt.g::geography)
			FROM commission_areas a, pt
			WHERE a.id <> $3
			  AND ((a.shape = 'circle' AND ST_DWithin(a.center, pt.g::geography, a.radius_m))
			    OR (a.shape = 'polygon' AND ST_Covers(a.boundary, pt.g)))
			ORDER BY 5, lower(a.name)`, lat, lon, exclude)
		if err != nil {
			return CheckResult{}, err
		}
		out.Covering = covering
		nearest, err := oneArea(ctx, q, `
			WITH pt AS (SELECT ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography AS g)
			SELECT a.id, a.name, a.commission_bps, a.is_active, ST_Distance(a.center, pt.g)
			FROM commission_areas a, pt
			WHERE a.id <> $3
			ORDER BY a.center <-> pt.g
			LIMIT 1`, lat, lon, exclude)
		if err != nil {
			return CheckResult{}, err
		}
		out.Nearest = nearest
	}
	if shape != nil {
		var (
			overlapping []CheckArea
			err         error
		)
		if shape.circle {
			// Two circles overlap when their centres are closer than the
			// sum of the radii; a circle meets a boundary within its radius.
			overlapping, err = areas(ctx, q, `
				WITH pt AS (SELECT ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography AS g)
				SELECT a.id, a.name, a.commission_bps, a.is_active, ST_Distance(a.center, pt.g)
				FROM commission_areas a, pt
				WHERE a.id <> $3
				  AND ((a.shape = 'circle' AND ST_DWithin(a.center, pt.g, a.radius_m + $4))
				    OR (a.shape = 'polygon' AND ST_DWithin(a.boundary::geography, pt.g, $4)))
				ORDER BY 5, lower(a.name)`, lat, lon, exclude, shape.radiusM)
		} else {
			overlapping, err = areas(ctx, q, `
				WITH b AS (SELECT ST_SetSRID(ST_GeomFromGeoJSON($4::text), 4326) AS g),
				     pt AS (SELECT ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography AS g)
				SELECT a.id, a.name, a.commission_bps, a.is_active,
				       CASE WHEN $5 THEN ST_Distance(a.center, pt.g) END
				FROM commission_areas a, b, pt
				WHERE a.id <> $3
				  AND ((a.shape = 'circle' AND ST_DWithin(b.g::geography, a.center, a.radius_m))
				    OR (a.shape = 'polygon' AND ST_Intersects(a.boundary, b.g)))
				ORDER BY 5 NULLS LAST, lower(a.name)`, lat, lon, exclude, shape.boundary, center != nil)
		}
		if err != nil {
			return CheckResult{}, err
		}
		// A village that already holds the centre is reported once, as covering.
		inside := map[uuid.UUID]bool{}
		for _, a := range out.Covering {
			inside[a.ID] = true
		}
		for _, a := range overlapping {
			if !inside[a.ID] {
				out.Overlapping = append(out.Overlapping, a)
			}
		}
	}
	return out, nil
}

func scanCheckArea(row pgx.Row) (CheckArea, error) {
	var a CheckArea
	var dist *float64
	if err := row.Scan(&a.ID, &a.Name, &a.CommissionBps, &a.IsActive, &dist); err != nil {
		return CheckArea{}, err
	}
	if dist != nil {
		d := int(*dist + 0.5)
		a.DistanceM = &d
	}
	return a, nil
}

func oneArea(ctx context.Context, q db.Querier, sql string, args ...any) (*CheckArea, error) {
	a, err := scanCheckArea(q.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("commission areas: check: %w", err)
	}
	return &a, nil
}

func areas(ctx context.Context, q db.Querier, sql string, args ...any) ([]CheckArea, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("commission areas: check: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CheckArea, error) { return scanCheckArea(row) })
	if err != nil {
		return nil, fmt.Errorf("commission areas: check: %w", err)
	}
	if out == nil {
		out = []CheckArea{}
	}
	return out, nil
}

// check answers POST /admin/commission-areas/check.
func (h *AdminHandler) check(w http.ResponseWriter, r *http.Request) error {
	var req checkRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	name, center, shape, err := req.parse(ctx, h.pool)
	if err != nil {
		return err
	}
	exclude := uuid.Nil
	if req.ExcludeID != nil {
		exclude = *req.ExcludeID
	}
	res, err := Check(ctx, h.pool, name, center, shape, exclude)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, res)
}
