package orders_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
)

func (e *env) locate(driverID uuid.UUID, lat, lon float64, age time.Duration) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO driver_locations (driver_id, lat, lon, source, recorded_at)
		VALUES ($1, $2, $3, 'miniapp', now() - make_interval(secs => $4))
		ON CONFLICT (driver_id) DO UPDATE SET lat = $2, lon = $3, recorded_at = now() - make_interval(secs => $4)`,
		driverID, lat, lon, age.Seconds()); err != nil {
		e.t.Fatalf("location: %v", err)
	}
}

func (e *env) available(d testDriver) dispatch.Available {
	e.t.Helper()
	r := e.do(d.c, http.MethodGet, "/driver/orders", nil)
	e.expect(r, http.StatusOK, "")
	var a dispatch.Available
	r.decode(e.t, &a)
	return a
}

func reason(a dispatch.Available) string {
	if a.Eligibility.Reason == nil {
		return ""
	}
	return *a.Eligibility.Reason
}

// TestAvailableOrdersPolicy covers §7: the city location policy decides who is
// offered and who sees an order; lists are sorted by distance when the driver
// has a fresh location and newest first otherwise.
func TestAvailableOrdersPolicy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE cities SET require_driver_location = true, dispatch_radius_m = 3000,
		driver_location_max_age_s = 300 WHERE id = $1`, e.cityID); err != nil {
		t.Fatal(err)
	}
	noLoc := e.driver(1000, true)
	near := e.driver(1000, true)
	e.locate(near.DriverID, 48.8300, 58.1560, 10*time.Second) // ~1 km from the pickups
	far := e.driver(1000, true)
	e.locate(far.DriverID, 49.0000, 58.1442, 10*time.Second) // ~19 km north
	stale := e.driver(1000, true)
	e.locate(stale.DriverID, 48.8264, 58.1442, time.Hour)
	offline := e.driver(1000, false)

	o1, _, _ := e.order(embiStops[:2], 800)  // pickup 48.8264, 58.1442
	o2, _, _ := e.order(embiStops[2:4], 700) // pickup 48.8310, 58.1550, nearer to "near"

	for _, d := range []testDriver{noLoc, far, stale, offline} {
		if got := e.count(`SELECT count(*) FROM order_offers WHERE driver_id = $1`, d.DriverID); got != 0 {
			t.Fatalf("driver %s offered %d orders", d.Code, got)
		}
	}
	if got := e.count(`SELECT count(*) FROM order_offers WHERE driver_id = $1 AND distance_to_pickup_m IS NOT NULL`, near.DriverID); got != 2 {
		t.Fatalf("near driver offers = %d", got)
	}

	if a := e.available(noLoc); a.Eligibility.Eligible || reason(a) != "location_required" || len(a.Items) != 0 {
		t.Fatalf("no location: %+v", a)
	}
	if a := e.available(stale); a.Eligibility.Eligible || reason(a) != "location_stale" || len(a.Items) != 0 {
		t.Fatalf("stale: %+v", a)
	}
	if a := e.available(offline); a.Eligibility.Eligible || reason(a) != "offline" {
		t.Fatalf("offline: %+v", a)
	}
	if a := e.available(far); !a.Eligibility.Eligible || len(a.Items) != 0 {
		t.Fatalf("far: %+v", a)
	}
	e.expect(e.do(far.c, http.MethodGet, path("/driver/orders/%s", o1.ID), nil), http.StatusNotFound, "not_found")

	a := e.available(near)
	if !a.Eligibility.Eligible || len(a.Items) != 2 || a.Items[0].ID != o2.ID || a.Items[1].ID != o1.ID ||
		a.Items[0].DistanceToPickupM == nil || a.Items[1].DistanceToPickupM == nil ||
		*a.Items[0].DistanceToPickupM >= *a.Items[1].DistanceToPickupM {
		t.Fatalf("near: %+v", a)
	}

	// Without the policy everyone online sees both, newest first without a
	// fresh location.
	if _, err := e.pool.Exec(ctx, `UPDATE cities SET require_driver_location = false WHERE id = $1`, e.cityID); err != nil {
		t.Fatal(err)
	}
	a = e.available(noLoc)
	if !a.Eligibility.Eligible || len(a.Items) != 2 || a.Items[0].ID != o2.ID || a.Items[0].DistanceToPickupM != nil {
		t.Fatalf("no policy, no location: %+v", a)
	}
	a = e.available(stale)
	if len(a.Items) != 2 || a.Items[0].ID != o2.ID || a.Items[0].DistanceToPickupM == nil {
		t.Fatalf("no policy, stale location: %+v", a)
	}

	// A driver with an active order sees nothing.
	e.expect(e.do(noLoc.c, http.MethodPost, path("/driver/orders/%s/accept", o1.ID), nil), http.StatusOK, "")
	if a := e.available(noLoc); a.Eligibility.Eligible || reason(a) != "has_active_order" || len(a.Items) != 0 {
		t.Fatalf("busy: %+v", a)
	}
	// A suspended driver is not eligible and cannot act.
	susp := testDriverWithStatus(e, "suspended")
	if a := e.available(susp); reason(a) != "not_approved" {
		t.Fatalf("suspended: %+v", a)
	}
	e.expect(e.do(susp.c, http.MethodPost, path("/driver/orders/%s/accept", o2.ID), nil), http.StatusForbidden, "driver_not_approved")
	// A driver must be online to accept.
	e.expect(e.do(offline.c, http.MethodPost, path("/driver/orders/%s/accept", o2.ID), nil), http.StatusConflict, "driver_offline")
}
