package payments

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// MaxAdjustment bounds a manual wallet adjustment (KZT, either sign).
const MaxAdjustment = 10_000_000

const adminLedgerColumns = ledgerViewColumns + `, l.wallet_id, d.id, d.code, d.first_name, d.last_name,
	COALESCE(d.phone_e164, u.phone_e164), l.admin_id, a.email, a.full_name, l.reservation_id, l.idempotency_key`

const adminLedgerFrom = ` FROM wallet_ledger l
	JOIN wallets w ON w.id = l.wallet_id
	JOIN driver_profiles d ON d.id = w.driver_id
	JOIN users u ON u.id = d.user_id` + ledgerViewJoins + `
	LEFT JOIN admin_users a ON a.id = l.admin_id`

func scanAdminLedger(row pgx.Row) (AdminLedgerEntryView, error) {
	var (
		e           AdminLedgerEntryView
		adminID     *uuid.UUID
		email, name *string
	)
	dests := append(e.LedgerEntryView.dests(), &e.WalletID, &e.Driver.ID, &e.Driver.Code, &e.Driver.FirstName,
		&e.Driver.LastName, &e.Driver.Phone, &adminID, &email, &name, &e.ReservationID, &e.IdempotencyKey)
	if err := row.Scan(dests...); err != nil {
		return AdminLedgerEntryView{}, err
	}
	e.Currency = Currency
	e.CreatedAt = e.CreatedAt.UTC()
	e.Admin = adminRef(adminID, email, name)
	return e, nil
}

// ledgerFilter selects admin ledger rows.
type ledgerFilter struct {
	DriverID *uuid.UUID
	Type     string
	From, To *time.Time
	Page     httpx.PageParams
}

func parseLedgerFilter(r *http.Request) (ledgerFilter, error) {
	var f ledgerFilter
	var err error
	if f.Page, err = httpx.ParsePage(r); err != nil {
		return f, err
	}
	if f.DriverID, err = httpx.QueryUUID(r, "driver_id"); err != nil {
		return f, err
	}
	f.Type = strings.TrimSpace(r.URL.Query().Get("type"))
	if f.Type != "" && !wallets.ValidEntryType(f.Type) {
		return f, httpx.Validation(map[string]string{"type": "invalid"})
	}
	f.From, f.To, err = queryRange(r)
	return f, err
}

func (s *Service) listLedger(ctx context.Context, f ledgerFilter) (httpx.Page[AdminLedgerEntryView], error) {
	var (
		where []string
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.DriverID != nil {
		where = append(where, "w.driver_id = "+arg(*f.DriverID))
	}
	if f.Type != "" {
		where = append(where, "l.entry_type = "+arg(f.Type))
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
	out := httpx.Page[AdminLedgerEntryView]{Items: []AdminLedgerEntryView{}, Page: f.Page.Page, PageSize: f.Page.PageSize}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id`+cond, args...).
		Scan(&out.Total); err != nil {
		return out, fmt.Errorf("payments: count ledger: %w", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+adminLedgerColumns+adminLedgerFrom+cond+
		` ORDER BY l.id DESC LIMIT `+arg(f.Page.PageSize)+` OFFSET `+arg(f.Page.Offset()), args...)
	if err != nil {
		return out, fmt.Errorf("payments: list ledger: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanAdminLedger(rows)
		if err != nil {
			return out, fmt.Errorf("payments: scan ledger: %w", err)
		}
		out.Items = append(out.Items, e)
	}
	return out, rows.Err()
}

func (s *Service) adminListLedger(w http.ResponseWriter, r *http.Request) error {
	f, err := parseLedgerFilter(r)
	if err != nil {
		return err
	}
	page, err := s.listLedger(r.Context(), f)
	if err != nil {
		return err
	}
	return httpx.OK(w, page)
}

func (s *Service) adminDriverLedger(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	f, err := parseLedgerFilter(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := driverUser(ctx, s.pool, id); err != nil {
		return err
	}
	f.DriverID = &id
	page, err := s.listLedger(ctx, f)
	if err != nil {
		return err
	}
	return httpx.OK(w, page)
}

// driverUser returns the user id of a driver profile, or 404.
func driverUser(ctx context.Context, q db.Querier, driverID uuid.UUID) (uuid.UUID, error) {
	var userID uuid.UUID
	err := q.QueryRow(ctx, `SELECT user_id FROM driver_profiles WHERE id = $1`, driverID).Scan(&userID)
	if db.IsNoRows(err) {
		return uuid.Nil, httpx.NotFound()
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("payments: load driver: %w", err)
	}
	return userID, nil
}

type adjustmentRequest struct {
	Amount         *int64  `json:"amount"`
	Reason         *string `json:"reason"`
	IdempotencyKey *string `json:"idempotency_key"`
}

// WalletAdjustmentResult is the adjustment response (OpenAPI WalletAdjustmentResult).
type WalletAdjustmentResult struct {
	Wallet WalletView           `json:"wallet"`
	Entry  AdminLedgerEntryView `json:"entry"`
}

func (s *Service) adminAdjustWallet(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	driverID, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var in adjustmentRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	fields := map[string]string{}
	switch {
	case in.Amount == nil:
		fields["amount"] = "required"
	case *in.Amount == 0:
		fields["amount"] = "invalid"
	case *in.Amount > MaxAdjustment:
		fields["amount"] = "too_large"
	case *in.Amount < -MaxAdjustment:
		fields["amount"] = "too_small"
	}
	var reason string
	if in.Reason == nil {
		fields["reason"] = "required"
	} else if reason, err = textutil.CleanRequired(*in.Reason, 300); err != nil {
		fields["reason"] = textutil.Reason(err)
	}
	var key string
	if in.IdempotencyKey == nil || strings.TrimSpace(*in.IdempotencyKey) == "" {
		fields["idempotency_key"] = "required"
	} else if k, err := uuid.Parse(strings.TrimSpace(*in.IdempotencyKey)); err != nil || k == uuid.Nil {
		fields["idempotency_key"] = "invalid"
	} else {
		key = "adjust:" + k.String()
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	amount := *in.Amount

	ctx := r.Context()
	var (
		out    WalletAdjustmentResult
		replay bool
	)
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		userID, err := driverUser(ctx, tx, driverID)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wallet_ledger WHERE idempotency_key = $1)`, key).Scan(&replay); err != nil {
			return fmt.Errorf("payments: check adjustment key: %w", err)
		}
		if err := wallets.Adjust(ctx, tx, driverID, amount, admin.AdminID, key, reason); err != nil {
			return walletError(ctx, tx, driverID, -amount, err)
		}
		entry, err := scanAdminLedger(tx.QueryRow(ctx, `SELECT `+adminLedgerColumns+adminLedgerFrom+` WHERE l.idempotency_key = $1`, key))
		if err != nil {
			return fmt.Errorf("payments: load adjustment: %w", err)
		}
		wal, err := wallets.Get(ctx, tx, driverID)
		if err != nil {
			return wallets.HTTPError(err)
		}
		if !replay {
			if err := audit.Log(ctx, tx, r, audit.Entry{
				Action: "wallet.adjust", EntityType: "driver", EntityID: driverID.String(),
				Before: map[string]any{"balance": wal.Balance - amount},
				After: map[string]any{"amount": amount, "reason": reason, "balance": wal.Balance,
					"ledger_entry_id": entry.ID, "idempotency_key": key},
			}); err != nil {
				return err
			}
			if err := publishWallet(ctx, tx, driverID, userID, wal); err != nil {
				return err
			}
		}
		view, err := loadWallet(ctx, tx, driverID)
		if err != nil {
			return err
		}
		out = WalletAdjustmentResult{Wallet: view, Entry: entry}
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

// ReconciliationMismatchView is one wallet that disagrees with its journal.
type ReconciliationMismatchView struct {
	WalletID           uuid.UUID `json:"wallet_id"`
	Driver             DriverRef `json:"driver"`
	Balance            int64     `json:"balance"`
	Reserved           int64     `json:"reserved"`
	LedgerBalance      int64     `json:"ledger_balance"`
	LedgerReserved     int64     `json:"ledger_reserved"`
	BalanceDiff        int64     `json:"balance_diff"`
	ReservedDiff       int64     `json:"reserved_diff"`
	ActiveReservations int64     `json:"active_reservations"`
	Reasons            []string  `json:"reasons"`
}

// ReconciliationResult is GET /admin/wallets/reconciliation.
type ReconciliationResult struct {
	CheckedAt      time.Time                    `json:"checked_at"`
	WalletsChecked int                          `json:"wallets_checked"`
	OK             bool                         `json:"ok"`
	Mismatches     []ReconciliationMismatchView `json:"mismatches"`
}

func (s *Service) adminReconcile(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	// One snapshot for the count and the comparison.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("payments: begin reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	out := ReconciliationResult{Mismatches: []ReconciliationMismatchView{}}
	if err := tx.QueryRow(ctx, `SELECT count(*), now() FROM wallets`).Scan(&out.WalletsChecked, &out.CheckedAt); err != nil {
		return fmt.Errorf("payments: count wallets: %w", err)
	}
	out.CheckedAt = out.CheckedAt.UTC()
	mism, err := wallets.Reconcile(ctx, tx)
	if err != nil {
		return err
	}
	for _, m := range mism {
		v := ReconciliationMismatchView{
			WalletID: m.WalletID, Balance: m.Balance, Reserved: m.Reserved,
			LedgerBalance: m.LedgerBalance, LedgerReserved: m.LedgerReserved,
			BalanceDiff: m.Balance - m.LedgerBalance, ReservedDiff: m.Reserved - m.LedgerReserved,
			ActiveReservations: m.ActiveReservations, Reasons: m.Reasons,
		}
		v.Driver.ID = m.DriverID
		if err := tx.QueryRow(ctx, `
			SELECT d.code, d.first_name, d.last_name, COALESCE(d.phone_e164, u.phone_e164)
			FROM driver_profiles d JOIN users u ON u.id = d.user_id WHERE d.id = $1`, m.DriverID).
			Scan(&v.Driver.Code, &v.Driver.FirstName, &v.Driver.LastName, &v.Driver.Phone); err != nil {
			return fmt.Errorf("payments: load mismatch driver: %w", err)
		}
		out.Mismatches = append(out.Mismatches, v)
	}
	out.OK = len(out.Mismatches) == 0
	return httpx.OK(w, out)
}
