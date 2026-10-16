package wallets

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func topupKey(topupID uuid.UUID) string       { return "topup:" + topupID.String() }
func topupRefundKey(topupID uuid.UUID) string { return "topup_refund:" + topupID.String() }

// maxAmount bounds single operations far above any real top-up to catch unit
// mistakes (e.g. tiyn instead of tenge) before they reach the journal.
const maxAmount int64 = 100_000_000

// CreditTopUp credits a confirmed top-up to the driver's balance (topup_credit,
// key "topup:<topup_id>"). Confirming the same top-up again is a no-op; reusing
// the top-up id with another driver or amount returns ErrIdempotencyConflict.
func CreditTopUp(ctx context.Context, tx pgx.Tx, driverID, topupID uuid.UUID, amount int64, adminID uuid.UUID) error {
	if amount <= 0 || amount > maxAmount {
		return ErrInvalidAmount
	}
	w, err := lockByDriver(ctx, tx, driverID, true)
	if err != nil {
		return err
	}
	return applyOnce(ctx, tx, &w, entry{
		typ:          EntryTopUpCredit,
		balanceDelta: amount,
		topupID:      idPtr(topupID),
		adminID:      nullID(adminID),
		key:          topupKey(topupID),
	}, nil)
}

// DebitTopUpRefund takes a refunded top-up back out of the driver's balance
// (topup_refund, key "topup_refund:<topup_id>"). The top-up must have been
// credited to this driver (ErrNotFound otherwise), amount may not exceed the
// credited amount (ErrInvalidAmount), and reserved money is never touched
// (ErrInsufficientBalance when available is short). Replays are no-ops.
func DebitTopUpRefund(ctx context.Context, tx pgx.Tx, driverID, topupID uuid.UUID, amount int64, adminID uuid.UUID) error {
	if amount <= 0 || amount > maxAmount {
		return ErrInvalidAmount
	}
	w, err := lockByDriver(ctx, tx, driverID, false)
	if err != nil {
		return err
	}
	return applyOnce(ctx, tx, &w, entry{
		typ:          EntryTopUpRefund,
		balanceDelta: -amount,
		topupID:      idPtr(topupID),
		adminID:      nullID(adminID),
		key:          topupRefundKey(topupID),
	}, func() error {
		credit, err := findEntry(ctx, tx, topupKey(topupID))
		if err != nil {
			return err
		}
		if credit == nil || credit.WalletID != w.ID {
			return ErrNotFound
		}
		if amount > credit.BalanceDelta {
			return ErrInvalidAmount
		}
		if w.Available() < amount {
			return ErrInsufficientBalance
		}
		return nil
	})
}

// Adjust applies a signed manual correction to the driver's balance
// (adjustment_credit / adjustment_debit). key is the client-supplied idempotency
// key: a UUID, optionally already prefixed with "adjust:"; it is stored as
// "adjust:<uuid>". A debit can only take available money. Replays with the same
// key and amount are no-ops; a different amount returns ErrIdempotencyConflict.
func Adjust(ctx context.Context, tx pgx.Tx, driverID uuid.UUID, amount int64, adminID uuid.UUID, key, reason string) error {
	if amount == 0 || amount > maxAmount || amount < -maxAmount {
		return ErrInvalidAmount
	}
	k, err := AdjustmentKey(key)
	if err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ErrInvalidReason
	}
	w, err := lockByDriver(ctx, tx, driverID, true)
	if err != nil {
		return err
	}
	typ := EntryAdjustmentCredit
	if amount < 0 {
		typ = EntryAdjustmentDebit
	}
	return applyOnce(ctx, tx, &w, entry{
		typ:          typ,
		balanceDelta: amount,
		adminID:      nullID(adminID),
		key:          k,
		description:  reason,
	}, func() error {
		if amount < 0 && w.Available() < -amount {
			return ErrInsufficientBalance
		}
		return nil
	})
}

// AdjustmentKey normalises a client idempotency key to "adjust:<uuid>".
func AdjustmentKey(key string) (string, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(key), "adjust:")
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return "", ErrInvalidKey
	}
	return "adjust:" + id.String(), nil
}

// applyOnce applies e on the locked wallet unless its key was already used. A
// replay with the same effect is a no-op; a key used for another wallet, type or
// amount returns ErrIdempotencyConflict. check runs only for a first
// application, after the replay check, so replays never fail on balance rules.
// The conflict is re-checked after the insert because a writer on a different
// wallet (not serialised by our lock) may have taken the key concurrently.
func applyOnce(ctx context.Context, tx pgx.Tx, w *Wallet, e entry, check func() error) error {
	replayed := func() error {
		prev, err := findEntry(ctx, tx, e.key)
		if err != nil {
			return err
		}
		if prev == nil {
			return fmt.Errorf("wallets: ledger key %q vanished", e.key)
		}
		if !prev.sameEffect(w.ID, e.typ, e.balanceDelta, e.reservedDelta) {
			return ErrIdempotencyConflict
		}
		return nil
	}
	if prev, err := findEntry(ctx, tx, e.key); err != nil {
		return err
	} else if prev != nil {
		return replayed()
	}
	if check != nil {
		if err := check(); err != nil {
			return err
		}
	}
	applied, err := apply(ctx, tx, w, e)
	if err != nil || applied {
		return err
	}
	return replayed()
}
