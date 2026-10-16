package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Server limits. There is deliberately no WriteTimeout: WebSocket connections
// and large file downloads are long-lived.
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 64 << 10
	shutdownTimeout   = 20 * time.Second
)

// Per-IP request limits for /api/v1. Login endpoints get a much stricter
// budget on top of the general one.
const (
	apiPerMinute, apiBurst   = 600, 120
	authPerMinute, authBurst = 30, 10
)

func (a *App) buildRouters() {
	root := chi.NewRouter()
	root.Use(
		httpx.RequestIDMiddleware(a.Log),
		httpx.RealIP(a.Cfg.TrustedProxies),
		httpx.AccessLog, // outside Recoverer so that recovered panics are logged with status 500
		httpx.Recoverer,
		httpx.SecurityHeaders,
		httpx.CORS(a.Cfg.CORSAllowedOrigins),
	)
	setJSONFallbacks(root)
	root.Get("/healthz", a.handleHealthz)
	root.Get("/readyz", a.handleReadyz)

	api := chi.NewRouter()
	setJSONFallbacks(api)
	api.Use(rateLimit(
		httpx.NewRateLimiter(apiPerMinute, apiBurst),
		httpx.NewRateLimiter(authPerMinute, authBurst),
	))
	api.Get("/openapi.yaml", serveOpenAPI)
	// ARCHITECTURE §19 serves the probes under /api/v1 as well as at the root.
	api.Get("/healthz", a.handleHealthz)
	api.Get("/readyz", a.handleReadyz)
	a.Public = api.With()
	a.Passenger = api.With(a.MW.Passenger)
	a.Driver = api.With(a.MW.Driver)
	a.AnyApp = api.With(a.MW.AnyApp)

	admin := chi.NewRouter()
	setJSONFallbacks(admin)
	a.AdminPublic = admin.With()
	a.Admin = admin.With(a.MW.Admin)
	api.Mount("/admin", admin)
	root.Mount("/api/v1", api)

	internal := chi.NewRouter()
	internal.Use(httpx.RequestIDMiddleware(a.Log), httpx.AccessLog, httpx.Recoverer, httpx.SecurityHeaders)
	setJSONFallbacks(internal)
	internal.Get("/healthz", a.handleHealthz)
	// The service middleware is mux-level so it also guards unknown paths (no
	// route enumeration without the secret). With() builds the mux handler
	// right away; an empty chi mux would otherwise answer 404 before running
	// any middleware.
	iv1 := chi.NewRouter()
	setJSONFallbacks(iv1)
	iv1.Use(a.MW.Service)
	a.Internal = iv1.With()
	internal.Mount("/internal/v1", iv1)

	a.root, a.internalRoot = root, internal
}

func setJSONFallbacks(r *chi.Mux) {
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.NotFound())
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.NewError(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"))
	})
}

// rateLimit applies the general per-IP budget to every /api/v1 request and the
// strict one to the Mini App and admin login endpoints.
func rateLimit(general, strict *httpx.RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := httpx.ClientIP(r)
			if (isLoginPath(r.URL.Path) && !strict.Allow(ip)) || !general.Allow(ip) {
				w.Header().Set("Retry-After", "60")
				httpx.WriteError(w, r, httpx.TooManyRequests())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isLoginPath(p string) bool {
	return strings.HasPrefix(p, "/api/v1/auth/") || p == "/api/v1/admin/auth/login"
}

// Run listens on API_PORT and INTERNAL_PORT and serves until ctx is cancelled
// (SIGINT/SIGTERM in main), then shuts down gracefully.
func (a *App) Run(ctx context.Context) error {
	var lc net.ListenConfig
	pub, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", a.Cfg.APIPort))
	if err != nil {
		return fmt.Errorf("listen public: %w", err)
	}
	internal, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", a.Cfg.InternalPort))
	if err != nil {
		_ = pub.Close()
		return fmt.Errorf("listen internal: %w", err)
	}
	return a.Serve(ctx, pub, internal)
}

// Serve runs both servers, the background jobs and workers on the given
// listeners. When ctx is cancelled or any component fails, it stops accepting
// connections, waits up to 20s for in-flight requests, then cancels the
// context of long-lived connections (WebSockets) and returns.
func (a *App) Serve(ctx context.Context, public, internal net.Listener) error {
	if !a.started.CompareAndSwap(false, true) {
		return errors.New("app: already running")
	}
	// Request contexts outlive ctx so that in-flight requests can finish; they
	// are cancelled once the graceful shutdown window has passed.
	connCtx, cancelConns := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelConns()

	pubSrv := a.newServer(a.root, connCtx)
	intSrv := a.newServer(a.internalRoot, connCtx)
	for _, fn := range a.onStop {
		pubSrv.RegisterOnShutdown(fn)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return serveHTTP(pubSrv, public, "public") })
	g.Go(func() error { return serveHTTP(intSrv, internal, "internal") })
	if a.Cfg.JobsEnabled {
		for _, j := range a.jobs {
			g.Go(func() error { a.runJob(gctx, j); return nil })
		}
	}
	for _, w := range a.workers {
		g.Go(func() error { a.runWorker(gctx, w); return nil })
	}
	g.Go(func() error {
		<-gctx.Done()
		a.draining.Store(true)
		a.Log.Info("shutting down", slog.Duration("timeout", shutdownTimeout))
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		errPub := pubSrv.Shutdown(sctx)
		errInt := intSrv.Shutdown(sctx)
		cancelConns()
		if err := errors.Join(errPub, errInt); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	})

	a.Log.Info("api listening",
		slog.String("public", public.Addr().String()),
		slog.String("internal", internal.Addr().String()),
		slog.Int("jobs", len(a.jobs)), slog.Bool("jobs_enabled", a.Cfg.JobsEnabled),
		slog.Int("workers", len(a.workers)))
	err := g.Wait()
	a.Log.Info("api stopped")
	return err
}

func (a *App) newServer(h http.Handler, base context.Context) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(a.Log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return base },
	}
}

func serveHTTP(srv *http.Server, ln net.Listener, name string) error {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s server: %w", name, err)
	}
	return nil
}
