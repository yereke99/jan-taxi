package dispatch

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// Dispatch is radius based (ARCHITECTURE §7): a driver may take a searching
// order when their fresh location lies within the order's effective radius of
// the pickup point. Neither the driver's registered city nor the order's
// tariff city restricts dispatch.
//
// The SQL fragments below expect the order as "o", the driver's location row
// as "dl" and the named arguments of policyArgs.

// effectiveRadius is the dispatch radius of order o now, in metres:
// min(max, base + step * floor(search_age_s / step_s)).
const effectiveRadius = `LEAST(@radius_max_m::bigint, @radius_base_m::bigint + @radius_step_m::bigint *
	floor(GREATEST(extract(epoch FROM now() - o.created_at), 0) / @radius_step_s::bigint)::bigint)`

// freshLocation holds when the driver's last fix is recent enough to dispatch on.
const freshLocation = `dl.recorded_at > now() - make_interval(secs => @location_max_age_s::int)`

// withinRadius holds when the driver's location is inside the effective
// radius of o. The first, constant-distance test lets PostGIS use the spatial
// indexes; the second applies the order's own radius.
const withinRadius = `ST_DWithin(dl.location, o.pickup, @radius_max_m::float8)
	AND ST_DWithin(dl.location, o.pickup, (` + effectiveRadius + `)::float8)`

// distanceToPickup is the rounded distance from the driver to the pickup of o.
const distanceToPickup = `round(ST_Distance(dl.location, o.pickup))::int`

// vehicleFits holds when the current vehicle of driver d can take order o
// (ARCHITECTURE §21.4): the vehicle's class serves the order's class, it has
// at least as many seats as the order has passengers and at least as much
// cargo space as the order's cargo needs. A vehicle without a class fits
// nothing.
const vehicleFits = `EXISTS (
	SELECT 1 FROM vehicles fv JOIN vehicle_classes fvc ON fvc.code = fv.class_code
	WHERE fv.driver_id = d.id AND fv.status <> 'archived'
	  AND o.vehicle_class = ANY (fvc.serves)
	  AND fv.seats >= COALESCE(o.passenger_count, 0)
	  AND fv.cargo_volume_l >= o.cargo_volume_l)`

// policyArgs returns the named arguments of the radius fragments plus extra.
func policyArgs(p settings.DispatchSettings, extra pgx.NamedArgs) pgx.NamedArgs {
	args := pgx.NamedArgs{
		"radius_base_m":      p.RadiusBaseM,
		"radius_step_m":      p.RadiusStepM,
		"radius_step_s":      max(p.RadiusStepS, 1),
		"radius_max_m":       max(p.RadiusMaxM, p.RadiusBaseM),
		"location_max_age_s": p.LocationMaxAgeS,
	}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

// reach is where a driver stands relative to one order's pickup.
type reach struct {
	HasLocation bool
	Fresh       bool
	Within      bool
	DistanceM   *int
	RadiusM     int
}

// problem is the first location reason that keeps the driver from the order,
// or "" when the driver may take it.
func (r reach) problem() string {
	switch {
	case !r.HasLocation:
		return ReasonLocationRequired
	case !r.Fresh:
		return ReasonLocationStale
	case !r.Within:
		return ReasonOutOfRadius
	}
	return ""
}

// loadReach measures driverID's last location against the pickup and the
// current effective radius of orderID.
func loadReach(ctx context.Context, q db.Querier, orderID, driverID uuid.UUID, p settings.DispatchSettings) (reach, error) {
	var r reach
	err := q.QueryRow(ctx, `
		SELECT dl.driver_id IS NOT NULL,
		       COALESCE(`+freshLocation+`, false),
		       COALESCE(`+withinRadius+`, false),
		       CASE WHEN dl.location IS NULL THEN NULL ELSE `+distanceToPickup+` END,
		       (`+effectiveRadius+`)::int
		FROM orders o
		LEFT JOIN driver_locations dl ON dl.driver_id = @driver_id
		WHERE o.id = @order_id`,
		policyArgs(p, pgx.NamedArgs{"order_id": orderID, "driver_id": driverID})).
		Scan(&r.HasLocation, &r.Fresh, &r.Within, &r.DistanceM, &r.RadiusM)
	if err != nil {
		return reach{}, fmt.Errorf("dispatch: driver reach: %w", err)
	}
	return r, nil
}

// ReasonVehicleMismatch tells an operator that the driver's vehicle cannot
// take the order: its class does not serve the order's class, or it has too
// few seats or too little cargo space.
const ReasonVehicleMismatch = "vehicle_mismatch"

// fits reports whether driverID's current vehicle can take orderID.
func fits(ctx context.Context, q db.Querier, orderID, driverID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT `+vehicleFits+` FROM orders o, driver_profiles d WHERE o.id = $1 AND d.id = $2`,
		orderID, driverID).Scan(&ok)
	if db.IsNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("dispatch: vehicle fit: %w", err)
	}
	return ok, nil
}
