package app

import (
	"context"
	"log/slog"

	"github.com/yereke99/jantaxi/admin/backend/internal/botapi"
)

// wireBotAPI constructs the internal bot API services and mounts their routes.
// It runs after wireFinance: receipts sent to the driver bot go to payments.
func (a *App) wireBotAPI() error {
	var receipts botapi.ReceiptIntake
	if a.Payments != nil {
		receipts = a.Payments
	}
	h, err := botapi.New(botapi.Options{
		Receipts:             receipts,
		Pool:                 a.Pool,
		Log:                  a.Log,
		PassengerMiniAppURL:  a.Cfg.PassengerMiniAppURL,
		DriverMiniAppURL:     a.Cfg.DriverMiniAppURL,
		PassengerBotUsername: a.Cfg.PassengerBotUsername,
		DriverBotUsername:    a.Cfg.DriverBotUsername,
		DefaultLanguage:      a.Cfg.DefaultLanguage,
		MaxReceiptBytes:      a.Cfg.MaxUploadBytes,
	})
	if err != nil {
		return err
	}
	// /internal/v1/{bots/{bot}/...,passenger/{tg_id}/orders,driver/{tg_id}/...,config}
	h.Mount(a.Internal)

	pool, log := a.Pool, a.Log
	a.AddJob("bot_update_cleanup", botapi.CleanupInterval, func(ctx context.Context) error {
		n, err := botapi.CleanupProcessedUpdates(ctx, pool, botapi.ProcessedUpdateRetention)
		if n > 0 {
			log.Info("processed bot updates pruned", slog.Int64("rows", n))
		}
		return err
	})
	return nil
}
