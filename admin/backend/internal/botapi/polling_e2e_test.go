package botapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/app"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

const e2eToken = "4242:E2E-polling-token"

// queueTelegram serves getUpdates from a growing list of update ids,
// honouring the offset like the real Bot API.
type queueTelegram struct {
	mu  sync.Mutex
	ids []int64
}

func (q *queueTelegram) add(ids ...int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, ids...)
}

func (q *queueTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	method, _ := strings.CutPrefix(r.URL.Path, "/bot"+e2eToken+"/")
	if method != "getUpdates" {
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		return
	}
	var p struct {
		Offset int64 `json:"offset"`
	}
	_ = json.NewDecoder(r.Body).Decode(&p)
	q.mu.Lock()
	var out []map[string]any
	for _, id := range q.ids {
		if id >= p.Offset {
			out = append(out, map[string]any{"update_id": id, "message": map[string]any{
				"message_id": id, "date": 1, "text": "hi",
				"chat": map[string]any{"id": 77, "type": "private"}, "from": map[string]any{"id": 77, "first_name": "A"},
			}})
		}
	}
	q.mu.Unlock()
	if len(out) == 0 {
		time.Sleep(20 * time.Millisecond) // a short "long poll"
	}
	b, _ := json.Marshal(map[string]any{"ok": true, "result": out})
	_, _ = w.Write(b)
}

func storedOffset(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), `SELECT last_update_id FROM bot_states WHERE bot = 'driver'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestTwoPollersEndToEnd runs two bot instances against the real internal
// API: only the lease holder polls, every update is handled exactly once,
// the offset is stored, and the standby takes over when the lease expires.
func TestTwoPollersEndToEnd(t *testing.T) {
	pool := testutil.NewDB(t)
	cfg := config.ForTesting("")
	cfg.FileStorageDir = t.TempDir()
	a, err := app.NewWithPool(context.Background(), cfg, slog.New(slog.DiscardHandler), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	api := httptest.NewServer(a.InternalHandler())
	t.Cleanup(api.Close)
	tq := &queueTelegram{}
	tgSrv := httptest.NewServer(tq)
	t.Cleanup(tgSrv.Close)

	var mu sync.Mutex
	handled := map[int64][]string{}
	start := func(instance string) (*runtime.Poller, context.CancelFunc, chan struct{}) {
		p, err := runtime.NewPoller(runtime.PollerConfig{
			Telegram: telegram.New(e2eToken, telegram.WithBaseURL(tgSrv.URL)),
			State:    botapi.New(api.URL, config.TestServiceSecret, botapi.BotDriver),
			Handler: runtime.HandlerFunc(func(_ context.Context, u telegram.Update) error {
				mu.Lock()
				handled[u.UpdateID] = append(handled[u.UpdateID], instance)
				mu.Unlock()
				return nil
			}),
			InstanceID: instance, LeaseTTL: 10 * time.Second, RenewEvery: 100 * time.Millisecond, PollTimeout: -1,
			MinBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, ConflictBackoff: 20 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = p.Run(ctx); close(done) }()
		t.Cleanup(func() { cancel(); <-done })
		return p, cancel, done
	}

	tq.add(1, 2, 3, 4, 5)
	pa, stopA, doneA := start("driver-bot-a")
	waitUntil(t, "instance a to lead", func() bool { return pa.Health().Role == runtime.RoleLeader })
	pb, _, _ := start("driver-bot-b")
	waitUntil(t, "offset 5", func() bool { return storedOffset(t, pool) == 5 })
	time.Sleep(300 * time.Millisecond) // several renewals and polls
	if pb.Health().Role != runtime.RoleStandby {
		t.Fatalf("instance b must stay on standby while a holds the lease: %+v", pb.Health())
	}

	// Failover: a stops (its lease is not released) and the lease expires.
	stopA()
	<-doneA
	if _, err := pool.Exec(context.Background(), `UPDATE bot_states SET lease_expires_at = now() - interval '1 second' WHERE bot = 'driver'`); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "instance b to lead", func() bool { return pb.Health().Role == runtime.RoleLeader })
	tq.add(6, 7)
	waitUntil(t, "offset 7", func() bool { return storedOffset(t, pool) == 7 })

	mu.Lock()
	defer mu.Unlock()
	for id := int64(1); id <= 7; id++ {
		want := "driver-bot-a"
		if id > 5 {
			want = "driver-bot-b"
		}
		if got := handled[id]; len(got) != 1 || got[0] != want {
			t.Errorf("update %d handled by %v, want once by %s", id, got, want)
		}
	}
	var claims int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM bot_processed_updates WHERE bot = 'driver'`).Scan(&claims); err != nil || claims != 7 {
		t.Fatalf("claims: %d %v", claims, err)
	}
	if len(handled) != 7 {
		t.Fatalf("handled %s", fmt.Sprint(handled))
	}
}
