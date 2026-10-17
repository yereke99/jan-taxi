package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// flakyTelegram is a TCP Bot API stand-in for network failures: each
// getUpdates request takes the next step of its script ("hang": the
// connection goes silent, "drop": it breaks mid-response); afterwards it
// serves the queued updates.
type flakyTelegram struct {
	ln      net.Listener
	mu      sync.Mutex
	script  []string
	updates []telegram.Update
	conns   int
	done    chan struct{}
}

func newFlakyTelegram(t *testing.T, script ...string) *flakyTelegram {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &flakyTelegram{ln: ln, script: script, done: make(chan struct{})}
	t.Cleanup(func() { close(f.done); _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns++
			f.mu.Unlock()
			go f.serve(conn)
		}
	}()
	return f
}

func (f *flakyTelegram) url() string { return "http://" + f.ln.Addr().String() }

func (f *flakyTelegram) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns
}

func (f *flakyTelegram) serve(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		var params map[string]any
		_ = json.NewDecoder(req.Body).Decode(&params)
		body := `{"ok":true,"result":true}`
		if strings.HasSuffix(req.URL.Path, "/getUpdates") {
			f.mu.Lock()
			step := ""
			if len(f.script) > 0 {
				step, f.script = f.script[0], f.script[1:]
			}
			offset, _ := params["offset"].(float64)
			var out []telegram.Update
			for _, u := range f.updates {
				if u.UpdateID >= int64(offset) {
					out = append(out, u)
				}
			}
			f.mu.Unlock()
			switch step {
			case "hang":
				<-f.done
				return
			case "drop":
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 300\r\n\r\n{\"ok\":true,\"res")
				return
			}
			b, _ := json.Marshal(map[string]any{"ok": true, "result": append([]telegram.Update{}, out...)})
			body = string(b)
		}
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	}
}

// resetCounter counts ResetConnections calls on top of a real client.
type resetCounter struct {
	*telegram.Client
	resets atomic.Int32
}

func (r *resetCounter) ResetConnections() {
	r.resets.Add(1)
	r.Client.ResetConnections()
}

func TestPollerRecoversFromHungAndDroppedConnections(t *testing.T) {
	e := newPollerEnv(t)
	tg := newFlakyTelegram(t, "hang", "drop")
	tg.updates = []telegram.Update{msgUpdate(1)}
	client := &resetCounter{Client: telegram.New(testToken, telegram.WithBaseURL(tg.url()),
		telegram.WithRequestTimeout(300*time.Millisecond))}
	rec := &recorder{}
	p := e.poller(t, "bot-a", rec.handler(), func(c *PollerConfig) { c.Telegram = client })
	begin := time.Now()
	stop := start(t, p)
	// The hang costs one request deadline (300ms) and the drop fails at once;
	// both are followed by short backoffs.
	waitFor(t, 3*time.Second, "update after the network failures", func() bool { return len(rec.ids()) == 1 })
	if elapsed := time.Since(begin); elapsed > 2500*time.Millisecond {
		t.Fatalf("recovered after %v", elapsed)
	}
	waitFor(t, 2*time.Second, "healthy again", func() bool {
		h := p.Health()
		return h.Healthy && h.ConsecutiveFailures == 0 && h.LastError == ""
	})
	stop()
	if client.resets.Load() < 1 {
		t.Fatal("connections not rebuilt after two network failures in a row")
	}
	if tg.connections() < 3 {
		t.Fatalf("%d connections: the dead ones must not be reused", tg.connections())
	}
	if strings.Contains(e.logs.String(), testToken) {
		t.Fatal("token leaked into logs")
	}
}

// stuckTelegram is a TelegramAPI whose first getUpdates ignores its context
// and never returns, like a transport blocked on a dead connection.
type stuckTelegram struct {
	mu      sync.Mutex
	calls   int
	resets  int
	release chan struct{}
}

func (s *stuckTelegram) GetUpdates(ctx context.Context, p telegram.GetUpdatesParams) ([]telegram.Update, error) {
	s.mu.Lock()
	s.calls++
	first := s.calls == 1
	s.mu.Unlock()
	if first {
		<-s.release
		return nil, errors.New("released")
	}
	if p.Offset <= 1 {
		return []telegram.Update{msgUpdate(1)}, nil
	}
	time.Sleep(5 * time.Millisecond)
	return nil, ctx.Err()
}

func (s *stuckTelegram) DeleteWebhook(context.Context, bool) error { return nil }

func (s *stuckTelegram) ResetConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resets++
}

func TestPollerAbandonsStalledPollAndRebuildsConnections(t *testing.T) {
	e := newPollerEnv(t)
	tg := &stuckTelegram{release: make(chan struct{})}
	defer close(tg.release)
	rec := &recorder{}
	p := e.poller(t, "bot-a", rec.handler(), func(c *PollerConfig) {
		c.Telegram, c.StallTimeout = tg, 200*time.Millisecond
	})
	stop := start(t, p)
	waitFor(t, 3*time.Second, "update after the stalled poll", func() bool { return slices.Equal(rec.ids(), []int64{1}) })
	waitFor(t, 2*time.Second, "healthy after the stall", func() bool { h := p.Health(); return h.Healthy && !h.Stalled })
	if h := p.Health(); h.Stalls != 1 {
		t.Fatalf("stalls = %d, want 1", h.Stalls)
	}
	stop()
	tg.mu.Lock()
	resets := tg.resets
	tg.mu.Unlock()
	if resets < 1 {
		t.Fatal("connections not rebuilt after a stall")
	}
	if !strings.Contains(e.logs.String(), "telegram request stalled") {
		t.Fatal("stall not logged")
	}

	// While the latest call is stalled the leader reports unhealthy.
	p.running.Store(true)
	p.mu.Lock()
	p.leader, p.stalled, p.lastProgress = true, true, time.Now()
	p.mu.Unlock()
	if h := p.Health(); h.Healthy || !h.Stalled {
		t.Fatalf("stalled leader: %+v", h)
	}
	p.running.Store(false)
}

func TestPollerMovesOnFromHandlerThatIgnoresItsContext(t *testing.T) {
	e := newPollerEnv(t)
	e.tg.push(msgUpdate(1), msgUpdate(2))
	release := make(chan struct{})
	defer close(release)
	rec := &recorder{}
	h := HandlerFunc(func(ctx context.Context, u telegram.Update) error {
		if u.UpdateID == 1 {
			<-release // blocked on something that knows nothing about ctx
		}
		return rec.handler().HandleUpdate(ctx, u)
	})
	p := e.poller(t, "bot-a", h, func(c *PollerConfig) {
		c.HandlerTimeout, c.HandlerGrace = 50*time.Millisecond, 50*time.Millisecond
	})
	stop := start(t, p)
	waitFor(t, 3*time.Second, "offset 2", func() bool { _, last, _ := e.state.snapshot(); return last == 2 })
	stop()
	if got := rec.ids(); !slices.Equal(got, []int64{2}) {
		t.Fatalf("handled %v, want [2]", got)
	}
	if p.Health().AbandonedHandlers != 1 || !strings.Contains(e.logs.String(), "did not return in time") {
		t.Fatalf("stuck handler not reported: %+v", p.Health())
	}
}

func TestStandbyHealthFollowsSuccessfulLeaseChecks(t *testing.T) {
	e := newPollerEnv(t)
	p := e.poller(t, "bot-a", (&recorder{}).handler(), nil)
	p.running.Store(true)
	defer p.running.Store(false)
	p.mu.Lock()
	p.started = time.Now().Add(-time.Hour)
	p.lastLeaseTry = time.Now() // attempts keep failing
	p.lastLeaseOK = time.Now().Add(-2 * HealthWindow)
	p.mu.Unlock()
	if h := p.Health(); h.Healthy || h.Role != RoleStandby {
		t.Fatalf("standby that cannot reach the API: %+v", h)
	}
	p.mu.Lock()
	p.lastLeaseOK = time.Now()
	p.mu.Unlock()
	if h := p.Health(); !h.Healthy {
		t.Fatalf("standby with a fresh lease check: %+v", h)
	}
	// A leader waiting out Telegram's retry_after is not stalled.
	p.mu.Lock()
	p.leader, p.leaderSince, p.lastProgress = true, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)
	p.retryUntil = time.Now().Add(time.Minute)
	p.mu.Unlock()
	if h := p.Health(); !h.Healthy {
		t.Fatalf("leader in retry_after: %+v", h)
	}
}

func TestPollerResetsStateConnectionsAfterLeaseFailures(t *testing.T) {
	e := newPollerEnv(t)
	state := &resettableState{StateAPI: e.state.client()}
	e.state.srv.Close() // the internal API is unreachable
	p := e.poller(t, "bot-a", (&recorder{}).handler(), func(c *PollerConfig) { c.State = state })
	stop := start(t, p)
	waitFor(t, 3*time.Second, "state reset", func() bool { return state.resets.Load() >= 1 })
	stop()
	if h := p.Health(); h.Role != RoleStandby {
		t.Fatalf("role %s without a lease", h.Role)
	}
}

type resettableState struct {
	StateAPI
	resets atomic.Int32
}

func (r *resettableState) ResetConnections() { r.resets.Add(1) }

// flappingHealth reports the health returned by fn.
type flappingHealth struct{ fn func() bool }

func (f flappingHealth) Health() Health { return Health{Healthy: f.fn()} }

func TestWatchHealth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if !watchHealth(ctx, flappingHealth{func() bool { return false }}, 100*time.Millisecond) {
		t.Fatal("continuously unhealthy source not reported")
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 2*time.Second {
		t.Fatalf("reported after %v", d)
	}
	var n atomic.Int32
	flapping := flappingHealth{func() bool { return n.Add(1)%2 == 0 }}
	short, cancelShort := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelShort()
	if watchHealth(short, flapping, 100*time.Millisecond) {
		t.Fatal("a source that recovers in between must not be reported")
	}
}

// fakeRunner is a pollRunner with a fixed health that stops with its context.
type fakeRunner struct{ healthy bool }

func (f fakeRunner) Health() Health { return Health{Healthy: f.healthy} }

func (f fakeRunner) Run(ctx context.Context) error { <-ctx.Done(); return nil }

func TestSuperviseStopsAnUnhealthyBot(t *testing.T) {
	listen := func() net.Listener {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return ln
	}
	b := Bot{Name: "driver", StallExit: 100 * time.Millisecond, Log: slog.New(slog.DiscardHandler)}
	done := make(chan error, 1)
	go func() { done <- b.supervise(context.Background(), fakeRunner{healthy: false}, listen()) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnhealthy) {
			t.Fatalf("supervise = %v, want ErrUnhealthy", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unhealthy bot kept running")
	}

	// A healthy bot runs until its context ends.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- b.supervise(ctx, fakeRunner{healthy: true}, listen()) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("healthy bot: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("healthy bot did not stop")
	}
}

func TestListenHealthWaitsForABusyPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := busy.Addr().String()
	go func() { time.Sleep(200 * time.Millisecond); _ = busy.Close() }() // the old process exits
	ln, err := listenHealth(context.Background(), addr, 5*time.Second, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("port not taken over: %v", err)
	}
	_ = ln.Close()

	held, _ := net.Listen("tcp", "127.0.0.1:0")
	defer held.Close()
	start := time.Now()
	if _, err := listenHealth(context.Background(), held.Addr().String(), 300*time.Millisecond, slog.New(slog.DiscardHandler)); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("busy port: %v after %v", err, time.Since(start))
	}
}
