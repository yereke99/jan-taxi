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
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// DefaultHealthPort is the bots' health port (§13).
const DefaultHealthPort = "8090"

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
	// the command menu and descriptions).
	OnLeader func(ctx context.Context)
}

// Run serves the health endpoint and polls until ctx ends.
func (b Bot) Run(ctx context.Context) error {
	if b.Telegram == nil || b.API == nil || b.Handler == nil {
		return errors.New("runtime: telegram, api and handler are required")
	}
	if b.Log == nil {
		b.Log = slog.New(slog.DiscardHandler)
	}
	poller, err := NewPoller(PollerConfig{
		Telegram: b.Telegram, State: b.API, Handler: b.Handler, InstanceID: b.InstanceID,
		Log: b.Log, OnLeader: b.OnLeader,
	})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", b.HealthAddr)
	if err != nil {
		return fmt.Errorf("health listener: %w", err)
	}
	b.Log.Info("bot starting", slog.String("bot", b.Name), slog.String("instance", b.InstanceID),
		slog.String("health", ln.Addr().String()))
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return ServeHealthOn(gctx, ln, poller, b.Log) })
	g.Go(func() error {
		err := poller.Run(gctx)
		return err
	})
	err = g.Wait()
	b.Log.Info("bot stopped", slog.String("bot", b.Name))
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
