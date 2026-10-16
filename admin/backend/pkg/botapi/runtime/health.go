package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"time"
)

// HealthWindow is how stale the last successful poll (leader) or lease
// attempt (standby) may be before the bot reports itself unhealthy (§13).
const HealthWindow = 90 * time.Second

// Roles reported by the health endpoint.
const (
	RoleLeader  = "leader"
	RoleStandby = "standby"
)

// Health is the poller's liveness.
type Health struct {
	Role         string   `json:"role"`
	Healthy      bool     `json:"healthy"`
	LastPollAgeS *float64 `json:"last_poll_age_s"`
	LastUpdateID int64    `json:"last_update_id"`
	InstanceID   string   `json:"instance_id"`
}

// Health reports the poller state. A leader is healthy while its last
// successful poll (or processed update) is younger than HealthWindow; a
// standby while its lease loop keeps running.
func (p *Poller) Health() Health {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	h := Health{Role: RoleStandby, LastUpdateID: p.lastProcessed, InstanceID: p.cfg.InstanceID}
	if !p.lastPoll.IsZero() {
		age := math.Round(now.Sub(p.lastPoll).Seconds()*10) / 10
		h.LastPollAgeS = &age
	}
	if !p.running.Load() {
		return h
	}
	if p.leader {
		h.Role = RoleLeader
		ref := p.lastProgress
		if ref.Before(p.leaderSince) {
			ref = p.leaderSince
		}
		h.Healthy = now.Sub(ref) < HealthWindow
		return h
	}
	ref := p.lastLeaseTry
	if ref.IsZero() {
		ref = p.started
	}
	h.Healthy = now.Sub(ref) < HealthWindow
	return h
}

// HealthSource is anything that reports Health.
type HealthSource interface{ Health() Health }

// HealthHandler serves GET /healthz: 200 when healthy, 503 otherwise, with
// the Health JSON as the body.
func HealthHandler(src HealthSource) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		h := src.Health()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if h.Healthy {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(h)
	})
	return mux
}

// ServeHealth serves the health endpoint on addr until ctx ends.
func ServeHealth(ctx context.Context, addr string, src HealthSource, log *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("health listener: %w", err)
	}
	return ServeHealthOn(ctx, ln, src, log)
}

// ServeHealthOn serves the health endpoint on an existing listener until ctx ends.
func ServeHealthOn(ctx context.Context, ln net.Listener, src HealthSource, log *slog.Logger) error {
	srv := &http.Server{Handler: HealthHandler(src), ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && log != nil {
		log.Warn("health server shutdown", slog.String("err", err.Error()))
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// CheckHealth GETs url and succeeds only on HTTP 200 (Docker HEALTHCHECK).
func CheckHealth(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: status %d", resp.StatusCode)
	}
	return nil
}
