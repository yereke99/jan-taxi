package botapi

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceAuthAndBotPath(t *testing.T) {
	e := newTestEnv(t)
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/internal/v1/config", nil)
	req.Header.Set("X-Jantaxi-Bot", "passenger")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no secret: status %d, want 401", resp.StatusCode)
	}

	// The {bot} path segment must equal X-Jantaxi-Bot.
	st, body := e.call(t, "passenger", http.MethodPost, "/bots/driver/lease", map[string]any{"instance_id": "a", "ttl_s": 30})
	if st != http.StatusForbidden || errCode(body) != "forbidden" {
		t.Fatalf("path/header mismatch: %d %v", st, body)
	}
	// Passenger-only and driver-only routes.
	if st, _ := e.call(t, "driver", http.MethodGet, "/passenger/123/orders", nil); st != http.StatusForbidden {
		t.Fatalf("driver bot on passenger route: %d", st)
	}
	if st, _ := e.call(t, "passenger", http.MethodGet, "/driver/123/summary", nil); st != http.StatusForbidden {
		t.Fatalf("passenger bot on driver route: %d", st)
	}
}

func TestLeaseExclusivityAndOffset(t *testing.T) {
	e := newTestEnv(t)
	lease := func(instance string) map[string]any {
		t.Helper()
		st, body := e.call(t, "driver", http.MethodPost, "/bots/driver/lease", map[string]any{"instance_id": instance, "ttl_s": 30})
		if st != http.StatusOK {
			t.Fatalf("lease %s: %d %v", instance, st, body)
		}
		return body
	}
	offset := func(instance string, id int64) (int, map[string]any) {
		return e.call(t, "driver", http.MethodPost, "/bots/driver/offset", map[string]any{"instance_id": instance, "last_update_id": id})
	}

	if b := lease("bot-a"); b["acquired"] != true || b["last_update_id"] != float64(0) || b["lease_expires_at"] == nil {
		t.Fatalf("first lease: %v", b)
	}
	if b := lease("bot-b"); b["acquired"] != false {
		t.Fatalf("second instance must not get the lease: %v", b)
	}
	if b := lease("bot-a"); b["acquired"] != true {
		t.Fatalf("holder renewal: %v", b)
	}
	// The passenger bot's lease is independent.
	if st, b := e.call(t, "passenger", http.MethodPost, "/bots/passenger/lease", map[string]any{"instance_id": "bot-b", "ttl_s": 30}); st != 200 || b["acquired"] != true {
		t.Fatalf("passenger lease: %d %v", st, b)
	}

	if st, b := offset("bot-b", 50); st != http.StatusConflict || errDetail(b, "reason") != "not_lease_holder" {
		t.Fatalf("non-holder offset: %d %v", st, b)
	}
	if st, b := offset("bot-a", 10); st != 200 || b["last_update_id"] != float64(10) {
		t.Fatalf("offset: %d %v", st, b)
	}
	if st, b := offset("bot-a", 5); st != 200 || b["last_update_id"] != float64(10) {
		t.Fatalf("offset must be monotonic: %d %v", st, b)
	}

	// Expired lease: another instance takes over and sees the stored offset.
	if _, err := e.pool.Exec(context.Background(), `UPDATE bot_states SET lease_expires_at = now() - interval '1 second' WHERE bot = 'driver'`); err != nil {
		t.Fatal(err)
	}
	if b := lease("bot-b"); b["acquired"] != true || b["last_update_id"] != float64(10) {
		t.Fatalf("takeover after expiry: %v", b)
	}
	if st, _ := offset("bot-a", 20); st != http.StatusConflict {
		t.Fatalf("former holder must not write the offset: %d", st)
	}
	if b := lease("bot-a"); b["acquired"] != false {
		t.Fatalf("former holder must not regain a live lease: %v", b)
	}

	// Validation.
	for _, body := range []map[string]any{
		{"instance_id": "", "ttl_s": 30},
		{"instance_id": "x", "ttl_s": 5},
		{"instance_id": "x", "ttl_s": 121},
	} {
		if st, b := e.call(t, "driver", http.MethodPost, "/bots/driver/lease", body); st != http.StatusUnprocessableEntity {
			t.Fatalf("lease %v: %d %v", body, st, b)
		}
	}
	if st, _ := e.call(t, "driver", http.MethodPost, "/bots/driver/lease", map[string]any{"instance_id": "x", "ttl_s": 30, "extra": 1}); st != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", st)
	}
}

func TestLeaseConcurrentAcquisition(t *testing.T) {
	e := newTestEnv(t)
	var acquired atomic.Int32
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := AcquireLease(context.Background(), e.pool, "passenger", fmt.Sprintf("instance-%d", i), 30*time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			if l.Acquired {
				acquired.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := acquired.Load(); n != 1 {
		t.Fatalf("%d instances acquired the lease, want exactly 1", n)
	}
}

func TestClaimUpdateIdempotent(t *testing.T) {
	e := newTestEnv(t)
	claim := func(bot string, id string) (int, map[string]any) {
		return e.call(t, bot, http.MethodPost, "/bots/"+bot+"/updates/"+id+"/claim", nil)
	}
	if st, b := claim("driver", "42"); st != 200 || b["claimed"] != true {
		t.Fatalf("first claim: %d %v", st, b)
	}
	if st, b := claim("driver", "42"); st != 200 || b["claimed"] != false {
		t.Fatalf("second claim: %d %v", st, b)
	}
	if st, b := claim("passenger", "42"); st != 200 || b["claimed"] != true {
		t.Fatalf("claims are per bot: %d %v", st, b)
	}
	for _, bad := range []string{"-1", "abc", "1.5"} {
		if st, _ := claim("driver", bad); st != http.StatusNotFound {
			t.Fatalf("claim %q: %d, want 404", bad, st)
		}
	}

	// Concurrent claims of one update: exactly one winner.
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := ClaimUpdate(context.Background(), e.pool, "driver", 777)
			if err != nil {
				t.Error(err)
			}
			if ok {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d concurrent claims succeeded, want 1", winners.Load())
	}
}

func TestCleanupProcessedUpdates(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `
		INSERT INTO bot_processed_updates (bot, update_id, processed_at) VALUES
		('driver', 1, now() - interval '8 days'),
		('passenger', 2, now() - interval '7 days 1 minute'),
		('driver', 3, now() - interval '6 days'),
		('driver', 4, now())`)
	if err != nil {
		t.Fatal(err)
	}
	n, err := CleanupProcessedUpdates(ctx, e.pool, ProcessedUpdateRetention)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deleted %d rows, want 2", n)
	}
	var left int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM bot_processed_updates`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 2 {
		t.Fatalf("%d rows left, want 2", left)
	}
}
