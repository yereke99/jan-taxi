package wallets_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

func TestReserveSettleReleaseFlow(t *testing.T) {
	e := newEnv(t)
	d := e.driver(1000)
	o1, o2 := e.order(), e.order()

	var res wallets.Reservation
	e.mustTx(func(tx pgx.Tx) (err error) {
		res, err = wallets.Reserve(e.ctx, tx, d.DriverID, o1, 100)
		return err
	})
	if res.Status != wallets.ReservationActive || res.Amount != 100 || res.WalletID != d.WalletID || res.OrderID != o1 {
		t.Fatalf("unexpected reservation %+v", res)
	}
	e.expectWallet(d.DriverID, 1000, 100)
	if w := e.wallet(d.DriverID); w.Available() != 900 {
		t.Fatalf("available = %d, want 900", w.Available())
	}
	if got, err := wallets.ActiveReservation(e.ctx, e.pool, o1); err != nil || got.ID != res.ID {
		t.Fatalf("active reservation = %+v, %v", got, err)
	}

	var charged int64
	e.mustTx(func(tx pgx.Tx) (err error) {
		charged, err = wallets.SettleForOrder(e.ctx, tx, o1)
		return err
	})
	if charged != 100 {
		t.Fatalf("charged = %d, want 100", charged)
	}
	e.expectWallet(d.DriverID, 900, 0)
	if n := e.count(`SELECT count(*) FROM wallet_reservations WHERE id = $1 AND status = 'settled' AND closed_at IS NOT NULL`, res.ID); n != 1 {
		t.Fatalf("reservation not settled")
	}
	if _, err := wallets.ActiveReservation(e.ctx, e.pool, o1); !errors.Is(err, wallets.ErrNotFound) {
		t.Fatalf("active reservation after settle: %v", err)
	}

	var res2 wallets.Reservation
	e.mustTx(func(tx pgx.Tx) (err error) {
		res2, err = wallets.Reserve(e.ctx, tx, d.DriverID, o2, 150)
		return err
	})
	e.expectWallet(d.DriverID, 900, 150)

	var released int64
	e.mustTx(func(tx pgx.Tx) (err error) {
		released, err = wallets.ReleaseForOrder(e.ctx, tx, o2, "cancelled by passenger")
		return err
	})
	if released != 150 {
		t.Fatalf("released = %d, want 150", released)
	}
	e.expectWallet(d.DriverID, 900, 0)
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE idempotency_key = $1 AND description = 'cancelled by passenger'`, "release:"+res2.ID.String()); n != 1 {
		t.Fatalf("release entry missing")
	}

	// Releasing again is a no-op; a released order was never charged.
	e.mustTx(func(tx pgx.Tx) (err error) {
		released, err = wallets.ReleaseForOrder(e.ctx, tx, o2, "again")
		return err
	})
	if released != 0 {
		t.Fatalf("second release = %d, want 0", released)
	}
	if err := e.tx(func(tx pgx.Tx) error { _, err := wallets.SettleForOrder(e.ctx, tx, o2); return err }); !errors.Is(err, wallets.ErrNotFound) {
		t.Fatalf("settle released order: %v, want ErrNotFound", err)
	}
	if err := e.tx(func(tx pgx.Tx) error { _, err := wallets.SettleForOrder(e.ctx, tx, e.order()); return err }); !errors.Is(err, wallets.ErrNotFound) {
		t.Fatalf("settle unknown order: %v, want ErrNotFound", err)
	}

	// Ledger after-values chain: fixture credit, reserve, charge, reserve, release.
	entries, total, err := wallets.ListLedger(e.ctx, e.pool, wallets.LedgerFilter{DriverID: &d.DriverID})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(entries) != 5 {
		t.Fatalf("ledger total=%d len=%d, want 5", total, len(entries))
	}
	wantTypes := []string{wallets.EntryCommissionRelease, wallets.EntryCommissionReserve, wallets.EntryCommissionCharge,
		wallets.EntryCommissionReserve, wallets.EntryAdjustmentCredit}
	for i, en := range entries {
		if en.Type != wantTypes[i] {
			t.Fatalf("entry %d type %s, want %s", i, en.Type, wantTypes[i])
		}
	}
	if top := entries[0]; top.BalanceAfter != 900 || top.ReservedAfter != 0 {
		t.Fatalf("last entry after = %d/%d", top.BalanceAfter, top.ReservedAfter)
	}
	e.expectReconciled()
}

func TestReserveReplayIsIdempotent(t *testing.T) {
	e := newEnv(t)
	d := e.driver(1000)
	other := e.driver(1000)
	o := e.order()

	var first, second wallets.Reservation
	e.mustTx(func(tx pgx.Tx) (err error) {
		first, err = wallets.Reserve(e.ctx, tx, d.DriverID, o, 100)
		return err
	})
	e.mustTx(func(tx pgx.Tx) (err error) {
		second, err = wallets.Reserve(e.ctx, tx, d.DriverID, o, 100)
		return err
	})
	if first.ID != second.ID {
		t.Fatalf("replay created a new reservation: %s vs %s", first.ID, second.ID)
	}
	if n := e.ledgerCount("reserve:" + o.String() + ":" + d.DriverID.String()); n != 1 {
		t.Fatalf("reserve entries = %d, want 1", n)
	}
	e.expectWallet(d.DriverID, 1000, 100)

	for name, fn := range map[string]func(tx pgx.Tx) error{
		"different amount": func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 200); return err },
		"different driver": func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, other.DriverID, o, 100); return err },
	} {
		if err := e.tx(fn); !errors.Is(err, wallets.ErrReservationConflict) {
			t.Fatalf("%s: err = %v, want ErrReservationConflict", name, err)
		}
	}
	e.expectWallet(other.DriverID, 1000, 0)
	e.expectReconciled()
}

func TestSettleReplayReturnsPreviousCharge(t *testing.T) {
	e := newEnv(t)
	d := e.driver(500)
	o := e.order()
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 120); return err })
	for i := range 3 {
		var charged int64
		e.mustTx(func(tx pgx.Tx) (err error) {
			charged, err = wallets.SettleForOrder(e.ctx, tx, o)
			return err
		})
		if charged != 120 {
			t.Fatalf("settle #%d charged %d, want 120", i, charged)
		}
	}
	if n := e.ledgerCount("charge:" + o.String()); n != 1 {
		t.Fatalf("charge entries = %d, want 1", n)
	}
	e.expectWallet(d.DriverID, 380, 0)

	// A settled order cannot be reserved again.
	if err := e.tx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 120); return err }); !errors.Is(err, wallets.ErrAlreadySettled) {
		t.Fatalf("reserve after settle: %v, want ErrAlreadySettled", err)
	}
	e.expectReconciled()
}

func TestZeroCommissionWritesNoLedgerRows(t *testing.T) {
	e := newEnv(t)
	d := e.driver(0)
	o := e.order()

	var res wallets.Reservation
	e.mustTx(func(tx pgx.Tx) (err error) {
		res, err = wallets.Reserve(e.ctx, tx, d.DriverID, o, 0)
		return err
	})
	if res.Status != wallets.ReservationActive || res.Amount != 0 {
		t.Fatalf("reservation %+v", res)
	}
	for i := range 2 {
		var charged int64
		e.mustTx(func(tx pgx.Tx) (err error) {
			charged, err = wallets.SettleForOrder(e.ctx, tx, o)
			return err
		})
		if charged != 0 {
			t.Fatalf("settle #%d charged %d", i, charged)
		}
	}
	var refunded int64
	e.mustTx(func(tx pgx.Tx) (err error) {
		refunded, err = wallets.RefundCommission(e.ctx, tx, o, uuid.Nil)
		return err
	})
	if refunded != 0 {
		t.Fatalf("refunded %d", refunded)
	}
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE wallet_id = $1`, d.WalletID); n != 0 {
		t.Fatalf("ledger rows = %d, want 0", n)
	}
	if n := e.count(`SELECT count(*) FROM wallet_reservations WHERE order_id = $1 AND status = 'settled'`, o); n != 1 {
		t.Fatalf("zero reservation not settled")
	}

	// Zero release: reservation closed, no ledger row.
	o2 := e.order()
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o2, 0); return err })
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.ReleaseForOrder(e.ctx, tx, o2, "x"); return err })
	if n := e.count(`SELECT count(*) FROM wallet_reservations WHERE order_id = $1 AND status = 'released'`, o2); n != 1 {
		t.Fatalf("zero reservation not released")
	}
	e.expectReconciled()
}

func TestReserveInsufficientBalance(t *testing.T) {
	e := newEnv(t)
	d := e.driver(50)
	o := e.order()
	err := e.tx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 100); return err })
	if !errors.Is(err, wallets.ErrInsufficientBalance) {
		t.Fatalf("err = %v, want ErrInsufficientBalance", err)
	}
	if n := e.count(`SELECT count(*) FROM wallet_reservations WHERE order_id = $1`, o); n != 0 {
		t.Fatalf("reservation rows = %d", n)
	}
	e.expectWallet(d.DriverID, 50, 0)

	if err := e.tx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, -1); return err }); !errors.Is(err, wallets.ErrInvalidAmount) {
		t.Fatalf("negative amount: %v", err)
	}
}

func TestReReserveSameDriverAfterRelease(t *testing.T) {
	e := newEnv(t)
	d := e.driver(1000)
	other := e.driver(1000)
	o := e.order()

	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 100); return err })
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.ReleaseForOrder(e.ctx, tx, o, "reassigned"); return err })
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, other.DriverID, o, 100); return err })
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.ReleaseForOrder(e.ctx, tx, o, "reassigned back"); return err })

	var res wallets.Reservation
	e.mustTx(func(tx pgx.Tx) (err error) {
		res, err = wallets.Reserve(e.ctx, tx, d.DriverID, o, 100)
		return err
	})
	e.expectWallet(d.DriverID, 1000, 100)
	e.expectWallet(other.DriverID, 1000, 0)
	if n := e.ledgerCount("reserve:" + o.String() + ":" + d.DriverID.String() + ":" + res.ID.String()); n != 1 {
		t.Fatalf("suffixed reserve key missing")
	}
	var charged int64
	e.mustTx(func(tx pgx.Tx) (err error) {
		charged, err = wallets.SettleForOrder(e.ctx, tx, o)
		return err
	})
	if charged != 100 {
		t.Fatalf("charged %d", charged)
	}
	e.expectWallet(d.DriverID, 900, 0)
	e.expectReconciled()
}

func TestRefundCommission(t *testing.T) {
	e := newEnv(t)
	d := e.driver(1000)
	admin := e.admin()
	o := e.order()
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 100); return err })
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.SettleForOrder(e.ctx, tx, o); return err })
	e.expectWallet(d.DriverID, 900, 0)

	for i := range 2 {
		var refunded int64
		e.mustTx(func(tx pgx.Tx) (err error) {
			refunded, err = wallets.RefundCommission(e.ctx, tx, o, admin)
			return err
		})
		if refunded != 100 {
			t.Fatalf("refund #%d = %d, want 100", i, refunded)
		}
	}
	e.expectWallet(d.DriverID, 1000, 0)
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE idempotency_key = $1 AND admin_id = $2`, "commission_refund:"+o.String(), admin); n != 1 {
		t.Fatalf("refund entries = %d, want 1", n)
	}
	if err := e.tx(func(tx pgx.Tx) error { _, err := wallets.RefundCommission(e.ctx, tx, e.order(), admin); return err }); !errors.Is(err, wallets.ErrNotFound) {
		t.Fatalf("refund of uncharged order: %v, want ErrNotFound", err)
	}
	e.expectReconciled()
}

func TestConcurrentReserveExactlySevenWin(t *testing.T) {
	e := newEnv(t)
	d := e.driver(700)
	const n = 50
	orders := make([]uuid.UUID, n)
	for i := range orders {
		orders[i] = e.order()
	}

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		ok, short  int
		unexpected []error
		start      = make(chan struct{})
	)
	for _, o := range orders {
		wg.Add(1)
		go func(o uuid.UUID) {
			defer wg.Done()
			<-start
			err := e.tx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 100); return err })
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, wallets.ErrInsufficientBalance):
				short++
			default:
				unexpected = append(unexpected, err)
			}
		}(o)
	}
	close(start)
	wg.Wait()
	if len(unexpected) > 0 {
		t.Fatalf("unexpected errors: %v", unexpected)
	}
	if ok != 7 || short != 43 {
		t.Fatalf("succeeded=%d insufficient=%d, want 7 and 43", ok, short)
	}
	e.expectWallet(d.DriverID, 700, 700)
	if c := e.count(`SELECT count(*) FROM wallet_reservations WHERE wallet_id = $1 AND status = 'active'`, d.WalletID); c != 7 {
		t.Fatalf("active reservations = %d, want 7", c)
	}
	if c := e.count(`SELECT count(*) FROM wallet_ledger WHERE wallet_id = $1 AND entry_type = 'commission_reserve'`, d.WalletID); c != 7 {
		t.Fatalf("reserve entries = %d, want 7", c)
	}
	e.expectReconciled()
}

func TestConcurrentSettleChargesOnce(t *testing.T) {
	e := newEnv(t)
	d := e.driver(1000)
	o := e.order()
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 100); return err })

	const n = 20
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		results = make([]int64, n)
		errs    = make([]error, n)
	)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = e.tx(func(tx pgx.Tx) (err error) {
				results[i], err = wallets.SettleForOrder(e.ctx, tx, o)
				return err
			})
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("settle %d: %v", i, errs[i])
		}
		if results[i] != 100 {
			t.Fatalf("settle %d returned %d, want 100", i, results[i])
		}
	}
	if c := e.ledgerCount("charge:" + o.String()); c != 1 {
		t.Fatalf("charge entries = %d, want 1", c)
	}
	e.expectWallet(d.DriverID, 900, 0)
	e.expectReconciled()
}
