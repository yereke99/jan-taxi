package orders_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// locate stores the driver's last fix as recorded age ago.
func (e *env) locate(driverID uuid.UUID, lat, lon float64, age time.Duration) {
	e.t.Helper()
	testutil.SetDriverLocation(e.t, e.pool, driverID, testutil.LatLon{Lat: lat, Lon: lon}, age)
}

// driverAt creates an online driver registered in city with a fresh fix at
// lat, lon (or none when noLocation is set).
func (e *env) driverAt(city string, balance int64, lat, lon float64) testDriver {
	e.t.Helper()
	f := testutil.CreateDriver(e.t, e.pool, testutil.DriverOptions{Online: true, Balance: balance, City: city,
		Location: &testutil.LatLon{Lat: lat, Lon: lon}})
	return testDriver{DriverFixture: f, c: e.token(f.UserID, auth.AppDriver)}
}

// addCity inserts an active city (Aktobe by default) so drivers can be
// registered somewhere other than Embi.
func (e *env) addCity(code string, lat, lon float64) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO cities (code, name_kk, name_ru, name_en, center_lat, center_lon)
		VALUES ($1, $1, $1, $1, $2, $3)`, code, lat, lon); err != nil {
		e.t.Fatalf("city %s: %v", code, err)
	}
}

// ageOrder moves the order's creation time back, as if it had been searching
// for age (the effective radius grows with it).
func (e *env) ageOrder(orderID uuid.UUID, age time.Duration) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE orders SET created_at = now() - $2::float8 * interval '1 second' WHERE id = $1`, orderID, age.Seconds()); err != nil {
		e.t.Fatalf("age order: %v", err)
	}
}

func (e *env) setting(key, value string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value); err != nil {
		e.t.Fatalf("setting %s: %v", key, err)
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

func (e *env) offers(orderID, driverID uuid.UUID) int {
	return e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND driver_id = $2`, orderID, driverID)
}

// offerNotices counts order_available_driver notifications about orderID for driverID.
func (e *env) offerNotices(orderID, driverID uuid.UUID) int {
	return e.count(`SELECT count(*) FROM notification_outbox WHERE template = 'order_available_driver' AND dedupe_key = $1`,
		"order_available:"+orderID.String()+":"+driverID.String())
}

func (e *env) expand() int {
	e.t.Helper()
	n, err := dispatch.ExpandOffers(context.Background(), e.pool)
	if err != nil {
		e.t.Fatalf("expand: %v", err)
	}
	return n
}

func reason(a dispatch.Available) string {
	if a.Eligibility.Reason == nil {
		return ""
	}
	return *a.Eligibility.Reason
}

// Points north of the Embi pickup embiStops[0] (48.8264, 58.1442).
const (
	pickupLat = 48.8264
	pickupLon = 58.1442
	lat1km    = 48.8354 // ~1.0 km
	lat7km    = 48.8894 // ~7.0 km
	lat20km   = 49.0063 // ~20.0 km
)

// TestRadiusDispatchIgnoresCity covers §7: dispatch is by the distance from
// the driver's fresh location to pickup A, never by city. A driver registered
// in another city but standing near the pickup is offered the order, lists it
// with its distance and wins it; drivers outside the radius, with a stale
// location or with none are not offered it and cannot accept it.
func TestRadiusDispatchIgnoresCity(t *testing.T) {
	e := newEnv(t)
	e.addCity("AKT", 50.2839, 57.1670) // Aktobe, ~140 km from Embi
	visitor := e.driverAt("AKT", 1000, lat1km, pickupLon)
	local := e.driverAt("EMB", 1000, 48.8300, pickupLon) // ~0.4 km
	far := e.driverAt("EMB", 1000, lat20km, pickupLon)
	stale := e.driverAt("EMB", 1000, pickupLat, pickupLon)
	e.locate(stale.DriverID, pickupLat, pickupLon, 10*time.Minute) // older than dispatch.location_max_age_s (300 s)
	noLoc := testDriver{DriverFixture: testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 1000, NoLocation: true})}
	noLoc.c = e.token(noLoc.UserID, auth.AppDriver)

	o, _, _ := e.order(embiStops[:2], 800)
	if e.offers(o.ID, visitor.DriverID) != 1 || e.offers(o.ID, local.DriverID) != 1 {
		t.Fatal("drivers within the base radius were not offered the order")
	}
	for _, d := range []testDriver{far, stale, noLoc} {
		if got := e.offers(o.ID, d.DriverID); got != 0 {
			t.Fatalf("driver %s offered", d.Code)
		}
	}
	if e.outbox("order_available_driver", visitor.UserID) != 1 || e.outbox("order_available_driver", far.UserID) != 0 {
		t.Fatal("order_available_driver notifications")
	}
	var dist int
	if err := e.pool.QueryRow(context.Background(), `SELECT distance_to_pickup_m FROM order_offers WHERE order_id = $1 AND driver_id = $2`,
		o.ID, visitor.DriverID).Scan(&dist); err != nil || dist < 900 || dist > 1100 {
		t.Fatalf("offer distance = %d (%v)", dist, err)
	}

	// The list is live and sorted by distance to the pickup.
	a := e.available(visitor)
	if !a.Eligibility.Eligible || len(a.Items) != 1 || a.Items[0].ID != o.ID || a.Items[0].DistanceToPickupM == nil ||
		*a.Items[0].DistanceToPickupM < 900 || *a.Items[0].DistanceToPickupM > 1100 {
		t.Fatalf("visitor list: %+v", a)
	}
	if a := e.available(far); !a.Eligibility.Eligible || len(a.Items) != 0 {
		t.Fatalf("far list: %+v", a)
	}
	if a := e.available(stale); a.Eligibility.Eligible || reason(a) != "location_stale" || len(a.Items) != 0 {
		t.Fatalf("stale list: %+v", a)
	}
	if a := e.available(noLoc); a.Eligibility.Eligible || reason(a) != "location_required" || len(a.Items) != 0 {
		t.Fatalf("no location list: %+v", a)
	}
	e.expect(e.do(far.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil), http.StatusNotFound, "not_found")
	e.expect(e.do(visitor.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil), http.StatusOK, "")

	// Accepting needs the same reach.
	e.expect(e.do(far.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusNotFound, "not_found")
	r := e.do(stale.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil)
	e.expect(r, http.StatusUnprocessableEntity, "location_required")
	if d := r.err(t).Error.Details; d["reason"] != "location_stale" || d["max_age_s"] != float64(300) {
		t.Fatalf("stale accept details: %v", d)
	}
	r = e.do(noLoc.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil)
	e.expect(r, http.StatusUnprocessableEntity, "location_required")
	if d := r.err(t).Error.Details; d["reason"] != "location_required" {
		t.Fatalf("no location accept details: %v", d)
	}
	if s := e.status(o.ID); s != "searching" {
		t.Fatalf("status %s", s)
	}

	// The driver registered in Aktobe wins the Embi order.
	e.expect(e.do(visitor.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	if got := e.count(`SELECT count(*) FROM orders WHERE id = $1 AND driver_id = $2 AND status = 'accepted'`, o.ID, visitor.DriverID); got != 1 {
		t.Fatal("order not assigned to the driver of another city")
	}
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND driver_id = $2 AND status = 'lost'`, o.ID, local.DriverID); got != 1 {
		t.Fatal("the other offer was not closed")
	}
	if _, res := e.wallet(visitor.DriverID); res != 80 {
		t.Fatalf("reserved %d", res)
	}
}

// TestRadiusExpandsWithOrderAge: the effective radius is
// min(max, base + step * floor(age / step_s)) — with the defaults 5 km, then
// 7.5 km after a minute and 10 km after two. The dispatch_expand job offers
// the order to each newly reached driver exactly once; a driver 20 km away is
// never reached until an admin raises the maximum radius.
func TestRadiusExpandsWithOrderAge(t *testing.T) {
	e := newEnv(t)
	mid := e.driverAt("EMB", 1000, lat7km, pickupLon)
	far := e.driverAt("EMB", 1000, lat20km, pickupLon)
	o, _, _ := e.order(embiStops[:2], 800)
	if e.offers(o.ID, mid.DriverID) != 0 || e.offers(o.ID, far.DriverID) != 0 {
		t.Fatal("drivers outside the base radius were offered the order")
	}
	if n := e.expand(); n != 0 {
		t.Fatalf("expand of a new order = %d", n)
	}
	if a := e.available(mid); !a.Eligibility.Eligible || len(a.Items) != 0 {
		t.Fatalf("mid before expansion: %+v", a)
	}

	// After a minute the radius is 7.5 km: the 7 km driver is reached.
	e.ageOrder(o.ID, 61*time.Second)
	if a := e.available(mid); len(a.Items) != 1 || a.Items[0].ID != o.ID {
		t.Fatalf("mid after a minute: %+v", a)
	}
	if n := e.expand(); n != 1 || e.offers(o.ID, mid.DriverID) != 1 || e.offers(o.ID, far.DriverID) != 0 {
		t.Fatalf("expand after a minute = %d", n)
	}
	if e.outbox("order_available_driver", mid.UserID) != 1 {
		t.Fatal("reached driver not notified")
	}
	if n := e.expand(); n != 0 || e.outbox("order_available_driver", mid.UserID) != 1 {
		t.Fatalf("second expand = %d", n)
	}

	// The default maximum (10 km) never reaches 20 km, however old the order.
	e.ageOrder(o.ID, 10*time.Minute)
	if n := e.expand(); n != 0 || e.offers(o.ID, far.DriverID) != 0 {
		t.Fatalf("expand beyond the maximum = %d", n)
	}
	e.expect(e.do(far.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusNotFound, "not_found")

	// With a 25 km maximum the radius after 6 minutes is 20 km, after 7 it
	// is 22.5 km.
	e.setting("dispatch.radius_max_m", "25000")
	e.ageOrder(o.ID, 6*time.Minute-time.Second)
	if n := e.expand(); n != 0 {
		t.Fatalf("expand at 17.5 km = %d", n)
	}
	e.ageOrder(o.ID, 7*time.Minute)
	if n := e.expand(); n != 1 || e.offers(o.ID, far.DriverID) != 1 || e.outbox("order_available_driver", far.UserID) != 1 {
		t.Fatalf("expand at 22.5 km = %d", n)
	}
	a := e.available(far)
	if len(a.Items) != 1 || *a.Items[0].DistanceToPickupM < 19500 || *a.Items[0].DistanceToPickupM > 20500 {
		t.Fatalf("far list: %+v", a)
	}
	e.expect(e.do(far.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	if n := e.expand(); n != 0 {
		t.Fatalf("expand of an accepted order = %d", n)
	}
}

// TestExpandOffersNewlyEligibleDrivers: drivers who come online, move closer
// or top up after the order was placed are offered it by the next
// dispatch_expand run, once; drivers already offered are not offered again,
// and orders whose search window has passed are left to search_expiry.
func TestExpandOffersNewlyEligibleDrivers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.driverAt("EMB", 1000, pickupLat, pickupLon)
	o, _, _ := e.order(embiStops[:2], 800)
	expired, _, _ := e.order(embiStops[:2], 700)
	e.expireSearch(expired.ID)

	late := e.driver(1000, false)                        // offline when the order was placed
	mover := e.driverAt("EMB", 1000, lat20km, pickupLon) // too far at first
	poor := e.driverAt("EMB", 30, pickupLat, pickupLon)  // the commission is 80
	if e.offers(o.ID, late.DriverID)+e.offers(o.ID, mover.DriverID)+e.offers(o.ID, poor.DriverID) != 0 {
		t.Fatal("ineligible drivers offered")
	}
	if n := e.expand(); n != 0 {
		t.Fatalf("nothing changed, expand = %d", n)
	}

	e.locate(late.DriverID, lat1km, pickupLon, 0)
	if _, err := drivers.SetAvailability(ctx, e.pool, late.DriverID, true); err != nil {
		t.Fatalf("late driver online: %v", err)
	}
	e.locate(mover.DriverID, lat1km, pickupLon, 0)
	if err := db.WithTx(ctx, e.pool, func(tx pgx.Tx) error {
		return wallets.Adjust(ctx, tx, poor.DriverID, 1000, e.adminID, "adjust:"+uuid.NewString(), "test top-up")
	}); err != nil {
		t.Fatalf("top up: %v", err)
	}
	if n := e.expand(); n != 3 {
		t.Fatalf("expand = %d, want 3", n)
	}
	for _, d := range []testDriver{first, late, mover, poor} {
		if e.offers(o.ID, d.DriverID) != 1 || e.offerNotices(o.ID, d.DriverID) != 1 {
			t.Fatalf("driver %s: offers %d", d.Code, e.offers(o.ID, d.DriverID))
		}
	}
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1`, expired.ID); got != 1 {
		t.Fatalf("expired search offers = %d (only the driver there at creation)", got)
	}
	if n := e.expand(); n != 0 {
		t.Fatalf("repeated expand = %d", n)
	}
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1`, o.ID); got != 4 {
		t.Fatalf("offers = %d", got)
	}
}

// TestAvailableOrdersPolicy covers the remaining §7 rules: lists are sorted by
// distance to the pickup, and offline, busy and suspended drivers see nothing.
func TestAvailableOrdersPolicy(t *testing.T) {
	e := newEnv(t)
	near := e.driverAt("EMB", 1000, 48.8300, 58.1560) // ~1 km from the pickups
	offline := e.driver(1000, false)
	e.locate(offline.DriverID, 48.8300, 58.1560, 0)

	o1, _, _ := e.order(embiStops[:2], 800)  // pickup 48.8264, 58.1442
	o2, _, _ := e.order(embiStops[2:4], 700) // pickup 48.8310, 58.1550, nearer to "near"
	if got := e.count(`SELECT count(*) FROM order_offers WHERE driver_id = $1 AND distance_to_pickup_m IS NOT NULL`, near.DriverID); got != 2 {
		t.Fatalf("near driver offers = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM order_offers WHERE driver_id = $1`, offline.DriverID); got != 0 {
		t.Fatalf("offline driver offers = %d", got)
	}

	a := e.available(near)
	if !a.Eligibility.Eligible || len(a.Items) != 2 || a.Items[0].ID != o2.ID || a.Items[1].ID != o1.ID ||
		a.Items[0].DistanceToPickupM == nil || a.Items[1].DistanceToPickupM == nil ||
		*a.Items[0].DistanceToPickupM >= *a.Items[1].DistanceToPickupM {
		t.Fatalf("near: %+v", a)
	}
	if a := e.available(offline); a.Eligibility.Eligible || reason(a) != "offline" || len(a.Items) != 0 {
		t.Fatalf("offline: %+v", a)
	}

	// A driver with an active order sees nothing.
	e.expect(e.do(near.c, http.MethodPost, path("/driver/orders/%s/accept", o1.ID), nil), http.StatusOK, "")
	if a := e.available(near); a.Eligibility.Eligible || reason(a) != "has_active_order" || len(a.Items) != 0 {
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
