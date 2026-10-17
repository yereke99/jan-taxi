package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/payments"
	"github.com/yereke99/jantaxi/admin/backend/internal/reports"
)

// topUpExpiryEvery is the topup_expiry job interval (ARCHITECTURE §16).
const topUpExpiryEvery = 5 * time.Minute

// wireFinance constructs the wallets, payments and reports services and mounts their routes.
func (a *App) wireFinance() error {
	pay := payments.NewService(a.Pool, a.Store, payments.Options{
		KaspiPaymentURL:    a.Cfg.KaspiPayPaymentURL,
		KaspiRecipientName: a.Cfg.KaspiPayRecipientName,
		MaxUploadBytes:     a.Cfg.MaxUploadBytes,
		Log:                a.Log.With(slog.String("component", "payments")),
	})
	a.Payments = pay
	// /api/v1/driver/wallet*, /api/v1/driver/wallet/topups*
	pay.MountDriver(a.Driver)
	// /api/v1/admin/{topups*,ledger,drivers/{id}/ledger,drivers/{id}/wallet/adjustments,wallets/reconciliation}
	pay.MountAdmin(a.Admin)
	// /api/v1/payments/webhooks/{provider}
	pay.MountPublic(a.Public)

	// /api/v1/admin/dashboard, /api/v1/admin/reports/*.csv
	reports.NewHandler(a.Pool).MountAdmin(a.Admin)

	a.AddJob("topup_expiry", topUpExpiryEvery, func(ctx context.Context) error {
		_, err := pay.ExpireTopUps(ctx)
		return err
	})
	return nil
}
