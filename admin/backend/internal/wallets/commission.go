package wallets

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Idempotency keys documented in ARCHITECTURE §6.
func reserveKey(orderID, driverID uuid.UUID) string {
	return "reserve:" + orderID.String() + ":" + driverID.String()
}
func releaseKey(reservationID uuid.UUID) string { return "release:" + reservationID.String() }
func chargeKey(orderID uuid.UUID) string        { return "charge:" + orderID.String() }
func refundKey(orderID uuid.UUID) string        { return "commission_refund:" + orderID.String() }

// Reserve earmarks amount of the driver's available balance for orderID
// (commission_reserve). It must run in the same transaction that assigns the
// order to the driver.
//
// Replaying for an order that already holds an active reservation of the same
// driver and amount returns that reservation unchanged. A zero amount creates
// the reservation row without a ledger entry. If the same driver held an earlier,
// released reservation for this order (admin re-assignment back to the same
// driver), the new ledger entry uses the key "reserve:<order>:<driver>:<reservation>"
// because the documented key is already taken by the first reservation.
//
// Errors: ErrInsufficientBalance, ErrReservationConflict (another active
// reservation for the order), ErrAlreadySettled, ErrInvalidAmount, ErrNotFound
// (no such driver).
func Reserve(ctx context.Context, tx pgx.Tx, driverID, orderID uuid.UUID, amount int64) (Reservation, error) {
	if amount < 0 {
		return Reservation{}, ErrInvalidAmount
	}
	w, err := lockByDriver(ctx, tx, driverID, true)
	if err != nil {
		return Reservation{}, err
	}
	existing, err := findActiveReservation(ctx, tx, orderID, uuid.Nil, false)
	if err != nil {
		return Reservation{}, err
	}
	if existing != nil {
		if existing.WalletID == w.ID && existing.Amount == amount {
			return *existing, nil
		}
		return Reservation{}, ErrReservationConflict
	}
	if charged, err := findEntry(ctx, tx, chargeKey(orderID)); err != nil {
		return Reservation{}, err
	} else if charged != nil {
		return Reservation{}, ErrAlreadySettled
	}
	if w.Available() < amount {
		return Reservation{}, ErrInsufficientBalance
	}

	r := Reservation{ID: uuid.New(), WalletID: w.ID, OrderID: orderID, Amount: amount, Status: ReservationActive}
	key := reserveKey(orderID, driverID)
	if amount > 0 {
		prev, err := findEntry(ctx, tx, key)
		if err != nil {
			return Reservation{}, err
		}
		if prev != nil {
			key += ":" + r.ID.String()
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_reservations (id, wallet_id, order_id, amount, status)
		VALUES ($1, $2, $3, $4, 'active')`, r.ID, r.WalletID, r.OrderID, r.Amount)
	if err != nil {
		if db.IsUniqueViolation(err, "wallet_reservations_one_active_per_order") {
			return Reservation{}, ErrReservationConflict
		}
		if db.IsForeignKeyViolation(err, "") {
			return Reservation{}, ErrNotFound
		}
		return Reservation{}, fmt.Errorf("wallets: insert reservation: %w", err)
	}
	if amount == 0 {
		return r, nil
	}
	applied, err := apply(ctx, tx, &w, entry{
		typ:           EntryCommissionReserve,
		reservedDelta: amount,
		orderID:       idPtr(orderID),
		reservationID: idPtr(r.ID),
		key:           key,
	})
	if err != nil {
		return Reservation{}, err
	}
	if !applied {
		// The wallet lock serialises every writer of this key, so a conflict here
		// means the journal and reservations disagree.
		return Reservation{}, fmt.Errorf("wallets: reserve key %q already used", key)
	}
	return r, nil
}

// ReleaseForOrder returns the order's active reservation to the driver's
// available balance (commission_release, key "release:<reservation_id>") and
// reports the released amount. Without an active reservation it is a no-op
// returning 0, so it is safe to call on every cancellation or re-assignment.
func ReleaseForOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, reason string) (int64, error) {
	w, r, err := lockActiveReservation(ctx, tx, orderID)
	if err != nil || r == nil {
		return 0, err
	}
	if err := closeReservation(ctx, tx, r.ID, ReservationReleased); err != nil {
		return 0, err
	}
	if r.Amount == 0 {
		return 0, nil
	}
	applied, err := apply(ctx, tx, &w, entry{
		typ:           EntryCommissionRelease,
		reservedDelta: -r.Amount,
		orderID:       idPtr(orderID),
		reservationID: idPtr(r.ID),
		key:           releaseKey(r.ID),
		description:   reason,
	})
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, fmt.Errorf("wallets: reservation %s was active but its release is already journaled", r.ID)
	}
	return r.Amount, nil
}

// SettleForOrder charges the order's commission exactly once: the active
// reservation is settled and its amount leaves both balance and reserved
// (commission_charge, key "charge:<order_id>"). A replay after settlement
// returns the amount charged before; with neither an active reservation nor a
// previous charge it returns ErrNotFound.
func SettleForOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (int64, error) {
	w, r, err := lockActiveReservation(ctx, tx, orderID)
	if err != nil {
		return 0, err
	}
	if r == nil {
		return previousCharge(ctx, tx, orderID)
	}
	key := chargeKey(orderID)
	if prev, err := findEntry(ctx, tx, key); err != nil {
		return 0, err
	} else if prev != nil {
		return 0, fmt.Errorf("wallets: order %s is charged but still has active reservation %s", orderID, r.ID)
	}
	if err := closeReservation(ctx, tx, r.ID, ReservationSettled); err != nil {
		return 0, err
	}
	if r.Amount == 0 {
		return 0, nil
	}
	applied, err := apply(ctx, tx, &w, entry{
		typ:           EntryCommissionCharge,
		balanceDelta:  -r.Amount,
		reservedDelta: -r.Amount,
		orderID:       idPtr(orderID),
		reservationID: idPtr(r.ID),
		key:           key,
	})
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, fmt.Errorf("wallets: charge key %q already used", key)
	}
	return r.Amount, nil
}

// RefundCommission returns a charged commission to the driver's balance after a
// dispute is resolved as cancelled (commission_refund, key
// "commission_refund:<order_id>"). Replays return the amount refunded before. An
// order settled with a zero commission refunds 0; an order that was never
// charged returns ErrNotFound. adminID may be uuid.Nil for system actions.
func RefundCommission(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, adminID uuid.UUID) (int64, error) {
	charge, err := findEntry(ctx, tx, chargeKey(orderID))
	if err != nil {
		return 0, err
	}
	if charge == nil {
		amount, err := settledAmount(ctx, tx, orderID)
		if err != nil {
			return 0, err
		}
		if amount != 0 {
			return 0, fmt.Errorf("wallets: order %s settled for %d without a charge entry", orderID, amount)
		}
		return 0, nil
	}
	w, err := lockByID(ctx, tx, charge.WalletID)
	if err != nil {
		return 0, err
	}
	key := refundKey(orderID)
	if prev, err := findEntry(ctx, tx, key); err != nil {
		return 0, err
	} else if prev != nil {
		return prev.BalanceDelta, nil
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

// lockActiveReservation locks the wallet holding the order's active reservation
// and then the reservation itself (always wallet first, so every mutation takes
// locks in the same order). It returns a nil reservation when none is active.
func lockActiveReservation(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (Wallet, *Reservation, error) {
	for range 3 {
		cand, err := findActiveReservation(ctx, tx, orderID, uuid.Nil, false)
		if err != nil || cand == nil {
			return Wallet{}, nil, err
		}
		w, err := lockByID(ctx, tx, cand.WalletID)
		if err != nil {
			return Wallet{}, nil, err
		}
		// Re-read under the wallet lock: a concurrent settle/release may have
		// closed it while this transaction waited.
		r, err := findActiveReservation(ctx, tx, orderID, w.ID, true)
		if err != nil {
			return Wallet{}, nil, err
		}
		if r != nil {
			return w, r, nil
		}
	}
	// The reservation keeps moving between wallets; let the caller retry.
	return Wallet{}, nil, fmt.Errorf("wallets: reservation for order %s changed concurrently", orderID)
}

// findActiveReservation returns the order's active reservation (optionally on a
// given wallet, optionally locking it), or nil.
func findActiveReservation(ctx context.Context, q db.Querier, orderID, walletID uuid.UUID, lock bool) (*Reservation, error) {
	sql := `SELECT id, wallet_id, order_id, amount, status FROM wallet_reservations
		WHERE order_id = $1 AND status = 'active' AND ($2::uuid IS NULL OR wallet_id = $2)`
	if lock {
		sql += ` FOR UPDATE`
	}
	var r Reservation
	err := q.QueryRow(ctx, sql, orderID, nullID(walletID)).Scan(&r.ID, &r.WalletID, &r.OrderID, &r.Amount, &r.Status)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("wallets: find reservation: %w", err)
	}
	return &r, nil
}

func closeReservation(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE wallet_reservations SET status = $2, closed_at = now()
		WHERE id = $1 AND status = 'active'`, id, status)
	if err != nil {
		return fmt.Errorf("wallets: close reservation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("wallets: reservation %s is no longer active", id)
	}
	return nil
}

// previousCharge returns what SettleForOrder charged before for the order.
func previousCharge(ctx context.Context, q db.Querier, orderID uuid.UUID) (int64, error) {
	charge, err := findEntry(ctx, q, chargeKey(orderID))
	if err != nil {
		return 0, err
	}
	if charge != nil {
		return -charge.BalanceDelta, nil
	}
	return settledAmount(ctx, q, orderID)
}

// settledAmount returns the amount of the order's settled reservation (zero
// commissions leave no ledger entry), or ErrNotFound.
func settledAmount(ctx context.Context, q db.Querier, orderID uuid.UUID) (int64, error) {
	var amount int64
	err := q.QueryRow(ctx, `
		SELECT amount FROM wallet_reservations
		WHERE order_id = $1 AND status = 'settled'
		ORDER BY closed_at DESC LIMIT 1`, orderID).Scan(&amount)
	if db.IsNoRows(err) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("wallets: settled reservation: %w", err)
	}
	return amount, nil
}
