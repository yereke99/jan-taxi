// Package payments implements driver wallet top-ups (ARCHITECTURE §6.1): the
// payment provider abstraction, the driver wallet and top-up endpoints, the
// finance admin verification flow, wallet ledger views and adjustments, and the
// top-up expiry job.
//
// Money only reaches a wallet when a finance admin confirms a top-up with the
// amount and transaction number taken from trusted merchant records. A driver's
// "I paid" and uploaded receipts are evidence for that review and never credit
// anything by themselves.
package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// ProviderKaspiManual is the only provider today: the driver pays through Kaspi
// and a finance admin verifies the payment in Kaspi Pay.
const ProviderKaspiManual = "kaspi_manual"

// PaymentInstructions tell the driver how to pay a top-up (OpenAPI
// TopUpPaymentInstructions); Instructions is rendered in the driver's language.
type PaymentInstructions struct {
	Method         string  `json:"method"`
	URL            *string `json:"url"`
	RecipientName  string  `json:"recipient_name"`
	RecipientPhone *string `json:"recipient_phone"`
	Instructions   string  `json:"instructions"`
	Reference      string  `json:"reference"`
	Amount         int64   `json:"amount"`
	Currency       string  `json:"currency"`
}

// ProviderPayment is a payment reported by a provider callback after its
// signature has been verified.
type ProviderPayment struct {
	ProviderTransactionID string
	Amount                int64
	PaidAt                *time.Time
	PayerInfo             string
	// Reference is the top-up reference quoted by the payer, when the provider
	// passes it through.
	Reference string
	Raw       json.RawMessage
}

// Provider is a way for drivers to pay top-ups.
type Provider interface {
	// Name is the provider id stored in top_up_requests.provider.
	Name() string
	// Instructions renders how to pay t, in lang (kk, ru or en).
	Instructions(ctx context.Context, t TopUp, lang string) PaymentInstructions
	// WebhookEnabled reports whether the provider sends verifiable callbacks.
	// While false, POST /payments/webhooks/{provider} answers 404.
	WebhookEnabled() bool
	// VerifyWebhook authenticates a callback and extracts the payment.
	VerifyWebhook(r *http.Request) (*ProviderPayment, error)
}

// ErrWebhookUnsupported is returned by providers without callbacks.
var ErrWebhookUnsupported = errors.New("payments: provider has no webhook")
