package payments

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// MaxVerifiedAmount bounds a single confirmed payment (KZT).
const MaxVerifiedAmount = 10_000_000

// paidAtSkew tolerates clock differences when checking that paid_at is not in the future.
const paidAtSkew = 10 * time.Minute

type confirmRequest struct {
	VerifiedAmount        *int64     `json:"verified_amount"`
	ProviderTransactionID *string    `json:"provider_transaction_id"`
	PaidAt                *time.Time `json:"paid_at"`
	PayerInfo             *string    `json:"payer_info"`
	Note                  *string    `json:"note"`
}

// ConfirmInput is a validated confirmation.
type ConfirmInput struct {
	VerifiedAmount        int64
	ProviderTransactionID string
	PaidAt                time.Time
	PayerInfo             *string
	Note                  *string
}

func (in confirmRequest) validate(now time.Time) (ConfirmInput, error) {
	fields := map[string]string{}
	var out ConfirmInput
	switch {
	case in.VerifiedAmount == nil:
		fields["verified_amount"] = "required"
	case *in.VerifiedAmount < 1:
		fields["verified_amount"] = "too_small"
	case *in.VerifiedAmount > MaxVerifiedAmount:
		fields["verified_amount"] = "too_large"
	default:
		out.VerifiedAmount = *in.VerifiedAmount
	}
	if in.ProviderTransactionID == nil {
		fields["provider_transaction_id"] = "required"
	} else {
		id := normalizeTransactionID(*in.ProviderTransactionID)
		switch {
		case id == "":
			fields["provider_transaction_id"] = "required"
		case len([]rune(id)) > 100:
			fields["provider_transaction_id"] = "too_long"
		case strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f }):
			fields["provider_transaction_id"] = "invalid"
		default:
			out.ProviderTransactionID = id
		}
	}
	switch {
	case in.PaidAt == nil:
		fields["paid_at"] = "required"
	case in.PaidAt.After(now.Add(paidAtSkew)):
		fields["paid_at"] = "invalid"
	default:
		out.PaidAt = in.PaidAt.UTC()
	}
	var err error
	if out.PayerInfo, err = textutil.CleanOptional(in.PayerInfo, 200); err != nil {
		fields["payer_info"] = textutil.Reason(err)
	}
	if out.Note, err = textutil.CleanOptional(in.Note, 300); err != nil {
		fields["note"] = textutil.Reason(err)
	}
	if len(fields) > 0 {
		return ConfirmInput{}, httpx.Validation(fields)
	}
	return out, nil
}

func (s *Service) adminConfirmTopUp(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req confirmRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in, err := req.validate(time.Now())
	if err != nil {
		return err
	}
	out, err := s.Confirm(r.Context(), r, admin.AdminID, id, in)
	if err != nil {
		return err
	}
	return httpx.OK(w, out)
}

// Confirm credits a verified payment to the top-up's driver: it records the
// payment transaction (unique per provider transaction id), credits exactly
// VerifiedAmount once, closes the top-up as confirmed and notifies the driver,
// all in one transaction. r may be nil outside HTTP (audit then has no IP).
func (s *Service) Confirm(ctx context.Context, r *http.Request, adminID, id uuid.UUID, in ConfirmInput) (AdminTopUpDetail, error) {
	var out AdminTopUpDetail
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		t, err := GetTopUp(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if !t.Open() {
			return invalidTransition(t.Status, "confirm")
		}
		paymentID, err := recordManualPayment(ctx, tx, t, in, adminID)
		if err != nil {
			return err
		}
		if err := wallets.CreditTopUp(ctx, tx, t.DriverID, t.ID, in.VerifiedAmount, adminID); err != nil {
			return wallets.HTTPError(err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE top_up_requests
			SET status = 'confirmed', verified_amount = $2, provider_transaction_id = $3, paid_at = $4,
			    confirmed_by = $5, confirmed_at = now()
			WHERE id = $1`, id, in.VerifiedAmount, in.ProviderTransactionID, in.PaidAt, adminID); err != nil {
			return fmt.Errorf("payments: confirm top-up: %w", err)
		}
		meta := map[string]any{
			"verified_amount":         in.VerifiedAmount,
			"provider_transaction_id": in.ProviderTransactionID,
			"payment_transaction_id":  paymentID.String(),
		}
		if err := addEvent(ctx, tx, id, t.Status, StatusConfirmed, ActorAdmin, &adminID, deref(in.Note), meta); err != nil {
			return err
		}
		wal, err := wallets.Get(ctx, tx, t.DriverID)
		if err != nil {
			return wallets.HTTPError(err)
		}
		if err := audit.Log(ctx, tx, r, audit.Entry{
			Action: "topup.confirm", EntityType: "topup", EntityID: id.String(),
			Before: map[string]any{"status": t.Status, "requested_amount": t.RequestedAmount, "reference": t.Reference},
			After: map[string]any{"status": StatusConfirmed, "verified_amount": in.VerifiedAmount,
				"provider_transaction_id": in.ProviderTransactionID, "paid_at": in.PaidAt,
				"payment_transaction_id": paymentID, "balance_after": wal.Balance, "note": in.Note},
			AdminID: &adminID,
		}); err != nil {
			return err
		}
		if err := notifications.Enqueue(ctx, tx, notifications.Notification{
			Bot: notifications.DriverBot, UserID: t.DriverUserID, Template: notifications.TplTopUpConfirmed,
			Params:    map[string]any{"reference": t.Reference, "amount": in.VerifiedAmount, "balance": wal.Balance},
			DedupeKey: "topup_confirmed:" + id.String(),
		}); err != nil {
			return fmt.Errorf("payments: enqueue topup_confirmed: %w", err)
		}
		if err := publishWallet(ctx, tx, t.DriverID, t.DriverUserID, wal); err != nil {
			return err
		}
		if err := publishTopUp(ctx, tx, t, realtime.TopUpConfirmed, StatusConfirmed, false); err != nil {
			return err
		}
		if err := publishAdminTopUp(ctx, tx, t, StatusConfirmed); err != nil {
			return err
		}
		out, err = loadAdminTopUpDetail(ctx, tx, id)
		return err
	})
	return out, err
}

// recordManualPayment stores the admin-verified payment. A transaction id that
// a verified provider callback already reported (source webhook, not yet tied
// to a top-up) is linked when the amounts agree; any other existing row means
// the payment was already used: 409 payment_reference_used.
func recordManualPayment(ctx context.Context, tx pgx.Tx, t TopUp, in ConfirmInput, adminID uuid.UUID) (uuid.UUID, error) {
	var (
		existingID     uuid.UUID
		existingTopUp  *uuid.UUID
		existingSource string
		existingAmount int64
	)
	err := tx.QueryRow(ctx, `
		SELECT id, topup_id, source, amount FROM payment_transactions
		WHERE provider = $1 AND provider_transaction_id = $2 FOR UPDATE`, t.Provider, in.ProviderTransactionID).
		Scan(&existingID, &existingTopUp, &existingSource, &existingAmount)
	switch {
	case err == nil:
		if existingTopUp == nil && existingSource == "webhook" && existingAmount == in.VerifiedAmount {
			if _, err := tx.Exec(ctx, `UPDATE payment_transactions SET topup_id = $2, recorded_by = $3 WHERE id = $1`,
				existingID, t.ID, adminID); err != nil {
				return uuid.Nil, fmt.Errorf("payments: link provider payment: %w", err)
			}
			return existingID, nil
		}
		return uuid.Nil, referenceUsed(ctx, tx, existingTopUp)
	case !db.IsNoRows(err):
		return uuid.Nil, fmt.Errorf("payments: find payment transaction: %w", err)
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_transactions (provider, provider_transaction_id, amount, currency, paid_at, payer_info,
		                                  source, topup_id, recorded_by)
		VALUES ($1, $2, $3, 'KZT', $4, $5, 'manual_admin', $6, $7)
		RETURNING id`, t.Provider, in.ProviderTransactionID, in.VerifiedAmount, in.PaidAt, in.PayerInfo, t.ID, adminID).Scan(&id)
	if db.IsUniqueViolation(err, "") {
		// A concurrent confirmation recorded the same transaction first.
		return uuid.Nil, errPaymentReferenceUsed(nil, nil)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("payments: record payment transaction: %w", err)
	}
	return id, nil
}

func referenceUsed(ctx context.Context, q db.Querier, topupID *uuid.UUID) error {
	if topupID == nil {
		return errPaymentReferenceUsed(nil, nil)
	}
	var ref string
	if err := q.QueryRow(ctx, `SELECT reference FROM top_up_requests WHERE id = $1`, *topupID).Scan(&ref); err != nil {
		return errPaymentReferenceUsed(topupID, nil)
	}
	return errPaymentReferenceUsed(topupID, &ref)
}

type reasonRequest struct {
	Reason *string `json:"reason"`
}

func decodeReason(w http.ResponseWriter, r *http.Request) (string, error) {
	var in reasonRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return "", err
	}
	if in.Reason == nil {
		return "", httpx.Validation(map[string]string{"reason": "required"})
	}
	reason, err := textutil.CleanRequired(*in.Reason, 300)
	if err != nil {
		return "", httpx.Validation(map[string]string{"reason": textutil.Reason(err)})
	}
	return reason, nil
}

func (s *Service) adminRejectTopUp(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	reason, err := decodeReason(w, r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out AdminTopUpDetail
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		t, err := GetTopUp(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if !t.Open() {
			return invalidTransition(t.Status, "reject")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE top_up_requests SET status = 'rejected', rejected_by = $2, rejected_at = now(), reject_reason = $3
			WHERE id = $1`, id, admin.AdminID, reason); err != nil {
			return fmt.Errorf("payments: reject top-up: %w", err)
		}
		if err := addEvent(ctx, tx, id, t.Status, StatusRejected, ActorAdmin, &admin.AdminID, reason, nil); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, r, audit.Entry{
			Action: "topup.reject", EntityType: "topup", EntityID: id.String(),
			Before: map[string]any{"status": t.Status, "reference": t.Reference},
			After:  map[string]any{"status": StatusRejected, "reason": reason},
		}); err != nil {
			return err
		}
		if err := notifications.Enqueue(ctx, tx, notifications.Notification{
			Bot: notifications.DriverBot, UserID: t.DriverUserID, Template: notifications.TplTopUpRejected,
			Params:    map[string]any{"topup_id": id.String(), "reference": t.Reference, "reason": reason},
			DedupeKey: "topup_rejected:" + id.String(),
		}); err != nil {
			return fmt.Errorf("payments: enqueue topup_rejected: %w", err)
		}
		if err := publishTopUp(ctx, tx, t, realtime.TopUpUpdated, StatusRejected, true); err != nil {
			return err
		}
		out, err = loadAdminTopUpDetail(ctx, tx, id)
		return err
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, out)
}

// adminRefundTopUp takes a confirmed top-up back out of the wallet. The money
// itself is returned to the payer outside the system (Kaspi).
func (s *Service) adminRefundTopUp(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	reason, err := decodeReason(w, r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out AdminTopUpDetail
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		t, err := GetTopUp(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if t.Status != StatusConfirmed || t.VerifiedAmount == nil {
			return invalidTransition(t.Status, "refund")
		}
		amount := *t.VerifiedAmount
		if err := wallets.DebitTopUpRefund(ctx, tx, t.DriverID, t.ID, amount, admin.AdminID); err != nil {
			return walletError(ctx, tx, t.DriverID, amount, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE top_up_requests SET status = 'refunded', refunded_by = $2, refunded_at = now(), refund_reason = $3
			WHERE id = $1`, id, admin.AdminID, reason); err != nil {
			return fmt.Errorf("payments: refund top-up: %w", err)
		}
		if err := addEvent(ctx, tx, id, StatusConfirmed, StatusRefunded, ActorAdmin, &admin.AdminID, reason,
			map[string]any{"amount": amount}); err != nil {
			return err
		}
		wal, err := wallets.Get(ctx, tx, t.DriverID)
		if err != nil {
			return wallets.HTTPError(err)
		}
		if err := audit.Log(ctx, tx, r, audit.Entry{
			Action: "topup.refund", EntityType: "topup", EntityID: id.String(),
			Before: map[string]any{"status": StatusConfirmed, "verified_amount": amount},
			After:  map[string]any{"status": StatusRefunded, "reason": reason, "balance_after": wal.Balance},
		}); err != nil {
			return err
		}
		if err := publishWallet(ctx, tx, t.DriverID, t.DriverUserID, wal); err != nil {
			return err
		}
		if err := publishTopUp(ctx, tx, t, realtime.TopUpUpdated, StatusRefunded, true); err != nil {
			return err
		}
		out, err = loadAdminTopUpDetail(ctx, tx, id)
		return err
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, out)
}

// publishWallet tells the driver's app (and admin screens) that the wallet changed.
func publishWallet(ctx context.Context, q db.Querier, driverID, userID uuid.UUID, w wallets.Wallet) error {
	return realtime.Publish(ctx, q, realtime.Event{
		Type:   realtime.WalletUpdated,
		Scopes: []string{realtime.DriverScope(driverID), realtime.UserScope(userID), realtime.AdminScope},
		Data:   map[string]any{"driver_id": driverID, "balance": w.Balance, "reserved": w.Reserved, "available": w.Available()},
	})
}

// publishAdminTopUp refreshes admin top-up screens.
func publishAdminTopUp(ctx context.Context, q db.Querier, t TopUp, status string) error {
	return realtime.Publish(ctx, q, realtime.Event{Type: realtime.TopUpUpdated, Scopes: []string{realtime.AdminScope},
		Data: map[string]any{"topup_id": t.ID, "reference": t.Reference, "status": status}})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
