package wallets

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// LedgerEntry is one row of the append-only wallet journal.
type LedgerEntry struct {
	ID             int64      `json:"id"`
	WalletID       uuid.UUID  `json:"wallet_id"`
	DriverID       uuid.UUID  `json:"driver_id"`
	Type           string     `json:"type"`
	BalanceDelta   int64      `json:"balance_delta"`
	ReservedDelta  int64      `json:"reserved_delta"`
	BalanceAfter   int64      `json:"balance_after"`
	ReservedAfter  int64      `json:"reserved_after"`
	OrderID        *uuid.UUID `json:"order_id"`
	TopUpID        *uuid.UUID `json:"topup_id"`
	ReservationID  *uuid.UUID `json:"reservation_id"`
	AdminID        *uuid.UUID `json:"admin_id"`
	IdempotencyKey string     `json:"idempotency_key"`
	Description    string     `json:"description"`
	CreatedAt      time.Time  `json:"created_at"`
}

// LedgerFilter selects ledger entries. All fields are optional; From is
// inclusive and To exclusive. Limit defaults to 50 (max 1000). BeforeID is a
// keyset cursor (entries with id < BeforeID) for Mini App lists.
type LedgerFilter struct {
	WalletID *uuid.UUID
	DriverID *uuid.UUID
	Types    []string
	From, To *time.Time
	Limit    int
	Offset   int
	BeforeID *int64
}

const (
	defaultLedgerLimit = 50
	maxLedgerLimit     = 1000
)

const ledgerColumns = `l.id, l.wallet_id, w.driver_id, l.entry_type, l.balance_delta, l.reserved_delta,
	l.balance_after, l.reserved_after, l.order_id, l.topup_id, l.reservation_id, l.admin_id,
	l.idempotency_key, COALESCE(l.description, ''), l.created_at`

func scanEntry(row pgx.Row) (LedgerEntry, error) {
	var e LedgerEntry
	err := row.Scan(&e.ID, &e.WalletID, &e.DriverID, &e.Type, &e.BalanceDelta, &e.ReservedDelta,
		&e.BalanceAfter, &e.ReservedAfter, &e.OrderID, &e.TopUpID, &e.ReservationID, &e.AdminID,
		&e.IdempotencyKey, &e.Description, &e.CreatedAt)
	return e, err
}

// ListLedger returns entries matching f, newest first, and the total number of
// entries matching f ignoring Limit, Offset and BeforeID.
func ListLedger(ctx context.Context, q db.Querier, f LedgerFilter) ([]LedgerEntry, int, error) {
	var (
		where []string
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.WalletID != nil {
		where = append(where, "l.wallet_id = "+arg(*f.WalletID))
	}
	if f.DriverID != nil {
		where = append(where, "w.driver_id = "+arg(*f.DriverID))
	}
	if len(f.Types) > 0 {
		where = append(where, "l.entry_type = ANY("+arg(f.Types)+")")
	}
	if f.From != nil {
		where = append(where, "l.created_at >= "+arg(*f.From))
	}
	if f.To != nil {
		where = append(where, "l.created_at < "+arg(*f.To))
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	const from = ` FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id`

	var total int
	if err := q.QueryRow(ctx, `SELECT count(*)`+from+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("wallets: count ledger: %w", err)
	}

	if f.BeforeID != nil {
		if cond == "" {
			cond = " WHERE l.id < " + arg(*f.BeforeID)
		} else {
			cond += " AND l.id < " + arg(*f.BeforeID)
		}
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLedgerLimit
	}
	if limit > maxLedgerLimit {
		limit = maxLedgerLimit
	}
	offset := max(f.Offset, 0)
	sql := `SELECT ` + ledgerColumns + from + cond + ` ORDER BY l.id DESC LIMIT ` + arg(limit) + ` OFFSET ` + arg(offset)

	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("wallets: list ledger: %w", err)
	}
	defer rows.Close()
	items := make([]LedgerEntry, 0, limit)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("wallets: scan ledger: %w", err)
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("wallets: list ledger: %w", err)
	}
	return items, total, nil
}

// findEntry returns the ledger entry with the given idempotency key, or nil.
func findEntry(ctx context.Context, q db.Querier, key string) (*LedgerEntry, error) {
	e, err := scanEntry(q.QueryRow(ctx, `SELECT `+ledgerColumns+`
		FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id
		WHERE l.idempotency_key = $1`, key))
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("wallets: find ledger entry: %w", err)
	}
	return &e, nil
}

// sameEffect reports whether an existing entry matches a replayed request.
func (e *LedgerEntry) sameEffect(walletID uuid.UUID, typ string, balanceDelta, reservedDelta int64) bool {
	return e.WalletID == walletID && e.Type == typ && e.BalanceDelta == balanceDelta && e.ReservedDelta == reservedDelta
}

// entry is a pending ledger write.
type entry struct {
	typ           string
	balanceDelta  int64
	reservedDelta int64
	orderID       *uuid.UUID
	topupID       *uuid.UUID
	reservationID *uuid.UUID
	adminID       *uuid.UUID
	key           string
	description   string
}

// apply writes e against the locked wallet w and updates w in place. It reports
// false (and changes nothing) when the idempotency key already exists.
func apply(ctx context.Context, tx pgx.Tx, w *Wallet, e entry) (bool, error) {
	if e.balanceDelta == 0 && e.reservedDelta == 0 {
		return false, fmt.Errorf("wallets: zero-delta %s entry", e.typ)
	}
	nb := w.Balance + e.balanceDelta
	nr := w.Reserved + e.reservedDelta
	if nr < 0 {
		return false, fmt.Errorf("wallets: %s would make reserved negative on wallet %s", e.typ, w.ID)
	}
	if nb < 0 || nr > nb {
		return false, ErrInsufficientBalance
	}
	var desc *string
	if e.description != "" {
		desc = &e.description
	}
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO wallet_ledger (wallet_id, entry_type, balance_delta, reserved_delta, balance_after, reserved_after,
		                           order_id, topup_id, reservation_id, admin_id, idempotency_key, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`,
		w.ID, e.typ, e.balanceDelta, e.reservedDelta, nb, nr,
		e.orderID, e.topupID, e.reservationID, e.adminID, e.key, desc).Scan(&id)
	if db.IsNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("wallets: insert ledger entry: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		UPDATE wallets SET balance = $2, reserved = $3, version = version + 1
		WHERE id = $1 RETURNING updated_at`, w.ID, nb, nr).Scan(&w.UpdatedAt); err != nil {
		return false, fmt.Errorf("wallets: update wallet: %w", err)
	}
	w.Balance, w.Reserved = nb, nr
	return true, nil
}

// nullID maps uuid.Nil to SQL NULL (e.g. a system actor instead of an admin).
func nullID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func idPtr(id uuid.UUID) *uuid.UUID { return &id }
