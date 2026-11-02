package wallets

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Promotion compensation (ARCHITECTURE §23.6). When a passenger pays part of
// a fare with a coupon or bonus, the driver collects only the rest in cash;
// the discount is credited to the driver's wallet balance once, when the
// order completes. The balance pays future commissions like any other credit
// (it is not withdrawable: the platform has no payouts).

// CreditPromoCompensation credits amount to the driver for orderID
// (promo_compensation) under key, normally "promo:<application_id>". A replay
// with the same key and amount is a no-op; another driver, type or amount
// returns ErrIdempotencyConflict.
func CreditPromoCompensation(ctx context.Context, tx pgx.Tx, driverID, orderID uuid.UUID, amount int64, key string) error {
	if amount <= 0 || amount > maxAmount {
		return ErrInvalidAmount
	}
	if strings.TrimSpace(key) == "" {
		return ErrInvalidKey
	}
	w, err := lockByDriver(ctx, tx, driverID, true)
	if err != nil {
		return err
	}
	return applyOnce(ctx, tx, &w, entry{
		typ:          EntryPromoCompensation,
		balanceDelta: amount,
		orderID:      idPtr(orderID),
		key:          key,
		description:  "promotion compensation",
	}, nil)
}

// ReversePromoCompensation takes a promotion compensation back on an
// operator's decision (promo_compensation_reversal, key "<key>:reversal").
// Only available money is taken (ErrInsufficientBalance otherwise); a replay
// is a no-op.
func ReversePromoCompensation(ctx context.Context, tx pgx.Tx, driverID, orderID uuid.UUID, amount int64,
	key string, adminID uuid.UUID, reason string) error {
	if amount <= 0 || amount > maxAmount {
		return ErrInvalidAmount
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ErrInvalidReason
	}
	w, err := lockByDriver(ctx, tx, driverID, false)
	if err != nil {
		return err
	}
	return applyOnce(ctx, tx, &w, entry{
		typ:          EntryPromoReversal,
		balanceDelta: -amount,
		orderID:      idPtr(orderID),
		adminID:      nullID(adminID),
		key:          key + ":reversal",
		description:  reason,
	}, func() error {
		credit, err := findEntry(ctx, tx, key)
		if err != nil {
			return err
		}
		if credit == nil || credit.WalletID != w.ID || credit.Type != EntryPromoCompensation {
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
