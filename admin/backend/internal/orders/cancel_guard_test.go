package orders_test

import (
	"context"
	"net/http"
	"testing"
)

// A passenger who confirms "cancel" on a searching order while a driver
// takes it must not cancel the accepted order: the driver has just paid the
// commission (§22.2). The app sends the status it showed; a changed order is
// 409 order_changed and stays as it is.
func TestPassengerCancelRefusedWhenOrderChanged(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	o, _, pc := e.order(embiStops[:2], 800)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")

	body := cancelBody("changed_plans")
	body["expected_status"] = "searching"
	r := e.do(pc, http.MethodPost, path("/orders/%s/cancel", o.ID), body)
	e.expect(r, http.StatusConflict, "conflict")
	if d := r.err(t).Error.Details; d["reason"] != "order_changed" || d["status"] != "accepted" {
		t.Fatalf("details: %s", r.Body)
	}
	if s := e.status(o.ID); s != "accepted" {
		t.Fatalf("status after refused cancel = %s", s)
	}
	if n := e.count(`SELECT count(*) FROM order_status_events WHERE order_id = $1 AND to_status = 'cancelled'`, o.ID); n != 0 {
		t.Fatalf("cancel events = %d", n)
	}
	if e.outbox("order_cancelled_driver", drv.UserID) != 0 {
		t.Fatal("driver told about a cancellation that did not happen")
	}

	// The passenger saw the driver and still cancels: allowed (and the
	// commission is kept, §22.2).
	body["expected_status"] = "accepted"
	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/cancel", o.ID), body), http.StatusOK, "")
	if s := e.status(o.ID); s != "cancelled" {
		t.Fatalf("status = %s", s)
	}
	if bal, _ := e.wallet(drv.DriverID); bal != 950 {
		t.Fatalf("driver balance = %d", bal)
	}

	// Unknown statuses are rejected; requests without expected_status
	// (older apps) keep working.
	o2, _, pc2 := e.order(embiStops[:2], 800)
	bad := cancelBody("changed_plans")
	bad["expected_status"] = "flying"
	r = e.do(pc2, http.MethodPost, path("/orders/%s/cancel", o2.ID), bad)
	e.expect(r, http.StatusUnprocessableEntity, "validation_failed")
	if r.err(t).Error.Fields["expected_status"] != "invalid" {
		t.Fatalf("fields: %s", r.Body)
	}
	e.expect(e.do(pc2, http.MethodPost, path("/orders/%s/cancel", o2.ID), cancelBody("changed_plans")), http.StatusOK, "")
}

// Creating orders and cancelling them once a driver has paid the commission
// is limited per passenger (orders.passenger_cancels_after_accept_per_day).
func TestPassengerCancelsAfterAcceptAreLimited(t *testing.T) {
	e := newEnv(t)
	e.setting("orders.passenger_cancels_after_accept_per_day", "2")
	drv := e.driver(5000, true)
	pid, pc := e.passenger()

	place := func() string {
		t.Helper()
		r := e.do(pc, http.MethodPost, "/orders", taxiBody(e.quote(pid, "taxi", embiStops[:2]), 800))
		if r.Status != http.StatusCreated {
			return r.err(t).Error.Details["reason"].(string)
		}
		var o struct {
			ID string `json:"id"`
		}
		r.decode(t, &o)
		return o.ID
	}
	// Cancelling while still searching costs nobody anything: not counted.
	for range 3 {
		id := place()
		e.expect(e.do(pc, http.MethodPost, "/orders/"+id+"/cancel", cancelBody("changed_plans")), http.StatusOK, "")
	}
	for range 2 {
		id := place()
		e.expect(e.do(drv.c, http.MethodPost, "/driver/orders/"+id+"/accept", nil), http.StatusOK, "")
		e.expect(e.do(pc, http.MethodPost, "/orders/"+id+"/cancel", cancelBody("changed_plans")), http.StatusOK, "")
	}
	r := e.do(pc, http.MethodPost, "/orders", taxiBody(e.quote(pid, "taxi", embiStops[:2]), 800))
	e.expect(r, http.StatusConflict, "conflict")
	if d := r.err(t).Error.Details; d["reason"] != "too_many_cancellations" || d["limit"] != float64(2) {
		t.Fatalf("details: %s", r.Body)
	}

	// Cancellations older than 24 hours no longer count.
	if _, err := e.pool.Exec(context.Background(), `
		UPDATE orders SET cancelled_at = now() - interval '25 hours'
		WHERE id = (SELECT id FROM orders WHERE passenger_id = $1 AND accepted_at IS NOT NULL
		            ORDER BY cancelled_at LIMIT 1)`, pid); err != nil {
		t.Fatal(err)
	}
	id := place()
	if len(id) != 36 {
		t.Fatalf("after a day: %s", id)
	}
	e.expect(e.do(drv.c, http.MethodPost, "/driver/orders/"+id+"/accept", nil), http.StatusOK, "")
	e.expect(e.do(pc, http.MethodPost, "/orders/"+id+"/cancel", cancelBody("changed_plans")), http.StatusOK, "")
	if reason := place(); reason != "too_many_cancellations" {
		t.Fatalf("limit reached again: %s", reason)
	}
	// 0 switches the limit off.
	e.setting("orders.passenger_cancels_after_accept_per_day", "0")
	if id := place(); len(id) != 36 {
		t.Fatalf("no limit: %s", id)
	}
}
