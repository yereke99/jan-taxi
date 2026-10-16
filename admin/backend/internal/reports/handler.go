package reports

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Handler serves the dashboard and the CSV exports.
type Handler struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewHandler builds the reports handler.
func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, now: time.Now}
}

// MountAdmin mounts the routes on the admin router (/api/v1/admin), which
// already runs RequireAdmin; permissions are checked per route.
func (h *Handler) MountAdmin(r chi.Router) {
	r.With(auth.Permit(auth.PermDashboardView)).Get("/dashboard", httpx.H(h.dashboard))
	exports := r.With(auth.Permit(auth.PermReportsExport))
	exports.Get("/reports/orders.csv", httpx.H(h.exportOrders))
	exports.Get("/reports/ledger.csv", httpx.H(h.exportLedger))
	exports.Get("/reports/topups.csv", httpx.H(h.exportTopUps))
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) error {
	cityID, err := httpx.QueryUUID(r, "city_id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	// One snapshot so the numbers agree with each other.
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("reports: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	loc, err := cityLocation(ctx, tx, cityID)
	if err != nil {
		return err
	}
	now := h.now()
	p, err := parsePeriod(r, loc, now, 1, MaxDashboardDays)
	if err != nil {
		return err
	}
	d, err := BuildDashboard(ctx, tx, p, cityID, now)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}
