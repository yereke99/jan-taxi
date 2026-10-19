package moderation

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// WelcomeBonusView is the welcome balance a driver received (§22.1).
type WelcomeBonusView struct {
	Amount    int64     `json:"amount"`
	GrantedAt time.Time `json:"granted_at"`
}

// grantWelcomeBonus credits the welcome balance (setting driver.welcome_bonus,
// ARCHITECTURE §22.1) when an admin approves a driver: once per driver, and
// only if neither the driver's phone nor the vehicle plate ever received it.
// The welcome_bonus_grants unique keys decide, so concurrent approvals of
// drivers sharing a phone or plate grant once. It returns the amount credited
// (0 when the driver is not eligible or the bonus is disabled).
func grantWelcomeBonus(ctx context.Context, tx pgx.Tx, r *http.Request, admin auth.AdminPrincipal, p drivers.Profile, plate string) (int64, error) {
	amount := settings.WelcomeBonus(ctx, tx)
	if amount <= 0 || plate == "" {
		return 0, nil
	}
	var phone *string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(dp.phone_e164, u.phone_e164)
		FROM driver_profiles dp JOIN users u ON u.id = dp.user_id WHERE dp.id = $1`, p.ID).Scan(&phone); err != nil {
		return 0, fmt.Errorf("moderation: bonus phone: %w", err)
	}
	if phone == nil || *phone == "" {
		return 0, nil
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO welcome_bonus_grants (driver_id, phone_e164, plate_normalized, amount, admin_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING`, p.ID, *phone, plate, amount, admin.AdminID)
	if err != nil {
		return 0, fmt.Errorf("moderation: record welcome bonus: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return 0, nil // this driver, phone or plate already had it
	}
	if err := wallets.CreditWelcomeBonus(ctx, tx, p.ID, amount, admin.AdminID); err != nil {
		return 0, wallets.HTTPError(err)
	}
	w, err := wallets.Get(ctx, tx, p.ID)
	if err != nil {
		return 0, wallets.HTTPError(err)
	}
	if err := audit.Log(ctx, tx, r, audit.Entry{
		Action: "driver.welcome_bonus", EntityType: "driver", EntityID: p.ID.String(),
		After: map[string]any{"amount": amount, "balance": w.Balance},
	}); err != nil {
		return 0, err
	}
	if err := notify(ctx, tx, p, notifications.TplDriverWelcomeBonus,
		map[string]any{"amount": amount, "balance": w.Balance}, "driver_welcome_bonus:"+p.ID.String()); err != nil {
		return 0, err
	}
	if err := realtime.Publish(ctx, tx, realtime.Event{
		Type: realtime.WalletUpdated, Scopes: []string{realtime.DriverScope(p.ID)},
		Data: map[string]any{"driver_id": p.ID.String(), "reason": wallets.EntryBonusCredit},
	}); err != nil {
		return 0, err
	}
	return amount, nil
}

// loadWelcomeBonus returns the driver's welcome bonus, if granted.
func loadWelcomeBonus(ctx context.Context, q db.Querier, driverID uuid.UUID) (*WelcomeBonusView, error) {
	var b WelcomeBonusView
	err := q.QueryRow(ctx, `SELECT amount, created_at FROM welcome_bonus_grants WHERE driver_id = $1`, driverID).
		Scan(&b.Amount, &b.GrantedAt)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("moderation: welcome bonus: %w", err)
	}
	b.GrantedAt = b.GrantedAt.UTC()
	return &b, nil
}

// BonusView is one bonus credited to a driver (OpenAPI DriverBonus): the
// welcome balance or a bonus an operator gave.
type BonusView struct {
	LedgerEntryID int64      `json:"ledger_entry_id"`
	Kind          string     `json:"kind"` // welcome | manual
	Amount        int64      `json:"amount"`
	Comment       *string    `json:"comment"`
	AdminID       *uuid.UUID `json:"admin_id"`
	AdminName     *string    `json:"admin_name"`
	CreatedAt     time.Time  `json:"created_at"`
}

// maxBonusHistory bounds the bonus history on the driver detail; the full
// history is the ledger filtered by bonus_credit.
const maxBonusHistory = 50

// loadBonuses returns the driver's latest bonus credits, newest first.
func loadBonuses(ctx context.Context, q db.Querier, driverID uuid.UUID) ([]BonusView, error) {
	rows, err := q.Query(ctx, `
		SELECT l.id, CASE WHEN l.idempotency_key LIKE 'welcome_bonus:%' THEN 'welcome' ELSE 'manual' END,
		       l.balance_delta, CASE WHEN l.idempotency_key LIKE 'bonus:%' AND l.description <> 'bonus'
		                             THEN l.description END,
		       l.admin_id, NULLIF(btrim(a.full_name), ''), l.created_at
		FROM wallet_ledger l
		JOIN wallets w ON w.id = l.wallet_id
		LEFT JOIN admin_users a ON a.id = l.admin_id
		WHERE w.driver_id = $1 AND l.entry_type = 'bonus_credit'
		ORDER BY l.id DESC LIMIT $2`, driverID, maxBonusHistory)
	if err != nil {
		return nil, fmt.Errorf("moderation: bonuses: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (BonusView, error) {
		var b BonusView
		err := row.Scan(&b.LedgerEntryID, &b.Kind, &b.Amount, &b.Comment, &b.AdminID, &b.AdminName, &b.CreatedAt)
		b.CreatedAt = b.CreatedAt.UTC()
		return b, err
	})
	if err != nil {
		return nil, fmt.Errorf("moderation: bonuses: %w", err)
	}
	if out == nil {
		out = []BonusView{}
	}
	return out, nil
}
