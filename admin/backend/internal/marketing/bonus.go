package marketing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// The passenger bonus balance (§23.5, §23.6) works like a driver wallet:
// balance is what the platform owes the passenger as discounts, reserved is
// the part earmarked for active orders, available = balance - reserved. Every
// change locks the account row, writes one append-only ledger row under a
// unique key and updates the account in the same transaction.

// Bonus ledger entry types (passenger_bonus_ledger.entry_type).
const (
	BonusGrant            = "grant"
	BonusReserve          = "reserve"
	BonusRelease          = "release"
	BonusSpend            = "spend"
	BonusAdjustmentCredit = "adjustment_credit"
	BonusAdjustmentDebit  = "adjustment_debit"
)

// BonusEntryTypes lists the ledger entry types, for filters.
var BonusEntryTypes = []string{BonusGrant, BonusReserve, BonusRelease, BonusSpend, BonusAdjustmentCredit, BonusAdjustmentDebit}

var (
	errBonusInsufficient = errors.New("marketing: bonus balance too low")
	errBonusKeyConflict  = errors.New("marketing: bonus ledger key reused with another effect")
)

// BonusAccount is a passenger's bonus balance.
type BonusAccount struct {
	Balance   int64 `json:"balance"`
	Reserved  int64 `json:"reserved"`
	Available int64 `json:"available"`
}

// BonusEntry is one bonus ledger row.
type BonusEntry struct {
	ID            int64      `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	Type          string     `json:"type"`
	BalanceDelta  int64      `json:"balance_delta"`
	ReservedDelta int64      `json:"reserved_delta"`
	BalanceAfter  int64      `json:"balance_after"`
	ReservedAfter int64      `json:"reserved_after"`
	CampaignID    *uuid.UUID `json:"campaign_id"`
	OrderID       *uuid.UUID `json:"order_id"`
	OrderNumber   *int64     `json:"order_number"`
	AdminID       *uuid.UUID `json:"admin_id"`
	Key           string     `json:"idempotency_key"`
	Description   string     `json:"description"`
	CreatedAt     time.Time  `json:"created_at"`
}

type bonusAccount struct {
	userID   uuid.UUID
	balance  int64
	reserved int64
}

// lockBonus locks (creating if needed) the passenger's bonus account.
func lockBonus(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (bonusAccount, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO passenger_bonus_accounts (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return bonusAccount{}, fmt.Errorf("marketing: ensure bonus account: %w", err)
	}
	a := bonusAccount{userID: userID}
	if err := tx.QueryRow(ctx, `
		SELECT balance, reserved FROM passenger_bonus_accounts WHERE user_id = $1 FOR UPDATE`, userID).
		Scan(&a.balance, &a.reserved); err != nil {
		return bonusAccount{}, fmt.Errorf("marketing: lock bonus account: %w", err)
	}
	return a, nil
}

// bonusEntry is a pending bonus ledger write.
type bonusEntry struct {
	typ           string
	balanceDelta  int64
	reservedDelta int64
	campaignID    *uuid.UUID
	versionID     *uuid.UUID
	orderID       *uuid.UUID
	applicationID *uuid.UUID
	adminID       *uuid.UUID
	key           string
	description   string
}

// applyBonus writes e on the locked account a. A key already used with the
// same effect is a no-op (false); with another effect errBonusKeyConflict.
func applyBonus(ctx context.Context, tx pgx.Tx, a *bonusAccount, e bonusEntry) (bool, error) {
	var prevType string
	var prevBalance, prevReserved int64
	var prevUser uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT user_id, entry_type, balance_delta, reserved_delta FROM passenger_bonus_ledger WHERE idempotency_key = $1`,
		e.key).Scan(&prevUser, &prevType, &prevBalance, &prevReserved)
	switch {
	case err == nil:
		if prevUser != a.userID || prevType != e.typ || prevBalance != e.balanceDelta || prevReserved != e.reservedDelta {
			return false, errBonusKeyConflict
		}
		return false, nil
	case !db.IsNoRows(err):
		return false, fmt.Errorf("marketing: bonus key lookup: %w", err)
	}
	nb, nr := a.balance+e.balanceDelta, a.reserved+e.reservedDelta
	if nr < 0 || nb < 0 || nr > nb {
		return false, errBonusInsufficient
	}
	var desc *string
	if s := strings.TrimSpace(e.description); s != "" {
		desc = &s
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO passenger_bonus_ledger (user_id, entry_type, balance_delta, reserved_delta, balance_after,
		                                    reserved_after, campaign_id, version_id, order_id, application_id,
		                                    admin_id, idempotency_key, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		a.userID, e.typ, e.balanceDelta, e.reservedDelta, nb, nr, e.campaignID, e.versionID, e.orderID,
		e.applicationID, e.adminID, e.key, desc); err != nil {
		return false, fmt.Errorf("marketing: insert bonus entry: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE passenger_bonus_accounts SET balance = $2, reserved = $3, version = version + 1 WHERE user_id = $1`,
		a.userID, nb, nr); err != nil {
		return false, fmt.Errorf("marketing: update bonus account: %w", err)
	}
	a.balance, a.reserved = nb, nr
	return true, nil
}

// GetBonus returns the passenger's bonus account (zero when none).
func GetBonus(ctx context.Context, q db.Querier, userID uuid.UUID) (BonusAccount, error) {
	var a BonusAccount
	err := q.QueryRow(ctx, `SELECT balance, reserved FROM passenger_bonus_accounts WHERE user_id = $1`, userID).
		Scan(&a.Balance, &a.Reserved)
	if err != nil && !db.IsNoRows(err) {
		return BonusAccount{}, fmt.Errorf("marketing: bonus account: %w", err)
	}
	a.Available = a.Balance - a.Reserved
	return a, nil
}

// BonusFilter selects bonus ledger rows; Limit defaults to 50 (max 200).
type BonusFilter struct {
	UserID   *uuid.UUID
	Types    []string
	BeforeID *int64
	Limit    int
	Offset   int
}

// ListBonusLedger returns matching rows newest first and the total ignoring
// Limit, Offset and BeforeID.
func ListBonusLedger(ctx context.Context, q db.Querier, f BonusFilter) ([]BonusEntry, int, error) {
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.UserID != nil {
		where = append(where, "l.user_id = "+arg(*f.UserID))
	}
	if len(f.Types) > 0 {
		where = append(where, "l.entry_type = ANY("+arg(f.Types)+")")
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM passenger_bonus_ledger l`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("marketing: count bonus ledger: %w", err)
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
		limit = 50
	}
	limit = min(limit, 200)
	rows, err := q.Query(ctx, `
		SELECT l.id, l.user_id, l.entry_type, l.balance_delta, l.reserved_delta, l.balance_after, l.reserved_after,
		       l.campaign_id, l.order_id, o.number, l.admin_id, l.idempotency_key, COALESCE(l.description, ''), l.created_at
		FROM passenger_bonus_ledger l LEFT JOIN orders o ON o.id = l.order_id`+cond+`
		ORDER BY l.id DESC LIMIT `+arg(limit)+` OFFSET `+arg(max(f.Offset, 0)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("marketing: list bonus ledger: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (BonusEntry, error) {
		var e BonusEntry
		err := row.Scan(&e.ID, &e.UserID, &e.Type, &e.BalanceDelta, &e.ReservedDelta, &e.BalanceAfter, &e.ReservedAfter,
			&e.CampaignID, &e.OrderID, &e.OrderNumber, &e.AdminID, &e.Key, &e.Description, &e.CreatedAt)
		return e, err
	})
	if err != nil {
		return nil, 0, fmt.Errorf("marketing: list bonus ledger: %w", err)
	}
	return items, total, nil
}

func idPtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
