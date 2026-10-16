package orders_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
)

type cursorPage[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// walk follows next_cursor from path until the last page and returns the ids.
func walk[T any](e *env, c caller, path string, id func(T) uuid.UUID) []uuid.UUID {
	e.t.Helper()
	var ids []uuid.UUID
	next := path
	for range 10 {
		r := e.do(c, http.MethodGet, next, nil)
		e.expect(r, http.StatusOK, "")
		var p cursorPage[T]
		r.decode(e.t, &p)
		for _, it := range p.Items {
			ids = append(ids, id(it))
		}
		if p.NextCursor == nil {
			return ids
		}
		next = path + "&before=" + url.QueryEscape(*p.NextCursor)
	}
	e.t.Fatal("pagination does not end")
	return nil
}

// TestCursorLists pages through passenger and driver lists newest first.
func TestCursorLists(t *testing.T) {
	e := newEnv(t)
	drv := e.driver(1000, true)
	pid, pc := e.passenger()
	var created []uuid.UUID
	for i := range 3 {
		q := e.quote(pid, "taxi", embiStops[:2])
		r := e.do(pc, http.MethodPost, "/orders", taxiBody(q, 600+50*i))
		e.expect(r, http.StatusCreated, "")
		var o orders.Order
		r.decode(t, &o)
		created = append(created, o.ID)
		e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
		e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/cancel", o.ID), map[string]any{"reason": "Көлік бұзылды"}), http.StatusOK, "")
	}
	want := []uuid.UUID{created[2], created[1], created[0]}
	same := func(got []uuid.UUID) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if got := walk(e, pc, "/orders?scope=history&limit=1", func(s orders.OrderSummary) uuid.UUID { return s.ID }); !same(got) {
		t.Fatalf("passenger history %v, want %v", got, want)
	}
	if got := walk(e, drv.c, "/driver/orders/history?limit=2", func(s orders.DriverOrderSummary) uuid.UUID { return s.ID }); !same(got) {
		t.Fatalf("driver history %v, want %v", got, want)
	}
	for _, bad := range []string{"/orders?limit=0", "/orders?limit=51", "/orders?before=zzz", "/orders?scope=all"} {
		e.expect(e.do(pc, http.MethodGet, bad, nil), http.StatusUnprocessableEntity, "validation_failed")
	}
	// A stranger's list is empty.
	_, stranger := e.passenger()
	if got := walk(e, stranger, "/orders?scope=history", func(s orders.OrderSummary) uuid.UUID { return s.ID }); len(got) != 0 {
		t.Fatalf("stranger sees %v", got)
	}
}
