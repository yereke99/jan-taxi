package orders_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// TestConcurrentAcceptExactlyOneWinner: 50 eligible drivers accept the same
// order at once; exactly one wins, the commission is charged exactly once
// (§22.2) and every other driver's offer message is marked taken (§22.4).
func TestConcurrentAcceptExactlyOneWinner(t *testing.T) {
	e := newEnv(t)
	const n = 50
	drivers := make([]testDriver, n)
	for i := range drivers {
		// Fresh fixes within 600 m of the pickup (48.8264, 58.1442).
		drivers[i] = e.driverAt("EMB", 1000, 48.8264+float64(i)*0.0001, 58.1442-float64(i%5)*0.0002)
	}
	o, pid, _ := e.order(embiStops[:2], 800)
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND status = 'sent'`, o.ID); got != n {
		t.Fatalf("offers sent = %d, want %d", got, n)
	}
	// Every offer reached the driver bot (message ids 1..n).
	if _, err := e.pool.Exec(context.Background(), `
		UPDATE notification_outbox SET status = 'sent', sent_at = now(), telegram_message_id = 1000 + attempts
		WHERE template = 'order_available_driver' AND params->>'order_id' = $1`, o.ID.String()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]response, n)
	for i := range drivers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = e.do(drivers[i].c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil)
		}(i)
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, r := range results {
		switch {
		case r.Status == http.StatusOK:
			if winner >= 0 {
				t.Fatalf("second winner %d (first %d)", i, winner)
			}
			winner = i
		case r.Status == http.StatusConflict && r.err(t).Error.Code == "order_already_taken":
		default:
			t.Fatalf("driver %d: %d %s", i, r.Status, r.Body)
		}
	}
	if winner < 0 {
		t.Fatal("no winner")
	}
	w := drivers[winner]
	if s := e.status(o.ID); s != "accepted" {
		t.Fatalf("status %s", s)
	}
	if got := e.count(`SELECT count(*) FROM orders WHERE id = $1 AND driver_id = $2`, o.ID, w.DriverID); got != 1 {
		t.Fatal("order not assigned to the winner")
	}
	if got := e.count(`SELECT count(*) FROM wallet_reservations WHERE order_id = $1`, o.ID); got != 0 {
		t.Fatalf("reservations = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1`, o.ID); got != 1 {
		t.Fatalf("ledger rows = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id
		WHERE l.order_id = $1 AND l.entry_type = 'commission_charge' AND l.balance_delta = -50
		  AND l.idempotency_key = 'charge:' || $1 AND w.driver_id = $2`, o.ID, w.DriverID); got != 1 {
		t.Fatal("winner's commission charge missing")
	}
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND status = 'accepted' AND driver_id = $2`, o.ID, w.DriverID); got != 1 {
		t.Fatal("winner offer not accepted")
	}
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND status = 'lost'`, o.ID); got != n-1 {
		t.Fatalf("lost offers = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM order_status_events WHERE order_id = $1 AND to_status = 'accepted'`, o.ID); got != 1 {
		t.Fatalf("accept events = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM notification_outbox o JOIN notification_outbox r ON r.id = o.ref_outbox_id
		WHERE o.kind = 'edit' AND o.template = 'order_offer_taken' AND r.params->>'order_id' = $1`, o.ID.String()); got != n-1 {
		t.Fatalf("taken offer edits = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM notification_outbox o JOIN notification_outbox r ON r.id = o.ref_outbox_id
		WHERE o.kind = 'edit' AND o.template = 'order_offer_won' AND o.user_id = $1 AND r.params->>'order_id' = $2`,
		w.UserID, o.ID.String()); got != 1 {
		t.Fatalf("won offer edits = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM notification_outbox WHERE template = 'order_taken_driver'`); got != 0 {
		t.Fatalf("separate taken messages = %d", got)
	}
	if e.outbox("order_accepted_passenger", pid) != 1 {
		t.Fatal("passenger not notified once")
	}
	for i, d := range drivers {
		bal, res := e.wallet(d.DriverID)
		want := int64(1000)
		if i == winner {
			want = 950
		}
		if bal != want || res != 0 {
			t.Fatalf("driver %d wallet %d/%d", i, bal, res)
		}
	}
	if m, err := wallets.Reconcile(context.Background(), e.pool); err != nil || len(m) != 0 {
		t.Fatalf("reconcile: %v %+v", err, m)
	}
}

// TestConcurrentCompleteChargesOnce: the commission is charged once when the
// order is taken; 20 simultaneous completes charge nothing more.
func TestConcurrentCompleteChargesOnce(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(500, true)
	o, _, _ := e.order(embiStops[:2], 1000)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	if bal, res := e.wallet(drv.DriverID); bal != 450 || res != 0 {
		t.Fatalf("wallet after accept %d/%d", bal, res)
	}
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/start", o.ID), nil), http.StatusOK, "")

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]response, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil)
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, r := range results {
		switch {
		case r.Status == http.StatusOK:
			ok++
		case r.Status == http.StatusConflict && r.err(t).Error.Code == "invalid_transition":
		default:
			t.Fatalf("complete: %d %s", r.Status, r.Body)
		}
	}
	if ok != 1 {
		t.Fatalf("successful completes = %d", ok)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge'`, o.ID); got != 1 {
		t.Fatalf("charges = %d", got)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 450 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	if got := e.count(`SELECT completed_trips FROM driver_profiles WHERE id = $1`, drv.DriverID); got != 1 {
		t.Fatalf("completed_trips = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM order_status_events WHERE order_id = $1 AND to_status = 'completed'`, o.ID); got != 1 {
		t.Fatalf("completion events = %d", got)
	}
}

// TestAcceptInsufficientBalance: 402 with amounts, nothing changes.
func TestAcceptInsufficientBalance(t *testing.T) {
	e := newEnv(t)
	poor := e.driver(30, true)
	o, _, _ := e.order(embiStops[:2], 800)
	// The poor driver is not offered the order and does not see it.
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1`, o.ID); got != 0 {
		t.Fatalf("offers = %d", got)
	}
	r := e.do(poor.c, http.MethodGet, "/driver/orders", nil)
	e.expect(r, http.StatusOK, "")
	var avail struct {
		Items       []any `json:"items"`
		Eligibility struct {
			Eligible bool    `json:"eligible"`
			Reason   *string `json:"reason"`
		} `json:"eligibility"`
	}
	r.decode(t, &avail)
	if len(avail.Items) != 0 || avail.Eligibility.Eligible || avail.Eligibility.Reason == nil || *avail.Eligibility.Reason != "insufficient_balance" {
		t.Fatalf("eligibility: %s", r.Body)
	}

	r = e.do(poor.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil)
	e.expect(r, http.StatusPaymentRequired, "insufficient_balance")
	d := r.err(t).Error.Details
	if d["required"] != float64(50) || d["available"] != float64(30) || d["currency"] != "KZT" || d["topup_min_amount"] != float64(500) {
		t.Fatalf("details: %v", d)
	}
	if s := e.status(o.ID); s != "searching" {
		t.Fatalf("status %s", s)
	}
	// The driver who missed the offer for lack of money is told once a day,
	// with a top-up button (§22.2), however many orders follow.
	e.order(embiStops[:2], 900)
	if got := e.count(`SELECT count(*) FROM notification_outbox WHERE template = 'wallet_low_balance' AND user_id = $1
		AND params->>'available' = '30' AND params->>'required' = '50'`, poor.UserID); got != 1 {
		t.Fatalf("low balance messages = %d", got)
	}
	if bal, res := e.wallet(poor.DriverID); bal != 30 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1`, o.ID); got != 0 {
		t.Fatalf("ledger rows = %d", got)
	}
	if got := e.count(`SELECT count(*) FROM order_status_events WHERE order_id = $1`, o.ID); got != 1 {
		t.Fatalf("events = %d", got)
	}
}

// TestDriverWithActiveOrderCannotAccept: 409 driver_has_active_order.
func TestDriverWithActiveOrderCannotAccept(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	first, _, _ := e.order(embiStops[:2], 800)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", first.ID), nil), http.StatusOK, "")
	second, _, _ := e.order(embiStops[1:3], 700)

	r := e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", second.ID), nil)
	e.expect(r, http.StatusConflict, "driver_has_active_order")
	if r.err(t).Error.Details["active_order_id"] != first.ID.String() {
		t.Fatalf("details: %s", r.Body)
	}
	if s := e.status(second.ID); s != "searching" {
		t.Fatalf("second order status %s", s)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 950 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	// A repeated accept of the order the driver already holds is a no-op.
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", first.ID), nil), http.StatusOK, "")
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1`, first.ID); got != 1 {
		t.Fatalf("ledger rows = %d", got)
	}
}
