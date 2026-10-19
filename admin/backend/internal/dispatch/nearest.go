package dispatch

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// Load is what a prospective order would carry: passengers of a taxi order
// (0 otherwise) and the cargo volume of a delivery (0 for taxi).
type Load struct {
	PassengerCount int
	CargoVolumeL   int
}

// Pickup estimates in the route preview (eta_s of each class option) must not
// reveal where a driver is. The passenger chooses pickup A freely and may ask
// for previews again and again, so an exact distance-based estimate would be
// a distance oracle: the points where it changes are rings around the
// nearest driver, and three of them give the driver's position. Therefore:
//   - the pickup is snapped to a grid of about 500 m (ETAGridLatDeg x
//     ETAGridLonDeg), so moving it inside a cell changes nothing;
//   - every driver's position is shifted by a pseudo-random offset of up to
//     ETAJitterM, fixed per driver for an hour (repeating a request cannot
//     average it away) and keyed by a secret made at process start;
//   - the estimate is rounded up to ETABucketS (5 minutes), and a driver
//     counts only when the shifted position is inside the base dispatch
//     radius from the snapped pickup.
//
// What remains visible is "some free driver within a few kilometres of this
// part of town, about 5 or 10 minutes away" — no finer than the estimate a
// passenger needs.
const (
	ETAGridLatDeg = 0.005  // ≈ 556 m
	ETAGridLonDeg = 0.0075 // ≈ 470–635 m in Kazakhstan (40.5°–55.5° N)
	ETAJitterM    = 500.0
	ETAJitterTTL  = time.Hour
	ETABucketS    = 300
)

// etaKey keys the per-driver offsets; it never leaves the process.
var etaKey = func() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic("dispatch: random key: " + err.Error())
	}
	return k
}()

// SnapETAPickup returns the centre of the grid cell that contains p.
func SnapETAPickup(p geo.Point) geo.Point {
	snap := func(v, step float64) float64 {
		return (math.Floor(v/step) + 0.5) * step
	}
	return geo.Point{Lat: snap(p.Lat, ETAGridLatDeg), Lon: snap(p.Lon, ETAGridLonDeg)}
}

// JitteredPosition shifts a driver's position by the driver's offset for
// the hour of now: up to ETAJitterM in a direction derived from an HMAC of
// the driver id and the hour, uniform over the disc.
func JitteredPosition(driverID uuid.UUID, at geo.Point, now time.Time) geo.Point {
	m := hmac.New(sha256.New, etaKey)
	m.Write(driverID[:])
	var slot [8]byte
	binary.BigEndian.PutUint64(slot[:], uint64(now.UTC().Truncate(ETAJitterTTL).Unix()))
	m.Write(slot[:])
	sum := m.Sum(nil)
	u1 := float64(binary.BigEndian.Uint32(sum[0:4])) / (1 << 32)
	u2 := float64(binary.BigEndian.Uint32(sum[4:8])) / (1 << 32)
	r := ETAJitterM * math.Sqrt(u1)
	theta := 2 * math.Pi * u2
	const metresPerDegLat = 111_320.0
	dLat := r * math.Cos(theta) / metresPerDegLat
	dLon := r * math.Sin(theta) / (metresPerDegLat * math.Max(math.Cos(at.Lat*math.Pi/180), 0.01))
	return geo.Point{Lat: at.Lat + dLat, Lon: at.Lon + dLon}
}

// PreviewETASeconds is the coarse pickup estimate for a driver distM metres
// away: one minute to set off plus 2 minutes per kilometre (30 km/h in town),
// rounded up to ETABucketS.
func PreviewETASeconds(distM float64) int {
	s := 60 + int(math.Ceil(math.Max(distM, 0)*0.12))
	return (s + ETABucketS - 1) / ETABucketS * ETABucketS
}

// PreviewETAs returns, for every class in classes, the coarse estimate (see
// above) of how long the nearest driver who would be offered such an order
// now needs to reach pickup: approved, online, not blocked, free, with a
// fresh location and a vehicle that serves the class and fits load. Classes
// without such a driver are absent. excludeUser (the passenger) is never
// counted. With a fixed commission (§22.2) the driver's available balance
// must cover it, since nobody else is offered the order; with a percentage
// the amount depends on the fare, which is not chosen yet.
func PreviewETAs(ctx context.Context, q db.Querier, pickup geo.Point, classes []string, load Load, excludeUser uuid.UUID) (map[string]int, error) {
	out := map[string]int{}
	if len(classes) == 0 {
		return out, nil
	}
	p := settings.Dispatch(ctx, q)
	var minAvailable int64
	if c := settings.Commission(ctx, q); c.Mode == settings.CommissionFixed {
		minAvailable = c.FixedAmount
	}
	cell := SnapETAPickup(pickup)
	// Candidates within the radius plus the largest offset; the exact test
	// on the shifted positions follows below.
	rows, err := q.Query(ctx, `
		WITH pk AS (SELECT ST_SetSRID(ST_MakePoint(@lon, @lat), 4326)::geography AS g)
		SELECT c.code, d.id, dl.lat, dl.lon
		FROM unnest(@classes::text[]) AS c(code)
		CROSS JOIN pk
		JOIN driver_locations dl ON ST_DWithin(dl.location, pk.g, @radius_base_m::float8 + @slack_m::float8)
		JOIN driver_profiles d ON d.id = dl.driver_id AND d.status = 'approved' AND d.is_online
		JOIN users u ON u.id = d.user_id AND NOT u.is_blocked
		JOIN vehicles v ON v.driver_id = d.id AND v.status <> 'archived'
		JOIN vehicle_classes vc ON vc.code = v.class_code
		WHERE `+freshLocation+`
		  AND d.user_id <> @exclude_user
		  AND c.code = ANY (vc.serves)
		  AND v.seats >= @passengers AND v.cargo_volume_l >= @cargo_l
		  AND NOT EXISTS (SELECT 1 FROM orders a WHERE a.driver_id = d.id
		                  AND a.status IN ('accepted', 'driver_arriving', 'in_progress'))
		  AND (@min_available::bigint <= 0 OR EXISTS (SELECT 1 FROM wallets w WHERE w.driver_id = d.id
		                  AND w.balance - w.reserved >= @min_available::bigint))`, policyArgs(p, pgx.NamedArgs{
		"lat": cell.Lat, "lon": cell.Lon, "slack_m": ETAJitterM + 10, "classes": classes, "exclude_user": excludeUser,
		"passengers": load.PassengerCount, "cargo_l": load.CargoVolumeL, "min_available": minAvailable,
	}))
	if err != nil {
		return nil, fmt.Errorf("dispatch: nearest drivers: %w", err)
	}
	defer rows.Close()
	now := time.Now()
	best := map[string]float64{}
	for rows.Next() {
		var (
			code     string
			driverID uuid.UUID
			at       geo.Point
		)
		if err := rows.Scan(&code, &driverID, &at.Lat, &at.Lon); err != nil {
			return nil, fmt.Errorf("dispatch: nearest drivers: %w", err)
		}
		d := geo.DistanceMeters(cell, JitteredPosition(driverID, at, now))
		if d > float64(p.RadiusBaseM) {
			continue
		}
		if cur, ok := best[code]; !ok || d < cur {
			best[code] = d
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dispatch: nearest drivers: %w", err)
	}
	for code, d := range best {
		out[code] = PreviewETASeconds(d)
	}
	return out, nil
}
