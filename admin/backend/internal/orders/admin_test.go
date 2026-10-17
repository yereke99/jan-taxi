package orders_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

func (e *env) audits(action, entityID string) int {
	return e.count(`SELECT count(*) FROM admin_audit_logs WHERE action = $1 AND entity_id = $2`, action, entityID)
}

// TestAdminReassignMovesReservation: re-assigning an accepted order releases
// the first driver's reservation, reserves on the new driver and tells
// everyone; only the new driver is charged on completion.
func TestAdminReassignMovesReservation(t *testing.T) {
	e := newEnv(t)
	a := e.driver(1000, true)
	b := e.driver(1000, true)
	o, pid, _ := e.order(embiStops[:2], 800)
	e.expect(e.do(a.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(a.c, http.MethodPost, path("/driver/orders/%s/arriving", o.ID), nil), http.StatusOK, "")

	r := e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/assign", o.ID), map[string]any{"driver_id": b.DriverID})
	e.expect(r, http.StatusOK, "")
	var ao orders.AdminOrder
	r.decode(t, &ao)
	if ao.Status != "accepted" || ao.Driver == nil || ao.Driver.ID != b.DriverID || ao.ArrivedAt != nil ||
		ao.Reservation == nil || ao.Reservation.Status != "active" || ao.Reservation.Amount != 80 || len(ao.Warnings) != 0 {
		t.Fatalf("reassigned order: %+v", ao)
	}
	if bal, res := e.wallet(a.DriverID); bal != 1000 || res != 0 {
		t.Fatalf("previous driver wallet %d/%d", bal, res)
	}
	if bal, res := e.wallet(b.DriverID); bal != 1000 || res != 80 {
		t.Fatalf("new driver wallet %d/%d", bal, res)
	}
	var types []string
	for _, l := range ao.Ledger {
		types = append(types, l.EntryType)
	}
	if len(types) != 3 || types[0] != "commission_reserve" || types[1] != "commission_release" || types[2] != "commission_reserve" ||
		ao.Ledger[1].DriverID != a.DriverID || ao.Ledger[2].DriverID != b.DriverID {
		t.Fatalf("ledger: %+v", ao.Ledger)
	}
	if got := e.count(`SELECT count(*) FROM wallet_reservations WHERE order_id = $1 AND status = 'active'`, o.ID); got != 1 {
		t.Fatalf("active reservations = %d", got)
	}
	if e.outbox("order_cancelled_driver", a.UserID) != 1 || e.outbox("order_accepted_driver", b.UserID) != 1 ||
		e.outbox("order_accepted_passenger", pid) != 2 {
		t.Fatal("re-assignment notifications missing")
	}
	if e.audits("order.reassign", o.ID.String()) != 1 {
		t.Fatal("re-assignment not audited")
	}
	last := ao.Events[len(ao.Events)-1]
	if last.ActorType != "admin" || last.ActorID == nil || *last.ActorID != e.adminID || last.Meta["previous_driver_id"] != a.DriverID.String() {
		t.Fatalf("assignment event: %+v", last)
	}

	// The previous driver lost the order; the new one runs it.
	e.expect(e.do(a.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil), http.StatusNotFound, "not_found")
	e.expect(e.do(a.c, http.MethodGet, "/driver/orders/active", nil), http.StatusNoContent, "")
	e.expect(e.do(a.c, http.MethodPost, path("/driver/orders/%s/start", o.ID), nil), http.StatusNotFound, "not_found")
	e.expect(e.do(b.c, http.MethodGet, "/driver/orders/active", nil), http.StatusOK, "")

	// Assigning the same driver again, or once the trip started, is refused.
	r = e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/assign", o.ID), map[string]any{"driver_id": b.DriverID})
	e.expect(r, http.StatusConflict, "conflict")
	e.expect(e.do(b.c, http.MethodPost, path("/driver/orders/%s/start", o.ID), nil), http.StatusOK, "")
	c := e.driver(1000, true)
	e.expect(e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/assign", o.ID), map[string]any{"driver_id": c.DriverID}),
		http.StatusConflict, "invalid_transition")

	e.expect(e.do(b.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil), http.StatusOK, "")
	if bal, res := e.wallet(a.DriverID); bal != 1000 || res != 0 {
		t.Fatalf("previous driver charged: %d/%d", bal, res)
	}
	if bal, res := e.wallet(b.DriverID); bal != 920 || res != 0 {
		t.Fatalf("new driver wallet %d/%d", bal, res)
	}
	if m, err := wallets.Reconcile(context.Background(), e.pool); err != nil || len(m) != 0 {
		t.Fatalf("reconcile: %v %+v", err, m)
	}
}

// TestAdminAssignChecks: a searching order goes to an offline driver with a
// warning; drivers who could not accept it are refused with the accept errors.
func TestAdminAssignChecks(t *testing.T) {
	e := newEnv(t)
	online := e.driver(1000, true)
	o, _, _ := e.order(embiStops[:2], 800)
	assign := func(driverID any) response {
		return e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/assign", o.ID), map[string]any{"driver_id": driverID})
	}

	poor := e.driver(10, false)
	r := assign(poor.DriverID)
	e.expect(r, http.StatusPaymentRequired, "insufficient_balance")
	pending := testDriverWithStatus(e, "pending")
	e.expect(assign(pending.DriverID), http.StatusForbidden, "driver_not_approved")
	e.expect(assign("not-a-uuid"), http.StatusUnprocessableEntity, "validation_failed")
	e.expect(assign(o.ID), http.StatusUnprocessableEntity, "validation_failed")

	busy := e.driver(1000, true)
	other, _, _ := e.order(embiStops[1:3], 700)
	e.expect(e.do(busy.c, http.MethodPost, path("/driver/orders/%s/accept", other.ID), nil), http.StatusOK, "")
	e.expect(assign(busy.DriverID), http.StatusConflict, "driver_has_active_order")

	// Permissions: an auditor may look but not act.
	auditor := caller{admin: e.adminID, role: auth.RoleAuditor}
	e.expect(e.do(auditor, http.MethodPost, path("/admin/orders/%s/assign", o.ID), map[string]any{"driver_id": online.DriverID}),
		http.StatusForbidden, "forbidden")
	e.expect(e.do(auditor, http.MethodGet, path("/admin/orders/%s", o.ID), nil), http.StatusOK, "")
	if s := e.status(o.ID); s != "searching" {
		t.Fatalf("status %s", s)
	}

	offline := e.driver(1000, false)
	r = assign(offline.DriverID)
	e.expect(r, http.StatusOK, "")
	var ao orders.AdminOrder
	r.decode(t, &ao)
	// The offline driver has no location either: the operator is warned of both.
	if ao.Status != "accepted" || ao.Driver == nil || ao.Driver.ID != offline.DriverID || len(ao.Warnings) != 2 ||
		ao.Warnings[0] != "offline" || ao.Warnings[1] != "location_required" {
		t.Fatalf("assigned: %+v", ao)
	}
	offers := map[string]string{}
	for _, f := range ao.Offers {
		offers[f.Driver.ID.String()] = f.Status
	}
	if offers[online.DriverID.String()] != "lost" || offers[offline.DriverID.String()] != "accepted" {
		t.Fatalf("offers: %+v", ao.Offers)
	}
	if e.audits("order.assign", o.ID.String()) != 1 {
		t.Fatal("assignment not audited")
	}
	if _, res := e.wallet(offline.DriverID); res != 80 {
		t.Fatalf("reserved %d", res)
	}
}

// TestAdminAssignSkipsRadius: an operator may give an order to a driver far
// from the pickup, registered in another city or with a stale location; the
// response warns about it and the radius never blocks the assignment.
func TestAdminAssignSkipsRadius(t *testing.T) {
	e := newEnv(t)
	e.addCity("AKT", 50.2839, 57.1670)
	assign := func(orderID uuid.UUID, d testDriver) orders.AdminOrder {
		t.Helper()
		r := e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/assign", orderID), map[string]any{"driver_id": d.DriverID})
		e.expect(r, http.StatusOK, "")
		var ao orders.AdminOrder
		r.decode(t, &ao)
		return ao
	}
	far := e.driverAt("AKT", 1000, lat20km, pickupLon)
	o1, _, _ := e.order(embiStops[:2], 800)
	if ao := assign(o1.ID, far); ao.Status != "accepted" || len(ao.Warnings) != 1 || ao.Warnings[0] != "out_of_radius" {
		t.Fatalf("far driver: %+v", ao.Warnings)
	}
	if e.audits("order.assign", o1.ID.String()) != 1 {
		t.Fatal("assignment not audited")
	}

	stale := e.driverAt("EMB", 1000, pickupLat, pickupLon)
	e.locate(stale.DriverID, pickupLat, pickupLon, time.Hour)
	o2, _, _ := e.order(embiStops[:2], 800)
	if ao := assign(o2.ID, stale); len(ao.Warnings) != 1 || ao.Warnings[0] != "location_stale" {
		t.Fatalf("stale driver: %+v", ao.Warnings)
	}

	near := e.driverAt("AKT", 1000, lat1km, pickupLon)
	o3, _, _ := e.order(embiStops[:2], 800)
	if ao := assign(o3.ID, near); len(ao.Warnings) != 0 {
		t.Fatalf("driver in reach: %+v", ao.Warnings)
	}
}

func testDriverWithStatus(e *env, status string) testDriver {
	e.t.Helper()
	d := e.driver(1000, false)
	if _, err := e.pool.Exec(context.Background(), `UPDATE driver_profiles SET status = $2 WHERE id = $1`, d.DriverID, status); err != nil {
		e.t.Fatalf("driver status: %v", err)
	}
	return d
}

// TestAdminCancel: operators may cancel a trip in progress; the reservation is
// released, both sides are told and the action is audited.
func TestAdminCancel(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	o, pid, pc := e.order(embiStops[:2], 800)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/start", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/cancel", o.ID), nil), http.StatusConflict, "invalid_transition")

	r := e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/cancel", o.ID), map[string]any{})
	e.expect(r, http.StatusUnprocessableEntity, "validation_failed")
	r = e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/cancel", o.ID), map[string]any{"reason": "Жолаушы өтініші бойынша"})
	e.expect(r, http.StatusOK, "")
	var ao orders.AdminOrder
	r.decode(t, &ao)
	if ao.Status != "cancelled" || ao.CancelledBy == nil || *ao.CancelledBy != "admin" || ao.Reservation == nil ||
		ao.Reservation.Status != "released" || ao.DriverLocation != nil {
		t.Fatalf("cancelled: %+v", ao)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 1000 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	if e.outbox("order_cancelled_passenger", pid) != 1 || e.outbox("order_cancelled_driver", drv.UserID) != 1 {
		t.Fatal("cancel notifications missing")
	}
	if e.audits("order.cancel", o.ID.String()) != 1 {
		t.Fatal("cancel not audited")
	}
	// The driver is free again.
	e.expect(e.do(drv.c, http.MethodGet, "/driver/orders/active", nil), http.StatusNoContent, "")
}

// TestAdminListAndSearch covers filters and q (number, id, phones, driver code).
func TestAdminListAndSearch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	drv := e.driver(1000, true)
	o1, p1, _ := e.order(embiStops[:2], 800)
	o2, _, _ := e.order(embiStops[1:3], 700)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o1.ID), nil), http.StatusOK, "")
	if _, err := e.pool.Exec(ctx, `UPDATE users SET phone_e164 = '+77051234567' WHERE id = $1`, p1); err != nil {
		t.Fatal(err)
	}
	type page struct {
		Items    []orders.AdminOrderListItem `json:"items"`
		Total    int                         `json:"total"`
		Page     int                         `json:"page"`
		PageSize int                         `json:"page_size"`
	}
	list := func(query string) page {
		t.Helper()
		r := e.do(e.operator(), http.MethodGet, "/admin/orders"+query, nil)
		e.expect(r, http.StatusOK, "")
		var p page
		r.decode(t, &p)
		return p
	}
	only := func(p page, id string) {
		t.Helper()
		if p.Total != 1 || len(p.Items) != 1 || p.Items[0].ID.String() != id {
			t.Fatalf("want only %s, got %+v", id, p)
		}
	}
	if p := list(""); p.Total != 2 || p.Items[0].ID != o2.ID || p.Page != 1 || p.PageSize != 25 {
		t.Fatalf("all: %+v", p)
	}
	only(list("?status=accepted"), o1.ID.String())
	only(list("?q=%23"+strconv.FormatInt(o2.Number, 10)), o2.ID.String())
	only(list("?q="+o1.ID.String()), o1.ID.String())
	only(list("?q=8%20705%20123%2045%2067"), o1.ID.String())
	only(list("?q="+drv.Code), o1.ID.String())
	only(list("?q="+url.QueryEscape(drv.Phone)), o1.ID.String())
	only(list("?city_id="+e.cityID.String()+"&category=taxi&status=searching"), o2.ID.String())
	if p := list("?q=nothing-matches"); p.Total != 0 || len(p.Items) != 0 {
		t.Fatalf("no match: %+v", p)
	}
	if p := list("?page=2&page_size=1"); len(p.Items) != 1 || p.Total != 2 || p.Items[0].ID != o1.ID {
		t.Fatalf("paging: %+v", p)
	}
	e.expect(e.do(e.operator(), http.MethodGet, "/admin/orders?status=bogus", nil), http.StatusUnprocessableEntity, "validation_failed")
	e.expect(e.do(e.operator(), http.MethodGet, "/admin/orders?from=yesterday", nil), http.StatusUnprocessableEntity, "validation_failed")

	r := e.do(e.operator(), http.MethodGet, path("/admin/drivers/%s/orders", drv.DriverID), nil)
	e.expect(r, http.StatusOK, "")
	var dp page
	r.decode(t, &dp)
	if dp.Total != 1 || dp.Items[0].ID != o1.ID || dp.Items[0].Driver == nil || dp.Items[0].Driver.Code != drv.Code {
		t.Fatalf("driver orders: %+v", dp)
	}
	e.expect(e.do(e.operator(), http.MethodGet, path("/admin/drivers/%s/orders", o1.ID), nil), http.StatusNotFound, "not_found")
	e.expect(e.do(e.operator(), http.MethodGet, path("/admin/orders/%s", p1), nil), http.StatusNotFound, "not_found")
}

// waitForLockWait blocks until a session of this test database waits for a
// row lock.
func (e *env) waitForLockWait() {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e.count(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("no session waits for a lock")
}

// TestAdminAssignLosesToConcurrentAccept: an operator filling a searching
// order never takes it from a driver who accepted it a moment earlier.
func TestAdminAssignLosesToConcurrentAccept(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.driver(1000, true)
	b := e.driver(1000, true)
	o, _, _ := e.order(embiStops[:2], 800)

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := dispatch.Accept(ctx, tx, o.ID, a.DriverID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	done := make(chan response, 1)
	go func() {
		done <- e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/assign", o.ID), map[string]any{"driver_id": b.DriverID})
	}()
	e.waitForLockWait()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	e.expect(<-done, http.StatusConflict, "order_already_taken")
	if got := e.count(`SELECT count(*) FROM orders WHERE id = $1 AND driver_id = $2 AND status = 'accepted'`, o.ID, a.DriverID); got != 1 {
		t.Fatal("order taken from the accepting driver")
	}
	if _, res := e.wallet(b.DriverID); res != 0 {
		t.Fatalf("operator's driver reserved %d", res)
	}
}
