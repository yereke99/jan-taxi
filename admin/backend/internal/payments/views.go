package payments

import (
	"time"

	"github.com/google/uuid"
)

// TopUpView is the driver-facing top-up (OpenAPI TopUp).
type TopUpView struct {
	ID                     uuid.UUID           `json:"id"`
	Number                 int64               `json:"number"`
	Reference              string              `json:"reference"`
	Amount                 int64               `json:"amount"`
	VerifiedAmount         *int64              `json:"verified_amount"`
	Currency               string              `json:"currency"`
	Status                 string              `json:"status"`
	Provider               string              `json:"provider"`
	Payment                PaymentInstructions `json:"payment"`
	DriverPaymentReference *string             `json:"driver_payment_reference"`
	DriverNote             *string             `json:"driver_note"`
	HasReceipt             bool                `json:"has_receipt"`
	CanUploadReceipt       bool                `json:"can_upload_receipt"`
	CanSubmit              bool                `json:"can_submit"`
	CanCancel              bool                `json:"can_cancel"`
	CreatedAt              time.Time           `json:"created_at"`
	DriverReportedPaidAt   *time.Time          `json:"driver_reported_paid_at"`
	ExpiresAt              time.Time           `json:"expires_at"`
	ConfirmedAt            *time.Time          `json:"confirmed_at"`
	RejectedAt             *time.Time          `json:"rejected_at"`
	RejectedReason         *string             `json:"rejected_reason"`
	RefundedAt             *time.Time          `json:"refunded_at"`
	// Receipt is the check of the latest uploaded receipt (nil without one).
	Receipt *DriverReceiptView `json:"receipt"`
}

func driverTopUpView(t TopUp, pay PaymentInstructions, rc *receiptRow) TopUpView {
	return TopUpView{
		ID:                     t.ID,
		Number:                 t.Number,
		Reference:              t.Reference,
		Amount:                 t.RequestedAmount,
		VerifiedAmount:         t.VerifiedAmount,
		Currency:               t.Currency,
		Status:                 t.Status,
		Provider:               t.Provider,
		Payment:                pay,
		DriverPaymentReference: t.DriverPaymentReference,
		DriverNote:             t.DriverNote,
		HasReceipt:             t.ReceiptFileID != nil,
		CanUploadReceipt:       t.Open(),
		CanSubmit:              t.Status == StatusCreated,
		CanCancel:              t.Status == StatusCreated,
		CreatedAt:              t.CreatedAt.UTC(),
		DriverReportedPaidAt:   utc(t.DriverReportedPaidAt),
		ExpiresAt:              t.ExpiresAt.UTC(),
		ConfirmedAt:            utc(t.ConfirmedAt),
		RejectedAt:             utc(t.RejectedAt),
		RejectedReason:         t.RejectReason,
		RefundedAt:             utc(t.RefundedAt),
		Receipt:                driverReceiptView(t, rc),
	}
}

// WalletView is the driver wallet (OpenAPI Wallet).
type WalletView struct {
	Balance          int64     `json:"balance"`
	Reserved         int64     `json:"reserved"`
	Available        int64     `json:"available"`
	Currency         string    `json:"currency"`
	MinOnlineBalance int64     `json:"min_online_balance"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// LedgerEntryView is a journal row as the driver sees it (OpenAPI LedgerEntry).
type LedgerEntryView struct {
	ID             int64      `json:"id"`
	EntryType      string     `json:"entry_type"`
	BalanceDelta   int64      `json:"balance_delta"`
	ReservedDelta  int64      `json:"reserved_delta"`
	BalanceAfter   int64      `json:"balance_after"`
	ReservedAfter  int64      `json:"reserved_after"`
	Currency       string     `json:"currency"`
	OrderID        *uuid.UUID `json:"order_id"`
	OrderNumber    *int64     `json:"order_number"`
	TopUpID        *uuid.UUID `json:"topup_id"`
	TopUpReference *string    `json:"topup_reference"`
	Description    *string    `json:"description"`
	CreatedAt      time.Time  `json:"created_at"`
}

// DriverRef is a short driver reference for admin views (OpenAPI DriverRef).
type DriverRef struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Phone     *string   `json:"phone"`
}

// AdminRef is a short admin reference (OpenAPI AdminRef).
type AdminRef struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	FullName string    `json:"full_name"`
}

// AdminLedgerEntryView is a journal row for finance admins (OpenAPI AdminLedgerEntry).
type AdminLedgerEntryView struct {
	LedgerEntryView
	WalletID       uuid.UUID  `json:"wallet_id"`
	Driver         DriverRef  `json:"driver"`
	Admin          *AdminRef  `json:"admin"`
	ReservationID  *uuid.UUID `json:"reservation_id"`
	IdempotencyKey string     `json:"idempotency_key"`
}

// AdminTopUpView is a top-up for finance admins (OpenAPI AdminTopUp).
type AdminTopUpView struct {
	ID                     uuid.UUID  `json:"id"`
	Number                 int64      `json:"number"`
	Reference              string     `json:"reference"`
	Status                 string     `json:"status"`
	Provider               string     `json:"provider"`
	Amount                 int64      `json:"amount"`
	VerifiedAmount         *int64     `json:"verified_amount"`
	Currency               string     `json:"currency"`
	Driver                 DriverRef  `json:"driver"`
	PaymentURL             *string    `json:"payment_url"`
	DriverPaymentReference *string    `json:"driver_payment_reference"`
	DriverNote             *string    `json:"driver_note"`
	HasReceipt             bool       `json:"has_receipt"`
	ReceiptURL             *string    `json:"receipt_url"`
	DriverReportedPaidAt   *time.Time `json:"driver_reported_paid_at"`
	ProviderTransactionID  *string    `json:"provider_transaction_id"`
	PaidAt                 *time.Time `json:"paid_at"`
	ConfirmedBy            *AdminRef  `json:"confirmed_by"`
	ConfirmedAutomatically bool       `json:"confirmed_automatically"`
	ConfirmedAt            *time.Time `json:"confirmed_at"`
	RejectedBy             *AdminRef  `json:"rejected_by"`
	RejectedAt             *time.Time `json:"rejected_at"`
	RejectedReason         *string    `json:"rejected_reason"`
	RefundedBy             *AdminRef  `json:"refunded_by"`
	RefundedAt             *time.Time `json:"refunded_at"`
	RefundReason           *string    `json:"refund_reason"`
	ExpiresAt              time.Time  `json:"expires_at"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// PaymentTransactionView is a recorded payment (OpenAPI PaymentTransaction).
type PaymentTransactionView struct {
	ID                    uuid.UUID  `json:"id"`
	Provider              string     `json:"provider"`
	ProviderTransactionID string     `json:"provider_transaction_id"`
	Amount                int64      `json:"amount"`
	Currency              string     `json:"currency"`
	PaidAt                *time.Time `json:"paid_at"`
	PayerInfo             *string    `json:"payer_info"`
	Source                string     `json:"source"`
	RecordedBy            *AdminRef  `json:"recorded_by"`
	CreatedAt             time.Time  `json:"created_at"`
}

// DuplicateWarning flags evidence that the same real payment may be claimed
// twice: another top-up with the same driver-entered payment reference, or a
// recorded payment transaction whose provider id equals that reference.
type DuplicateWarning struct {
	Kind                 string     `json:"kind"` // same_driver_payment_reference | provider_transaction_recorded | receipt_serial_used
	TopUpID              *uuid.UUID `json:"topup_id"`
	Reference            *string    `json:"reference"`
	Status               *string    `json:"status"`
	DriverCode           *string    `json:"driver_code"`
	PaymentTransactionID *uuid.UUID `json:"payment_transaction_id"`
	ProviderTransaction  *string    `json:"provider_transaction_id"`
}

// Duplicate warning kinds.
const (
	WarnSameDriverReference = "same_driver_payment_reference"
	WarnTransactionRecorded = "provider_transaction_recorded"
	WarnReceiptSerialUsed   = "receipt_serial_used"
)

// AdminTopUpDetail adds the history, the recorded payment, duplicate
// warnings and the parsed receipts to AdminTopUpView.
type AdminTopUpDetail struct {
	AdminTopUpView
	Events             []TopUpEvent            `json:"events"`
	PaymentTransaction *PaymentTransactionView `json:"payment_transaction"`
	DuplicateWarnings  []DuplicateWarning      `json:"duplicate_warnings"`
	// Receipt is the latest parsed PDF receipt, Receipts every one (newest
	// first), ConfirmPrefill the confirmation form values taken from Receipt.
	Receipt        *AdminReceiptView  `json:"receipt"`
	Receipts       []AdminReceiptView `json:"receipts"`
	ConfirmPrefill *ConfirmPrefill    `json:"confirm_prefill"`
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
