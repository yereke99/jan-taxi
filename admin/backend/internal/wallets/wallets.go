// Package wallets implements the driver commission wallet: a balance held by the
// platform, a reserved part earmarked for active orders, and the append-only
// wallet_ledger journal that explains every change (ARCHITECTURE §6).
//
// Every mutation runs inside the caller's transaction, locks the wallet row
// FOR UPDATE, checks the invariants, writes one ledger row under a globally unique
// idempotency key and updates the wallet in the same transaction. Replaying an
// operation with the same key returns the original result and never moves money
// twice. The package never publishes realtime events; callers do that.
package wallets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Currency is the only wallet currency.
const Currency = "KZT"

// Reservation statuses.
const (
	ReservationActive   = "active"
	ReservationSettled  = "settled"
	ReservationReleased = "released"
)

// Ledger entry types (wallet_ledger.entry_type).
const (
	EntryTopUpCredit       = "topup_credit"
	EntryTopUpRefund       = "topup_refund"
	EntryCommissionReserve = "commission_reserve"
	EntryCommissionRelease = "commission_release"
	EntryCommissionCharge  = "commission_charge"
	EntryCommissionRefund  = "commission_refund"
	EntryAdjustmentCredit  = "adjustment_credit"
	EntryAdjustmentDebit   = "adjustment_debit"
)

// EntryTypes lists every ledger entry type, for filter validation.
var EntryTypes = []string{
	EntryTopUpCredit, EntryTopUpRefund, EntryCommissionReserve, EntryCommissionRelease,
	EntryCommissionCharge, EntryCommissionRefund, EntryAdjustmentCredit, EntryAdjustmentDebit,
}

// ValidEntryType reports whether t is a known ledger entry type.
func ValidEntryType(t string) bool {
	for _, v := range EntryTypes {
		if v == t {
			return true
		}
	}
	return false
}

var (
	// ErrInsufficientBalance means available (balance - reserved) does not cover the debit.
	ErrInsufficientBalance = errors.New("wallets: insufficient balance")
	// ErrNotFound means the wallet, reservation or referenced entry does not exist.
	ErrNotFound = errors.New("wallets: not found")
	// ErrInvalidAmount means a zero, negative or out-of-range amount was passed.
	ErrInvalidAmount = errors.New("wallets: invalid amount")
	// ErrInvalidKey means a client-supplied idempotency key is malformed.
	ErrInvalidKey = errors.New("wallets: invalid idempotency key")
	// ErrInvalidReason means a required reason is empty.
	ErrInvalidReason = errors.New("wallets: reason required")
	// ErrIdempotencyConflict means the idempotency key was already used for a
	// different wallet, entry type or amount.
	ErrIdempotencyConflict = errors.New("wallets: idempotency key reused with different parameters")
	// ErrReservationConflict means the order already holds an active reservation on
	// another wallet or with another amount.
	ErrReservationConflict = errors.New("wallets: order already has an active reservation")
	// ErrAlreadySettled means the order's commission was already charged, so it
	// cannot be reserved again.
	ErrAlreadySettled = errors.New("wallets: order commission already settled")
)

// Wallet is a driver's commission wallet. Amounts are integer KZT.
type Wallet struct {
	ID        uuid.UUID `json:"id"`
	DriverID  uuid.UUID `json:"driver_id"`
	Balance   int64     `json:"balance"`
	Reserved  int64     `json:"reserved"`
	Currency  string    `json:"currency"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Available is the part of the balance not earmarked for active orders.
func (w Wallet) Available() int64 { return w.Balance - w.Reserved }

// MarshalJSON adds the derived "available" field.
func (w Wallet) MarshalJSON() ([]byte, error) {
	type plain Wallet
	return json.Marshal(struct {
		plain
		Available int64 `json:"available"`
	}{plain(w), w.Available()})
}

// Reservation earmarks a commission on a wallet for one order.
type Reservation struct {
	ID       uuid.UUID `json:"id"`
	WalletID uuid.UUID `json:"wallet_id"`
	OrderID  uuid.UUID `json:"order_id"`
	Amount   int64     `json:"amount"`
	Status   string    `json:"status"`
}

const walletColumns = `id, driver_id, balance, reserved, currency, updated_at`

func scanWallet(row pgx.Row) (Wallet, error) {
	var w Wallet
	err := row.Scan(&w.ID, &w.DriverID, &w.Balance, &w.Reserved, &w.Currency, &w.UpdatedAt)
	if db.IsNoRows(err) {
		return Wallet{}, ErrNotFound
	}
	if err != nil {
		return Wallet{}, fmt.Errorf("wallets: scan wallet: %w", err)
	}
	return w, nil
}

// Ensure returns the driver's wallet id, creating an empty wallet on first use.
// It returns ErrNotFound when the driver profile does not exist.
func Ensure(ctx context.Context, q db.Querier, driverID uuid.UUID) (uuid.UUID, error) {
	_, err := q.Exec(ctx, `INSERT INTO wallets (driver_id) VALUES ($1) ON CONFLICT (driver_id) DO NOTHING`, driverID)
	if err != nil {
		if db.IsForeignKeyViolation(err, "") {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("wallets: ensure: %w", err)
	}
	// A separate statement sees a row committed by a concurrent Ensure.
	var id uuid.UUID
	if err := q.QueryRow(ctx, `SELECT id FROM wallets WHERE driver_id = $1`, driverID).Scan(&id); err != nil {
		if db.IsNoRows(err) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("wallets: ensure: %w", err)
	}
	return id, nil
}

// Get returns the driver's wallet without locking it, or ErrNotFound.
func Get(ctx context.Context, q db.Querier, driverID uuid.UUID) (Wallet, error) {
	return scanWallet(q.QueryRow(ctx, `SELECT `+walletColumns+` FROM wallets WHERE driver_id = $1`, driverID))
}

// GetByID returns a wallet by its id without locking it, or ErrNotFound.
func GetByID(ctx context.Context, q db.Querier, walletID uuid.UUID) (Wallet, error) {
	return scanWallet(q.QueryRow(ctx, `SELECT `+walletColumns+` FROM wallets WHERE id = $1`, walletID))
}

// Lock locks the driver's wallet row FOR UPDATE until the transaction ends and
// returns its current state, creating the wallet if needed. Callers that must
// check the balance before other writes (e.g. atomic order acceptance) use it to
// serialise with every other wallet mutation.
func Lock(ctx context.Context, tx pgx.Tx, driverID uuid.UUID) (Wallet, error) {
	return lockByDriver(ctx, tx, driverID, true)
}

// ActiveReservation returns the order's active reservation, or ErrNotFound.
func ActiveReservation(ctx context.Context, q db.Querier, orderID uuid.UUID) (Reservation, error) {
	r, err := findActiveReservation(ctx, q, orderID, uuid.Nil, false)
	if err != nil {
		return Reservation{}, err
	}
	if r == nil {
		return Reservation{}, ErrNotFound
	}
	return *r, nil
}

func lockByDriver(ctx context.Context, tx pgx.Tx, driverID uuid.UUID, create bool) (Wallet, error) {
	const sql = `SELECT ` + walletColumns + ` FROM wallets WHERE driver_id = $1 FOR UPDATE`
	w, err := scanWallet(tx.QueryRow(ctx, sql, driverID))
	if !errors.Is(err, ErrNotFound) || !create {
		return w, err
	}
	if _, err := Ensure(ctx, tx, driverID); err != nil {
		return Wallet{}, err
	}
	return scanWallet(tx.QueryRow(ctx, sql, driverID))
}

func lockByID(ctx context.Context, tx pgx.Tx, walletID uuid.UUID) (Wallet, error) {
	return scanWallet(tx.QueryRow(ctx, `SELECT `+walletColumns+` FROM wallets WHERE id = $1 FOR UPDATE`, walletID))
}
