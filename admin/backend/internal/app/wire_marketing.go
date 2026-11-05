package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/marketing"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Marketing job intervals (ARCHITECTURE §16, §23).
const (
	marketingEventsEvery   = 2 * time.Second
	marketingExpiryEvery   = time.Minute
	marketingScheduleEvery = 30 * time.Second
	marketingGrantsEvery   = 5 * time.Second
	marketingReconcile     = time.Hour
)

// wireMarketing builds the marketing service (§23): passenger, admin and bot
// routes, the referral hook of passenger sign-in, the award, expiry,
// schedule, credit and reconciliation jobs, and — with the notification
// worker — the broadcast sender.
func (a *App) wireMarketing() error {
	svc := marketing.New(a.Pool, marketing.Options{PassengerBotUsername: a.Cfg.PassengerBotUsername, Log: a.Log})
	// /api/v1/promotions*
	svc.MountPassenger(a.Passenger)
	svc.MountDriver(a.Driver)
	// /api/v1/admin/marketing/*
	svc.MountAdmin(a.Admin)
	// /internal/v1/passenger/{tg_id}/referral, /internal/v1/bots/{bot}/users/{tg_id}/marketing
	svc.MountInternal(a.Internal)
	// startapp=ref_<code> inside the signed initData of a passenger sign-in.
	a.Auth.OnPassengerStart(svc.HandleStartParam)

	log := a.Log
	a.AddJob("marketing_events", marketingEventsEvery, func(ctx context.Context) error {
		_, err := svc.ProcessEvents(ctx)
		return err
	})
	a.AddJob("marketing_coupon_expiry", marketingExpiryEvery, func(ctx context.Context) error {
		n, err := svc.ExpireCoupons(ctx)
		if n > 0 {
			log.Info("coupons expired", slog.Int64("count", n))
		}
		return err
	})
	a.AddJob("marketing_campaign_schedule", marketingScheduleEvery, svc.ActivateDue)
	a.AddJob("marketing_grants", marketingGrantsEvery, func(ctx context.Context) error {
		_, err := svc.CreditPending(ctx)
		return err
	})
	a.AddJob("marketing_reconcile", marketingReconcile, svc.ReconcileJob)
	a.AddJob("marketing_presence_cleanup", time.Hour, func(ctx context.Context) error {
		_, err := a.Pool.Exec(ctx, `DELETE FROM marketing_app_presence WHERE seen_at<now()-interval '7 days'`)
		return err
	})

	if !a.Cfg.NotificationWorkerEnabled {
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
	worker := marketing.NewBroadcastWorker(a.Pool, notifications.NewTelegramSender(passenger, driver),
		marketing.WorkerOptions{PassengerMiniAppURL: a.Cfg.PassengerMiniAppURL, DriverMiniAppURL: a.Cfg.DriverMiniAppURL,
			PassengerBotUsername: a.Cfg.PassengerBotUsername, DriverBotUsername: a.Cfg.DriverBotUsername,
			PassengerMiniAppShortName: a.Cfg.PassengerMiniAppShortName, DriverMiniAppShortName: a.Cfg.DriverMiniAppShortName,
			Log: a.Log})
	// One replica sends at a time, so the per-bot rate is global.
	a.AddWorker("broadcast_worker", func(ctx context.Context) error {
		for {
			ran, err := db.RunExclusive(ctx, a.Pool, "broadcast_worker", worker.Run)
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
