package payments

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
)

var providerNameRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// webhook accepts a provider callback. Unknown providers and providers without
// verifiable callbacks (kaspi_manual) get 404. A verified callback is recorded
// as an unmatched provider payment for finance review; it never credits a
// wallet by itself — Confirm links it when an admin confirms the top-up.
func (s *Service) webhook(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "provider")
	p, ok := s.providers[name]
	if !providerNameRe.MatchString(name) || !ok || !p.WebhookEnabled() {
		return httpx.NotFound()
	}
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxJSONBody)
	pay, err := p.VerifyWebhook(r)
	if err != nil {
		httpx.Logger(r.Context()).Warn("payment webhook rejected", slog.String("provider", name), slog.Any("err", err))
		return httpx.Unauthorized()
	}
	if err := s.RecordProviderPayment(r.Context(), name, *pay); err != nil {
		return err
	}
	return httpx.OK(w, map[string]bool{"received": true})
}

// RecordProviderPayment stores a verified provider payment once (idempotent by
// provider transaction id) without linking or crediting it, and tells admin
// screens to refresh.
func (s *Service) RecordProviderPayment(ctx context.Context, provider string, p ProviderPayment) error {
	txn := normalizeTransactionID(p.ProviderTransactionID)
	if txn == "" || len(txn) > 100 || p.Amount <= 0 || p.Amount > MaxVerifiedAmount {
		return httpx.BadRequest("invalid provider payment")
	}
	var paidAt *time.Time
	if p.PaidAt != nil {
		t := p.PaidAt.UTC()
		paidAt = &t
	}
	var payer *string
	if p.PayerInfo != "" {
		v := []rune(p.PayerInfo)
		if len(v) > 200 {
			v = v[:200]
		}
		info := string(v)
		payer = &info
	}
	var raw any
	if len(p.Raw) > 0 {
		raw = p.Raw
	}
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO payment_transactions (provider, provider_transaction_id, amount, currency, paid_at, payer_info, source, raw)
			VALUES ($1, $2, $3, 'KZT', $4, $5, 'webhook', $6)
			ON CONFLICT (provider, provider_transaction_id) DO NOTHING`, provider, txn, p.Amount, paidAt, payer, raw)
		if err != nil {
			return fmt.Errorf("payments: record provider payment: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return realtime.Publish(ctx, tx, realtime.Event{Type: realtime.TopUpUpdated, Scopes: []string{realtime.AdminScope},
			Data: map[string]any{"provider": provider, "provider_transaction_id": txn, "reference": p.Reference}})
	})
}
