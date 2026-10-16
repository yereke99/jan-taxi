package payments

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Currency is the only currency.
const Currency = "KZT"

// Top-up statuses (top_up_requests.status).
const (
	StatusCreated             = "created"
	StatusPendingVerification = "pending_verification"
	StatusConfirmed           = "confirmed"
	StatusRejected            = "rejected"
	StatusExpired             = "expired"
	StatusRefunded            = "refunded"
)

// Statuses lists every top-up status, for filter validation.
var Statuses = []string{StatusCreated, StatusPendingVerification, StatusConfirmed, StatusRejected, StatusExpired, StatusRefunded}

// ValidStatus reports whether s is a known top-up status.
func ValidStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}
	return false
}

// Event actor types (top_up_events.actor_type).
const (
	ActorDriver   = "driver"
	ActorAdmin    = "admin"
	ActorSystem   = "system"
	ActorProvider = "provider"
)

// Event notes that clients and reports rely on.
const (
	NoteCancelledByDriver = "cancelled_by_driver"
	NoteExpired           = "expired"
	NoteReceiptUploaded   = "receipt_uploaded"
)

// TopUp is a top_up_requests row plus the owning driver's user id and code.
type TopUp struct {
	ID                     uuid.UUID
	Number                 int64
	Reference              string
	DriverID               uuid.UUID
	WalletID               uuid.UUID
	DriverUserID           uuid.UUID
	DriverCode             string
	RequestedAmount        int64
	Currency               string
	Provider               string
	Status                 string
	PaymentURL             *string
	DriverPaymentReference *string
	DriverNote             *string
	ReceiptFileID          *uuid.UUID
	DriverReportedPaidAt   *time.Time
	VerifiedAmount         *int64
	ProviderTransactionID  *string
	PaidAt                 *time.Time
	ConfirmedBy            *uuid.UUID
	ConfirmedAt            *time.Time
	RejectedBy             *uuid.UUID
	RejectedAt             *time.Time
	RejectReason           *string
	RefundedBy             *uuid.UUID
	RefundedAt             *time.Time
	RefundReason           *string
	IdempotencyKey         string
	ExpiresAt              time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// Open reports whether the top-up still waits for payment or verification.
func (t TopUp) Open() bool {
	return t.Status == StatusCreated || t.Status == StatusPendingVerification
}

const topUpColumns = `t.id, t.number, t.reference, t.driver_id, t.wallet_id, d.user_id, d.code,
	t.requested_amount, t.currency, t.provider, t.status, t.payment_url, t.driver_payment_reference,
	t.driver_note, t.receipt_file_id, t.driver_reported_paid_at, t.verified_amount,
	t.provider_transaction_id, t.paid_at, t.confirmed_by, t.confirmed_at, t.rejected_by, t.rejected_at,
	t.reject_reason, t.refunded_by, t.refunded_at, t.refund_reason, t.idempotency_key, t.expires_at,
	t.created_at, t.updated_at`

const topUpFrom = ` FROM top_up_requests t JOIN driver_profiles d ON d.id = t.driver_id`

func (t *TopUp) dests() []any {
	return []any{&t.ID, &t.Number, &t.Reference, &t.DriverID, &t.WalletID, &t.DriverUserID, &t.DriverCode,
		&t.RequestedAmount, &t.Currency, &t.Provider, &t.Status, &t.PaymentURL, &t.DriverPaymentReference,
		&t.DriverNote, &t.ReceiptFileID, &t.DriverReportedPaidAt, &t.VerifiedAmount,
		&t.ProviderTransactionID, &t.PaidAt, &t.ConfirmedBy, &t.ConfirmedAt, &t.RejectedBy, &t.RejectedAt,
		&t.RejectReason, &t.RefundedBy, &t.RefundedAt, &t.RefundReason, &t.IdempotencyKey, &t.ExpiresAt,
		&t.CreatedAt, &t.UpdatedAt}
}

// GetTopUp loads a top-up by id; with lock it is locked FOR UPDATE (q must then
// be a transaction). Unknown ids yield 404.
func GetTopUp(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (TopUp, error) {
	sql := `SELECT ` + topUpColumns + topUpFrom + ` WHERE t.id = $1`
	if lock {
		sql += ` FOR UPDATE OF t`
	}
	var t TopUp
	err := q.QueryRow(ctx, sql, id).Scan(t.dests()...)
	if db.IsNoRows(err) {
		return TopUp{}, httpx.NotFound()
	}
	if err != nil {
		return TopUp{}, fmt.Errorf("payments: load top-up: %w", err)
	}
	return t, nil
}

// getDriverTopUp loads a top-up owned by driverID; other drivers' top-ups are
// indistinguishable from unknown ids (404).
func getDriverTopUp(ctx context.Context, q db.Querier, driverID, id uuid.UUID, lock bool) (TopUp, error) {
	t, err := GetTopUp(ctx, q, id, lock)
	if err != nil {
		return TopUp{}, err
	}
	if t.DriverID != driverID {
		return TopUp{}, httpx.NotFound()
	}
	return t, nil
}

func scanTopUps(rows pgx.Rows) ([]TopUp, error) {
	defer rows.Close()
	var out []TopUp
	for rows.Next() {
		var t TopUp
		if err := rows.Scan(t.dests()...); err != nil {
			return nil, fmt.Errorf("payments: scan top-up: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payments: list top-ups: %w", err)
	}
	return out, nil
}

// TopUpEvent is one top_up_events row.
type TopUpEvent struct {
	ID         int64      `json:"id"`
	FromStatus *string    `json:"from_status"`
	ToStatus   string     `json:"to_status"`
	ActorType  string     `json:"actor_type"`
	ActorID    *uuid.UUID `json:"actor_id"`
	Note       *string    `json:"note"`
	CreatedAt  time.Time  `json:"created_at"`
}

// addEvent appends a status-history row. from may be empty for the creation event.
func addEvent(ctx context.Context, q db.Querier, topupID uuid.UUID, from, to, actorType string, actorID *uuid.UUID, note string, meta map[string]any) error {
	var fromArg, noteArg any
	if from != "" {
		fromArg = from
	}
	if note != "" {
		noteArg = note
	}
	if meta == nil {
		meta = map[string]any{}
	}
	_, err := q.Exec(ctx, `
		INSERT INTO top_up_events (topup_id, from_status, to_status, actor_type, actor_id, note, meta)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, topupID, fromArg, to, actorType, actorID, noteArg, meta)
	if err != nil {
		return fmt.Errorf("payments: add top-up event: %w", err)
	}
	return nil
}

// ListEvents returns a top-up's history, oldest first.
func ListEvents(ctx context.Context, q db.Querier, topupID uuid.UUID) ([]TopUpEvent, error) {
	rows, err := q.Query(ctx, `
		SELECT id, from_status, to_status, actor_type, actor_id, note, created_at
		FROM top_up_events WHERE topup_id = $1 ORDER BY id`, topupID)
	if err != nil {
		return nil, fmt.Errorf("payments: list top-up events: %w", err)
	}
	defer rows.Close()
	out := []TopUpEvent{}
	for rows.Next() {
		var e TopUpEvent
		if err := rows.Scan(&e.ID, &e.FromStatus, &e.ToStatus, &e.ActorType, &e.ActorID, &e.Note, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("payments: scan top-up event: %w", err)
		}
		e.CreatedAt = e.CreatedAt.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
