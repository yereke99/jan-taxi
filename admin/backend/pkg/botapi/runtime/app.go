package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// DefaultHealthPort is the bots' health port (§13).
const DefaultHealthPort = "8090"

// DefaultStallExit is how long a bot may stay unhealthy before Run gives up,
// so that the process supervisor (Docker restart policy) restarts it.
const DefaultStallExit = 5 * time.Minute

// stopWait bounds the wait for a stalled bot to stop after Run gave up.
const stopWait = 30 * time.Second

// healthPortWait is how long a starting bot waits for its health port while
// the previous process is still finishing its shutdown.
const healthPortWait = 45 * time.Second

// ErrUnhealthy is returned by Bot.Run when the poller stayed unhealthy for
// Bot.StallExit.
var ErrUnhealthy = errors.New("runtime: bot unhealthy for too long")

// Getenv returns the trimmed environment variable or def when it is empty.
func Getenv(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// InstanceID returns INSTANCE_ID, else the hostname. It identifies the lease
// holder, so it must differ between processes polling the same bot.
func InstanceID() string {
	if v := Getenv("INSTANCE_ID", ""); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return fmt.Sprintf("bot-%d", os.Getpid())
}

// NewLogger returns a JSON (or text when LOG_FORMAT=text) logger on w at LOG_LEVEL.
func NewLogger(w io.Writer, service string) *slog.Logger {
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(Getenv("LOG_LEVEL", "info")))
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewJSONHandler(w, opts)
	if Getenv("LOG_FORMAT", "json") == "text" {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h).With(slog.String("service", service))
}

// HealthcheckCommand implements "<bot> healthcheck": it GETs the local
// health endpoint and returns the process exit code (0 healthy, 1 not).
func HealthcheckCommand() int {
	port := Getenv("HEALTH_PORT", DefaultHealthPort)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := CheckHealth(ctx, "http://127.0.0.1:"+port+"/healthz"); err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		return 1
	}
	return 0
}

// SignalContext is cancelled on SIGINT or SIGTERM.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Bot is a configured bot process: Telegram client, internal API client,
// update handler and the polling/health runtime.
type Bot struct {
	Name       string
	Telegram   *telegram.Client
	API        *botapi.Client
	Handler    Handler
	InstanceID string
	HealthAddr string // ":8090"
	Log        *slog.Logger
	// OnLeader runs whenever this instance starts polling (e.g. to publish
	// the command menu, descriptions and default menu button). It never runs
	// twice at the same time.
	OnLeader func(ctx context.Context)
	// StallExit is how long the poller may stay unhealthy before Run returns
	// ErrUnhealthy; 0 means DefaultStallExit, negative disables it.
	StallExit time.Duration
}

// Run serves the health endpoint and polls until ctx ends.
func (b Bot) Run(ctx context.Context) error {
	if b.Telegram == nil || b.API == nil || b.Handler == nil {
		return errors.New("runtime: telegram, api and handler are required")
	}
	if b.Log == nil {
		b.Log = slog.New(slog.DiscardHandler)
	}
	var onLeader func(ctx context.Context)
	if b.OnLeader != nil {
		var busy atomic.Bool
		onLeader = func(ctx context.Context) {
			if !busy.CompareAndSwap(false, true) {
				return // the previous run is still publishing the same profile
			}
			defer busy.Store(false)
			b.OnLeader(ctx)
		}
	}
	poller, err := NewPoller(PollerConfig{
		Telegram: b.Telegram, State: b.API, Handler: b.Handler, InstanceID: b.InstanceID,
		Log: b.Log, OnLeader: onLeader,
	})
	if err != nil {
		return err
	}
	ln, err := listenHealth(ctx, b.HealthAddr, healthPortWait, b.Log)
	if err != nil {
		return fmt.Errorf("health listener: %w", err)
	}
	b.Log.Info("bot starting", slog.String("bot", b.Name), slog.String("instance", b.InstanceID),
		slog.String("health", ln.Addr().String()))
	err = b.supervise(ctx, poller, ln)
	b.Log.Info("bot stopped", slog.String("bot", b.Name))
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// listenHealth binds addr, retrying while the port is still held (by a
// previous instance finishing its shutdown) for at most wait.
func listenHealth(ctx context.Context, addr string, wait time.Duration, log *slog.Logger) (net.Listener, error) {
	deadline := time.Now().Add(wait)
	for logged := false; ; logged = true {
		ln, err := net.Listen("tcp", addr)
		if err == nil || !errors.Is(err, syscall.EADDRINUSE) || time.Now().After(deadline) {
			return ln, err
		}
		if !logged {
			log.Warn("health port busy, waiting for it", slog.String("addr", addr))
		}
		t := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// pollRunner is the part of *Poller that Bot.Run supervises.
type pollRunner interface {
	HealthSource
	Run(ctx context.Context) error
}

// supervise serves health on ln and runs the poller until ctx ends, or
// until the poller has been unhealthy for StallExit (ErrUnhealthy).
func (b Bot) supervise(ctx context.Context, poller pollRunner, ln net.Listener) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	g, gctx := errgroup.WithContext(runCtx)
	g.Go(func() error { return ServeHealthOn(gctx, ln, poller, b.Log) })
	g.Go(func() error { return poller.Run(gctx) })
	done := make(chan error, 1)
	go func() { done <- g.Wait() }()

	stallExit := b.StallExit
	if stallExit == 0 {
		stallExit = DefaultStallExit
	}
	unhealthy := make(chan struct{})
	if stallExit > 0 {
		go func() {
			if watchHealth(gctx, poller, stallExit) {
				close(unhealthy)
			}
		}()
	}
	var err error
	select {
	case err = <-done:
	case <-unhealthy:
		h := poller.Health()
		b.Log.Error("bot unhealthy for too long; stopping so that it is restarted", slog.String("bot", b.Name),
			slog.Duration("unhealthy_for", stallExit), slog.String("role", h.Role), slog.String("last_error", h.LastError))
		cancel()
		select {
		case <-done:
		case <-time.After(stopWait):
			b.Log.Error("bot did not stop in time", slog.String("bot", b.Name))
		}
		err = ErrUnhealthy
	}
	return err
}

// watchHealth reports true once src has been unhealthy for d without a
// break, and false when ctx ends first.
func watchHealth(ctx context.Context, src HealthSource, d time.Duration) bool {
	ticker := time.NewTicker(min(max(d/10, 10*time.Millisecond), 10*time.Second))
	defer ticker.Stop()
	var since time.Time
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
		if src.Health().Healthy {
			since = time.Time{}
			continue
		}
		now := time.Now()
		if since.IsZero() {
			since = now
		}
		if now.Sub(since) >= d {
			return true
		}
	}
}
