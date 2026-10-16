// Package orders implements the order lifecycle API (ARCHITECTURE §5, §19):
// passenger orders, the driver's trip actions, ratings, disputes and the
// operator endpoints. State changes go through orders/lifecycle; driver
// selection and acceptance through dispatch.
package orders

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Service serves the order endpoints.
type Service struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// New builds the service.
func New(pool *pgxpool.Pool, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{pool: pool, log: log}
}

// MountPassenger mounts the passenger routes on a router carrying the
// passenger app-token middleware. Routes are registered individually so other
// packages can add sub-paths such as /orders/{id}/driver-photo.
func (s *Service) MountPassenger(r chi.Router) {
	r.Post("/orders", httpx.H(s.createOrder))
	r.Get("/orders", httpx.H(s.listOrders))
	r.Get("/orders/{id}", httpx.H(s.getOrder))
	r.Post("/orders/{id}/cancel", httpx.H(s.cancelOrder))
	r.Post("/orders/{id}/rating", httpx.H(s.rateOrder))
	r.Post("/orders/{id}/dispute", httpx.H(s.disputeOrder))
}

// MountDriver mounts the driver routes on a router carrying the driver
// app-token middleware and the optional driver-profile loader.
func (s *Service) MountDriver(r chi.Router) {
	r.Get("/driver/orders", httpx.H(s.listAvailable))
	r.Get("/driver/orders/active", httpx.H(s.getActiveDriverOrder))
	r.Get("/driver/orders/history", httpx.H(s.listDriverHistory))
	r.Get("/driver/orders/{id}", httpx.H(s.getDriverOrder))
	r.Post("/driver/orders/{id}/accept", httpx.H(s.acceptOrder))
	r.Post("/driver/orders/{id}/arriving", httpx.H(s.markArriving))
	r.Post("/driver/orders/{id}/start", httpx.H(s.startTrip))
	r.Post("/driver/orders/{id}/stops/{stopId}/complete", httpx.H(s.completeStop))
	r.Post("/driver/orders/{id}/complete", httpx.H(s.completeOrder))
	r.Post("/driver/orders/{id}/cancel", httpx.H(s.cancelByDriver))
}

// MountAdmin mounts the operator routes on the admin router (session + CSRF
// already enforced); each route checks its permission.
func (s *Service) MountAdmin(r chi.Router) {
	view := r.With(auth.Permit(auth.PermOrdersView))
	manage := r.With(auth.Permit(auth.PermOrdersManage))
	view.Get("/orders", httpx.H(s.adminListOrders))
	view.Get("/orders/{id}", httpx.H(s.adminGetOrder))
	manage.Post("/orders/{id}/cancel", httpx.H(s.adminCancelOrder))
	manage.Post("/orders/{id}/assign", httpx.H(s.adminAssignOrder))
	manage.Post("/orders/{id}/dispute", httpx.H(s.adminOpenDispute))
	view.Get("/disputes", httpx.H(s.adminListDisputes))
	manage.Post("/disputes/{id}/resolve", httpx.H(s.adminResolveDispute))
	r.With(auth.Permit(auth.PermDriversView)).Get("/drivers/{id}/orders", httpx.H(s.adminListDriverOrders))
}

// tx runs fn in a transaction and translates concurrency errors.
func (s *Service) tx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return lifecycle.DBError(db.WithTx(ctx, s.pool, fn))
}

// approvedDriver returns the caller's driver profile and requires it to be
// approved (actions on orders).
func approvedDriver(r *http.Request) (auth.DriverContext, error) {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return auth.DriverContext{}, err
	}
	if d.Status != "approved" {
		return auth.DriverContext{}, lifecycle.ErrDriverNotApproved()
	}
	return d, nil
}
