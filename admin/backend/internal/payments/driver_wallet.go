package payments

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// walletDriver is the authenticated driver of a wallet request.
type walletDriver struct {
	auth.DriverContext
	UserID uuid.UUID
}

// requireWalletDriver demands a driver profile that has left the draft stage;
// drafts get 403 driver_not_registered like requests without a profile.
func requireWalletDriver(r *http.Request) (walletDriver, error) {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return walletDriver{}, err
	}
	d, err := auth.RequireDriver(r)
	if err != nil {
		return walletDriver{}, err
	}
	if d.Status == "draft" {
		return walletDriver{}, httpx.NewError(http.StatusForbidden, "driver_not_registered", "driver profile required")
	}
	return walletDriver{DriverContext: d, UserID: p.UserID}, nil
}

// loadWallet returns the driver's wallet view, creating the wallet row on first use.
func loadWallet(ctx context.Context, q db.Querier, driverID uuid.UUID) (WalletView, error) {
	if _, err := wallets.Ensure(ctx, q, driverID); err != nil {
		return WalletView{}, wallets.HTTPError(err)
	}
	w, err := wallets.Get(ctx, q, driverID)
	if err != nil {
		return WalletView{}, wallets.HTTPError(err)
	}
	return WalletView{
		Balance:          w.Balance,
		Reserved:         w.Reserved,
		Available:        w.Available(),
		Currency:         w.Currency,
		MinOnlineBalance: settings.MinOnlineBalance(ctx, q),
		UpdatedAt:        w.UpdatedAt.UTC(),
	}, nil
}

func (s *Service) getWallet(w http.ResponseWriter, r *http.Request) error {
	d, err := requireWalletDriver(r)
	if err != nil {
		return err
	}
	v, err := loadWallet(r.Context(), s.pool, d.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}

// cursorParams parses ?limit=1..50 (default 20) and ?before=<positive integer>.
func cursorParams(r *http.Request) (int, *int64, error) {
	limit, err := httpx.QueryInt(r, "limit", 20, 1, 50)
	if err != nil {
		return 0, nil, err
	}
	raw := strings.TrimSpace(r.URL.Query().Get("before"))
	if raw == "" {
		return limit, nil, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, nil, httpx.Validation(map[string]string{"before": "invalid"})
	}
	return limit, &n, nil
}

func nextCursor(n int64) *string {
	s := strconv.FormatInt(n, 10)
	return &s
}

const ledgerViewColumns = `l.id, l.entry_type, l.balance_delta, l.reserved_delta, l.balance_after, l.reserved_after,
	l.order_id, o.number, l.topup_id, tu.reference, l.description, l.created_at`

const ledgerViewJoins = ` LEFT JOIN orders o ON o.id = l.order_id LEFT JOIN top_up_requests tu ON tu.id = l.topup_id`

func (e *LedgerEntryView) dests() []any {
	return []any{&e.ID, &e.EntryType, &e.BalanceDelta, &e.ReservedDelta, &e.BalanceAfter, &e.ReservedAfter,
		&e.OrderID, &e.OrderNumber, &e.TopUpID, &e.TopUpReference, &e.Description, &e.CreatedAt}
}

func (s *Service) listTransactions(w http.ResponseWriter, r *http.Request) error {
	d, err := requireWalletDriver(r)
	if err != nil {
		return err
	}
	limit, before, err := cursorParams(r)
	if err != nil {
		return err
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT `+ledgerViewColumns+`
		FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id`+ledgerViewJoins+`
		WHERE w.driver_id = $1 AND ($2::bigint IS NULL OR l.id < $2)
		ORDER BY l.id DESC LIMIT $3`, d.ID, before, limit+1)
	if err != nil {
		return fmt.Errorf("payments: list transactions: %w", err)
	}
	defer rows.Close()
	items := make([]LedgerEntryView, 0, limit)
	for rows.Next() {
		var e LedgerEntryView
		if err := rows.Scan(e.dests()...); err != nil {
			return fmt.Errorf("payments: scan transaction: %w", err)
		}
		e.Currency = Currency
		e.CreatedAt = e.CreatedAt.UTC()
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("payments: list transactions: %w", err)
	}
	out := httpx.CursorList[LedgerEntryView]{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		out.NextCursor = nextCursor(items[limit-1].ID)
	}
	return httpx.OK(w, out)
}
