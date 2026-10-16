package orders_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
)

func (e *env) expireSearch(orderID uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE orders SET search_expires_at = now() - interval '1 second' WHERE id = $1`, orderID); err != nil {
		e.t.Fatalf("expire search: %v", err)
	}
}

// TestSearchExpiry: the search_expiry job cancels searching orders whose
// window passed (system, no_driver_found), expires their offers and tells the
// passenger; other orders are untouched and a second run does nothing.
func TestSearchExpiry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	drv := e.driver(1000, true)
	busy := e.driver(1000, true)

	due, duePID, duePC := e.order(embiStops[:2], 800)
	fresh, _, _ := e.order(embiStops[:2], 700)
	taken, _, _ := e.order(embiStops[1:3], 600)
	e.expect(e.do(busy.c, http.MethodPost, path("/driver/orders/%s/accept", taken.ID), nil), http.StatusOK, "")
	e.expireSearch(due.ID)
	e.expireSearch(taken.ID)

	n, err := dispatch.ExpireSearches(ctx, e.pool)
	if err != nil || n != 1 {
		t.Fatalf("ExpireSearches = %d, %v", n, err)
	}
	var po orders.Order
	r := e.do(duePC, http.MethodGet, path("/orders/%s", due.ID), nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &po)
	if po.Status != "cancelled" || po.CancelledBy == nil || *po.CancelledBy != "system" || po.CancelReason == nil ||
		*po.CancelReason != "no_driver_found" || po.SearchExpiresAt != nil || po.CanCancel {
		t.Fatalf("expired order: %+v", po)
	}
	last := po.Events[len(po.Events)-1]
	if last.ActorType != "system" || last.Reason == nil || *last.Reason != "no_driver_found" || last.ToStatus != "cancelled" {
		t.Fatalf("expiry event: %+v", last)
	}
	if got := e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND driver_id = $2 AND status = 'expired'`, due.ID, drv.DriverID); got != 1 {
		t.Fatalf("expired offers = %d", got)
	}
	if e.outbox("order_no_driver", duePID) != 1 {
		t.Fatal("passenger not told that no driver was found")
	}
	if s := e.status(fresh.ID); s != "searching" {
		t.Fatalf("fresh order %s", s)
	}
	if s := e.status(taken.ID); s != "accepted" {
		t.Fatalf("accepted order %s", s)
	}
	// The expired order is gone from the driver's list.
	var avail dispatch.Available
	r = e.do(drv.c, http.MethodGet, "/driver/orders", nil)
	r.decode(t, &avail)
	for _, c := range avail.Items {
		if c.ID == due.ID {
			t.Fatal("expired order still available")
		}
	}

	if n, err := dispatch.ExpireSearches(ctx, e.pool); err != nil || n != 0 {
		t.Fatalf("second run = %d, %v", n, err)
	}
	if e.outbox("order_no_driver", duePID) != 1 {
		t.Fatal("duplicate notification")
	}
	// The passenger can order again right away.
	q := e.quote(duePID, "taxi", embiStops[:2])
	e.expect(e.do(duePC, http.MethodPost, "/orders", taxiBody(q, 800)), http.StatusCreated, "")
}
