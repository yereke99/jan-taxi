package notifications

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// sent is one message accepted by fakeSender.
type sent struct {
	bot    Bot
	chatID int64
	text   string
	markup *telegram.InlineKeyboardMarkup
}

// fakeSender records sends and returns scripted errors per chat (consumed in
// order; an exhausted script means success).
type fakeSender struct {
	mu      sync.Mutex
	nextID  int64
	sent    []sent
	calls   map[int64]int
	script  map[int64][]error
	onSend  func(chatID int64)
	latency time.Duration
}

func newFakeSender() *fakeSender {
	return &fakeSender{calls: map[int64]int{}, script: map[int64][]error{}}
}

func (f *fakeSender) fail(chatID int64, errs ...error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script[chatID] = append(f.script[chatID], errs...)
}

func (f *fakeSender) Send(ctx context.Context, bot Bot, chatID int64, text string, markup *telegram.InlineKeyboardMarkup) (int64, error) {
	if f.latency > 0 {
		select {
		case <-time.After(f.latency):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[chatID]++
	if f.onSend != nil {
		f.onSend(chatID)
	}
	if s := f.script[chatID]; len(s) > 0 {
		f.script[chatID] = s[1:]
		return 0, s[0]
	}
	f.nextID++
	f.sent = append(f.sent, sent{bot: bot, chatID: chatID, text: text, markup: markup})
	return 1000 + f.nextID, nil
}

func (f *fakeSender) sentTo(chatID int64) []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sent
	for _, s := range f.sent {
		if s.chatID == chatID {
			out = append(out, s)
		}
	}
	return out
}

func apiErr(code int, desc string, retryAfter time.Duration) error {
	return &telegram.APIError{Code: code, Description: desc, RetryAfter: retryAfter}
}

var errNetwork = &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

// testWorker returns a worker with test-friendly limits.
func testWorker(pool *pgxpool.Pool, s Sender, id string) *Worker {
	return NewWorker(pool, s, Options{
		InstanceID:          id,
		Log:                 slog.New(slog.DiscardHandler),
		PassengerMiniAppURL: "https://jantaxi.example.kz/passenger/",
		DriverMiniAppURL:    "https://jantaxi.example.kz/driver/",
		PerBotRate:          10000,
		PerChatInterval:     time.Millisecond,
		Concurrency:         4,
	})
}

type outboxState struct {
	status      string
	attempts    int
	maxAttempts int
	nextAt      time.Time
	lastError   *string
	messageID   *int64
	lockedBy    *string
	dbNow       time.Time
}

func readOutbox(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) outboxState {
	t.Helper()
	var s outboxState
	err := pool.QueryRow(context.Background(), `
		SELECT status, attempts, max_attempts, next_attempt_at, last_error, telegram_message_id, locked_by, now()
		FROM notification_outbox WHERE id = $1`, id).
		Scan(&s.status, &s.attempts, &s.maxAttempts, &s.nextAt, &s.lastError, &s.messageID, &s.lockedBy, &s.dbNow)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return s
}

// enqueue adds a passenger order_started message for user and returns its id.
func enqueue(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, dedupe string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	err := Enqueue(ctx, pool, Notification{
		Bot: PassengerBot, UserID: user, Template: TplOrderStarted, DedupeKey: dedupe,
		Params: map[string]any{"order_id": uuid.NewString(), "order_number": 1042},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM notification_outbox WHERE dedupe_key = $1`, dedupe).Scan(&id); err != nil {
		t.Fatalf("find outbox row: %v", err)
	}
	return id
}

// makeDue moves a pending row's next attempt into the past.
func makeDue(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE notification_outbox SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func attemptOutcomes(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT outcome FROM notification_attempts WHERE outbox_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			t.Fatal(err)
		}
		out = append(out, o)
	}
	return out
}

func newUser(t *testing.T, pool *pgxpool.Pool, lang string) (uuid.UUID, int64) {
	t.Helper()
	id, tg := testutil.CreateUser(t, pool)
	if lang != "" {
		if _, err := pool.Exec(context.Background(), `UPDATE user_preferences SET language = $2 WHERE user_id = $1`, id, lang); err != nil {
			t.Fatal(err)
		}
	}
	return id, tg
}
