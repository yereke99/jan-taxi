package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// Arrival estimates (ARCHITECTURE §22.6): how long the driver who took an
// order needs to reach pickup A. Inside the acceptance transaction only the
// straight-line estimate is used (distance at 30 km/h); right after the
// commit the road route from the driver's latest fresh fix is asked from OSRM
// (at most ETATimeout), and the driver_eta job repeats that at most every
// ETARefreshAge while the order is accepted and the driver keeps moving. The
// estimate is stored on the order (driver_eta_s, driver_eta_at), carried by
// order.accepted and driver.location_updated, and put into the passenger's
// "driver found" message, which is held back ETAHold so the route estimate
// can make it in.

// Arrival estimate parameters.
const (
	ETATimeout          = 2 * time.Second
	ETARefreshAge       = 30 * time.Second
	ETAJobInterval      = 10 * time.Second
	ETAHold             = 3 * time.Second
	etaFallbackSpeedMps = 30000.0 / 3600.0
	etaBatch            = 100
)

// RouteDuration returns the driving time in seconds between two points
// (OSRM in production).
type RouteDuration func(ctx context.Context, from, to geo.Point) (int, error)

// FallbackETASeconds is the straight-line estimate: distance at 30 km/h,
// rounded up to whole minutes, at least one minute.
func FallbackETASeconds(distanceM float64) int {
	minutes := math.Ceil(math.Max(distanceM, 0) / etaFallbackSpeedMps / 60)
	return int(math.Max(minutes, 1)) * 60
}

// ETAMinutes rounds an estimate up to whole minutes, at least one.
func ETAMinutes(seconds int) int { return max((seconds+59)/60, 1) }

// fallbackETA returns the straight-line estimate from driverID's last fix to
// the pickup of orderID, or nil without a fix.
func fallbackETA(ctx context.Context, q db.Querier, orderID, driverID uuid.UUID) (*int, error) {
	var dist *float64
	err := q.QueryRow(ctx, `
		SELECT ST_Distance(dl.location, o.pickup) FROM orders o
		JOIN driver_locations dl ON dl.driver_id = $2
		WHERE o.id = $1`, orderID, driverID).Scan(&dist)
	if db.IsNoRows(err) || dist == nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dispatch: arrival estimate: %w", err)
	}
	s := FallbackETASeconds(*dist)
	return &s, nil
}

// storeETA writes the estimate on the order assigned to driverID.
func storeETA(ctx context.Context, q db.Querier, orderID uuid.UUID, eta *int) error {
	_, err := q.Exec(ctx, `UPDATE orders SET driver_eta_s = $2, driver_eta_at = CASE WHEN $2::int IS NULL THEN NULL ELSE now() END
		WHERE id = $1`, orderID, eta)
	if err != nil {
		return fmt.Errorf("dispatch: store arrival estimate: %w", err)
	}
	return nil
}

// ArrivalETA refines arrival estimates after acceptance and while orders
// are accepted. A nil Route keeps the straight-line estimate.
type ArrivalETA struct {
	pool  *pgxpool.Pool
	route RouteDuration
	log   *slog.Logger
	wg    sync.WaitGroup
}

// NewArrivalETA builds the estimator.
func NewArrivalETA(pool *pgxpool.Pool, route RouteDuration, log *slog.Logger) *ArrivalETA {
	if log == nil {
		log = slog.Default()
	}
	return &ArrivalETA{pool: pool, route: route, log: log}
}

// Go refines the estimate of orderID in the background (after the
// acceptance committed), so the response to the driver is not delayed.
func (a *ArrivalETA) Go(orderID uuid.UUID) {
	if a == nil {
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), ETATimeout+5*time.Second)
		defer cancel()
		if err := a.Refine(ctx, orderID); err != nil {
			a.log.Warn("arrival estimate failed", slog.String("order_id", orderID.String()), slog.Any("err", err))
		}
	}()
}

// Wait blocks until background refinements finish (shutdown, tests).
func (a *ArrivalETA) Wait() {
	if a != nil {
		a.wg.Wait()
	}
}

// Refine recomputes the estimate of an accepted order from the driver's
// latest fix (the road route when the fix is fresh, the straight line
// otherwise), stores it, puts it into the passenger's held "driver found"
// message and sends that message, and tells the passenger's app.
func (a *ArrivalETA) Refine(ctx context.Context, orderID uuid.UUID) error {
	var (
		driverID, passengerID uuid.UUID
		pickup, at            geo.Point
		dist                  float64
		fresh                 bool
	)
	p := settings.Dispatch(ctx, a.pool)
	err := a.pool.QueryRow(ctx, `
		SELECT o.driver_id, o.passenger_id, o.pickup_lat, o.pickup_lon, dl.lat, dl.lon,
		       ST_Distance(dl.location, o.pickup), dl.recorded_at > now() - make_interval(secs => $2::int)
		FROM orders o JOIN driver_locations dl ON dl.driver_id = o.driver_id
		WHERE o.id = $1 AND o.status = 'accepted'`, orderID, p.LocationMaxAgeS).
		Scan(&driverID, &passengerID, &pickup.Lat, &pickup.Lon, &at.Lat, &at.Lon, &dist, &fresh)
	if db.IsNoRows(err) {
		return a.release(ctx, orderID, nil) // no longer accepted, or no fix: send what we have
	}
	if err != nil {
		return fmt.Errorf("dispatch: arrival estimate: %w", err)
	}
	eta := FallbackETASeconds(dist)
	if fresh && a.route != nil {
		rctx, cancel := context.WithTimeout(ctx, ETATimeout)
		d, rerr := a.route(rctx, at, pickup)
		cancel()
		if rerr == nil && d >= 0 {
			eta = d
		}
	}
	err = db.WithTx(ctx, a.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE orders SET driver_eta_s = $2, driver_eta_at = now()
			WHERE id = $1 AND status = 'accepted' AND driver_id = $3`, orderID, eta, driverID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		data := map[string]any{"order_id": orderID.String(), "driver_id": driverID.String(), "driver_eta_s": eta}
		return realtime.Publish(ctx, tx, realtime.Event{Type: realtime.DriverLocationUpdated,
			Scopes: []string{realtime.UserScope(passengerID), realtime.DriverScope(driverID)}, Data: data})
	})
	if err != nil {
		return fmt.Errorf("dispatch: store arrival estimate: %w", err)
	}
	return a.release(ctx, orderID, &eta)
}

// release puts eta into the passenger's held "driver found" message (when
// still waiting) and sends it now.
func (a *ArrivalETA) release(ctx context.Context, orderID uuid.UUID, eta *int) error {
	var minutes *int
	if eta != nil {
		m := ETAMinutes(*eta)
		minutes = &m
	}
	_, err := a.pool.Exec(ctx, `
		UPDATE notification_outbox
		SET params = CASE WHEN $2::int IS NULL THEN params ELSE params || jsonb_build_object('eta_min', $2::int) END,
		    next_attempt_at = LEAST(next_attempt_at, now())
		WHERE template = 'order_accepted_passenger' AND kind = 'send' AND status = 'pending'
		  AND params->>'order_id' = $1`, orderID.String(), minutes)
	if err != nil {
		return fmt.Errorf("dispatch: release driver found message: %w", err)
	}
	return nil
}

// RefreshDue recomputes the estimates of accepted orders whose driver
// reported a newer location since the last estimate, at most every
// ETARefreshAge per order. It returns how many orders were refreshed.
func (a *ArrivalETA) RefreshDue(ctx context.Context) (int, error) {
	rows, err := a.pool.Query(ctx, `
		SELECT o.id FROM orders o JOIN driver_locations dl ON dl.driver_id = o.driver_id
		WHERE o.status = 'accepted'
		  AND (o.driver_eta_at IS NULL OR (o.driver_eta_at < now() - make_interval(secs => $1::int)
		                                   AND dl.recorded_at > o.driver_eta_at))
		ORDER BY o.driver_eta_at NULLS FIRST LIMIT $2`, int(ETARefreshAge.Seconds()), etaBatch)
	if err != nil {
		return 0, fmt.Errorf("dispatch: due arrival estimates: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, fmt.Errorf("dispatch: due arrival estimates: %w", err)
	}
	n := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		if err := a.Refine(ctx, id); err != nil {
			a.log.Warn("arrival estimate failed", slog.String("order_id", id.String()), slog.Any("err", err))
			continue
		}
		n++
	}
	return n, nil
}
