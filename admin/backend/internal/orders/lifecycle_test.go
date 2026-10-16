package orders_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// TestFullLifecycle drives one order from creation to rating through the HTTP
// API and checks money, stops, notifications and history along the way.
func TestFullLifecycle(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	o, pid, pc := e.order(embiStops[:3], 800)

	if o.Status != "searching" || o.StopCount != 3 || o.Fare != 800 || o.CanCancel != true {
		t.Fatalf("created order: %+v", o)
	}
	if len(o.Events) != 1 || o.Events[0].ToStatus != "searching" || o.Events[0].ActorType != "passenger" {
		t.Fatalf("creation event: %+v", o.Events)
	}
	if e.outbox("order_created_passenger", pid) != 1 || e.outbox("order_available_driver", drv.UserID) != 1 {
		t.Fatal("creation notifications missing")
	}
	if n := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND driver_id = $2 AND status = 'sent'`, o.ID, drv.DriverID); n != 1 {
		t.Fatalf("offers = %d", n)
	}

	// Available list and card.
	var avail dispatch.Available
	r := e.do(drv.c, http.MethodGet, "/driver/orders", nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &avail)
	if !avail.Eligibility.Eligible || len(avail.Items) != 1 || avail.Items[0].ID != o.ID || avail.Items[0].CommissionAmount != 80 {
		t.Fatalf("available: %+v", avail)
	}
	if len(avail.Items[0].Stops) != 3 || avail.Items[0].Stops[2].Kind != "destination" {
		t.Fatalf("card stops: %+v", avail.Items[0].Stops)
	}
	// Available detail withholds contact data and address details.
	var dd orders.DriverOrderDetail
	r = e.do(drv.c, http.MethodGet, path("/driver/orders/%s", o.ID), nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &dd)
	if dd.IsMine || !dd.CanAccept || dd.Passenger != nil || dd.Stops[0].AddressDetails != nil || len(dd.Events) != 0 {
		t.Fatalf("available detail leaks data: %+v", dd)
	}

	// Accept.
	r = e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &dd)
	if !dd.IsMine || dd.Status != "accepted" || dd.Passenger == nil || dd.Stops[0].AddressDetails == nil ||
		!dd.CanArrive || !dd.CanStart || !dd.CanCancel || dd.CanComplete {
		t.Fatalf("accepted detail: %+v", dd)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 1000 || res != 80 {
		t.Fatalf("after accept wallet = %d/%d", bal, res)
	}
	if e.outbox("order_accepted_passenger", pid) != 1 || e.outbox("order_accepted_driver", drv.UserID) != 1 {
		t.Fatal("accept notifications missing")
	}

	// Passenger sees the driver with phone and vehicle while active.
	var po orders.Order
	r = e.do(pc, http.MethodGet, path("/orders/%s", o.ID), nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &po)
	if po.Driver == nil || po.Driver.Phone == nil || po.Driver.LastName != "D." || po.Vehicle == nil || po.Vehicle.Make != "Toyota" {
		t.Fatalf("passenger driver view: %+v %+v", po.Driver, po.Vehicle)
	}

	// Arrive, start.
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/arriving", o.ID), nil), http.StatusOK, "")
	if e.outbox("order_driver_arriving", pid) != 1 {
		t.Fatal("arriving notification missing")
	}
	r = e.do(drv.c, http.MethodPost, path("/driver/orders/%s/start", o.ID), nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &dd)
	if dd.Status != "in_progress" || dd.Stops[0].CompletedAt == nil || dd.CanComplete || dd.NextStopID == nil || *dd.NextStopID != dd.Stops[1].ID {
		t.Fatalf("started detail: %+v", dd)
	}

	// Complete before the intermediate stop is refused with the pending ids.
	r = e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil)
	e.expect(r, http.StatusConflict, "invalid_transition")
	if ids, _ := r.err(t).Error.Details["pending_stop_ids"].([]any); len(ids) != 1 || ids[0] != dd.Stops[1].ID.String() {
		t.Fatalf("pending stops: %s", r.Body)
	}
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/stops/%s/complete", o.ID, dd.Stops[1].ID), nil), http.StatusOK, "")
	// Idempotent for an already completed stop.
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/stops/%s/complete", o.ID, dd.Stops[1].ID), nil), http.StatusOK, "")
	if n := e.outbox("order_stop_completed", pid); n != 1 {
		t.Fatalf("stop notifications = %d", n)
	}

	r = e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &dd)
	if dd.Status != "completed" || dd.CompletedAt == nil || dd.Stops[2].CompletedAt == nil || dd.Passenger != nil {
		t.Fatalf("completed detail: %+v", dd)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 920 || res != 0 {
		t.Fatalf("after complete wallet = %d/%d", bal, res)
	}
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge'`, o.ID); n != 1 {
		t.Fatalf("charges = %d", n)
	}
	if n := e.count(`SELECT completed_trips FROM driver_profiles WHERE id = $1`, drv.DriverID); n != 1 {
		t.Fatalf("completed_trips = %d", n)
	}
	if e.outbox("order_completed_passenger", pid) != 1 || e.outbox("order_completed_driver", drv.UserID) != 1 {
		t.Fatal("completion notifications missing")
	}
	mism, err := wallets.Reconcile(context.Background(), e.pool)
	if err != nil || len(mism) != 0 {
		t.Fatalf("reconcile: %v %+v", err, mism)
	}

	// Passenger history and rating.
	r = e.do(pc, http.MethodGet, path("/orders/%s", o.ID), nil)
	r.decode(t, &po)
	if po.Driver.Phone != nil || po.DriverLocation != nil || !po.CanRate || !po.CanDispute || po.CanCancel {
		t.Fatalf("completed passenger view: %+v", po)
	}
	wantEvents := []string{"searching", "accepted", "driver_arriving", "in_progress", "completed"}
	if len(po.Events) != len(wantEvents) {
		t.Fatalf("events: %+v", po.Events)
	}
	for i, ev := range po.Events {
		if ev.ToStatus != wantEvents[i] {
			t.Fatalf("event %d = %s, want %s", i, ev.ToStatus, wantEvents[i])
		}
	}
	r = e.do(pc, http.MethodPost, path("/orders/%s/rating", o.ID), map[string]any{"rating": 5, "comment": "Жүргізуші сыпайы, көлігі таза"})
	e.expect(r, http.StatusOK, "")
	r.decode(t, &po)
	if po.Rating == nil || po.Rating.Rating != 5 || po.CanRate {
		t.Fatalf("rating: %+v", po.Rating)
	}
	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/rating", o.ID), map[string]any{"rating": 4}), http.StatusConflict, "conflict")
	if n := e.count(`SELECT rating_sum * 100 + rating_count FROM driver_profiles WHERE id = $1`, drv.DriverID); n != 501 {
		t.Fatalf("rating aggregate = %d", n)
	}

	// Lists.
	var list struct {
		Items      []orders.OrderSummary `json:"items"`
		NextCursor *string               `json:"next_cursor"`
	}
	r = e.do(pc, http.MethodGet, "/orders?scope=history", nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &list)
	if len(list.Items) != 1 || list.Items[0].Rating == nil || *list.Items[0].Rating != 5 || list.NextCursor != nil {
		t.Fatalf("history: %+v", list)
	}
	r = e.do(pc, http.MethodGet, "/orders?scope=active", nil)
	r.decode(t, &list)
	if len(list.Items) != 0 {
		t.Fatalf("active: %+v", list)
	}
	var dh struct {
		Items []orders.DriverOrderSummary `json:"items"`
	}
	r = e.do(drv.c, http.MethodGet, "/driver/orders/history", nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &dh)
	if len(dh.Items) != 1 || dh.Items[0].CommissionAmount != 80 || dh.Items[0].Destination.Seq != 2 {
		t.Fatalf("driver history: %+v", dh)
	}
	e.expect(e.do(drv.c, http.MethodGet, "/driver/orders/active", nil), http.StatusNoContent, "")
}

// TestStopsOrderPreserved checks that five stops keep their order end to end
// and that intermediate stops must be completed in sequence.
func TestStopsOrderPreserved(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	o, _, pc := e.order(embiStops, 1000)
	if len(o.Stops) != 5 {
		t.Fatalf("stops: %d", len(o.Stops))
	}
	kinds := []string{"pickup", "intermediate", "intermediate", "intermediate", "destination"}
	for i, s := range o.Stops {
		if s.Seq != i || s.Kind != kinds[i] || s.Address != embiStops[i].Address || s.Lat != embiStops[i].Lat {
			t.Fatalf("stop %d: %+v", i, s)
		}
	}
	if o.Stops[0].AddressDetails == nil || *o.Stops[0].AddressDetails != "2-кіреберіс" || o.Stops[1].AddressDetails != nil {
		t.Fatalf("address details: %+v", o.Stops)
	}
	var legs []orders.RouteLeg
	if err := jsonUnmarshal(o.Route.Legs, &legs); err != nil || len(legs) != 4 || o.Route.DistanceM != o.DistanceM {
		t.Fatalf("route: %+v %v", o.Route, err)
	}

	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	// Stops cannot be completed before the trip starts.
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/stops/%s/complete", o.ID, o.Stops[1].ID), nil), http.StatusConflict, "invalid_transition")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/start", o.ID), nil), http.StatusOK, "")

	// Out of order.
	r := e.do(drv.c, http.MethodPost, path("/driver/orders/%s/stops/%s/complete", o.ID, o.Stops[2].ID), nil)
	e.expect(r, http.StatusConflict, "invalid_transition")
	if r.err(t).Error.Details["next_stop_id"] != o.Stops[1].ID.String() {
		t.Fatalf("next stop: %s", r.Body)
	}
	// The destination is completed by /complete, the pickup by /start.
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/stops/%s/complete", o.ID, o.Stops[4].ID), nil), http.StatusConflict, "invalid_transition")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/stops/%s/complete", o.ID, o.Stops[0].ID), nil), http.StatusConflict, "invalid_transition")
	// A stop of another order is not found.
	other, _, _ := e.order(embiStops[:3], 600)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/stops/%s/complete", o.ID, other.Stops[1].ID), nil), http.StatusNotFound, "not_found")

	for _, s := range o.Stops[1:4] {
		e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/stops/%s/complete", o.ID, s.ID), nil), http.StatusOK, "")
	}
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil), http.StatusOK, "")

	var po orders.Order
	r = e.do(pc, http.MethodGet, path("/orders/%s", o.ID), nil)
	r.decode(t, &po)
	prev := po.Stops[0].CompletedAt
	for i, s := range po.Stops {
		if s.CompletedAt == nil || s.ID != o.Stops[i].ID {
			t.Fatalf("stop %d after completion: %+v", i, s)
		}
		if s.CompletedAt.Before(*prev) {
			t.Fatalf("stop %d completed before stop %d", i, i-1)
		}
		prev = s.CompletedAt
	}
}

// TestLowBalanceNotice: a completion that leaves less than the minimum online
// balance tells the driver to top up.
func TestLowBalanceNotice(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(120, true)
	o, _, _ := e.order(embiStops[:2], 1000)
	for _, step := range []string{"accept", "start", "complete"} {
		e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/%s", o.ID, step), nil), http.StatusOK, "")
	}
	if bal, res := e.wallet(drv.DriverID); bal != 20 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	if e.outbox("wallet_low_balance", drv.UserID) != 1 || e.outbox("order_completed_driver", drv.UserID) != 1 {
		t.Fatal("low balance notice missing")
	}
	// The driver no longer covers a new order's commission.
	o2, _, _ := e.order(embiStops[:2], 800)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o2.ID), nil), http.StatusPaymentRequired, "insufficient_balance")
}
