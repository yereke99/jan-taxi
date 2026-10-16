package wallets

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Mismatch reasons reported by Reconcile.
const (
	MismatchBalance      = "balance_ne_ledger_sum"  // wallet.balance != sum(balance_delta)
	MismatchReserved     = "reserved_ne_ledger_sum" // wallet.reserved != sum(reserved_delta)
	MismatchReservations = "reserved_ne_active_reservations"
	MismatchChain        = "wallet_ne_last_ledger_after" // wallet != last entry's *_after
)

// Mismatch describes a wallet whose stored state disagrees with its journal.
type Mismatch struct {
	WalletID           uuid.UUID `json:"wallet_id"`
	DriverID           uuid.UUID `json:"driver_id"`
	Balance            int64     `json:"balance"`
	Reserved           int64     `json:"reserved"`
	LedgerBalance      int64     `json:"ledger_balance"`
	LedgerReserved     int64     `json:"ledger_reserved"`
	ActiveReservations int64     `json:"active_reservations"`
	LastBalanceAfter   *int64    `json:"last_balance_after"`
	LastReservedAfter  *int64    `json:"last_reserved_after"`
	Reasons            []string  `json:"reasons"`
}

// Reconcile checks every wallet against its journal in one consistent snapshot:
// balance and reserved must equal the sums of the ledger deltas and the last
// entry's *_after values, and reserved must equal the sum of active reservations.
// It returns only the wallets that disagree (empty when the books balance).
func Reconcile(ctx context.Context, q db.Querier) ([]Mismatch, error) {
	rows, err := q.Query(ctx, `
		SELECT w.id, w.driver_id, w.balance, w.reserved,
		       COALESCE(l.sum_balance, 0), COALESCE(l.sum_reserved, 0),
		       COALESCE(r.active, 0), last.balance_after, last.reserved_after
		FROM wallets w
		LEFT JOIN (
			SELECT wallet_id, sum(balance_delta)::bigint AS sum_balance, sum(reserved_delta)::bigint AS sum_reserved
			FROM wallet_ledger GROUP BY wallet_id
		) l ON l.wallet_id = w.id
		LEFT JOIN (
			SELECT wallet_id, sum(amount)::bigint AS active
			FROM wallet_reservations WHERE status = 'active' GROUP BY wallet_id
		) r ON r.wallet_id = w.id
		LEFT JOIN LATERAL (
			SELECT balance_after, reserved_after FROM wallet_ledger
			WHERE wallet_id = w.id ORDER BY id DESC LIMIT 1
		) last ON true
		WHERE w.balance <> COALESCE(l.sum_balance, 0)
		   OR w.reserved <> COALESCE(l.sum_reserved, 0)
		   OR w.reserved <> COALESCE(r.active, 0)
		   OR (last.balance_after IS NOT NULL AND (last.balance_after <> w.balance OR last.reserved_after <> w.reserved))
		ORDER BY w.created_at, w.id`)
	if err != nil {
		return nil, fmt.Errorf("wallets: reconcile: %w", err)
	}
	defer rows.Close()
	out := []Mismatch{}
	for rows.Next() {
		var m Mismatch
		if err := rows.Scan(&m.WalletID, &m.DriverID, &m.Balance, &m.Reserved, &m.LedgerBalance, &m.LedgerReserved,
			&m.ActiveReservations, &m.LastBalanceAfter, &m.LastReservedAfter); err != nil {
			return nil, fmt.Errorf("wallets: reconcile scan: %w", err)
		}
		if m.Balance != m.LedgerBalance {
			m.Reasons = append(m.Reasons, MismatchBalance)
		}
		if m.Reserved != m.LedgerReserved {
			m.Reasons = append(m.Reasons, MismatchReserved)
		}
		if m.Reserved != m.ActiveReservations {
			m.Reasons = append(m.Reasons, MismatchReservations)
		}
		if m.LastBalanceAfter != nil && (*m.LastBalanceAfter != m.Balance || *m.LastReservedAfter != m.Reserved) {
			m.Reasons = append(m.Reasons, MismatchChain)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wallets: reconcile: %w", err)
	}
	return out, nil
}

// HTTPError maps wallet errors to API errors (ARCHITECTURE §10 codes); other
// errors pass through unchanged.
func HTTPError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInsufficientBalance):
		return httpx.NewError(http.StatusPaymentRequired, "insufficient_balance", "insufficient wallet balance").WithCause(err)
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound().WithCause(err)
	case errors.Is(err, ErrInvalidAmount):
		return httpx.Validation(map[string]string{"amount": "invalid"})
	case errors.Is(err, ErrInvalidKey):
		return httpx.Validation(map[string]string{"idempotency_key": "invalid"})
	case errors.Is(err, ErrInvalidReason):
		return httpx.Validation(map[string]string{"reason": "required"})
	case errors.Is(err, ErrIdempotencyConflict):
		return httpx.Conflict("conflict", "idempotency key already used with different parameters").WithCause(err)
	case errors.Is(err, ErrReservationConflict), errors.Is(err, ErrAlreadySettled):
		return httpx.Conflict("conflict", "order commission already reserved or settled").WithCause(err)
	default:
		return err
	}
}
