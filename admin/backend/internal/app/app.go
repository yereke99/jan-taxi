// Package app wires the API process: configuration, database pool, file
// storage, domain services, HTTP routers, background jobs and the two HTTP
// listeners (public and internal). Each domain area builds its services and
// mounts its routes in its own wire_<area>.go file.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/payments"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
)

// App holds everything the wire_*.go hooks need to construct services and
// mount routes.
type App struct {
	Cfg   config.Config
	Log   *slog.Logger
	Pool  *pgxpool.Pool
	Store *storage.Store

	Auth *auth.Service // set by wireIdentity
	Hub  *realtime.Hub // set by wireIdentity
	MW   Middlewares   // set by wireIdentity

	Payments *payments.Service // set by wireFinance
	// ETA refines drivers' arrival estimates (§22.6); set by wireRouting.
	ETA *dispatch.ArrivalETA

	// Mounted under /api/v1. Passenger, Driver and AnyApp carry the app-token
	// middleware (Driver also loads the driver profile).
	Public, Passenger, Driver, AnyApp chi.Router
	// Mounted under /api/v1/admin. Admin carries the session + CSRF middleware;
	// add auth.Permit(perm) per route.
	AdminPublic, Admin chi.Router
	// Mounted under /internal/v1 on the internal listener; carries the bot
	// service-secret middleware.
	Internal chi.Router

	root, internalRoot *chi.Mux

	jobs     []job
	workers  []worker
	checks   []readinessCheck
	onStop   []func()
	wireErrs []error

	latestMigration int64
	ownsPool        bool
	started         atomic.Bool
	draining        atomic.Bool
}

// Middlewares are the authentication middlewares produced by wireIdentity.
type Middlewares struct {
	Passenger, Driver, AnyApp, Admin, Service func(http.Handler) http.Handler
}

// New opens the database pool and builds the App.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConns)
	if err != nil {
		return nil, err
	}
	a, err := NewWithPool(ctx, cfg, log, pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	a.ownsPool = true
	return a, nil
}

// NewWithPool builds the App on an existing pool (tests use a per-test
// database). The caller keeps ownership of the pool.
func NewWithPool(ctx context.Context, cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) (*App, error) {
	if log == nil {
		log = slog.Default()
	}
	for _, w := range cfg.Warnings {
		log.Warn("configuration warning", slog.String("warning", w))
	}
	store, err := storage.New(cfg.FileStorageDir, cfg.MaxUploadBytes)
	if err != nil {
		return nil, err
	}
	latest, err := db.LatestMigrationVersion()
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	a := &App{Cfg: cfg, Log: log, Pool: pool, Store: store, latestMigration: latest}

	if err := a.bootstrapAdmin(ctx); err != nil {
		return nil, fmt.Errorf("bootstrap admin: %w", err)
	}
	if err := a.wireIdentity(); err != nil {
		return nil, fmt.Errorf("wire identity: %w", err)
	}
	if err := a.MW.validate(); err != nil {
		return nil, err
	}
	a.buildRouters()

	hooks := []struct {
		name string
		fn   func() error
	}{
		{"identity routes", a.mountIdentity},
		{"core", a.wireCore},
		{"notifications", a.wireNotifications},
		{"routing", a.wireRouting},
		{"orders", a.wireOrders},
		{"drivers", a.wireDrivers},
		{"finance", a.wireFinance},
		{"bot api", a.wireBotAPI},
	}
	for _, h := range hooks {
		if err := h.fn(); err != nil {
			return nil, fmt.Errorf("wire %s: %w", h.name, err)
		}
	}
	a.addPlatformJobs()
	if err := errors.Join(a.wireErrs...); err != nil {
		return nil, err
	}
	return a, nil
}

func (m Middlewares) validate() error {
	if m.Passenger == nil || m.Driver == nil || m.AnyApp == nil || m.Admin == nil || m.Service == nil {
		return errors.New("wire identity: every middleware in App.MW must be set")
	}
	return nil
}

// Handler returns the public HTTP handler (port API_PORT).
func (a *App) Handler() http.Handler { return a.root }

// InternalHandler returns the internal HTTP handler (port INTERNAL_PORT).
func (a *App) InternalHandler() http.Handler { return a.internalRoot }

// OnShutdown registers fn to run when graceful shutdown begins, e.g. to tell
// WebSocket clients to reconnect elsewhere. Must be called before Run.
func (a *App) OnShutdown(fn func()) {
	if fn != nil {
		a.onStop = append(a.onStop, fn)
	}
}

// AddReadinessCheck adds a named check to /readyz (e.g. the realtime LISTEN
// connection). Must be called before Run.
func (a *App) AddReadinessCheck(name string, fn func(ctx context.Context) error) {
	if name == "" || fn == nil {
		a.wireErrs = append(a.wireErrs, errors.New("AddReadinessCheck: name and fn are required"))
		return
	}
	a.checks = append(a.checks, readinessCheck{name: name, fn: fn})
}

// Close releases the database pool when the App opened it.
func (a *App) Close() {
	if a.ownsPool && a.Pool != nil {
		a.Pool.Close()
	}
}
