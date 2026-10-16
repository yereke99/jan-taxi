package payments

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
)

// Options configure the Service.
type Options struct {
	// KaspiPaymentURL and KaspiRecipientName are the KASPI_PAY_* environment
	// values, used when the settings provide none.
	KaspiPaymentURL, KaspiRecipientName string
	// MaxUploadBytes bounds receipt images (MAX_UPLOAD_BYTES).
	MaxUploadBytes int64
	Log            *slog.Logger
}

// Service serves the wallet, top-up and finance endpoints.
type Service struct {
	pool      *pgxpool.Pool
	store     *storage.Store
	kaspi     *KaspiManual
	providers map[string]Provider
	maxUpload int64
	log       *slog.Logger
}

// NewService builds the service with the kaspi_manual provider plus any extra
// providers (a provider with the same name replaces the built-in one).
func NewService(pool *pgxpool.Pool, store *storage.Store, opt Options, extra ...Provider) *Service {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.MaxUploadBytes <= 0 {
		opt.MaxUploadBytes = storage.DefaultMaxBytes
	}
	k := NewKaspiManual(pool, opt.KaspiPaymentURL, opt.KaspiRecipientName)
	s := &Service{
		pool:      pool,
		store:     store,
		kaspi:     k,
		providers: map[string]Provider{k.Name(): k},
		maxUpload: opt.MaxUploadBytes,
		log:       opt.Log,
	}
	for _, p := range extra {
		if p != nil {
			s.providers[p.Name()] = p
		}
	}
	return s
}

// provider returns the provider a top-up was created with, falling back to
// kaspi_manual for unknown names so old rows still render.
func (s *Service) provider(name string) Provider {
	if p, ok := s.providers[name]; ok {
		return p
	}
	return s.kaspi
}

// MountDriver mounts the driver wallet and top-up endpoints on the driver-app
// router (/api/v1).
func (s *Service) MountDriver(r chi.Router) {
	r.Get("/driver/wallet", httpx.H(s.getWallet))
	r.Get("/driver/wallet/transactions", httpx.H(s.listTransactions))
	r.Post("/driver/wallet/topups", httpx.H(s.createTopUp))
	r.Get("/driver/wallet/topups", httpx.H(s.listDriverTopUps))
	r.Get("/driver/wallet/topups/{id}", httpx.H(s.getDriverTopUp))
	r.Post("/driver/wallet/topups/{id}/receipt", httpx.H(s.uploadReceipt))
	r.Post("/driver/wallet/topups/{id}/submit", httpx.H(s.submitTopUp))
	r.Post("/driver/wallet/topups/{id}/cancel", httpx.H(s.cancelTopUp))
}

// MountAdmin mounts the finance endpoints on the admin router (/api/v1/admin),
// which already runs RequireAdmin; permissions are checked per route.
func (s *Service) MountAdmin(r chi.Router) {
	view := r.With(auth.Permit(auth.PermFinanceView))
	manage := r.With(auth.Permit(auth.PermFinanceManage))

	view.Get("/topups", httpx.H(s.adminListTopUps))
	view.Get("/topups/{id}", httpx.H(s.adminGetTopUp))
	view.Get("/topups/{id}/receipt", httpx.H(s.adminGetReceipt))
	manage.Post("/topups/{id}/confirm", httpx.H(s.adminConfirmTopUp))
	manage.Post("/topups/{id}/reject", httpx.H(s.adminRejectTopUp))
	manage.Post("/topups/{id}/refund", httpx.H(s.adminRefundTopUp))

	view.Get("/ledger", httpx.H(s.adminListLedger))
	view.Get("/drivers/{id}/ledger", httpx.H(s.adminDriverLedger))
	manage.Post("/drivers/{id}/wallet/adjustments", httpx.H(s.adminAdjustWallet))
	view.Get("/wallets/reconciliation", httpx.H(s.adminReconcile))
}

// MountPublic mounts the provider webhook (/api/v1/payments/webhooks/{provider}).
func (s *Service) MountPublic(r chi.Router) {
	r.Post("/payments/webhooks/{provider}", httpx.H(s.webhook))
}
