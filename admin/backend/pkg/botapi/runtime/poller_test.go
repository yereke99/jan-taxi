package runtime

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

type pollerEnv struct {
	tg    *fakeTelegram
	state *fakeState
	logs  *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newPollerEnv(t *testing.T) *pollerEnv {
	return &pollerEnv{tg: newFakeTelegram(t), state: newFakeState(t), logs: &syncBuffer{}}
}

func (e *pollerEnv) poller(t *testing.T, instance string, h Handler, mod func(*PollerConfig)) *Poller {
	t.Helper()
	cfg := PollerConfig{
		Telegram: e.tg.client(), State: e.state.client(), Handler: h, InstanceID: instance,
		Log:      slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		LeaseTTL: 2 * time.Second, RenewEvery: 50 * time.Millisecond, PollTimeout: -1,
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, ConflictBackoff: 50 * time.Millisecond,
	}
	if mod != nil {
		mod(&cfg)
	}
	p, err := NewPoller(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// start runs p until the returned stop function is called.
func start(t *testing.T, p *Poller) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = p.Run(ctx); close(done) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("poller did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func TestPollerProcessesEachUpdateOnceAndStoresOffset(t *testing.T) {
	e := newPollerEnv(t)
	e.tg.push(msgUpdate(1), msgUpdate(2), msgUpdate(3))
	rec := &recorder{}
	stop := start(t, e.poller(t, "bot-a", rec.handler(), nil))
	waitFor(t, 3*time.Second, "offset 3", func() bool { _, last, _ := e.state.snapshot(); return last == 3 })
	time.Sleep(50 * time.Millisecond) // let a few more polls happen
	stop()

	if got := rec.ids(); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("handled %v, want [1 2 3]", got)
	}
	_, deletes := e.tg.stats()
	if deletes != 1 {
		t.Fatalf("deleteWebhook called %d times, want 1", deletes)
	}
	e.tg.mu.Lock()
	defer e.tg.mu.Unlock()
	if last := e.tg.offsets[len(e.tg.offsets)-1]; last != 4 {
		t.Fatalf("polls continue from offset %d, want 4", last)
	}
	allowed, _ := e.tg.lastParams["allowed_updates"].([]any)
	if len(allowed) != 4 || allowed[0] != "message" || allowed[3] != "my_chat_member" || e.tg.lastParams["limit"] != float64(50) {
		t.Fatalf("getUpdates params: %v", e.tg.lastParams)
	}
	if strings.Contains(e.logs.String(), testToken) {
		t.Fatal("token leaked into logs")
	}
}

func TestPollerClaimPreventsDoubleProcessingAfterRestart(t *testing.T) {
	e := newPollerEnv(t)
	// A previous run claimed and handled update 5 but crashed before storing
	// the offset (still 4), so Telegram delivers 5 again.
	e.state.lastUpdate = 4
	e.state.claims[5] = true
	e.tg.push(msgUpdate(5), msgUpdate(6))
	rec := &recorder{}
	stop := start(t, e.poller(t, "bot-restarted", rec.handler(), nil))
	waitFor(t, 3*time.Second, "offset 6", func() bool { _, last, _ := e.state.snapshot(); return last == 6 })
	stop()
	if got := rec.ids(); !slices.Equal(got, []int64{6}) {
		t.Fatalf("handled %v, want only [6]", got)
	}
	e.tg.mu.Lock()
	first := e.tg.offsets[0]
	e.tg.mu.Unlock()
	if first != 5 {
		t.Fatalf("first poll offset %d, want stored offset+1 = 5", first)
	}

	// A second restart replays nothing.
	rec2 := &recorder{}
	stop2 := start(t, e.poller(t, "bot-restarted", rec2.handler(), nil))
	time.Sleep(200 * time.Millisecond)
	stop2()
	if len(rec2.ids()) != 0 {
		t.Fatalf("second restart handled %v", rec2.ids())
	}
}

func TestPollerConflictBacksOffAndRechecksLease(t *testing.T) {
	e := newPollerEnv(t)
	e.tg.conflicts = 3
	e.tg.push(msgUpdate(1))
	rec := &recorder{}
	begin := time.Now()
	stop := start(t, e.poller(t, "bot-a", rec.handler(), func(c *PollerConfig) { c.RenewEvery = time.Second }))
	waitFor(t, 5*time.Second, "update after conflicts", func() bool { return len(rec.ids()) == 1 })
	elapsed := time.Since(begin)
	stop()
	// Backoff after each 409: >= 50ms, then >= 50ms, then >= 50ms (jittered up to 100ms, 200ms).
	if elapsed < 150*time.Millisecond {
		t.Fatalf("processed after %v: 409 must back off", elapsed)
	}
	calls, _ := e.tg.stats()
	if calls > 10 {
		t.Fatalf("%d getUpdates calls: conflicts must not be retried in a tight loop", calls)
	}
	_, _, leaseCalls := e.state.snapshot()
	if leaseCalls < 3 {
		t.Fatalf("lease re-checked %d times, want a re-check after each conflict", leaseCalls)
	}
}

func TestPollerDeletesActiveWebhook(t *testing.T) {
	e := newPollerEnv(t)
	e.tg.webhookActive = true
	e.tg.push(msgUpdate(1))
	rec := &recorder{}
	stop := start(t, e.poller(t, "bot-a", rec.handler(), nil))
	waitFor(t, 3*time.Second, "update", func() bool { return len(rec.ids()) == 1 })
	stop()
	if _, deletes := e.tg.stats(); deletes < 1 {
		t.Fatal("webhook not deleted")
	}
}

func TestPollerHonoursRetryAfter(t *testing.T) {
	e := newPollerEnv(t)
	e.tg.tooMany = 1
	e.tg.push(msgUpdate(1))
	rec := &recorder{}
	stop := start(t, e.poller(t, "bot-a", rec.handler(), nil))
	waitFor(t, 5*time.Second, "update after 429", func() bool { return len(rec.ids()) == 1 })
	stop()
	e.tg.mu.Lock()
	defer e.tg.mu.Unlock()
	if gap := e.tg.getTimes[1].Sub(e.tg.getTimes[0]); gap < 950*time.Millisecond {
		t.Fatalf("retried after %v, want retry_after (1s)", gap)
	}
}

func TestPollerLeaseLossStopsPolling(t *testing.T) {
	e := newPollerEnv(t)
	rec := &recorder{}
	p := e.poller(t, "bot-a", rec.handler(), nil)
	stop := start(t, p)
	waitFor(t, 3*time.Second, "leader", func() bool { return p.Health().Role == RoleLeader })

	// Another instance takes the lease (e.g. after a network partition).
	e.state.mu.Lock()
	e.state.holder, e.state.expires = "bot-b", time.Now().Add(time.Hour)
	e.state.mu.Unlock()
	waitFor(t, 3*time.Second, "standby", func() bool { return p.Health().Role == RoleStandby })
	time.Sleep(30 * time.Millisecond)
	before, _ := e.tg.stats()
	e.tg.push(msgUpdate(1))
	time.Sleep(300 * time.Millisecond)
	after, _ := e.tg.stats()
	stop()
	if after != before {
		t.Fatalf("standby kept polling: %d -> %d getUpdates", before, after)
	}
	if len(rec.ids()) != 0 {
		t.Fatalf("standby handled %v", rec.ids())
	}
	if h := p.Health(); h.Healthy {
		t.Fatal("a stopped poller must not report healthy")
	}
}

func TestPollerStepsDownWhenLeaseCannotBeRenewed(t *testing.T) {
	e := newPollerEnv(t)
	p := e.poller(t, "bot-a", (&recorder{}).handler(), func(c *PollerConfig) {
		c.LeaseTTL, c.RenewEvery = 300*time.Millisecond, 100*time.Millisecond
	})
	stop := start(t, p)
	waitFor(t, 3*time.Second, "leader", func() bool { return p.Health().Role == RoleLeader })
	e.state.srv.Close() // the internal API becomes unreachable
	waitFor(t, 3*time.Second, "step down", func() bool { return p.Health().Role == RoleStandby })
	stop()
}

func TestOnlyLeaseHolderHandlesUpdates(t *testing.T) {
	e := newPollerEnv(t)
	for i := int64(1); i <= 20; i++ {
		e.tg.push(msgUpdate(i))
	}
	var mu sync.Mutex
	byInstance := map[string][]int64{}
	mk := func(name string) Handler {
		return HandlerFunc(func(_ context.Context, u telegram.Update) error {
			mu.Lock()
			byInstance[name] = append(byInstance[name], u.UpdateID)
			mu.Unlock()
			return nil
		})
	}
	stopA := start(t, e.poller(t, "bot-a", mk("a"), nil))
	stopB := start(t, e.poller(t, "bot-b", mk("b"), nil))
	waitFor(t, 5*time.Second, "offset 20", func() bool { _, last, _ := e.state.snapshot(); return last == 20 })
	stopA()
	stopB()
	mu.Lock()
	defer mu.Unlock()
	total := len(byInstance["a"]) + len(byInstance["b"])
	if total != 20 || (len(byInstance["a"]) > 0 && len(byInstance["b"]) > 0) {
		t.Fatalf("updates handled by both instances or more than once: %v", byInstance)
	}
}

func TestPollerRecoversFromHandlerPanicAndClaimFailures(t *testing.T) {
	e := newPollerEnv(t)
	e.state.claimFail = 2
	e.tg.push(msgUpdate(1), msgUpdate(2))
	rec := &recorder{fn: func(u telegram.Update) {
		if u.UpdateID == 1 {
			panic("boom")
		}
	}}
	stop := start(t, e.poller(t, "bot-a", rec.handler(), nil))
	waitFor(t, 3*time.Second, "offset 2", func() bool { _, last, _ := e.state.snapshot(); return last == 2 })
	stop()
	if got := rec.ids(); !slices.Equal(got, []int64{2}) {
		t.Fatalf("handled %v, want [2] (1 panicked before recording)", got)
	}
	if !strings.Contains(e.logs.String(), "update handler panicked") {
		t.Fatal("panic not logged")
	}
}

func TestPollerFinishesCurrentUpdateOnShutdown(t *testing.T) {
	e := newPollerEnv(t)
	e.tg.push(msgUpdate(1), msgUpdate(2))
	entered := make(chan struct{})
	release := make(chan struct{})
	var finishedCtxErr error
	rec := &recorder{}
	h := HandlerFunc(func(ctx context.Context, u telegram.Update) error {
		if u.UpdateID == 1 {
			close(entered)
			<-release
			finishedCtxErr = ctx.Err()
		}
		return rec.handler().HandleUpdate(ctx, u)
	})
	stop := start(t, e.poller(t, "bot-a", h, nil))
	<-entered
	go func() { time.Sleep(50 * time.Millisecond); close(release) }()
	stop() // SIGTERM while update 1 is in progress
	if got := rec.ids(); !slices.Equal(got, []int64{1}) {
		t.Fatalf("handled %v, want the in-flight update only", got)
	}
	if finishedCtxErr != nil {
		t.Fatalf("handler context cancelled by shutdown: %v", finishedCtxErr)
	}
	if _, last, _ := e.state.snapshot(); last != 1 {
		t.Fatalf("offset after shutdown = %d, want 1", last)
	}
}
