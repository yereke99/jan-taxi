package orders_test

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// fakeRoute is the road router of the arrival estimate: a fixed duration or
// an error, counting calls.
type fakeRoute struct {
	seconds atomic.Int64
	fail    atomic.Bool
	calls   atomic.Int64
}

func (f *fakeRoute) duration(ctx context.Context, from, to geo.Point) (int, error) {
	f.calls.Add(1)
	if f.fail.Load() {
		return 0, errors.New("osrm down")
	}
	return int(f.seconds.Load()), nil
}

func newETAEnv(t *testing.T, route *fakeRoute) (*env, *dispatch.ArrivalETA) {
	t.Helper()
	var eta *dispatch.ArrivalETA
	e := newEnv(t, func(svc *orders.Service, pool *pgxpool.Pool) {
		eta = dispatch.NewArrivalETA(pool, route.duration, slog.New(slog.DiscardHandler))
		svc.SetArrivalETA(eta)
	})
	t.Cleanup(eta.Wait)
	return e, eta
}

type etaRow struct {
	seconds *int
	at      *time.Time
}

func (e *env) eta(orderID any) etaRow {
	e.t.Helper()
	var r etaRow
	if err := e.pool.QueryRow(context.Background(), `SELECT driver_eta_s, driver_eta_at FROM orders WHERE id = $1`, orderID).
		Scan(&r.seconds, &r.at); err != nil {
		e.t.Fatalf("eta: %v", err)
	}
	return r
}

// passengerPush returns the eta_min param and whether the "driver found"
// message is due now.
func (e *env) passengerPush(orderID string) (etaMin *int, due bool) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), `
		SELECT (params->>'eta_min')::int, next_attempt_at <= now() FROM notification_outbox
		WHERE template = 'order_accepted_passenger' AND params->>'order_id' = $1`, orderID).Scan(&etaMin, &due); err != nil {
		e.t.Fatalf("passenger push: %v", err)
	}
	return etaMin, due
}

// TestArrivalETAAfterAccept (§22.6): the acceptance stores the straight-line
// estimate and holds the passenger's message; right after the commit the
// road estimate from the driver's fix replaces it, goes into the message and
// releases it; the passenger's order shows it.
func TestArrivalETAAfterAccept(t *testing.T) {
	route := &fakeRoute{}
	route.seconds.Store(420)
	e, eta := newETAEnv(t, route)
	o, _, pc := e.order(embiStops[:2], 1000)
	// The driver is about 2.2 km north of pickup A.
	drv := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 500,
		Location: &testutil.LatLon{Lat: 48.8464, Lon: 58.1442}})
	dc := e.token(drv.UserID, "driver")
	e.expect(e.do(dc, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	// The message waits for the road estimate (held ETAHold = 3 s)...
	if _, due := e.passengerPush(o.ID.String()); due && route.calls.Load() == 0 {
		t.Fatal("passenger message not held for the estimate")
	}
	eta.Wait()

	if route.calls.Load() != 1 {
		t.Fatalf("route calls = %d", route.calls.Load())
	}
	got := e.eta(o.ID)
	if got.seconds == nil || *got.seconds != 420 || got.at == nil || time.Since(*got.at) > time.Minute {
		t.Fatalf("stored estimate = %+v", got)
	}
	// ...and is released with it.
	min, due := e.passengerPush(o.ID.String())
	if min == nil || *min != 7 || !due {
		t.Fatalf("passenger message eta_min = %v due = %v", min, due)
	}
	var view orders.Order
	r := e.do(pc, http.MethodGet, path("/orders/%s", o.ID), nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &view)
	if view.DriverETAS == nil || *view.DriverETAS != 420 || view.DriverETAAt == nil {
		t.Fatalf("order view estimate = %v %v", view.DriverETAS, view.DriverETAAt)
	}
}

// TestArrivalETAFallback: without the road router, or with a stale fix, the
// estimate is the straight-line distance at 30 km/h in whole minutes.
func TestArrivalETAFallback(t *testing.T) {
	route := &fakeRoute{}
	route.fail.Store(true)
	e, eta := newETAEnv(t, route)
	o, _, _ := e.order(embiStops[:2], 1000)
	drv := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 500,
		Location: &testutil.LatLon{Lat: 48.8464, Lon: 58.1442}})
	e.expect(e.do(e.token(drv.UserID, "driver"), http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	eta.Wait()

	var dist float64
	if err := e.pool.QueryRow(context.Background(), `SELECT ST_Distance(dl.location, o.pickup) FROM orders o
		JOIN driver_locations dl ON dl.driver_id = $2 WHERE o.id = $1`, o.ID, drv.DriverID).Scan(&dist); err != nil {
		t.Fatal(err)
	}
	wantMin := int(math.Ceil(dist / (30000.0 / 3600) / 60))
	if got := e.eta(o.ID); got.seconds == nil || *got.seconds != wantMin*60 {
		t.Fatalf("fallback estimate = %v, want %d min (%.0f m)", got.seconds, wantMin, dist)
	}
	if min, due := e.passengerPush(o.ID.String()); min == nil || *min != wantMin || !due {
		t.Fatalf("passenger message eta_min = %v due = %v", min, due)
	}
	if dispatch.FallbackETASeconds(0) != 60 || dispatch.FallbackETASeconds(501) != 120 || dispatch.ETAMinutes(61) != 2 {
		t.Fatal("rounding: at least one minute, always up")
	}

	// A stale fix is not routed: the straight line again.
	route.fail.Store(false)
	route.seconds.Store(60)
	calls := route.calls.Load()
	if _, err := e.pool.Exec(context.Background(), `UPDATE driver_locations SET recorded_at = now() - interval '1 hour'
		WHERE driver_id = $1`, drv.DriverID); err != nil {
		t.Fatal(err)
	}
	if err := eta.Refine(context.Background(), o.ID); err != nil {
		t.Fatal(err)
	}
	if route.calls.Load() != calls {
		t.Fatal("stale fix routed")
	}
	if got := e.eta(o.ID); *got.seconds != wantMin*60 {
		t.Fatalf("stale estimate = %d", *got.seconds)
	}
}

// TestArrivalETARefresh: while the order is accepted the estimate is
// recomputed from newer driver locations at most every 30 s, and never after
// the driver arrived.
func TestArrivalETARefresh(t *testing.T) {
	route := &fakeRoute{}
	route.seconds.Store(600)
	e, eta := newETAEnv(t, route)
	o, _, _ := e.order(embiStops[:2], 1000)
	drv := e.driver(500, true)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	eta.Wait()
	ctx := context.Background()

	// Nothing is due: the estimate is fresh.
	if n, err := eta.RefreshDue(ctx); err != nil || n != 0 {
		t.Fatalf("fresh refresh = %d %v", n, err)
	}
	// Older than 30 s but the driver has not moved since: still nothing.
	if _, err := e.pool.Exec(ctx, `UPDATE orders SET driver_eta_at = now() - interval '31 seconds' WHERE id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE driver_locations SET recorded_at = now() - interval '40 seconds' WHERE driver_id = $1`, drv.DriverID); err != nil {
		t.Fatal(err)
	}
	if n, err := eta.RefreshDue(ctx); err != nil || n != 0 {
		t.Fatalf("no new location refresh = %d %v", n, err)
	}
	// A new fix: recomputed.
	route.seconds.Store(300)
	if _, err := e.pool.Exec(ctx, `UPDATE driver_locations SET recorded_at = now() WHERE driver_id = $1`, drv.DriverID); err != nil {
		t.Fatal(err)
	}
	if n, err := eta.RefreshDue(ctx); err != nil || n != 1 {
		t.Fatalf("refresh = %d %v", n, err)
	}
	if got := e.eta(o.ID); *got.seconds != 300 {
		t.Fatalf("refreshed estimate = %d", *got.seconds)
	}
	// Once the driver is at the pickup the estimate is no longer refreshed.
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/arriving", o.ID), nil), http.StatusOK, "")
	if _, err := e.pool.Exec(ctx, `UPDATE orders SET driver_eta_at = now() - interval '1 minute' WHERE id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := eta.RefreshDue(ctx); err != nil || n != 0 {
		t.Fatalf("refresh after arriving = %d %v", n, err)
	}
}
