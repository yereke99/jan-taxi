package notifications

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestEnqueue(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")

	n := Notification{
		Bot: PassengerBot, UserID: user, Template: TplOrderCreatedPassenger, DedupeKey: "order_created:1",
		Params: map[string]any{"order_id": uuid.NewString(), "order_number": 1001, "fare": 1500},
	}
	// Inside a business transaction, twice: the duplicate key is a no-op.
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if err := Enqueue(ctx, tx, n); err != nil {
			return err
		}
		return Enqueue(ctx, tx, n)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Enqueue(ctx, pool, n); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var (
		count  int
		chatID int64
		status string
		fare   string
	)
	if err := pool.QueryRow(ctx, `SELECT count(*), max(chat_id), max(status), max(params->>'fare') FROM notification_outbox WHERE dedupe_key = $1`, n.DedupeKey).
		Scan(&count, &chatID, &status, &fare); err != nil {
		t.Fatal(err)
	}
	if count != 1 || chatID != tg || status != StatusPending || fare != "1500" {
		t.Fatalf("outbox count=%d chat=%d status=%s fare=%s", count, chatID, status, fare)
	}

	// A rolled-back transaction leaves nothing behind.
	rolled := n
	rolled.DedupeKey = "order_created:rolled"
	_ = db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if err := Enqueue(ctx, tx, rolled); err != nil {
			return err
		}
		return errors.New("business failure")
	})
	var exists bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notification_outbox WHERE dedupe_key = $1)`, rolled.DedupeKey).Scan(&exists)
	if exists {
		t.Fatal("notification survived a rollback")
	}

	tests := []struct {
		name string
		mut  func(*Notification)
		want error
	}{
		{"unknown template", func(n *Notification) { n.Template = "nope" }, ErrUnknownTemplate},
		{"wrong bot", func(n *Notification) { n.Bot = DriverBot }, ErrInvalidNotification},
		{"missing param", func(n *Notification) { delete(n.Params, "fare") }, ErrInvalidNotification},
		{"extra param", func(n *Notification) { n.Params["foo"] = "bar" }, ErrInvalidNotification},
		{"empty dedupe", func(n *Notification) { n.DedupeKey = " " }, ErrInvalidNotification},
		{"unknown user", func(n *Notification) { n.UserID = uuid.New() }, ErrUserNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := n
			c.Params = map[string]any{"order_id": uuid.NewString(), "order_number": 1001, "fare": 1500}
			c.DedupeKey = "case:" + tc.name
			tc.mut(&c)
			if err := Enqueue(ctx, pool, c); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestWorkerSendsInUserLanguage(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, LangRU)
	orderID := uuid.New()
	if err := Enqueue(ctx, pool, Notification{
		Bot: PassengerBot, UserID: user, Template: TplOrderAcceptedPassenger, DedupeKey: "acc:" + orderID.String(),
		Params: map[string]any{"order_id": orderID, "order_number": 1042, "driver_name": "Ерлан <b>",
			"vehicle": "Toyota Camry, белый", "plate": "123ABC04"},
	}); err != nil {
		t.Fatal(err)
	}
	fs := newFakeSender()
	w := testWorker(pool, fs, "w1")
	n, err := w.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}
	got := fs.sentTo(tg)
	if len(got) != 1 {
		t.Fatalf("sent %d messages", len(got))
	}
	want := "<b>Водитель найден · №1042</b>\nЕрлан &lt;b&gt;\nToyota Camry, белый\nГосномер: <b>123ABC04</b>"
	if got[0].text != want || got[0].bot != PassengerBot {
		t.Fatalf("text = %q", got[0].text)
	}
	btn := got[0].markup.InlineKeyboard[0][0]
	if btn.Text != "Смотреть на карте" || btn.WebApp.URL != "https://jantaxi.example.kz/passenger/?startapp=order_"+orderID.String() {
		t.Fatalf("button = %+v / %+v", btn, btn.WebApp)
	}
	var id uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM notification_outbox`).Scan(&id)
	st := readOutbox(t, pool, id)
	if st.status != StatusSent || st.messageID == nil || *st.messageID != 1001 || st.lockedBy != nil || st.attempts != 1 {
		t.Fatalf("outbox state %+v", st)
	}
	if o := attemptOutcomes(t, pool, id); len(o) != 1 || o[0] != "sent" {
		t.Fatalf("attempts = %v", o)
	}
	// Nothing left to do.
	if n, err := w.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("second RunOnce = %d, %v", n, err)
	}
}

func TestWorkerUsesAdminOverride(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	if _, err := pool.Exec(ctx, `INSERT INTO notification_templates (key, language, body) VALUES ($1, 'kk', $2)`,
		TplOrderStarted, "Сапар басталды: №{{order_number}}"); err != nil {
		t.Fatal(err)
	}
	enqueue(t, pool, user, "ovr")
	fs := newFakeSender()
	if _, err := testWorker(pool, fs, "w").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fs.sentTo(tg); len(got) != 1 || got[0].text != "Сапар басталды: №1042" {
		t.Fatalf("sent = %+v", got)
	}
}

func TestWorker429RetryAfter(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	other, otherTG := newUser(t, pool, "")
	id := enqueue(t, pool, user, "ra-1")
	// Created a moment later so it is processed after the throttled message.
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET created_at = created_at - interval '1 minute' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	id2 := enqueue(t, pool, other, "ra-2")

	fs := newFakeSender()
	fs.fail(tg, apiErr(429, "Too Many Requests: retry after 30", 30*time.Second))
	w := testWorker(pool, fs, "w")
	w.opt.Concurrency = 1
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	st := readOutbox(t, pool, id)
	if st.status != StatusPending || st.attempts != 0 {
		t.Fatalf("throttled row state %+v", st)
	}
	if d := st.nextAt.Sub(st.dbNow); d < 28*time.Second || d > 31*time.Second {
		t.Fatalf("next attempt in %s, want ~30s", d)
	}
	var retryAfter, httpStatus int
	if err := pool.QueryRow(ctx, `SELECT retry_after_s, http_status FROM notification_attempts WHERE outbox_id = $1`, id).Scan(&retryAfter, &httpStatus); err != nil {
		t.Fatal(err)
	}
	if retryAfter != 30 || httpStatus != 429 {
		t.Fatalf("attempt retry_after=%d status=%d", retryAfter, httpStatus)
	}
	// The whole bot is paused: the second message was not sent and costs no attempt.
	if len(fs.sentTo(otherTG)) != 0 {
		t.Fatal("message sent while the bot was paused")
	}
	st2 := readOutbox(t, pool, id2)
	if st2.status != StatusPending || st2.attempts != 0 || st2.nextAt.Sub(st2.dbNow) < 25*time.Second {
		t.Fatalf("paused row state %+v", st2)
	}
}

func TestWorker403MarksBlocked(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	id := enqueue(t, pool, user, "blocked")
	fs := newFakeSender()
	fs.fail(tg, apiErr(403, "Forbidden: bot was blocked by the user", 0))
	if _, err := testWorker(pool, fs, "w").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	st := readOutbox(t, pool, id)
	if st.status != StatusDead || st.lastError == nil || !strings.Contains(*st.lastError, "blocked") {
		t.Fatalf("state %+v", st)
	}
	var passengerBlocked, driverBlocked *time.Time
	if err := pool.QueryRow(ctx, `SELECT passenger_bot_blocked_at, driver_bot_blocked_at FROM users WHERE id = $1`, user).
		Scan(&passengerBlocked, &driverBlocked); err != nil {
		t.Fatal(err)
	}
	if passengerBlocked == nil || driverBlocked != nil {
		t.Fatalf("blocked flags passenger=%v driver=%v", passengerBlocked, driverBlocked)
	}
	if o := attemptOutcomes(t, pool, id); len(o) != 1 || o[0] != "fatal" {
		t.Fatalf("attempts %v", o)
	}
}

func TestWorker5xxBackoffThenSuccess(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	id := enqueue(t, pool, user, "flaky")
	fs := newFakeSender()
	fs.fail(tg, apiErr(502, "Bad Gateway", 0), errNetwork)
	w := testWorker(pool, fs, "w")

	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	st := readOutbox(t, pool, id)
	if st.status != StatusPending || st.attempts != 1 {
		t.Fatalf("after 502: %+v", st)
	}
	if d := st.nextAt.Sub(st.dbNow); d < 4*time.Second || d > 6*time.Second {
		t.Fatalf("first backoff %s, want 5s", d)
	}
	// Not due yet: nothing is claimed.
	if n, _ := w.RunOnce(ctx); n != 0 {
		t.Fatalf("claimed a row before its backoff elapsed")
	}

	makeDue(t, pool, id)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	st = readOutbox(t, pool, id)
	if st.status != StatusPending || st.attempts != 2 {
		t.Fatalf("after network error: %+v", st)
	}
	if d := st.nextAt.Sub(st.dbNow); d < 4*time.Second || d > 11*time.Second {
		t.Fatalf("second backoff %s, want 5..10s", d)
	}

	makeDue(t, pool, id)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	st = readOutbox(t, pool, id)
	if st.status != StatusSent || st.attempts != 3 || st.lastError != nil {
		t.Fatalf("after success: %+v", st)
	}
	if o := attemptOutcomes(t, pool, id); strings.Join(o, ",") != "retry,retry,sent" {
		t.Fatalf("attempts %v", o)
	}
	if len(fs.sentTo(tg)) != 1 {
		t.Fatal("expected exactly one delivered message")
	}
}

func TestWorkerMaxAttemptsDead(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	id := enqueue(t, pool, user, "doomed")
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET max_attempts = 2 WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	fs := newFakeSender()
	fs.fail(tg, apiErr(500, "Internal Server Error", 0), apiErr(500, "Internal Server Error", 0), nil)
	w := testWorker(pool, fs, "w")
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	makeDue(t, pool, id)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	st := readOutbox(t, pool, id)
	if st.status != StatusDead || st.attempts != 2 {
		t.Fatalf("state %+v", st)
	}
	if o := attemptOutcomes(t, pool, id); strings.Join(o, ",") != "retry,fatal" {
		t.Fatalf("attempts %v", o)
	}
	// A dead row is never claimed again.
	if n, _ := w.RunOnce(ctx); n != 0 {
		t.Fatal("dead row claimed")
	}
}

func TestWorkerOther4xxAndRenderFailuresAreDead(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	id := enqueue(t, pool, user, "bad-request")
	fs := newFakeSender()
	fs.fail(tg, apiErr(400, "Bad Request: chat not found", 0))

	var broken uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO notification_outbox (bot, user_id, chat_id, template, params, dedupe_key)
		VALUES ('passenger', $1, $2, 'no_such_template', '{}', 'broken') RETURNING id`, user, tg).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if _, err := testWorker(pool, fs, "w").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for _, rid := range []uuid.UUID{id, broken} {
		if st := readOutbox(t, pool, rid); st.status != StatusDead || st.lastError == nil {
			t.Fatalf("row %s state %+v", rid, st)
		}
	}
}

func TestWorkerReclaimsExpiredLease(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	id := enqueue(t, pool, user, "orphan")
	// Simulate a worker that claimed the row and crashed.
	if _, err := pool.Exec(ctx, `
		UPDATE notification_outbox SET status = 'sending', attempts = 1, locked_by = 'crashed',
		       locked_until = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	fs := newFakeSender()
	if _, err := testWorker(pool, fs, "rescuer").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	st := readOutbox(t, pool, id)
	if st.status != StatusSent || st.attempts != 2 {
		t.Fatalf("state %+v", st)
	}
	if len(fs.sentTo(tg)) != 1 {
		t.Fatal("orphaned message not delivered")
	}

	// A live lease is respected.
	id2 := enqueue(t, pool, user, "leased")
	if _, err := pool.Exec(ctx, `
		UPDATE notification_outbox SET status = 'sending', attempts = 1, locked_by = 'busy',
		       locked_until = now() + interval '1 minute' WHERE id = $1`, id2); err != nil {
		t.Fatal(err)
	}
	if n, _ := testWorker(pool, fs, "rescuer").RunOnce(ctx); n != 0 {
		t.Fatal("claimed a row with a live lease")
	}
	if st := readOutbox(t, pool, id2); st.status != StatusSending || *st.lockedBy != "busy" {
		t.Fatalf("leased row changed: %+v", st)
	}
}

func TestWorkerPerChatInterval(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	user, tg := newUser(t, pool, "")
	first := enqueue(t, pool, user, "chat-1")
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET created_at = created_at - interval '1 minute' WHERE id = $1`, first); err != nil {
		t.Fatal(err)
	}
	second := enqueue(t, pool, user, "chat-2")

	fs := newFakeSender()
	w := testWorker(pool, fs, "w")
	w.lim = newLimiter(10000, time.Hour)
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fs.sentTo(tg)) != 1 {
		t.Fatalf("sent %d messages to one chat inside the interval", len(fs.sentTo(tg)))
	}
	if st := readOutbox(t, pool, first); st.status != StatusSent {
		t.Fatalf("oldest message should go first: %+v", st)
	}
	st := readOutbox(t, pool, second)
	if st.status != StatusPending || st.attempts != 0 || st.nextAt.Sub(st.dbNow) < 50*time.Minute {
		t.Fatalf("deferred row %+v", st)
	}
	if o := attemptOutcomes(t, pool, second); len(o) != 0 {
		t.Fatalf("deferral recorded attempts %v", o)
	}
}

func TestTwoWorkersSendEachMessageOnce(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	const total = 100
	// Bulk users so every message goes to its own chat.
	base := testutil.NextTelegramID() * 1000
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (telegram_user_id, first_name) SELECT $1::bigint + g, 'U' || g FROM generate_series(1, $2::int) g`, base, total); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT id FROM users WHERE telegram_user_id > $1::bigint ORDER BY telegram_user_id`, base)
	if err != nil {
		t.Fatal(err)
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil || len(users) != total {
		t.Fatalf("users: %d %v", len(users), err)
	}
	for i, u := range users {
		if err := Enqueue(ctx, pool, Notification{Bot: DriverBot, UserID: u, Template: TplWalletLowBalance,
			DedupeKey: fmt.Sprintf("low:%d", i), Params: map[string]any{"available": 40, "required": 50}}); err != nil {
			t.Fatal(err)
		}
	}

	fs := newFakeSender()
	fs.latency = time.Millisecond
	workers := []*Worker{testWorker(pool, fs, "a"), testWorker(pool, fs, "b")}
	var wg sync.WaitGroup
	claimed := make([]int, len(workers))
	for i, w := range workers {
		w.opt.BatchSize = 7
		wg.Add(1)
		go func(i int, w *Worker) {
			defer wg.Done()
			for {
				n, err := w.RunOnce(ctx)
				if err != nil {
					t.Errorf("worker %d: %v", i, err)
					return
				}
				if n == 0 {
					return
				}
				claimed[i] += n
			}
		}(i, w)
	}
	wg.Wait()

	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.sent) != total {
		t.Fatalf("sent %d messages, want %d", len(fs.sent), total)
	}
	for chat, n := range fs.calls {
		if n != 1 {
			t.Fatalf("chat %d received %d sends", chat, n)
		}
	}
	if claimed[0]+claimed[1] != total || claimed[0] == 0 || claimed[1] == 0 {
		t.Logf("claims per worker: %v (both workers should normally participate)", claimed)
	}
	if claimed[0]+claimed[1] != total {
		t.Fatalf("claims %v do not add up to %d", claimed, total)
	}
	var sentRows int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE status = 'sent'`).Scan(&sentRows)
	if sentRows != total {
		t.Fatalf("sent rows = %d", sentRows)
	}
}

func TestWorkerRunStopsOnCancel(t *testing.T) {
	pool := testutil.NewDB(t)
	user, tg := newUser(t, pool, "")
	enqueue(t, pool, user, "run-loop")
	fs := newFakeSender()
	w := testWorker(pool, fs, "loop")
	w.opt.PollInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(fs.sentTo(tg)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
	if len(fs.sentTo(tg)) != 1 {
		t.Fatal("Run did not deliver the message")
	}
}

func TestBackoffBounds(t *testing.T) {
	w := NewWorker(nil, nil, Options{InstanceID: "x"})
	for attempt := 1; attempt <= 40; attempt++ {
		for range 20 {
			d := w.backoff(attempt)
			if d < 5*time.Second || d > 10*time.Minute {
				t.Fatalf("backoff(%d) = %s out of bounds", attempt, d)
			}
		}
	}
	if d := w.backoff(30); d < 5*time.Minute {
		t.Fatalf("late backoff %s should approach the cap", d)
	}
}
