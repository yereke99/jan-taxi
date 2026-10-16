package payments

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// invalidTransition is 409 invalid_transition with details {from, action}.
func invalidTransition(from, action string) *httpx.Error {
	return httpx.Conflict("invalid_transition", "transition not allowed").
		WithDetails(map[string]any{"from": from, "action": action})
}

// errPaymentReferenceUsed is 409 payment_reference_used; details name the
// top-up that already holds the transaction when there is one.
func errPaymentReferenceUsed(existingTopUp *uuid.UUID, existingReference *string) *httpx.Error {
	e := httpx.Conflict("payment_reference_used", "provider transaction already recorded")
	details := map[string]any{}
	if existingTopUp != nil {
		details["topup_id"] = existingTopUp.String()
	}
	if existingReference != nil {
		details["reference"] = *existingReference
	}
	if len(details) > 0 {
		e = e.WithDetails(details)
	}
	return e
}

// walletError maps wallet errors to API errors; insufficient balance carries
// the §10 details {required, available, currency, topup_min_amount}.
func walletError(ctx context.Context, q db.Querier, driverID uuid.UUID, required int64, err error) error {
	if !errors.Is(err, wallets.ErrInsufficientBalance) {
		return wallets.HTTPError(err)
	}
	var available int64
	if w, werr := wallets.Get(ctx, q, driverID); werr == nil {
		available = w.Available()
	}
	return httpx.NewError(http.StatusPaymentRequired, "insufficient_balance", "insufficient wallet balance").
		WithDetails(map[string]any{
			"required":         required,
			"available":        available,
			"currency":         Currency,
			"topup_min_amount": settings.TopUp(ctx, q).Min,
		}).WithCause(err)
}

// errPaymentNotConfigured is returned when no Kaspi link or recipient is set,
// so a driver could not be told where to pay.
var errPaymentNotConfigured = httpx.Conflict("conflict", "payment details are not configured").
	WithDetails(map[string]any{"reason": "payment_not_configured"})
