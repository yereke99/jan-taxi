package orders_test

import (
	"net/http"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
)

// TestInvalidTransitions walks an order through its states and checks that
// every action not allowed from the current state is 409 invalid_transition
// with {from, action} and changes nothing.
func TestInvalidTransitions(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	o, _, pc := e.order(embiStops[:2], 800)
	id := o.ID
	act := func(c caller, action string, body any) response {
		return e.do(c, http.MethodPost, path("/driver/orders/%s/%s", id, action), body)
	}
	cancelBody := map[string]any{"reason": "Көлік бұзылды"}
	invalid := func(r response, from, action string) {
		t.Helper()
		e.expect(r, http.StatusConflict, "invalid_transition")
		d := r.err(t).Error.Details
		if d["from"] != from || d["action"] != action {
			t.Fatalf("details %v, want from=%s action=%s", d, from, action)
		}
	}
	version := func() int {
		return e.count(`SELECT version FROM orders WHERE id = $1`, id)
	}

	// searching: the order is not the driver's yet, so trip actions are 404.
	for _, a := range []string{"arriving", "start", "complete"} {
		e.expect(act(drv.c, a, nil), http.StatusNotFound, "not_found")
	}
	e.expect(act(drv.c, "cancel", cancelBody), http.StatusNotFound, "not_found")

	e.expect(act(drv.c, "accept", nil), http.StatusOK, "")
	v := version()
	invalid(act(drv.c, "complete", nil), "accepted", "complete")
	if version() != v {
		t.Fatal("version changed on a rejected transition")
	}

	e.expect(act(drv.c, "arriving", nil), http.StatusOK, "")
	invalid(act(drv.c, "arriving", nil), "driver_arriving", "arrive")
	invalid(act(drv.c, "complete", nil), "driver_arriving", "complete")

	e.expect(act(drv.c, "start", nil), http.StatusOK, "")
	invalid(act(drv.c, "arriving", nil), "in_progress", "arrive")
	invalid(act(drv.c, "start", nil), "in_progress", "start")
	invalid(act(drv.c, "cancel", cancelBody), "in_progress", "cancel")
	// The passenger cannot cancel a trip in progress.
	invalid(e.do(pc, http.MethodPost, path("/orders/%s/cancel", id), nil), "in_progress", "cancel")
	// Rating only after completion.
	invalid(e.do(pc, http.MethodPost, path("/orders/%s/rating", id), map[string]any{"rating": 5}), "in_progress", "rate")

	e.expect(act(drv.c, "complete", nil), http.StatusOK, "")
	v = version()
	invalid(act(drv.c, "complete", nil), "completed", "complete")
	invalid(act(drv.c, "start", nil), "completed", "start")
	invalid(act(drv.c, "arriving", nil), "completed", "arrive")
	invalid(act(drv.c, "cancel", cancelBody), "completed", "cancel")
	invalid(e.do(pc, http.MethodPost, path("/orders/%s/cancel", id), nil), "completed", "cancel")
	invalid(e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/cancel", id), cancelBody), "completed", "cancel")
	// Another driver learns only that the order was taken.
	other := e.driver(1000, true)
	e.expect(e.do(other.c, http.MethodPost, path("/driver/orders/%s/accept", id), nil), http.StatusConflict, "order_already_taken")
	e.expect(e.do(other.c, http.MethodGet, path("/driver/orders/%s", id), nil), http.StatusNotFound, "not_found")
	if version() != v || e.status(id) != "completed" {
		t.Fatal("completed order changed")
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge'`, id); got != 1 {
		t.Fatalf("charges = %d", got)
	}

	// A cancelled order cannot be accepted.
	c, _, cpc := e.order(embiStops[:2], 600)
	e.expect(e.do(cpc, http.MethodPost, path("/orders/%s/cancel", c.ID), nil), http.StatusOK, "")
	invalid(e.do(other.c, http.MethodPost, path("/driver/orders/%s/accept", c.ID), nil), "cancelled", "accept")
	invalid(e.do(cpc, http.MethodPost, path("/orders/%s/cancel", c.ID), nil), "cancelled", "cancel")
}

// TestPassengerIDOR: another passenger sees 404 for every order endpoint, and
// a driver sees only orders offered to or held by them.
func TestPassengerIDOR(t *testing.T) {
	e := newEnv(t)
	o, _, _ := e.order(embiStops[:2], 800)
	_, stranger := e.passenger()
	for _, r := range []response{
		e.do(stranger, http.MethodGet, path("/orders/%s", o.ID), nil),
		e.do(stranger, http.MethodPost, path("/orders/%s/cancel", o.ID), nil),
		e.do(stranger, http.MethodPost, path("/orders/%s/rating", o.ID), map[string]any{"rating": 1}),
		e.do(stranger, http.MethodPost, path("/orders/%s/dispute", o.ID), map[string]any{"reason": "Жүргізуші келмеді"}),
		e.do(stranger, http.MethodGet, "/orders/not-a-uuid", nil),
	} {
		e.expect(r, http.StatusNotFound, "not_found")
	}
	if e.status(o.ID) != "searching" {
		t.Fatal("order changed")
	}
	// A driver token cannot use passenger routes.
	drv := e.driver(1000, true)
	if r := e.do(drv.c, http.MethodGet, path("/orders/%s", o.ID), nil); r.Status != http.StatusForbidden && r.Status != http.StatusUnauthorized {
		t.Fatalf("driver on passenger route: %d", r.Status)
	}
	// Offline drivers do not see the searching order.
	off := e.driver(1000, false)
	e.expect(e.do(off.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil), http.StatusNotFound, "not_found")
	// After another driver takes it, the order disappears for everyone else.
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	late := e.driver(1000, true)
	e.expect(e.do(late.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil), http.StatusNotFound, "not_found")
	e.expect(e.do(late.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusConflict, "order_already_taken")
}

// TestCancellationReleasesReservation covers passenger and driver cancels
// after acceptance.
func TestCancellationReleasesReservation(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)

	o, _, pc := e.order(embiStops[:2], 800)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/arriving", o.ID), nil), http.StatusOK, "")
	r := e.do(pc, http.MethodPost, path("/orders/%s/cancel", o.ID), map[string]any{"reason": "Жоспарым өзгерді"})
	e.expect(r, http.StatusOK, "")
	var po orders.Order
	r.decode(t, &po)
	if po.Status != "cancelled" || po.CancelledBy == nil || *po.CancelledBy != "passenger" || po.CancelReason == nil ||
		*po.CancelReason != "Жоспарым өзгерді" || po.Driver.Phone != nil {
		t.Fatalf("cancelled order: %+v", po)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 1000 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_release'`, o.ID); got != 1 {
		t.Fatalf("release rows = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM wallet_reservations WHERE order_id = $1 AND status = 'released'`, o.ID); got != 1 {
		t.Fatalf("released reservations = %d", got)
	}
	if e.outbox("order_cancelled_driver", drv.UserID) != 1 {
		t.Fatal("driver not notified")
	}

	// Driver cancel needs a reason, then notifies the passenger.
	o2, pid2, _ := e.order(embiStops[:2], 900)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o2.ID), nil), http.StatusOK, "")
	r = e.do(drv.c, http.MethodPost, path("/driver/orders/%s/cancel", o2.ID), map[string]any{"reason": "   "})
	e.expect(r, http.StatusUnprocessableEntity, "validation_failed")
	if r.err(t).Error.Fields["reason"] != "required" {
		t.Fatalf("fields: %s", r.Body)
	}
	r = e.do(drv.c, http.MethodPost, path("/driver/orders/%s/cancel", o2.ID), map[string]any{"reason": "Көлік бұзылды"})
	e.expect(r, http.StatusOK, "")
	var dd orders.DriverOrderDetail
	r.decode(t, &dd)
	if dd.Status != "cancelled" || dd.CancelledBy == nil || *dd.CancelledBy != "driver" || dd.CanCancel {
		t.Fatalf("driver cancel: %+v", dd)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 1000 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	if e.outbox("order_cancelled_passenger", pid2) != 1 {
		t.Fatal("passenger not notified")
	}

	// Cancelling while searching closes the open offers.
	other := e.driver(1000, true)
	o3, _, pc3 := e.order(embiStops[:2], 700)
	e.expect(e.do(pc3, http.MethodPost, path("/orders/%s/cancel", o3.ID), nil), http.StatusOK, "")
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND status = 'cancelled'`, o3.ID); got != 2 {
		t.Fatalf("cancelled offers = %d", got)
	}
	_ = other
}
