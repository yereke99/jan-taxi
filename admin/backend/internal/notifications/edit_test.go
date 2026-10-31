package notifications

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func offerNotification(user, orderID uuid.UUID, key string) Notification {
	p := map[string]any{
		"order_id": orderID.String(), "order_number": json.Number("1042"), "category": "taxi",
		"vehicle_class": map[string]string{"kk": "Седан", "ru": "Седан", "en": "Sedan"},
		"pickup":        "Абай 1", "destination": "Достық 5", "route_distance": 3200, "route_duration": 420,
		"fare": 900, "payment_method": "cash",
	}
	return Notification{Bot: DriverBot, UserID: user, Template: TplOrderAvailableDriver, Params: p, DedupeKey: key}
}

// An offer that is already in the chat is edited: the status goes above the
// original text and the keyboard is replaced (or removed).
func TestEditSentOffer(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	orderID := uuid.New()
	if err := Enqueue(ctx, pool, offerNotification(user, orderID, "offer:1")); err != nil {
		t.Fatal(err)
	}
	fs := newFakeSender()
	w := testWorker(pool, fs, "w1")
	if n, err := w.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("send offer: %d %v", n, err)
	}
	offer := fs.sentTo(tg)[0]
	if offer.markup == nil || offer.markup.InlineKeyboard[0][0].CallbackData != "take:"+orderID.String() {
		t.Fatalf("offer markup = %+v", offer.markup)
	}

	for i := range 2 { // the edit is deduplicated
		queued, err := EnqueueEdit(ctx, pool, Edit{RefDedupeKey: "offer:1", Template: TplOrderOfferTaken, DedupeKey: "taken:1"})
		if err != nil || queued != (i == 0) {
			t.Fatalf("enqueue edit #%d = %v %v", i, queued, err)
		}
	}
	time.Sleep(5 * time.Millisecond) // per-chat interval
	if n, err := w.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("apply edit: %d %v", n, err)
	}
	got := fs.sentTo(tg)
	if len(got) != 2 || got[1].editOf != 1001 || got[1].markup != nil {
		t.Fatalf("edit = %+v", got)
	}
	if !strings.HasPrefix(got[1].text, "<b>❌ Тапсырысты басқа жүргізуші алды</b>\n\n🚕 <b>Жаңа тапсырыс №1042</b>") ||
		!strings.Contains(got[1].text, "Қайдан (A): Абай 1") {
		t.Fatalf("edited text = %q", got[1].text)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM notification_outbox WHERE dedupe_key = 'taken:1'`).Scan(&status)
	if status != StatusSent {
		t.Fatalf("edit row status = %s", status)
	}

	// The winner's edit keeps a button that opens the order.
	if _, err := EnqueueEdit(ctx, pool, Edit{RefDedupeKey: "offer:1", Template: TplOrderOfferWon,
		Params: map[string]any{"order_id": orderID.String()}, DedupeKey: "won:1"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got = fs.sentTo(tg)
	won := got[len(got)-1]
	if !strings.HasPrefix(won.text, "<b>✅ Сіз алдыңыз</b>") || won.markup == nil ||
		!strings.HasSuffix(won.markup.InlineKeyboard[0][0].WebApp.URL, "startapp=order_"+orderID.String()) {
		t.Fatalf("won edit = %+v", won)
	}
}

// An offer that was never sent is cancelled instead of edited; an edit for an
// offer that does not exist queues nothing.
func TestEditCancelsUnsentOffer(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	if err := Enqueue(ctx, pool, offerNotification(user, uuid.New(), "offer:2")); err != nil {
		t.Fatal(err)
	}
	queued, err := EnqueueEdit(ctx, pool, Edit{RefDedupeKey: "offer:2", Template: TplOrderOfferCancelled, DedupeKey: "cancelled:2"})
	if err != nil || queued {
		t.Fatalf("enqueue edit = %v %v", queued, err)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM notification_outbox WHERE dedupe_key = 'offer:2'`).Scan(&status)
	if status != StatusCancelled {
		t.Fatalf("unsent offer status = %s", status)
	}
	fs := newFakeSender()
	if n, err := testWorker(pool, fs, "w1").RunOnce(ctx); err != nil || n != 0 || len(fs.sentTo(tg)) != 0 {
		t.Fatalf("worker sent something: %d %v", n, err)
	}
	if queued, err := EnqueueEdit(ctx, pool, Edit{RefDedupeKey: "offer:none", Template: TplOrderOfferTaken, DedupeKey: "taken:none"}); err != nil || queued {
		t.Fatalf("edit of nothing = %v %v", queued, err)
	}
	// Only edit templates edit, and edit templates are never sent.
	if _, err := EnqueueEdit(ctx, pool, Edit{RefDedupeKey: "offer:2", Template: TplOrderCancelledDriver, DedupeKey: "x"}); err == nil {
		t.Fatal("a send template was accepted as an edit")
	}
	if err := Enqueue(ctx, pool, Notification{Bot: DriverBot, UserID: user, Template: TplOrderOfferTaken, DedupeKey: "y"}); err == nil {
		t.Fatal("an edit template was enqueued as a message")
	}
}

// An edit waits while its message is being sent and is applied afterwards;
// an edit of a message that died is cancelled.
func TestEditWaitsForTheOriginal(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	if err := Enqueue(ctx, pool, offerNotification(user, uuid.New(), "offer:3")); err != nil {
		t.Fatal(err)
	}
	// A worker holds the offer (status sending) when the order is taken.
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET status = 'sending', locked_by = 'other',
		locked_until = now() + interval '1 minute' WHERE dedupe_key = 'offer:3'`); err != nil {
		t.Fatal(err)
	}
	if queued, err := EnqueueEdit(ctx, pool, Edit{RefDedupeKey: "offer:3", Template: TplOrderOfferTaken, DedupeKey: "taken:3"}); err != nil || !queued {
		t.Fatalf("enqueue edit = %v %v", queued, err)
	}
	fs := newFakeSender()
	w := testWorker(pool, fs, "w1")
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var editID uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM notification_outbox WHERE dedupe_key = 'taken:3'`).Scan(&editID)
	if st := readOutbox(t, pool, editID); st.status != StatusPending || st.attempts != 0 || len(fs.sentTo(tg)) != 0 {
		t.Fatalf("edit before the original was sent: %+v", st)
	}
	// The other worker delivers the offer as message 77.
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET status = 'sent', telegram_message_id = 77, locked_by = NULL,
		locked_until = NULL, sent_at = now() WHERE dedupe_key = 'offer:3'`); err != nil {
		t.Fatal(err)
	}
	makeDue(t, pool, editID)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fs.sentTo(tg); len(got) != 1 || got[0].editOf != 77 {
		t.Fatalf("edit not applied: %+v", got)
	}

	// "message is not modified" (the bot edited it first) counts as done.
	if err := Enqueue(ctx, pool, offerNotification(user, uuid.New(), "offer:4")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET status = 'sent', telegram_message_id = 78 WHERE dedupe_key = 'offer:4'`); err != nil {
		t.Fatal(err)
	}
	if _, err := EnqueueEdit(ctx, pool, Edit{RefDedupeKey: "offer:4", Template: TplOrderOfferTaken, DedupeKey: "taken:4"}); err != nil {
		t.Fatal(err)
	}
	fs.fail(tg, apiErr(400, "Bad Request: message is not modified", 0))
	time.Sleep(5 * time.Millisecond)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM notification_outbox WHERE dedupe_key = 'taken:4'`).Scan(&status)
	if status != StatusSent {
		t.Fatalf("not-modified edit status = %s", status)
	}

	// The original died: the edit is cancelled without calling Telegram.
	if err := Enqueue(ctx, pool, offerNotification(user, uuid.New(), "offer:5")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET status = 'sending' WHERE dedupe_key = 'offer:5'`); err != nil {
		t.Fatal(err)
	}
	if _, err := EnqueueEdit(ctx, pool, Edit{RefDedupeKey: "offer:5", Template: TplOrderOfferTaken, DedupeKey: "taken:5"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET status = 'dead' WHERE dedupe_key = 'offer:5'`); err != nil {
		t.Fatal(err)
	}
	before := len(fs.sentTo(tg))
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, `SELECT status FROM notification_outbox WHERE dedupe_key = 'taken:5'`).Scan(&status)
	if status != StatusCancelled || len(fs.sentTo(tg)) != before {
		t.Fatalf("edit of a dead message: status %s", status)
	}
}

func TestDelayedNotificationAndRelease(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	n := Notification{Bot: PassengerBot, UserID: user, Template: TplOrderStarted, DedupeKey: "late:1", Delay: time.Hour,
		Params: map[string]any{"order_id": uuid.NewString(), "order_number": 1}}
	if err := Enqueue(ctx, pool, n); err != nil {
		t.Fatal(err)
	}
	fs := newFakeSender()
	w := testWorker(pool, fs, "w1")
	if n, err := w.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("delayed message claimed: %d %v", n, err)
	}
	if err := Release(ctx, pool, "late:1"); err != nil {
		t.Fatal(err)
	}
	if n, err := w.RunOnce(ctx); err != nil || n != 1 || len(fs.sentTo(tg)) != 1 {
		t.Fatalf("released message not sent: %d %v", n, err)
	}
}

// insertSearchingOrder stores a minimal searching Embi taxi order.
func insertSearchingOrder(t *testing.T, pool *pgxpool.Pool, passengerID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO orders (passenger_id, city_id, category, service_type, cargo_type, status, fare,
		                    commission_bps, commission_amount, payment_method, tariff_id, distance_m, duration_s,
		                    stop_count, pickup_lat, pickup_lon, idempotency_key, search_expires_at, vehicle_class, passenger_count)
		SELECT $1, t.city_id, 'taxi', 'passenger', 'none', 'searching', 900, 1000, 90, 'cash', t.id, 1800, 300,
		       2, 48.8264, 58.1442, $2, now() + interval '10 minutes', 'sedan', 1
		FROM tariffs t JOIN cities c ON c.id = t.city_id
		WHERE c.code = 'EMB' AND t.category = 'taxi' AND t.vehicle_class = 'sedan' AND t.is_active
		RETURNING id`, passengerID, uuid.NewString()).Scan(&id)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	return id
}

// TestOfferNotSentAfterOrderClosed: an offer still waiting in the outbox when
// its order stops searching (taken, cancelled, expired) is dropped at send
// time instead of reaching the driver as a fresh offer; offers of searching
// orders go out as usual.
func TestOfferNotSentAfterOrderClosed(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	d := testutil.CreateDriver(t, pool, testutil.DriverOptions{Online: true})
	// One active order per passenger: two passengers.
	p1, _ := testutil.CreateUser(t, pool)
	p2, _ := testutil.CreateUser(t, pool)
	closed := insertSearchingOrder(t, pool, p1)
	open := insertSearchingOrder(t, pool, p2)
	for _, id := range []uuid.UUID{closed, open} {
		if _, err := pool.Exec(ctx, `INSERT INTO order_offers (order_id, driver_id) VALUES ($1, $2)`, id, d.DriverID); err != nil {
			t.Fatal(err)
		}
	}
	if err := Enqueue(ctx, pool, offerNotification(d.UserID, closed, "offer:closed")); err != nil {
		t.Fatal(err)
	}
	if err := Enqueue(ctx, pool, offerNotification(d.UserID, open, "offer:open")); err != nil {
		t.Fatal(err)
	}
	// Taken by another driver after the offer was queued.
	if _, err := pool.Exec(ctx, `UPDATE orders SET status = 'cancelled', cancelled_at = now(), cancelled_by = 'passenger',
		search_expires_at = NULL WHERE id = $1`, closed); err != nil {
		t.Fatal(err)
	}
	fs := newFakeSender()
	w := testWorker(pool, fs, "w1")
	for range 2 {
		if _, err := w.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond) // per-chat interval
	}
	sent := fs.sentTo(d.TelegramID)
	if len(sent) != 1 || !strings.Contains(sent[0].text, "№1042") || sent[0].markup.InlineKeyboard[0][0].CallbackData != "take:"+open.String() {
		t.Fatalf("sent = %+v", sent)
	}
	var status, lastErr string
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(last_error, '') FROM notification_outbox WHERE dedupe_key = 'offer:closed'`).
		Scan(&status, &lastErr); err != nil {
		t.Fatal(err)
	}
	if status != StatusCancelled || !strings.Contains(lastErr, "no longer searching") {
		t.Fatalf("closed offer row: %s %q", status, lastErr)
	}
}
