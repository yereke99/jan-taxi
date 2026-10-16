package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

type readinessCheck struct {
	name string
	fn   func(ctx context.Context) error
}

// handleHealthz is the liveness probe: the process is up and serving HTTP.
func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz is the readiness probe: the database answers, its schema is at
// the version embedded in this binary, registered checks pass, and the process
// is not shutting down. Failure details are logged, not returned.
func (a *App) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if a.draining.Load() {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "draining"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]string{}
	ready := true
	fail := func(name, state string, err error) {
		ready = false
		checks[name] = state
		httpx.Logger(r.Context()).Warn("readiness check failed", slog.String("check", name), slog.Any("err", err))
	}

	version, err := a.schemaVersion(ctx)
	switch {
	case err != nil:
		fail("database", "unavailable", err)
	case version != a.latestMigration:
		checks["database"] = "ok"
		fail("migrations", "pending", nil)
	default:
		checks["database"] = "ok"
		checks["migrations"] = "ok"
	}
	for _, c := range a.checks {
		if err := c.fn(ctx); err != nil {
			fail(c.name, "failing", err)
		} else {
			checks[c.name] = "ok"
		}
	}

	status, code := "ready", http.StatusOK
	if !ready {
		status, code = "not_ready", http.StatusServiceUnavailable
	}
	httpx.WriteJSON(w, code, map[string]any{"status": status, "checks": checks, "schema_version": version})
}

// schemaVersion reads the applied goose version directly: goose keeps global
// state, so calling it from concurrent probes would race.
func (a *App) schemaVersion(ctx context.Context) (int64, error) {
	var v *int64
	err := a.Pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&v)
	if err != nil {
		return 0, err
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}
