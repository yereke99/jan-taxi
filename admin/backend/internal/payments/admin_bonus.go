package payments

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// Limits of a manual bonus (§22.7).
const (
	MaxBonus           = 1_000_000 // KZT per bonus
	maxBonusCommentLen = 200       // code points
)

type bonusRequest struct {
	Amount         *int64  `json:"amount"`
	Comment        *string `json:"comment"`
	IdempotencyKey *string `json:"idempotency_key"`
}

// BonusResult is the POST /admin/drivers/{id}/bonus response (OpenAPI
// DriverBonusResult): the wallet after the credit and the ledger entry.
type BonusResult struct {
	Wallet WalletView           `json:"wallet"`
	Entry  AdminLedgerEntryView `json:"entry"`
}

// adminGrantBonus handles POST /admin/drivers/{id}/bonus (finance.manage,
// ARCHITECTURE §22.7): the company credits a driver a bonus (bonus_credit,
// key "bonus:<idempotency_key>"). The credit is audited, published as
// wallet.updated and announced in the driver bot (driver_bonus_granted, with
// the comment when given and a wallet button). A replay of the same key and
// amount returns 200 with the original entry and changes nothing; the same
// key with another amount or driver is 409 conflict.
func (s *Service) adminGrantBonus(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	driverID, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var in bonusRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	fields := map[string]string{}
	switch {
	case in.Amount == nil:
		fields["amount"] = "required"
	case *in.Amount <= 0:
		fields["amount"] = "too_small"
	case *in.Amount > MaxBonus:
		fields["amount"] = "too_large"
	}
	var comment string
	if in.Comment != nil {
		if comment, err = textutil.CleanText(*in.Comment, maxBonusCommentLen); err != nil {
			fields["comment"] = textutil.Reason(err)
		}
	}
	var key string
	if in.IdempotencyKey == nil || strings.TrimSpace(*in.IdempotencyKey) == "" {
		fields["idempotency_key"] = "required"
	} else if key, err = wallets.BonusKey(*in.IdempotencyKey); err != nil {
		fields["idempotency_key"] = "invalid"
	}
	if len(fields) > 0 {
		if fields["amount"] == "too_large" {
			return httpx.Validation(fields).WithDetails(map[string]any{"max": MaxBonus})
		}
		return httpx.Validation(fields)
	}
	amount := *in.Amount

	ctx := r.Context()
	var (
		out    BonusResult
		replay bool
	)
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		userID, err := driverUser(ctx, tx, driverID)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wallet_ledger WHERE idempotency_key = $1)`, key).Scan(&replay); err != nil {
			return fmt.Errorf("payments: check bonus key: %w", err)
		}
		if err := wallets.CreditBonus(ctx, tx, driverID, amount, admin.AdminID, key, comment); err != nil {
			return wallets.HTTPError(err)
		}
		entry, err := scanAdminLedger(tx.QueryRow(ctx, `SELECT `+adminLedgerColumns+adminLedgerFrom+` WHERE l.idempotency_key = $1`, key))
		if err != nil {
			return fmt.Errorf("payments: load bonus: %w", err)
		}
		wal, err := wallets.Get(ctx, tx, driverID)
		if err != nil {
			return wallets.HTTPError(err)
		}
		if !replay {
			if err := audit.Log(ctx, tx, r, audit.Entry{
				Action: "wallet.bonus", EntityType: "driver", EntityID: driverID.String(),
				Before: map[string]any{"balance": wal.Balance - amount},
				After: map[string]any{"amount": amount, "comment": comment, "balance": wal.Balance,
					"ledger_entry_id": entry.ID, "idempotency_key": key},
			}); err != nil {
				return err
			}
			if err := publishWallet(ctx, tx, driverID, userID, wal); err != nil {
				return err
			}
			params := map[string]any{"amount": amount, "balance": wal.Balance}
			if comment != "" {
				params["comment"] = comment
			}
			if err := notifications.Enqueue(ctx, tx, notifications.Notification{
				Bot: notifications.DriverBot, UserID: userID, Template: notifications.TplDriverBonusGranted,
				Params: params, DedupeKey: "driver_bonus_granted:" + strings.TrimPrefix(key, "bonus:"),
			}); err != nil {
				return fmt.Errorf("payments: notify bonus: %w", err)
			}
		}
		view, err := loadWallet(ctx, tx, driverID)
		if err != nil {
			return err
		}
		out = BonusResult{Wallet: view, Entry: entry}
		return nil
	})
	if err != nil {
		return err
	}
	if replay {
		return httpx.OK(w, out)
	}
	return httpx.Created(w, out)
}
