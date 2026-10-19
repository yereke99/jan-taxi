package wallets

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Commission at acceptance (ARCHITECTURE §22.2). A driver pays the order's
// commission in the transaction that gives them the order; nothing is
// reserved and completion charges nothing more. The commission is not
// returned when the passenger cancels; only an operator refunds it.
//
// Orders accepted under the earlier model (§6) still hold an active
// reservation: SettleOnComplete charges it once and lifecycle cancellations
// release it, exactly as before.

// welcomeBonusKey is the idempotency key of a driver's welcome bonus.
func welcomeBonusKey(driverID uuid.UUID) string { return "welcome_bonus:" + driverID.String() }

// ChargeOnAccept debits amount from the driver's available balance for
// orderID (commission_charge) and returns the amount charged. It must run in
// the transaction that assigns the order to the driver. The first charge of
// an order uses the key "charge:<order_id>"; when an operator later gives the
// order to another driver, that charge uses "charge:<order_id>:<assignment>",
// where assignment is the order version after the assignment, so every
// assignment is charged at most once. A zero amount writes nothing.
//
// Errors: ErrInsufficientBalance (available < amount), ErrInvalidAmount,
// ErrNotFound (no such driver), ErrIdempotencyConflict.
func ChargeOnAccept(ctx context.Context, tx pgx.Tx, driverID, orderID uuid.UUID, amount int64, assignment int) (int64, error) {
	if amount < 0 || amount > maxAmount {
		return 0, ErrInvalidAmount
	}
	w, err := lockByDriver(ctx, tx, driverID, true)
	if err != nil {
		return 0, err
	}
	if amount == 0 {
		return 0, nil
	}
	// The assignment is recorded on the entry, so a repeated charge of the
	// same assignment is recognised whatever key it was given.
	desc := "order assignment " + strconv.Itoa(assignment)
	key := chargeKey(orderID)
	if prev, err := latestCharge(ctx, tx, orderID, uuid.Nil); err != nil {
		return 0, err
	} else if prev != nil {
		if prev.WalletID == w.ID && prev.Description == desc {
			return -prev.BalanceDelta, nil
		}
		key += ":" + strconv.Itoa(assignment)
	}
	err = applyOnce(ctx, tx, &w, entry{
		typ:          EntryCommissionCharge,
		balanceDelta: -amount,
		orderID:      idPtr(orderID),
		key:          key,
		description:  desc,
	}, func() error {
		if w.Available() < amount {
			return ErrInsufficientBalance
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return amount, nil
}

// SettleOnComplete finishes the money side of a completed order. An order
// accepted under the earlier model still holds a reservation, which is
// charged now (SettleForOrder, key "charge:<order_id>"); an order charged at
// acceptance owes nothing more. It returns the commission the order's
// current driver paid (0 when the commission was zero).
func SettleOnComplete(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) (int64, error) {
	if r, err := findActiveReservation(ctx, tx, orderID, uuid.Nil, false); err != nil {
		return 0, err
	} else if r != nil {
		return SettleForOrder(ctx, tx, orderID)
	}
	c, err := latestCharge(ctx, tx, orderID, driverID)
	if err != nil || c == nil {
		return 0, err
	}
	return -c.BalanceDelta, nil
}

// ChargedAmount returns the commission driverID paid for orderID (the latest
// charge, 0 when none).
func ChargedAmount(ctx context.Context, q db.Querier, orderID, driverID uuid.UUID) (int64, error) {
	c, err := latestCharge(ctx, q, orderID, driverID)
	if err != nil || c == nil {
		return 0, err
	}
	return -c.BalanceDelta, nil
}

// RefundCommission returns the commission charged to driverID for orderID
// (commission_refund) on an operator's decision: an admin cancellation,
// re-assignment or dispute resolved with refund_commission. The latest charge
// of that driver for the order is refunded once; its refund key is the charge
// key with "charge:" replaced by "commission_refund:" (for the first charge
// "commission_refund:<order_id>"). A replay returns the amount refunded
// before; an order the driver was never charged for refunds 0. adminID may be
// uuid.Nil for system actions.
func RefundCommission(ctx context.Context, tx pgx.Tx, orderID, driverID, adminID uuid.UUID) (int64, error) {
	charge, err := latestCharge(ctx, tx, orderID, driverID)
	if err != nil || charge == nil {
		return 0, err
	}
	key := "commission_refund:" + strings.TrimPrefix(charge.IdempotencyKey, "charge:")
	if prev, err := findEntry(ctx, tx, key); err != nil {
		return 0, err
	} else if prev != nil {
		return prev.BalanceDelta, nil
	}
	w, err := lockByID(ctx, tx, charge.WalletID)
	if err != nil {
		return 0, err
	}
	amount := -charge.BalanceDelta
	applied, err := apply(ctx, tx, &w, entry{
		typ:          EntryCommissionRefund,
		balanceDelta: amount,
		orderID:      idPtr(orderID),
		adminID:      nullID(adminID),
		key:          key,
	})
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, fmt.Errorf("wallets: refund key %q already used", key)
	}
	return amount, nil
}

// latestCharge returns driverID's newest commission_charge entry for orderID
// (driverID uuid.Nil: any driver), or nil.
func latestCharge(ctx context.Context, q db.Querier, orderID, driverID uuid.UUID) (*LedgerEntry, error) {
	e, err := scanEntry(q.QueryRow(ctx, `SELECT `+ledgerColumns+`
		FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id
		WHERE l.order_id = $1 AND l.entry_type = 'commission_charge'
		  AND ($2::uuid IS NULL OR w.driver_id = $2)
		ORDER BY l.id DESC LIMIT 1`, orderID, nullID(driverID)))
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("wallets: find charge: %w", err)
	}
	return &e, nil
}

// CreditWelcomeBonus credits the welcome balance of a new driver
// (bonus_credit, key "welcome_bonus:<driver_id>", §22.1). The caller decides
// eligibility; a replay is a no-op. adminID is the approving admin (uuid.Nil
// for the system).
func CreditWelcomeBonus(ctx context.Context, tx pgx.Tx, driverID uuid.UUID, amount int64, adminID uuid.UUID) error {
	if amount <= 0 || amount > maxAmount {
		return ErrInvalidAmount
	}
	w, err := lockByDriver(ctx, tx, driverID, true)
	if err != nil {
		return err
	}
	return applyOnce(ctx, tx, &w, entry{
		typ:          EntryBonusCredit,
		balanceDelta: amount,
		adminID:      nullID(adminID),
		key:          welcomeBonusKey(driverID),
		description:  "welcome bonus",
	}, nil)
}

// BonusKey normalises a client idempotency key of a manual bonus to
// "bonus:<uuid>" (a UUID, optionally already prefixed with "bonus:").
func BonusKey(key string) (string, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(key), "bonus:")
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return "", ErrInvalidKey
	}
	return "bonus:" + id.String(), nil
}

// CreditBonus credits a bonus an operator gives a driver (bonus_credit, key
// "bonus:<uuid>", §22.7). comment is stored as the entry description ("bonus"
// when empty). A replay with the same key and amount is a no-op; another
// amount or driver returns ErrIdempotencyConflict.
func CreditBonus(ctx context.Context, tx pgx.Tx, driverID uuid.UUID, amount int64, adminID uuid.UUID, key, comment string) error {
	if amount <= 0 || amount > maxAmount {
		return ErrInvalidAmount
	}
	k, err := BonusKey(key)
	if err != nil {
		return err
	}
	desc := strings.TrimSpace(comment)
	if desc == "" {
		desc = "bonus"
	}
	w, err := lockByDriver(ctx, tx, driverID, true)
	if err != nil {
		return err
	}
	return applyOnce(ctx, tx, &w, entry{
		typ:          EntryBonusCredit,
		balanceDelta: amount,
		adminID:      nullID(adminID),
		key:          k,
		description:  desc,
	}, nil)
}
