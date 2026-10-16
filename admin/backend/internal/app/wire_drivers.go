package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/moderation"
)

// Driver housekeeping intervals (ARCHITECTURE §16).
const (
	staleDriverInterval       = 5 * time.Minute
	locationRetentionInterval = 24 * time.Hour
)

// wireDrivers constructs the drivers, vehicles and moderation services and mounts their routes.
func (a *App) wireDrivers() error {
	dh := drivers.NewHandler(a.Pool, a.Store, drivers.Options{
		DefaultCity:    a.Cfg.DefaultCity,
		MaxUploadBytes: a.Cfg.MaxUploadBytes,
	})
	// /api/v1/driver/{registration,registration/files,registration/check,files,profile,availability,location}
	dh.MountDriver(a.Driver)
	// /api/v1/orders/{id}/driver-photo
	dh.MountPassenger(a.Passenger)
	// /api/v1/admin/files/{id}, /api/v1/admin/drivers/locations
	dh.MountAdmin(a.Admin)

	mh := moderation.NewHandler(a.Pool)
	// /api/v1/driver/register
	mh.MountDriver(a.Driver)
	// /api/v1/admin/drivers, /api/v1/admin/drivers/{id}[/approve|/reject|/request-corrections|/suspend|/restore]
	mh.MountAdmin(a.Admin)

	a.AddJob("stale_driver_offline", staleDriverInterval, func(ctx context.Context) error {
		n, err := drivers.SetStaleDriversOffline(ctx, a.Pool, drivers.IdleTimeout(ctx, a.Pool, a.Cfg.DriverOnlineIdleTimeout))
		if n > 0 {
			a.Log.Info("stale drivers set offline", slog.Int("count", n))
		}
		return err
	})
	a.AddJob("location_history_retention", locationRetentionInterval, func(ctx context.Context) error {
		n, err := drivers.PruneLocationHistory(ctx, a.Pool, drivers.LocationHistoryRetention)
		if n > 0 {
			a.Log.Info("location history pruned", slog.Int64("rows", n))
		}
		return err
	})
	return nil
}
