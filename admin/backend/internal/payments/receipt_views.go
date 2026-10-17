package payments

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// receiptRow is one payment_receipts row.
type receiptRow struct {
	id            uuid.UUID
	provider      string
	fileID        uuid.UUID
	fileSHA256    string
	qrPayloadHash *string
	serial        *string
	amount        *int64
	paidAt        *time.Time
	recipientName *string
	recipientBIN  *string
	payerMasked   *string
	paymentMethod *string
	producer      *string
	parseStatus   string
	flags         []string
	decision      string
	reasons       []string
	createdAt     time.Time
}

const receiptColumns = `id, provider, file_id, file_sha256, qr_payload_hash, receipt_serial, amount, paid_at,
	recipient_name, recipient_bin, payer_masked, payment_method, producer, parse_status, flags, decision, reasons, created_at`

func scanReceipt(row pgx.Row) (receiptRow, error) {
	var r receiptRow
	err := row.Scan(&r.id, &r.provider, &r.fileID, &r.fileSHA256, &r.qrPayloadHash, &r.serial, &r.amount, &r.paidAt,
		&r.recipientName, &r.recipientBIN, &r.payerMasked, &r.paymentMethod, &r.producer, &r.parseStatus, &r.flags,
		&r.decision, &r.reasons, &r.createdAt)
	if r.flags == nil {
		r.flags = []string{}
	}
	if r.reasons == nil {
		r.reasons = []string{}
	}
	return r, err
}

// latestReceipt loads the top-up's latest PDF receipt (nil without one).
func latestReceipt(ctx context.Context, q db.Querier, t TopUp) (*receiptRow, error) {
	if t.PaymentReceiptID == nil {
		return nil, nil
	}
	r, err := scanReceipt(q.QueryRow(ctx, `SELECT `+receiptColumns+` FROM payment_receipts WHERE id = $1`, *t.PaymentReceiptID))
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("payments: load receipt: %w", err)
	}
	return &r, nil
}

// receiptsByID loads receipts by id.
func receiptsByID(ctx context.Context, q db.Querier, ids []uuid.UUID) (map[uuid.UUID]*receiptRow, error) {
	out := map[uuid.UUID]*receiptRow{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+receiptColumns+` FROM payment_receipts WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("payments: load receipts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanReceipt(rows)
		if err != nil {
			return nil, fmt.Errorf("payments: scan receipt: %w", err)
		}
		out[r.id] = &r
	}
	return out, rows.Err()
}

// receiptsForTopUp lists every PDF receipt attached to a top-up, newest first.
func receiptsForTopUp(ctx context.Context, q db.Querier, topupID uuid.UUID) ([]receiptRow, error) {
	rows, err := q.Query(ctx, `SELECT `+receiptColumns+` FROM payment_receipts WHERE topup_id = $1 ORDER BY created_at DESC, id`, topupID)
	if err != nil {
		return nil, fmt.Errorf("payments: list receipts: %w", err)
	}
	defer rows.Close()
	out := []receiptRow{}
	for rows.Next() {
		r, err := scanReceipt(rows)
		if err != nil {
			return nil, fmt.Errorf("payments: scan receipt: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DriverReceiptView is the receipt check a driver sees on a top-up.
type DriverReceiptView struct {
	Kind          string     `json:"kind"`     // pdf | image
	Decision      *string    `json:"decision"` // auto_confirmed | manual_review (pdf only)
	Reasons       []string   `json:"reasons"`
	ReceiptSerial *string    `json:"receipt_serial"`
	Amount        *int64     `json:"amount"`
	PaidAt        *time.Time `json:"paid_at"`
}

// driverReceiptView describes the receipt currently shown on t: the latest
// PDF when it is also the latest file, else the image evidence.
func driverReceiptView(t TopUp, rc *receiptRow) *DriverReceiptView {
	switch {
	case t.ReceiptFileID == nil:
		return nil
	case rc == nil || rc.fileID != *t.ReceiptFileID:
		if t.RejectReason != nil && *t.RejectReason == RejectDuplicateReceipt {
			return &DriverReceiptView{Kind: "pdf", Reasons: []string{RejectDuplicateReceipt}}
		}
		return &DriverReceiptView{Kind: "image", Reasons: []string{ReasonImageReceipt}}
	}
	d := rc.decision
	return &DriverReceiptView{Kind: "pdf", Decision: &d, Reasons: driverReasons(rc.reasons), ReceiptSerial: rc.serial,
		Amount: rc.amount, PaidAt: utc(rc.paidAt)}
}

// AdminReceiptView is a parsed receipt for finance admins (OpenAPI PaymentReceipt).
type AdminReceiptView struct {
	ID            uuid.UUID  `json:"id"`
	Provider      string     `json:"provider"`
	FileID        uuid.UUID  `json:"file_id"`
	FileURL       string     `json:"file_url"`
	FileSHA256    string     `json:"file_sha256"`
	ReceiptSerial *string    `json:"receipt_serial"`
	QRPayloadHash *string    `json:"qr_payload_hash"`
	Amount        *int64     `json:"amount"`
	PaidAt        *time.Time `json:"paid_at"`
	RecipientName *string    `json:"recipient_name"`
	RecipientBIN  *string    `json:"recipient_bin"`
	PayerMasked   *string    `json:"payer_masked"`
	PaymentMethod *string    `json:"payment_method"`
	Producer      *string    `json:"producer"`
	ParseStatus   string     `json:"parse_status"`
	Flags         []string   `json:"flags"`
	Decision      string     `json:"decision"`
	Reasons       []string   `json:"reasons"`
	CreatedAt     time.Time  `json:"created_at"`
}

func adminReceiptView(r receiptRow) AdminReceiptView {
	return AdminReceiptView{
		ID: r.id, Provider: r.provider, FileID: r.fileID, FileURL: "/api/v1/admin/files/" + r.fileID.String(),
		FileSHA256: r.fileSHA256, ReceiptSerial: r.serial, QRPayloadHash: r.qrPayloadHash, Amount: r.amount,
		PaidAt: utc(r.paidAt), RecipientName: r.recipientName, RecipientBIN: r.recipientBIN, PayerMasked: r.payerMasked,
		PaymentMethod: r.paymentMethod, Producer: r.producer, ParseStatus: r.parseStatus, Flags: r.flags,
		Decision: r.decision, Reasons: r.reasons, CreatedAt: r.createdAt.UTC(),
	}
}

// ConfirmPrefill is what the admin confirmation form is pre-filled with,
// taken from the latest parsed receipt.
type ConfirmPrefill struct {
	VerifiedAmount        *int64     `json:"verified_amount"`
	ProviderTransactionID *string    `json:"provider_transaction_id"`
	PaidAt                *time.Time `json:"paid_at"`
	PayerInfo             *string    `json:"payer_info"`
}
