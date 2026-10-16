// Package geo holds the coordinate type used across the API, the Kazakhstan
// service-area check (every stop and driver location must lie inside the
// country, ARCHITECTURE §3) and great-circle distance.
package geo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Point is a WGS84 coordinate, serialised as {"lat": 48.8264, "lon": 58.1442}.
type Point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// ErrInvalidPoint means a coordinate is not a finite latitude/longitude pair.
var ErrInvalidPoint = errors.New("invalid coordinates")

// Valid checks that the coordinate is finite and within WGS84 ranges.
func (p Point) Valid() error {
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lon) || math.IsInf(p.Lat, 0) || math.IsInf(p.Lon, 0) {
		return ErrInvalidPoint
	}
	if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return ErrInvalidPoint
	}
	return nil
}

// Rounded returns the point rounded to 6 decimals (about 0.1 m), the
// precision used in API responses.
func (p Point) Rounded() Point {
	return Point{Lat: math.Round(p.Lat*1e6) / 1e6, Lon: math.Round(p.Lon*1e6) / 1e6}
}

func (p Point) String() string { return fmt.Sprintf("%.6f,%.6f", p.Lat, p.Lon) }

// Kazakhstan's extent (lon 46.49–87.32, lat 40.57–55.44) padded by about half a
// degree. Points outside this box are rejected without a database round trip;
// points inside are checked against the real boundary.
const (
	kzMinLat, kzMaxLat = 40.0, 56.0
	kzMinLon, kzMaxLon = 46.0, 88.0
)

func inKZBox(p Point) bool {
	return p.Lat >= kzMinLat && p.Lat <= kzMaxLat && p.Lon >= kzMinLon && p.Lon <= kzMaxLon
}

// The boundary column geom_check is the Natural Earth line buffered by 3 km,
// so border towns are not rejected because of generalisation.
const coversSQL = `
SELECT EXISTS (
    SELECT 1 FROM country_boundaries
    WHERE code = 'KZ' AND ST_Covers(geom_check, ST_SetSRID(ST_MakePoint($2, $1), 4326))
)`

// InServiceArea reports whether p lies inside Kazakhstan. Invalid coordinates
// return ErrInvalidPoint. A missing boundary row makes every point outside
// (fail closed).
func InServiceArea(ctx context.Context, q db.Querier, p Point) (bool, error) {
	if err := p.Valid(); err != nil {
		return false, err
	}
	if !inKZBox(p) {
		return false, nil
	}
	var ok bool
	if err := q.QueryRow(ctx, coversSQL, p.Lat, p.Lon).Scan(&ok); err != nil {
		return false, fmt.Errorf("service area check: %w", err)
	}
	return ok, nil
}

// firstOutsideSQL returns the 1-based ordinal of the first point not covered
// by the Kazakhstan boundary, or no row when all points are inside.
const firstOutsideSQL = `
SELECT p.i
FROM unnest($1::float8[], $2::float8[]) WITH ORDINALITY AS p(lat, lon, i)
WHERE NOT EXISTS (
    SELECT 1 FROM country_boundaries b
    WHERE b.code = 'KZ' AND ST_Covers(b.geom_check, ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326))
)
ORDER BY p.i
LIMIT 1`

// RequireInServiceArea checks every point in one query. It returns:
//   - 422 validation_failed {fields: {field: "invalid_coordinates"}, details: {field, index}}
//     for a malformed coordinate;
//   - 422 outside_service_area {details: {field, index}} for the first point
//     outside Kazakhstan (index is 0-based in pts);
//   - nil when all points are inside.
func RequireInServiceArea(ctx context.Context, q db.Querier, field string, pts ...Point) error {
	if len(pts) == 0 {
		return nil
	}
	lats := make([]float64, len(pts))
	lons := make([]float64, len(pts))
	for i, p := range pts {
		if err := p.Valid(); err != nil {
			return httpx.Validation(map[string]string{field: "invalid_coordinates"}).
				WithDetails(map[string]any{"field": field, "index": i})
		}
		if !inKZBox(p) {
			return OutsideServiceArea(field, i)
		}
		lats[i], lons[i] = p.Lat, p.Lon
	}
	var ordinal int64
	err := q.QueryRow(ctx, firstOutsideSQL, lats, lons).Scan(&ordinal)
	switch {
	case db.IsNoRows(err):
		return nil
	case err != nil:
		return httpx.Internal(fmt.Errorf("service area check: %w", err))
	default:
		return OutsideServiceArea(field, int(ordinal-1))
	}
}

// OutsideServiceArea builds the 422 outside_service_area error.
func OutsideServiceArea(field string, index int) *httpx.Error {
	return httpx.NewError(http.StatusUnprocessableEntity, "outside_service_area", "location is outside the service area").
		WithDetails(map[string]any{"field": field, "index": index})
}

// earthRadiusM is the IUGG mean Earth radius.
const earthRadiusM = 6371008.8

// DistanceMeters returns the great-circle (haversine) distance between a and b.
func DistanceMeters(a, b Point) float64 {
	const rad = math.Pi / 180
	lat1, lat2 := a.Lat*rad, b.Lat*rad
	dLat := (b.Lat - a.Lat) * rad
	dLon := (b.Lon - a.Lon) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}
