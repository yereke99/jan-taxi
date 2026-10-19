package botapi

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/plate"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func (e *testEnv) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func acceptPath(tg int64, orderID uuid.UUID) string {
	return "/driver/" + itoa(tg) + "/orders/" + orderID.String() + "/accept"
}

// TestDriverAcceptFromBot: «Алу» in the driver bot takes the order with the
// same atomic acceptance as the Mini App, charges the commission and returns
// the passenger's contact; the bot sends the trip card itself.
func TestDriverAcceptFromBot(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 500})
	pid, ptg := testutil.CreateUser(t, e.pool)
	if _, err := e.pool.Exec(ctx, `UPDATE users SET phone_e164 = '+77011234567', phone_verified_at = now(), username = 'aigul_kz',
		first_name = 'Айгүл' WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	oid := insertOrder(t, e.pool, pid, nil, "searching", 800, time.Now())

	// Only the driver bot may take orders.
	if st, b := e.call(t, "passenger", http.MethodPost, acceptPath(d.TelegramID, oid), nil); st != http.StatusForbidden ||
		errDetail(b, "reason") != "wrong_bot" {
		t.Fatalf("passenger bot: %d %v", st, b)
	}

	st, b := e.call(t, "driver", http.MethodPost, acceptPath(d.TelegramID, oid), nil)
	if st != http.StatusOK || b["replayed"] != false || b["charged"] != float64(80) {
		t.Fatalf("accept: %d %v", st, b)
	}
	if w := b["wallet"].(map[string]any); w["balance"] != float64(420) || w["available"] != float64(420) {
		t.Fatalf("wallet: %v", w)
	}
	o := b["order"].(map[string]any)
	p, _ := o["passenger"].(map[string]any)
	if o["id"] != oid.String() || o["status"] != "accepted" || o["is_mine"] != true || p == nil ||
		p["first_name"] != "Айгүл" || p["phone"] != "+77011234567" || p["phone_verified"] != true ||
		p["telegram_username"] != "aigul_kz" || p["telegram_url"] != "https://t.me/aigul_kz" || o["can_cancel"] != false {
		t.Fatalf("order: %v", o)
	}
	if n := e.count(t, `SELECT count(*) FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id
		WHERE w.driver_id = $1 AND l.order_id = $2 AND l.entry_type = 'commission_charge'`, d.DriverID, oid); n != 1 {
		t.Fatalf("charges = %d", n)
	}
	// The passenger is told; the driver's trip card and offer edit come from the bot.
	if n := e.count(t, `SELECT count(*) FROM notification_outbox WHERE template = 'order_accepted_passenger' AND user_id = $1`, pid); n != 1 {
		t.Fatal("passenger not told")
	}
	// The push shows the plate as written on the car ("123 ABC 04"), not the search key.
	if n := e.count(t, `SELECT count(*) FROM notification_outbox WHERE template = 'order_accepted_passenger' AND user_id = $1
		AND params->>'plate' = $2`, pid, plate.Format(d.Plate)); n != 1 || !strings.Contains(plate.Format(d.Plate), " ") {
		t.Fatalf("plate in the passenger push: %s", plate.Format(d.Plate))
	}
	if n := e.count(t, `SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template IN ('order_accepted_driver', 'order_offer_won')`, d.UserID); n != 0 {
		t.Fatalf("outbox duplicates the bot's messages: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM order_status_events WHERE order_id = $1 AND to_status = 'accepted' AND meta->>'channel' = 'bot'`, oid); n != 1 {
		t.Fatal("bot acceptance not journaled")
	}

	// A second tap is a replay: nothing is charged again.
	st, b = e.call(t, "driver", http.MethodPost, acceptPath(d.TelegramID, oid), nil)
	if st != http.StatusOK || b["replayed"] != true || b["charged"] != float64(0) {
		t.Fatalf("replay: %d %v", st, b)
	}
	if w := b["wallet"].(map[string]any); w["balance"] != float64(420) {
		t.Fatalf("replay wallet: %v", w)
	}

	// Another driver is late.
	late := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 500})
	if st, b := e.call(t, "driver", http.MethodPost, acceptPath(late.TelegramID, oid), nil); st != http.StatusConflict ||
		errCode(b) != "order_already_taken" {
		t.Fatalf("late driver: %d %v", st, b)
	}
	// A driver who cannot pay the commission, an offline driver, an unknown order.
	pid2, _ := testutil.CreateUser(t, e.pool)
	o2 := insertOrder(t, e.pool, pid2, nil, "searching", 800, time.Now())
	poor := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 30})
	if st, b := e.call(t, "driver", http.MethodPost, acceptPath(poor.TelegramID, o2), nil); st != http.StatusPaymentRequired ||
		errCode(b) != "insufficient_balance" || errDetail(b, "required") != float64(80) || errDetail(b, "available") != float64(30) {
		t.Fatalf("poor driver: %d %v", st, b)
	}
	off := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Balance: 500})
	if st, b := e.call(t, "driver", http.MethodPost, acceptPath(off.TelegramID, o2), nil); st != http.StatusConflict ||
		errCode(b) != "driver_offline" {
		t.Fatalf("offline driver: %d %v", st, b)
	}
	if st, _ := e.call(t, "driver", http.MethodPost, acceptPath(late.TelegramID, uuid.New()), nil); st != http.StatusNotFound {
		t.Fatalf("unknown order: %d", st)
	}
	if st, b := e.call(t, "driver", http.MethodPost, acceptPath(ptg, o2), nil); st != http.StatusForbidden ||
		errCode(b) != "driver_not_registered" {
		t.Fatalf("passenger account: %d %v", st, b)
	}
}

// TestConcurrentBotTaps: drivers tapping «Алу» at the same moment
// get exactly one winner and one charge.
func TestConcurrentBotTaps(t *testing.T) {
	e := newTestEnv(t)
	pid, _ := testutil.CreateUser(t, e.pool)
	oid := insertOrder(t, e.pool, pid, nil, "searching", 800, time.Now())
	const n = 20
	drivers := make([]testutil.DriverFixture, n)
	for i := range drivers {
		drivers[i] = testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 500})
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, n)
	for i := range drivers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st, b := e.call(t, "driver", http.MethodPost, acceptPath(drivers[i].TelegramID, oid), nil)
			if st == http.StatusConflict && errCode(b) != "order_already_taken" {
				t.Errorf("loser: %v", b)
			}
			codes[i] = st
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Fatalf("status %d", c)
		}
	}
	if ok != 1 {
		t.Fatalf("winners = %d", ok)
	}
	if c := e.count(t, `SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge'`, oid); c != 1 {
		t.Fatalf("charges = %d", c)
	}
}

// TestPassengerContact: the passenger bot stores the passenger's own number.
func TestPassengerContact(t *testing.T) {
	e := newTestEnv(t)
	uid, tg := testutil.CreateUser(t, e.pool)
	path := "/passenger/" + itoa(tg) + "/contact"
	if st, b := e.call(t, "passenger", http.MethodPost, path, map[string]any{"phone": "77011234567", "contact_user_id": tg + 1}); st != http.StatusForbidden ||
		errDetail(b, "reason") != "contact_mismatch" {
		t.Fatalf("mismatch: %d %v", st, b)
	}
	if st, b := e.call(t, "passenger", http.MethodPost, path, map[string]any{"phone": "+12025550100", "contact_user_id": tg}); st != http.StatusUnprocessableEntity ||
		errCode(b) != "validation_failed" {
		t.Fatalf("foreign number: %d %v", st, b)
	}
	if st, b := e.call(t, "driver", http.MethodPost, path, map[string]any{"phone": "77011234567", "contact_user_id": tg}); st != http.StatusForbidden ||
		errDetail(b, "reason") != "wrong_bot" {
		t.Fatalf("driver bot: %d %v", st, b)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND phone_e164 IS NOT NULL`, uid); n != 0 {
		t.Fatal("phone stored before a valid contact")
	}
	st, b := e.call(t, "passenger", http.MethodPost, path, map[string]any{"phone": "8 701 123 45 67", "contact_user_id": tg})
	if st != http.StatusOK || b["phone"] != "+77011234567" || b["phone_verified"] != true {
		t.Fatalf("verify: %d %v", st, b)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND phone_e164 = '+77011234567' AND phone_verified_at IS NOT NULL`, uid); n != 1 {
		t.Fatal("phone not stored")
	}
	if st, _ := e.call(t, "passenger", http.MethodPost, "/passenger/999999999999/contact",
		map[string]any{"phone": "77011234567", "contact_user_id": 999999999999}); st != http.StatusNotFound {
		t.Fatalf("unknown user: %d", st)
	}
}
