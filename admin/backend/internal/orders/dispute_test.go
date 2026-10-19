package orders_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// trip creates an order and drives it to in_progress with drv.
func (e *env) trip(drv testDriver, fare int) (orders.Order, uuid.UUID, caller) {
	e.t.Helper()
	o, pid, pc := e.order(embiStops[:2], fare)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/start", o.ID), nil), http.StatusOK, "")
	return o, pid, pc
}

func (e *env) resolve(disputeID uuid.UUID, body map[string]any) response {
	return e.do(e.operator(), http.MethodPost, path("/admin/disputes/%s/resolve", disputeID), body)
}

type disputePage struct {
	Items []orders.Dispute `json:"items"`
	Total int              `json:"total"`
}

// TestDisputeRefund: a completed trip disputed by the passenger and resolved
// as cancelled with a refund returns the commission exactly once.
func TestDisputeRefund(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	o, pid, pc := e.trip(drv, 800)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil), http.StatusOK, "")
	if bal, _ := e.wallet(drv.DriverID); bal != 950 {
		t.Fatalf("balance after completion %d", bal)
	}

	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/dispute", o.ID), map[string]any{"reason": " "}), http.StatusUnprocessableEntity, "validation_failed")
	r := e.do(pc, http.MethodPost, path("/orders/%s/dispute", o.ID), map[string]any{"reason": "Жүргізуші мені межелі жерге жеткізбеді"})
	e.expect(r, http.StatusOK, "")
	var po orders.Order
	r.decode(t, &po)
	if po.Status != "disputed" || po.CanDispute || po.CanRate || po.Dispute == nil || po.Dispute.Status != "open" {
		t.Fatalf("disputed order: %+v", po)
	}
	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/dispute", o.ID), map[string]any{"reason": "Тағы да"}), http.StatusConflict, "conflict")
	if e.outbox("dispute_opened", drv.UserID) != 1 || e.outbox("dispute_opened", pid) != 0 {
		t.Fatal("dispute_opened notifications")
	}

	r = e.do(e.operator(), http.MethodGet, "/admin/disputes?status=open", nil)
	e.expect(r, http.StatusOK, "")
	var dp disputePage
	r.decode(t, &dp)
	if dp.Total != 1 || dp.Items[0].OrderID != o.ID || dp.Items[0].PreviousStatus != "completed" || dp.Items[0].OpenedByType != "passenger" {
		t.Fatalf("open disputes: %+v", dp)
	}
	d := dp.Items[0]

	e.expect(e.resolve(d.ID, map[string]any{"outcome": "completed", "resolution": "x", "refund_commission": true}),
		http.StatusUnprocessableEntity, "validation_failed")
	r = e.resolve(d.ID, map[string]any{"outcome": "cancelled", "resolution": "Сапар орындалмады, комиссия қайтарылды", "refund_commission": true})
	e.expect(r, http.StatusOK, "")
	var resolved orders.Dispute
	r.decode(t, &resolved)
	if resolved.Status != "resolved" || resolved.Outcome == nil || *resolved.Outcome != "cancelled" || resolved.ResolvedBy == nil ||
		resolved.ResolvedBy.ID != e.adminID || resolved.ResolvedAt == nil {
		t.Fatalf("resolved: %+v", resolved)
	}
	if s := e.status(o.ID); s != "cancelled" {
		t.Fatalf("order %s", s)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 1000 || res != 0 {
		t.Fatalf("wallet after refund %d/%d", bal, res)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_refund'`, o.ID); got != 1 {
		t.Fatalf("refunds = %d", got)
	}
	if got := e.count(`SELECT completed_trips FROM driver_profiles WHERE id = $1`, drv.DriverID); got != 0 {
		t.Fatalf("completed_trips = %d", got)
	}
	if e.outbox("dispute_resolved", pid) != 1 || e.outbox("dispute_resolved", drv.UserID) != 1 {
		t.Fatal("dispute_resolved notifications")
	}
	if e.audits("dispute.resolve", d.ID.String()) != 1 {
		t.Fatal("resolution not audited")
	}

	// Resolving twice is refused and refunds nothing more.
	e.expect(e.resolve(d.ID, map[string]any{"outcome": "cancelled", "resolution": "Қайта", "refund_commission": true}),
		http.StatusConflict, "invalid_transition")
	if bal, _ := e.wallet(drv.DriverID); bal != 1000 {
		t.Fatalf("balance %d", bal)
	}
	e.expect(e.resolve(uuid.New(), map[string]any{"outcome": "cancelled", "resolution": "x"}), http.StatusNotFound, "not_found")
	if m, err := wallets.Reconcile(context.Background(), e.pool); err != nil || len(m) != 0 {
		t.Fatalf("reconcile: %v %+v", err, m)
	}
	r = e.do(pc, http.MethodGet, path("/orders/%s", o.ID), nil)
	r.decode(t, &po)
	if po.Dispute == nil || po.Dispute.Status != "resolved" || po.Dispute.Resolution == nil || po.CanDispute {
		t.Fatalf("passenger dispute view: %+v", po.Dispute)
	}
}

// TestDisputeDuringTrip: the commission was charged when the driver took the
// order; a dispute opened while in progress keeps it when resolved completed
// or cancelled, and refunds it only with refund_commission.
func TestDisputeDuringTrip(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)

	o, _, pc := e.trip(drv, 800)
	e.expect(e.do(pc, http.MethodPost, path("/orders/%s/dispute", o.ID), map[string]any{"reason": "Жүргізуші бағытты өзгертті"}), http.StatusOK, "")
	// The trip can no longer be completed by the driver.
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil), http.StatusConflict, "invalid_transition")
	e.expect(e.do(drv.c, http.MethodGet, "/driver/orders/active", nil), http.StatusNoContent, "")
	var dp disputePage
	r := e.do(e.operator(), http.MethodGet, "/admin/disputes", nil)
	r.decode(t, &dp)
	e.expect(e.resolve(dp.Items[0].ID, map[string]any{"outcome": "completed", "resolution": "Сапар орындалды"}), http.StatusOK, "")
	if s := e.status(o.ID); s != "completed" {
		t.Fatalf("order %s", s)
	}
	if bal, res := e.wallet(drv.DriverID); bal != 950 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge'`, o.ID); got != 1 {
		t.Fatalf("charges = %d", got)
	}
	if got := e.count(`SELECT completed_trips FROM driver_profiles WHERE id = $1`, drv.DriverID); got != 1 {
		t.Fatalf("completed_trips = %d", got)
	}

	// Opened by an operator, resolved as cancelled without a refund: the
	// commission stays charged.
	o2, pid2, _ := e.trip(drv, 1000)
	r = e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/dispute", o2.ID), map[string]any{"reason": "Жолаушы шағымы"})
	e.expect(r, http.StatusOK, "")
	var ao orders.AdminOrder
	r.decode(t, &ao)
	if ao.Status != "disputed" || ao.Dispute == nil || ao.Dispute.OpenedByType != "admin" || ao.Dispute.PreviousStatus != "in_progress" {
		t.Fatalf("admin dispute: %+v", ao.Dispute)
	}
	if e.outbox("dispute_opened", pid2) != 1 || e.audits("order.dispute", o2.ID.String()) != 1 {
		t.Fatal("operator dispute not notified or audited")
	}
	e.expect(e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/dispute", o2.ID), map[string]any{"reason": "Қайта"}),
		http.StatusConflict, "conflict")
	if bal, res := e.wallet(drv.DriverID); bal != 900 || res != 0 {
		t.Fatalf("wallet during dispute %d/%d", bal, res)
	}
	e.expect(e.resolve(ao.Dispute.ID, map[string]any{"outcome": "cancelled", "resolution": "Тапсырыс жойылды"}), http.StatusOK, "")
	if bal, res := e.wallet(drv.DriverID); bal != 900 || res != 0 {
		t.Fatalf("wallet %d/%d", bal, res)
	}
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type <> 'commission_charge'`, o2.ID); got != 0 {
		t.Fatalf("other ledger rows = %d", got)
	}

	// Disputed during the trip and resolved cancelled with a refund.
	o3, _, pc3 := e.trip(drv, 900)
	e.expect(e.do(pc3, http.MethodPost, path("/orders/%s/dispute", o3.ID), map[string]any{"reason": "Жүргізуші келмеді"}), http.StatusOK, "")
	r = e.do(e.operator(), http.MethodGet, "/admin/disputes?status=open", nil)
	r.decode(t, &dp)
	e.expect(e.resolve(dp.Items[0].ID, map[string]any{"outcome": "cancelled", "resolution": "Қайтарылды", "refund_commission": true}), http.StatusOK, "")
	if bal, res := e.wallet(drv.DriverID); bal != 900 || res != 0 {
		t.Fatalf("wallet after refund %d/%d", bal, res)
	}
	if m, err := wallets.Reconcile(context.Background(), e.pool); err != nil || len(m) != 0 {
		t.Fatalf("reconcile: %v %+v", err, m)
	}
}

// TestDisputeWindow: passengers may dispute a completed trip for 72 hours.
func TestDisputeWindow(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	o, _, pc := e.trip(drv, 800)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil), http.StatusOK, "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE orders SET completed_at = now() - interval '73 hours' WHERE id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	var po orders.Order
	r := e.do(pc, http.MethodGet, path("/orders/%s", o.ID), nil)
	r.decode(t, &po)
	if po.CanDispute {
		t.Fatal("can_dispute after the window")
	}
	r = e.do(pc, http.MethodPost, path("/orders/%s/dispute", o.ID), map[string]any{"reason": "Кеш болса да"})
	e.expect(r, http.StatusConflict, "invalid_transition")
	if r.err(t).Error.Details["reason"] != "dispute_window_closed" {
		t.Fatalf("details: %s", r.Body)
	}
	// A searching order cannot be disputed either.
	s, _, spc := e.order(embiStops[:2], 600)
	e.expect(e.do(spc, http.MethodPost, path("/orders/%s/dispute", s.ID), map[string]any{"reason": "Жүргізуші жоқ"}),
		http.StatusConflict, "invalid_transition")
}
