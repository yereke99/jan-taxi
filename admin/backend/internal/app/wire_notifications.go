package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// notificationLockRetry is how often a replica without the worker lock checks
// whether the active replica has gone away.
const notificationLockRetry = 5 * time.Second

// wireNotifications mounts the admin outbox/template endpoints and, when
// NOTIFICATION_WORKER_ENABLED, starts the outbox worker.
func (a *App) wireNotifications() error {
	// /api/v1/admin/notifications*, /api/v1/admin/notification-templates*
	notifications.NewAdmin(a.Pool).MountAdmin(a.Admin)

	// Finished outbox rows (and their attempts) are kept for 30 days whether or
	// not this replica sends; the job runs under JOBS_ENABLED like the others.
	pool, log := a.Pool, a.Log
	a.AddJob("notification_retention", notifications.OutboxRetentionInterval, func(ctx context.Context) error {
		n, err := notifications.PruneOutbox(ctx, pool, notifications.OutboxRetention)
		if n > 0 {
			log.Info("finished notifications pruned", slog.Int64("rows", n))
		}
		return err
	})

	if !a.Cfg.NotificationWorkerEnabled {
		a.Log.Info("notification worker disabled")
		return nil
	}
	passenger, err := a.telegramClient("passenger", a.Cfg.PassengerBotToken)
	if err != nil {
		return err
	}
	driver, err := a.telegramClient("driver", a.Cfg.DriverBotToken)
	if err != nil {
		return err
	}
	worker := notifications.NewWorker(a.Pool, notifications.NewTelegramSender(passenger, driver), notifications.Options{
		PassengerMiniAppURL: a.Cfg.PassengerMiniAppURL,
		DriverMiniAppURL:    a.Cfg.DriverMiniAppURL,
		Log:                 a.Log,
		// Driver photos in the passenger's "driver found" message (§22.6).
		Photos: notifications.NewFilePhotos(a.Pool, a.Store),
	})

	// One replica drains the outbox at a time (advisory lock), so the in-process
	// per-bot rate limit is also the global one. The worker itself is safe to run
	// concurrently (SKIP LOCKED + lease), so a brief overlap after a lost lock
	// connection cannot double-send.
	a.AddWorker("notification_worker", func(ctx context.Context) error {
		for {
			ran, err := db.RunExclusive(ctx, a.Pool, "notification_worker", worker.Run)
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				return err
			}
			if !ran {
				t := time.NewTimer(notificationLockRetry)
				select {
				case <-ctx.Done():
					t.Stop()
					return nil
				case <-t.C:
				}
			}
		}
	})
	return nil
}

// telegramClient builds a send-only Bot API client. A missing token is fatal in
// production; elsewhere that bot's notifications are marked dead with a clear
// error instead of being sent.
func (a *App) telegramClient(bot, token string) (*telegram.Client, error) {
	if token == "" {
		if a.Cfg.IsProduction() {
			return nil, fmt.Errorf("%s bot token is required for notifications in production", bot)
		}
		a.Log.Warn("bot token not set; its notifications will not be delivered", slog.String("bot", bot))
		return nil, nil
	}
	return telegram.New(token,
		telegram.WithBaseURL(a.Cfg.TelegramAPIBaseURL),
		telegram.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		telegram.WithLogger(a.Log.With(slog.String("component", "telegram"), slog.String("bot", bot))),
	), nil
}
