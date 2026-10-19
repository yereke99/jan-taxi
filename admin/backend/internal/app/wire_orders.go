package app

import (
	"context"
	"log/slog"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
)

// wireOrders constructs the orders, dispatch and ratings services and mounts their routes.
func (a *App) wireOrders() error {
	svc := orders.New(a.Pool, a.Log)
	svc.SetArrivalETA(a.ETA)
	// /api/v1/orders*, passenger token
	svc.MountPassenger(a.Passenger)
	// /api/v1/driver/orders*, driver token (+ profile)
	svc.MountDriver(a.Driver)
	// /api/v1/admin/{orders,disputes,drivers/{id}/orders}, per-route permissions
	svc.MountAdmin(a.Admin)

	pool, log := a.Pool, a.Log
	a.AddJob("dispatch_expand", dispatch.ExpandInterval, func(ctx context.Context) error {
		n, err := dispatch.ExpandOffers(ctx, pool)
		if n > 0 {
			log.Info("orders offered to more drivers", slog.Int("offers", n))
		}
		return err
	})
	a.AddJob("search_expiry", dispatch.SearchExpiryInterval, func(ctx context.Context) error {
		n, err := dispatch.ExpireSearches(ctx, pool)
		if n > 0 {
			log.Info("searches expired", slog.Int("orders", n))
		}
		return err
	})
	return nil
}
