package orders_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
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
	adminCancel := map[string]any{"reason": "Көлік бұзылды"}
	passengerCancel := cancelBody("changed_plans")
	// Drivers have no cancel endpoint at all (§22.3).
	noRoute := func(r response) {
		t.Helper()
		if r.Status != http.StatusNotFound && r.Status != http.StatusMethodNotAllowed {
			t.Fatalf("driver cancel: %d %s", r.Status, r.Body)
		}
	}
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
	noRoute(act(drv.c, "cancel", adminCancel))

	e.expect(act(drv.c, "accept", nil), http.StatusOK, "")
	noRoute(act(drv.c, "cancel", adminCancel))
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
	// The passenger cannot cancel a trip in progress.
	invalid(e.do(pc, http.MethodPost, path("/orders/%s/cancel", id), passengerCancel), "in_progress", "cancel")
	// Rating only after completion.
	invalid(e.do(pc, http.MethodPost, path("/orders/%s/rating", id), map[string]any{"rating": 5}), "in_progress", "rate")

	e.expect(act(drv.c, "complete", nil), http.StatusOK, "")
	v = version()
	invalid(act(drv.c, "complete", nil), "completed", "complete")
	invalid(act(drv.c, "start", nil), "completed", "start")
	invalid(act(drv.c, "arriving", nil), "completed", "arrive")
	invalid(e.do(pc, http.MethodPost, path("/orders/%s/cancel", id), passengerCancel), "completed", "cancel")
	invalid(e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/cancel", id), adminCancel), "completed", "cancel")
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
	e.expect(e.do(cpc, http.MethodPost, path("/orders/%s/cancel", c.ID), passengerCancel), http.StatusOK, "")
	invalid(e.do(other.c, http.MethodPost, path("/driver/orders/%s/accept", c.ID), nil), "cancelled", "accept")
	invalid(e.do(cpc, http.MethodPost, path("/orders/%s/cancel", c.ID), passengerCancel), "cancelled", "cancel")
}

// TestPassengerIDOR: another passenger sees 404 for every order endpoint, and
// a driver sees only orders offered to or held by them.
func TestPassengerIDOR(t *testing.T) {
	e := newEnv(t)
	o, _, _ := e.order(embiStops[:2], 800)
	_, stranger := e.passenger()
	for _, r := range []response{
		e.do(stranger, http.MethodGet, path("/orders/%s", o.ID), nil),
		e.do(stranger, http.MethodPost, path("/orders/%s/cancel", o.ID), cancelBody("long_wait")),
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

// TestPassengerCancellation: only passengers cancel, with a reason (§22.3);
// the commission the driver paid is not returned (§22.2) and the driver is
// told without the passenger's reason.
func TestPassengerCancellation(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)

	o, _, pc := e.order(embiStops[:2], 800)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/arriving", o.ID), nil), http.StatusOK, "")
	cancel := func(body any) response {
		return e.do(pc, http.MethodPost, path("/orders/%s/cancel", o.ID), body)
	}
	for _, tc := range []struct {
		body        any
		field, want string
	}{
		{map[string]any{}, "reason_code", "required"},
		{map[string]any{"reason_code": "too_expensive"}, "reason_code", "invalid"},
		{map[string]any{"reason_code": "other"}, "comment", "required"},
		{map[string]any{"reason_code": "other", "comment": "   "}, "comment", "required"},
		{map[string]any{"reason_code": "long_wait", "comment": strings.Repeat("ә", 121)}, "comment", "too_long"},
	} {
		r := cancel(tc.body)
		e.expect(r, http.StatusUnprocessableEntity, "validation_failed")
		if got := r.err(t).Error.Fields[tc.field]; got != tc.want {
			t.Fatalf("%v: fields %s", tc.body, r.Body)
		}
	}
	e.expect(cancel(nil), http.StatusBadRequest, "bad_request")
	e.expect(cancel(map[string]any{"reason": "old field"}), http.StatusBadRequest, "bad_request")

	r := cancel(map[string]any{"reason_code": "other", "comment": "  Басқа көлік  таптым "})
	e.expect(r, http.StatusOK, "")
	var po orders.Order
	r.decode(t, &po)
	if po.Status != "cancelled" || po.CancelledBy == nil || *po.CancelledBy != "passenger" || po.CancelReason == nil ||
		*po.CancelReason != "Басқа көлік таптым" || po.CancelReasonCode == nil || *po.CancelReasonCode != "other" ||
		po.Driver.Phone != nil || po.CanCancel {
		t.Fatalf("cancelled order: %+v", po)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 950 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type <> 'commission_charge'`, o.ID); got != 0 {
		t.Fatalf("money moved back: %d rows", got)
	}
	if e.count(`SELECT count(*) FROM notification_outbox WHERE template = 'order_cancelled_driver' AND user_id = $1
		AND params ? 'reason' = false`, drv.UserID) != 1 {
		t.Fatal("driver not notified, or told the passenger's reason")
	}
	if e.count(`SELECT count(*) FROM order_status_events WHERE order_id = $1 AND to_status = 'cancelled'
		AND meta->>'reason_code' = 'other'`, o.ID) != 1 {
		t.Fatal("reason code not journaled")
	}
	// The driver sees that the passenger cancelled, never why.
	dr := e.do(drv.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil)
	e.expect(dr, http.StatusOK, "")
	var dv orders.DriverOrderDetail
	dr.decode(t, &dv)
	if dv.Status != "cancelled" || dv.CancelledBy == nil || *dv.CancelledBy != "passenger" || dv.CancelReason != nil ||
		dv.CommissionCharged != 50 {
		t.Fatalf("driver view of a passenger cancellation: %s", dr.Body)
	}
	hr := e.do(drv.c, http.MethodGet, "/driver/orders/history", nil)
	e.expect(hr, http.StatusOK, "")
	var hist struct {
		Items []orders.DriverOrderSummary `json:"items"`
	}
	hr.decode(t, &hist)
	if len(hist.Items) != 1 || hist.Items[0].CommissionCharged != 50 {
		t.Fatalf("history of a cancelled order: %s", hr.Body)
	}
	// The driver is free and has no way to cancel.
	e.expect(e.do(drv.c, http.MethodGet, "/driver/orders/active", nil), http.StatusNoContent, "")
	if r := e.do(drv.c, http.MethodPost, path("/driver/orders/%s/cancel", o.ID), cancelBody("other")); r.Status != http.StatusNotFound {
		t.Fatalf("driver cancel route: %d", r.Status)
	}

	// A reason without a comment is enough for the other codes.
	o2, _, pc2 := e.order(embiStops[:2], 900)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o2.ID), nil), http.StatusOK, "")
	r = e.do(pc2, http.MethodPost, path("/orders/%s/cancel", o2.ID), cancelBody("driver_issue"))
	e.expect(r, http.StatusOK, "")
	r.decode(t, &po)
	if po.CancelReasonCode == nil || *po.CancelReasonCode != "driver_issue" || po.CancelReason != nil {
		t.Fatalf("second cancel: %+v", po)
	}
	if bal, _ := e.wallet(drv.DriverID); bal != 900 {
		t.Fatalf("balance %d", bal)
	}

	// Cancelling while searching closes the open offers and their messages.
	other := e.driver(1000, true)
	o3, _, pc3 := e.order(embiStops[:2], 700)
	if _, err := e.pool.Exec(context.Background(), `UPDATE notification_outbox SET status = 'sent', telegram_message_id = 5
		WHERE template = 'order_available_driver' AND params->>'order_id' = $1`, o3.ID.String()); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do(pc3, http.MethodPost, path("/orders/%s/cancel", o3.ID), cancelBody("long_wait")), http.StatusOK, "")
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND status = 'cancelled'`, o3.ID); got != 2 {
		t.Fatalf("cancelled offers = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM notification_outbox o JOIN notification_outbox r ON r.id = o.ref_outbox_id
		WHERE o.template = 'order_offer_cancelled' AND r.params->>'order_id' = $1`, o3.ID.String()); got != 2 {
		t.Fatalf("cancelled offer edits = %d", got)
	}
	_ = other
}

// TestLegacyReservedOrders: an order accepted under the earlier commission
// model (a reservation, nothing charged) is still settled correctly: a
// passenger cancellation releases the reservation and a completion charges
// it once.
func TestLegacyReservedOrders(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	legacy := func(fare int) (orders.Order, caller) {
		t.Helper()
		o, _, pc := e.order(embiStops[:2], fare)
		ctx := context.Background()
		err := db.WithTx(ctx, e.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE orders SET status = 'accepted', driver_id = $2, commission_amount = 80,
				commission_mode = 'percent', accepted_at = now(), version = version + 1 WHERE id = $1`, o.ID, drv.DriverID); err != nil {
				return err
			}
			_, err := wallets.Reserve(ctx, tx, drv.DriverID, o.ID, 80)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return o, pc
	}
	o, pc := legacy(800)
	if bal, res := e.wallet(drv.DriverID); bal != 1000 || res != 80 {
		t.Fatalf("legacy wallet %d/%d", bal, res)
	}
	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/cancel", o.ID), cancelBody("changed_plans")), http.StatusOK, "")
	if bal, res := e.wallet(drv.DriverID); bal != 1000 || res != 0 {
		t.Fatalf("after cancel %d/%d", bal, res)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_release'`, o.ID); got != 1 {
		t.Fatalf("release rows = %d", got)
	}

	o2, _ := legacy(800)
	for _, step := range []string{"start", "complete"} {
		e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/%s", o2.ID, step), nil), http.StatusOK, "")
	}
	if bal, res := e.wallet(drv.DriverID); bal != 920 || res != 0 {
		t.Fatalf("after completion %d/%d", bal, res)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge'`, o2.ID); got != 1 {
		t.Fatalf("charges = %d", got)
	}
	if m, err := wallets.Reconcile(context.Background(), e.pool); err != nil || len(m) != 0 {
		t.Fatalf("reconcile: %v %+v", err, m)
	}
}
