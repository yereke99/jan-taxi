package botapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
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
	// The trip card with the passenger's contact comes from the outbox for
	// either channel (retried, sent once, fenced against a later
	// cancellation); the bot only marks the tapped offer itself.
	if n := e.count(t, `SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'order_accepted_driver'
		AND params->>'order_id' = $2 AND params->>'passenger_phone' = '+7 701 123 45 67' AND NOT params ? 'commission'`, d.UserID, oid.String()); n != 1 {
		t.Fatalf("trip cards queued: %d", n)
	}
	// No offer message was sent to this driver in this test, so there is
	// nothing to mark "won" (an edit is queued only for a sent offer).
	if n := e.count(t, `SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'order_offer_won'`, d.UserID); n != 0 {
		t.Fatalf("won edit without an offer message: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM order_status_events WHERE order_id = $1 AND to_status = 'accepted' AND meta->>'channel' = 'bot'`, oid); n != 1 {
		t.Fatal("bot acceptance not journaled")
	}

	// A second tap is a replay: nothing is charged or queued again.
	st, b = e.call(t, "driver", http.MethodPost, acceptPath(d.TelegramID, oid), nil)
	if st != http.StatusOK || b["replayed"] != true || b["charged"] != float64(0) {
		t.Fatalf("replay: %d %v", st, b)
	}
	if n := e.count(t, `SELECT count(*) FROM notification_outbox WHERE user_id = $1 AND template = 'order_accepted_driver'`, d.UserID); n != 1 {
		t.Fatalf("trip cards after a replay: %d", n)
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

// miniAppAccept takes the order exactly as POST /driver/orders/{id}/accept
// does: the shared dispatch.AcceptOrder without the bot option.
func miniAppAccept(t *testing.T, e *testEnv, orderID, driverID uuid.UUID) (int, string, bool) {
	t.Helper()
	res, err := dispatch.AcceptOrder(context.Background(), e.pool, orderID, driverID, dispatch.AcceptOptions{})
	if err == nil {
		return http.StatusOK, "", res.Replayed
	}
	var he *httpx.Error
	if !errors.As(err, &he) {
		t.Errorf("mini app accept: %v", err)
		return 0, "", false
	}
	return he.Status, he.Code, false
}

// TestAcceptAcrossChannels: drivers taking one order at the same moment, half
// from the Mini App and half from the bot's accept button, get exactly one
// winner, one charge and one assignment; every other attempt is 409
// order_already_taken whichever channel it came from.
func TestAcceptAcrossChannels(t *testing.T) {
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
	statuses := make([]int, n)
	codes := make([]string, n)
	for i := range drivers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				st, b := e.call(t, "driver", http.MethodPost, acceptPath(drivers[i].TelegramID, oid), nil)
				statuses[i], codes[i] = st, errCode(b)
				return
			}
			statuses[i], codes[i], _ = miniAppAccept(t, e, oid, drivers[i].DriverID)
		}(i)
	}
	close(start)
	wg.Wait()
	winner := -1
	for i := range drivers {
		switch {
		case statuses[i] == http.StatusOK:
			if winner >= 0 {
				t.Fatalf("second winner %d (first %d)", i, winner)
			}
			winner = i
		case statuses[i] == http.StatusConflict && codes[i] == "order_already_taken":
		default:
			t.Fatalf("driver %d: %d %s", i, statuses[i], codes[i])
		}
	}
	if winner < 0 {
		t.Fatal("no winner")
	}
	if c := e.count(t, `SELECT count(*) FROM orders WHERE id = $1 AND status = 'accepted' AND driver_id = $2`, oid, drivers[winner].DriverID); c != 1 {
		t.Fatal("order not assigned to the winner")
	}
	if c := e.count(t, `SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge'`, oid); c != 1 {
		t.Fatalf("charges = %d", c)
	}
	if c := e.count(t, `SELECT count(*) FROM order_status_events WHERE order_id = $1 AND to_status = 'accepted'`, oid); c != 1 {
		t.Fatalf("accept events = %d", c)
	}
	for i, d := range drivers {
		want := 500
		if i == winner {
			want = 420
		}
		if bal := e.count(t, `SELECT balance FROM wallets WHERE driver_id = $1`, d.DriverID); bal != want {
			t.Fatalf("driver %d balance %d, want %d", i, bal, want)
		}
	}
}

// TestSameDriverDoubleAccept: one driver's repeated taps arriving together
// from both channels (a double tap, a redelivered callback, an HTTP retry)
// all succeed; exactly one of them takes the order and charges the
// commission, the others are replays, and no channel reports the order as
// taken by someone else or the balance as too low.
func TestSameDriverDoubleAccept(t *testing.T) {
	e := newTestEnv(t)
	pid, _ := testutil.CreateUser(t, e.pool)
	oid := insertOrder(t, e.pool, pid, nil, "searching", 800, time.Now())
	// Enough for exactly one commission: a second charge would also fail with 402.
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Online: true, Balance: 100})
	const n = 12
	var wg sync.WaitGroup
	start := make(chan struct{})
	statuses := make([]int, n)
	codes := make([]string, n)
	replayed := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				st, b := e.call(t, "driver", http.MethodPost, acceptPath(d.TelegramID, oid), nil)
				statuses[i], codes[i] = st, errCode(b)
				replayed[i] = b["replayed"] == true
				return
			}
			statuses[i], codes[i], replayed[i] = miniAppAccept(t, e, oid, d.DriverID)
		}(i)
	}
	close(start)
	wg.Wait()
	first := 0
	for i := 0; i < n; i++ {
		if statuses[i] != http.StatusOK {
			t.Fatalf("tap %d: %d %s", i, statuses[i], codes[i])
		}
		if !replayed[i] {
			first++
		}
	}
	if first != 1 {
		t.Fatalf("non-replayed acceptances = %d, want 1", first)
	}
	if c := e.count(t, `SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge'`, oid); c != 1 {
		t.Fatalf("charges = %d", c)
	}
	if bal := e.count(t, `SELECT balance FROM wallets WHERE driver_id = $1`, d.DriverID); bal != 20 {
		t.Fatalf("balance %d, want 20", bal)
	}
	if c := e.count(t, `SELECT count(*) FROM order_status_events WHERE order_id = $1 AND to_status = 'accepted'`, oid); c != 1 {
		t.Fatalf("accept events = %d", c)
	}
}
