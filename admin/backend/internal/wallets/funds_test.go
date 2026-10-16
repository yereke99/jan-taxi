package wallets_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

func TestCreditTopUpOnce(t *testing.T) {
	e := newEnv(t)
	d := e.driver(0)
	admin := e.admin()
	topup := e.topup(d, 2000)

	for range 2 {
		e.mustTx(func(tx pgx.Tx) error { return wallets.CreditTopUp(e.ctx, tx, d.DriverID, topup, 2000, admin) })
	}
	e.expectWallet(d.DriverID, 2000, 0)
	if n := e.ledgerCount("topup:" + topup.String()); n != 1 {
		t.Fatalf("credit entries = %d, want 1", n)
	}
	err := e.tx(func(tx pgx.Tx) error { return wallets.CreditTopUp(e.ctx, tx, d.DriverID, topup, 2500, admin) })
	if !errors.Is(err, wallets.ErrIdempotencyConflict) {
		t.Fatalf("different amount: %v, want ErrIdempotencyConflict", err)
	}
	if err := e.tx(func(tx pgx.Tx) error { return wallets.CreditTopUp(e.ctx, tx, d.DriverID, topup, 0, admin) }); !errors.Is(err, wallets.ErrInvalidAmount) {
		t.Fatalf("zero amount: %v", err)
	}
	e.expectWallet(d.DriverID, 2000, 0)
	e.expectReconciled()
}

func TestDebitTopUpRefund(t *testing.T) {
	e := newEnv(t)
	d := e.driver(0)
	admin := e.admin()
	topup := e.topup(d, 500)
	e.mustTx(func(tx pgx.Tx) error { return wallets.CreditTopUp(e.ctx, tx, d.DriverID, topup, 500, admin) })

	// 300 is reserved for an active order, so only 200 is available.
	o := e.order()
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 300); return err })
	err := e.tx(func(tx pgx.Tx) error { return wallets.DebitTopUpRefund(e.ctx, tx, d.DriverID, topup, 500, admin) })
	if !errors.Is(err, wallets.ErrInsufficientBalance) {
		t.Fatalf("refund with reserved money: %v, want ErrInsufficientBalance", err)
	}
	e.expectWallet(d.DriverID, 500, 300)

	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.ReleaseForOrder(e.ctx, tx, o, "cancel"); return err })
	if err := e.tx(func(tx pgx.Tx) error { return wallets.DebitTopUpRefund(e.ctx, tx, d.DriverID, topup, 600, admin) }); !errors.Is(err, wallets.ErrInvalidAmount) {
		t.Fatalf("refund above credit: %v, want ErrInvalidAmount", err)
	}
	for range 2 {
		e.mustTx(func(tx pgx.Tx) error { return wallets.DebitTopUpRefund(e.ctx, tx, d.DriverID, topup, 500, admin) })
	}
	e.expectWallet(d.DriverID, 0, 0)
	if n := e.ledgerCount("topup_refund:" + topup.String()); n != 1 {
		t.Fatalf("refund entries = %d, want 1", n)
	}

	other := e.topup(d, 100)
	if err := e.tx(func(tx pgx.Tx) error { return wallets.DebitTopUpRefund(e.ctx, tx, d.DriverID, other, 100, admin) }); !errors.Is(err, wallets.ErrNotFound) {
		t.Fatalf("refund of uncredited top-up: %v, want ErrNotFound", err)
	}
	e.expectReconciled()
}

func TestAdjust(t *testing.T) {
	e := newEnv(t)
	d := e.driver(100)
	admin := e.admin()
	credit, debit := uuid.NewString(), "adjust:"+uuid.NewString()

	e.mustTx(func(tx pgx.Tx) error { return wallets.Adjust(e.ctx, tx, d.DriverID, 250, admin, credit, "bonus") })
	e.mustTx(func(tx pgx.Tx) error {
		return wallets.Adjust(e.ctx, tx, d.DriverID, 250, admin, "adjust:"+credit, "bonus")
	})
	e.expectWallet(d.DriverID, 350, 0)
	e.mustTx(func(tx pgx.Tx) error { return wallets.Adjust(e.ctx, tx, d.DriverID, -50, admin, debit, "correction") })
	e.expectWallet(d.DriverID, 300, 0)
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE idempotency_key = $1 AND entry_type = 'adjustment_credit' AND description = 'bonus'`, "adjust:"+credit); n != 1 {
		t.Fatalf("credit entry missing")
	}
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE idempotency_key = $1 AND entry_type = 'adjustment_debit'`, debit); n != 1 {
		t.Fatalf("debit entry missing")
	}

	tests := []struct {
		name   string
		amount int64
		key    string
		reason string
		want   error
	}{
		{"replayed key with other amount", 300, credit, "bonus", wallets.ErrIdempotencyConflict},
		{"debit beyond available", -301, uuid.NewString(), "fine", wallets.ErrInsufficientBalance},
		{"malformed key", 10, "not-a-uuid", "x", wallets.ErrInvalidKey},
		{"empty reason", 10, uuid.NewString(), "  ", wallets.ErrInvalidReason},
		{"zero amount", 0, uuid.NewString(), "x", wallets.ErrInvalidAmount},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := e.tx(func(tx pgx.Tx) error {
				return wallets.Adjust(e.ctx, tx, d.DriverID, tc.amount, admin, tc.key, tc.reason)
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	e.expectWallet(d.DriverID, 300, 0)
	e.expectReconciled()
}

func TestLedgerIsAppendOnly(t *testing.T) {
	e := newEnv(t)
	d := e.driver(100)
	for name, sql := range map[string]string{
		"update": `UPDATE wallet_ledger SET balance_delta = 1 WHERE wallet_id = $1`,
		"delete": `DELETE FROM wallet_ledger WHERE wallet_id = $1`,
	} {
		_, err := e.pool.Exec(e.ctx, sql, d.WalletID)
		if err == nil {
			t.Fatalf("%s on wallet_ledger succeeded", name)
		}
		if pg := db.PgError(err); pg == nil || pg.Code != "23001" {
			t.Fatalf("%s: err = %v, want restrict_violation", name, err)
		}
	}
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE wallet_id = $1`, d.WalletID); n != 1 {
		t.Fatalf("ledger rows = %d", n)
	}
}

func TestReconcileDetectsCorruption(t *testing.T) {
	e := newEnv(t)
	good := e.driver(500)
	bad := e.driver(500)
	e.expectReconciled()

	if _, err := e.pool.Exec(e.ctx, `UPDATE wallets SET balance = balance + 1 WHERE id = $1`, bad.WalletID); err != nil {
		t.Fatal(err)
	}
	ms, err := wallets.Reconcile(e.ctx, e.pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].WalletID != bad.WalletID || ms[0].DriverID != bad.DriverID {
		t.Fatalf("mismatches = %+v, want only %s", ms, bad.WalletID)
	}
	m := ms[0]
	if m.Balance != 501 || m.LedgerBalance != 500 {
		t.Fatalf("mismatch values %+v", m)
	}
	want := map[string]bool{wallets.MismatchBalance: true, wallets.MismatchChain: true}
	if len(m.Reasons) != len(want) {
		t.Fatalf("reasons = %v", m.Reasons)
	}
	for _, r := range m.Reasons {
		if !want[r] {
			t.Fatalf("unexpected reason %s", r)
		}
	}
	_ = good

	// Reserved drifting from the active reservations is reported too.
	if _, err := e.pool.Exec(e.ctx, `UPDATE wallets SET reserved = 10 WHERE id = $1`, good.WalletID); err != nil {
		t.Fatal(err)
	}
	ms, err = wallets.Reconcile(e.ctx, e.pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("mismatches = %d, want 2", len(ms))
	}
}

func TestEnsureGetAndListLedger(t *testing.T) {
	e := newEnv(t)
	d := e.driver(300)

	id, err := wallets.Ensure(e.ctx, e.pool, d.DriverID)
	if err != nil || id != d.WalletID {
		t.Fatalf("Ensure = %s, %v; want existing wallet %s", id, err, d.WalletID)
	}
	if _, err := wallets.Ensure(e.ctx, e.pool, uuid.New()); !errors.Is(err, wallets.ErrNotFound) {
		t.Fatalf("Ensure unknown driver: %v", err)
	}
	if _, err := wallets.Get(e.ctx, e.pool, uuid.New()); !errors.Is(err, wallets.ErrNotFound) {
		t.Fatalf("Get unknown driver: %v", err)
	}
	var locked wallets.Wallet
	e.mustTx(func(tx pgx.Tx) (err error) {
		locked, err = wallets.Lock(e.ctx, tx, d.DriverID)
		return err
	})
	if locked.ID != d.WalletID || locked.Balance != 300 || locked.Currency != "KZT" {
		t.Fatalf("Lock = %+v", locked)
	}

	admin := e.admin()
	for range 3 {
		e.mustTx(func(tx pgx.Tx) error {
			return wallets.Adjust(e.ctx, tx, d.DriverID, 10, admin, uuid.NewString(), "test")
		})
	}
	other := e.driver(50)

	all, total, err := wallets.ListLedger(e.ctx, e.pool, wallets.LedgerFilter{DriverID: &d.DriverID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(all) != 2 || all[0].ID <= all[1].ID || all[0].DriverID != d.DriverID {
		t.Fatalf("page 1: total=%d items=%+v", total, all)
	}
	next, _, err := wallets.ListLedger(e.ctx, e.pool, wallets.LedgerFilter{DriverID: &d.DriverID, Limit: 10, BeforeID: &all[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 2 || next[0].ID >= all[1].ID {
		t.Fatalf("cursor page: %+v", next)
	}
	typed, total, err := wallets.ListLedger(e.ctx, e.pool, wallets.LedgerFilter{
		WalletID: &d.WalletID,
		Types:    []string{wallets.EntryAdjustmentCredit},
		From:     timePtr(time.Now().Add(-time.Hour)),
		To:       timePtr(time.Now().Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(typed) != 4 {
		t.Fatalf("typed filter total=%d len=%d", total, len(typed))
	}
	everyone, total, err := wallets.ListLedger(e.ctx, e.pool, wallets.LedgerFilter{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(everyone) != 1 {
		t.Fatalf("unfiltered total=%d len=%d", total, len(everyone))
	}
	_ = other
}

func TestWalletJSONAndErrors(t *testing.T) {
	b, err := json.Marshal(wallets.Wallet{Balance: 1500, Reserved: 200, Currency: "KZT"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["available"] != float64(1300) || m["balance"] != float64(1500) || m["currency"] != "KZT" {
		t.Fatalf("json = %s", b)
	}

	tests := []struct {
		err    error
		status int
		code   string
	}{
		{wallets.ErrInsufficientBalance, http.StatusPaymentRequired, "insufficient_balance"},
		{wallets.ErrNotFound, http.StatusNotFound, "not_found"},
		{wallets.ErrInvalidAmount, http.StatusUnprocessableEntity, "validation_failed"},
		{wallets.ErrInvalidKey, http.StatusUnprocessableEntity, "validation_failed"},
		{wallets.ErrIdempotencyConflict, http.StatusConflict, "conflict"},
		{wallets.ErrReservationConflict, http.StatusConflict, "conflict"},
	}
	for _, tc := range tests {
		got := httpx.AsError(wallets.HTTPError(tc.err))
		if got.Status != tc.status || got.Code != tc.code {
			t.Errorf("%v → %d %s, want %d %s", tc.err, got.Status, got.Code, tc.status, tc.code)
		}
	}

	keys := []struct {
		in, want string
		err      error
	}{
		{"0b6c5f3e-2f0e-4b8e-9a51-6d2b8f0f6a11", "adjust:0b6c5f3e-2f0e-4b8e-9a51-6d2b8f0f6a11", nil},
		{"adjust:0B6C5F3E-2F0E-4B8E-9A51-6D2B8F0F6A11", "adjust:0b6c5f3e-2f0e-4b8e-9a51-6d2b8f0f6a11", nil},
		{"", "", wallets.ErrInvalidKey},
		{"adjust:", "", wallets.ErrInvalidKey},
		{"00000000-0000-0000-0000-000000000000", "", wallets.ErrInvalidKey},
	}
	for _, tc := range keys {
		got, err := wallets.AdjustmentKey(tc.in)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("AdjustmentKey(%q) = %q, %v", tc.in, got, err)
		}
	}
}

func TestAdjustKeyReusedAcrossWalletsConcurrently(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	a, b := e.driver(0), e.driver(0)
	key := uuid.NewString()

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, d := range []uuid.UUID{a.DriverID, b.DriverID} {
		go func(driverID uuid.UUID) {
			<-start
			errs <- e.tx(func(tx pgx.Tx) error { return wallets.Adjust(e.ctx, tx, driverID, 100, admin, key, "bonus") })
		}(d)
	}
	close(start)
	var ok, conflict int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, wallets.ErrIdempotencyConflict):
			conflict++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d, want 1 and 1", ok, conflict)
	}
	total := e.wallet(a.DriverID).Balance + e.wallet(b.DriverID).Balance
	if total != 100 {
		t.Fatalf("credited %d in total, want 100", total)
	}
	e.expectReconciled()
}
