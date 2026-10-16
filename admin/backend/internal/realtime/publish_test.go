package realtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// runHub starts the hub's listener against pool and waits until it is listening.
func runHub(t *testing.T, pool *pgxpool.Pool) *Hub {
	t.Helper()
	h := quietHub()
	if err := h.Ready(); err != nil {
		t.Fatalf("hub must be ready before Run starts: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx, pool) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("hub did not stop")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for !h.Listening() {
		if time.Now().After(deadline) {
			t.Fatal("hub never started listening")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := h.Ready(); err != nil {
		t.Fatalf("listening hub not ready: %v", err)
	}
	return h
}

var errRollback = errors.New("rollback")

func TestPublishDeliveredOnlyOnCommit(t *testing.T) {
	pool := testutil.NewDB(t)
	h := runHub(t, pool)
	ctx := context.Background()
	user := uuid.New()
	other := uuid.New()
	sub := h.Subscribe([]string{UserScope(user)})
	defer sub.Close()
	otherSub := h.Subscribe([]string{UserScope(other)})
	defer otherSub.Close()

	// Rolled back: never delivered.
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if err := Publish(ctx, tx, Event{Type: OrderCancelled, Scopes: []string{UserScope(user)}, Data: map[string]string{"n": "rolled-back"}}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}

	// Committed: delivered after commit, not before.
	err = db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if err := Publish(ctx, tx, Event{Type: OrderAccepted, Scopes: []string{UserScope(user)}, Data: map[string]string{"n": "committed"}}); err != nil {
			return err
		}
		expectNone(t, sub, 200*time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	m := recv(t, sub)
	if m["type"] != OrderAccepted || m["data"].(map[string]any)["n"] != "committed" {
		t.Fatalf("unexpected event (rolled-back event leaked?): %v", m)
	}
	expectNone(t, sub, 200*time.Millisecond)
	expectNone(t, otherSub, 10*time.Millisecond)

	// Publishing on the pool (outside a transaction) is immediate.
	if err := Publish(ctx, pool, Event{Type: WalletUpdated, Scopes: []string{UserScope(other)}}); err != nil {
		t.Fatal(err)
	}
	if recv(t, otherSub)["type"] != WalletUpdated {
		t.Fatal("pool publish not delivered")
	}

	// Oversized events fail inside the transaction instead of at commit.
	big := map[string]string{"x": string(make([]byte, MaxPayloadBytes))}
	if err := Publish(ctx, pool, Event{Type: OrderCreated, Scopes: []string{AdminScope}, Data: big}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestListenerReconnectsAndAsksForResync(t *testing.T) {
	pool := testutil.NewDB(t)
	h := runHub(t, pool)
	ctx := context.Background()
	sub := h.Subscribe([]string{AdminScope})
	defer sub.Close()

	// Kill the listener's backend; the hub must reconnect and tell clients to resync.
	if _, err := pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = $1 AND datname = current_database()`, listenerAppName); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-sub.C():
		if string(msg) != string(resyncMessage) {
			t.Fatalf("expected resync, got %s", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no resync after reconnect")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !h.Listening() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := Publish(ctx, pool, Event{Type: ModerationSubmitted, Scopes: []string{AdminScope}}); err != nil {
		t.Fatal(err)
	}
	if recv(t, sub)["type"] != ModerationSubmitted {
		t.Fatal("event after reconnect not delivered")
	}
}
