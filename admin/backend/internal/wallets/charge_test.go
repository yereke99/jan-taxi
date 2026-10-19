package wallets_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

func (e *env) charge(driverID, orderID uuid.UUID, amount int64, assignment int) (int64, error) {
	var charged int64
	err := e.tx(func(tx pgx.Tx) (err error) {
		charged, err = wallets.ChargeOnAccept(e.ctx, tx, driverID, orderID, amount, assignment)
		return err
	})
	return charged, err
}

func TestChargeOnAccept(t *testing.T) {
	e := newEnv(t)
	d := e.driver(500)
	o := e.order()

	charged, err := e.charge(d.DriverID, o, 50, 2)
	if err != nil || charged != 50 {
		t.Fatalf("charge = %d %v", charged, err)
	}
	e.expectWallet(d.DriverID, 450, 0)
	if n := e.ledgerCount("charge:" + o.String()); n != 1 {
		t.Fatalf("charge entries = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM wallet_reservations WHERE order_id = $1`, o); n != 0 {
		t.Fatalf("a reservation was created")
	}
	// The same assignment charged again is recognised, not charged twice.
	if again, err := e.charge(d.DriverID, o, 50, 2); err != nil || again != 50 {
		t.Fatalf("repeated charge = %d %v", again, err)
	}
	e.expectWallet(d.DriverID, 450, 0)

	// Completion owes nothing more and reports the commission paid.
	var settled int64
	e.mustTx(func(tx pgx.Tx) (err error) {
		settled, err = wallets.SettleOnComplete(e.ctx, tx, o, d.DriverID)
		return err
	})
	if settled != 50 {
		t.Fatalf("settle on complete = %d", settled)
	}
	e.expectWallet(d.DriverID, 450, 0)
	if paid, err := wallets.ChargedAmount(e.ctx, e.pool, o, d.DriverID); err != nil || paid != 50 {
		t.Fatalf("charged amount = %d %v", paid, err)
	}
	e.expectReconciled()
}

func TestChargeOnAcceptInsufficientAndZero(t *testing.T) {
	e := newEnv(t)
	d := e.driver(40)
	o := e.order()
	if _, err := e.charge(d.DriverID, o, 50, 2); !errors.Is(err, wallets.ErrInsufficientBalance) {
		t.Fatalf("err = %v, want ErrInsufficientBalance", err)
	}
	e.expectWallet(d.DriverID, 40, 0)
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1`, o); n != 0 {
		t.Fatalf("ledger rows = %d", n)
	}
	// Reserved money (an order accepted under the earlier model) is not available.
	legacy := e.order()
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, legacy, 30); return err })
	if _, err := e.charge(d.DriverID, o, 20, 2); !errors.Is(err, wallets.ErrInsufficientBalance) {
		t.Fatalf("charge over reserved money: %v", err)
	}
	if charged, err := e.charge(d.DriverID, o, 0, 2); err != nil || charged != 0 {
		t.Fatalf("zero charge = %d %v", charged, err)
	}
	if _, err := e.charge(d.DriverID, o, -1, 2); !errors.Is(err, wallets.ErrInvalidAmount) {
		t.Fatalf("negative charge: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1`, o); n != 0 {
		t.Fatalf("ledger rows after zero charge = %d", n)
	}
	e.expectReconciled()
}

// An operator re-assigning an order charges the new driver under its own
// key; refunds go to the driver named and only once.
func TestChargeReassignAndRefund(t *testing.T) {
	e := newEnv(t)
	a, b := e.driver(500), e.driver(500)
	admin := e.admin()
	o := e.order()
	if _, err := e.charge(a.DriverID, o, 50, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.charge(b.DriverID, o, 50, 3); err != nil {
		t.Fatal(err)
	}
	if n := e.ledgerCount("charge:" + o.String() + ":3"); n != 1 {
		t.Fatalf("re-assignment charge key missing")
	}
	e.expectWallet(a.DriverID, 450, 0)
	e.expectWallet(b.DriverID, 450, 0)

	for i := range 2 {
		var refunded int64
		e.mustTx(func(tx pgx.Tx) (err error) {
			refunded, err = wallets.RefundCommission(e.ctx, tx, o, a.DriverID, admin)
			return err
		})
		if refunded != 50 {
			t.Fatalf("refund #%d = %d", i, refunded)
		}
	}
	e.expectWallet(a.DriverID, 500, 0)
	e.expectWallet(b.DriverID, 450, 0)
	if n := e.ledgerCount("commission_refund:" + o.String()); n != 1 {
		t.Fatalf("refund of the first charge missing")
	}
	var refunded int64
	e.mustTx(func(tx pgx.Tx) (err error) {
		refunded, err = wallets.RefundCommission(e.ctx, tx, o, b.DriverID, admin)
		return err
	})
	if refunded != 50 || e.ledgerCount("commission_refund:"+o.String()+":3") != 1 {
		t.Fatalf("refund of the re-assignment = %d", refunded)
	}
	e.expectWallet(b.DriverID, 500, 0)
	e.expectReconciled()
}

// An order accepted under the earlier model keeps its reservation; completion
// charges it once and never on top of an acceptance charge.
func TestSettleOnCompleteLegacyReservation(t *testing.T) {
	e := newEnv(t)
	d := e.driver(500)
	o := e.order()
	e.mustTx(func(tx pgx.Tx) error { _, err := wallets.Reserve(e.ctx, tx, d.DriverID, o, 100); return err })
	e.expectWallet(d.DriverID, 500, 100)
	for i := range 2 {
		var charged int64
		e.mustTx(func(tx pgx.Tx) (err error) {
			charged, err = wallets.SettleOnComplete(e.ctx, tx, o, d.DriverID)
			return err
		})
		if charged != 100 {
			t.Fatalf("settle #%d = %d", i, charged)
		}
	}
	e.expectWallet(d.DriverID, 400, 0)
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge'`, o); n != 1 {
		t.Fatalf("charges = %d, want 1", n)
	}
	// An order without any charge settles to zero.
	var none int64
	e.mustTx(func(tx pgx.Tx) (err error) {
		none, err = wallets.SettleOnComplete(e.ctx, tx, e.order(), d.DriverID)
		return err
	})
	if none != 0 {
		t.Fatalf("settle of an uncharged order = %d", none)
	}
	e.expectReconciled()
}

// Concurrent charges of one wallet never take more than the balance: 500 KZT
// pays for exactly ten 50 KZT commissions.
func TestConcurrentChargesNeverOverdraw(t *testing.T) {
	e := newEnv(t)
	d := e.driver(500)
	const n = 30
	orders := make([]uuid.UUID, n)
	for i := range orders {
		orders[i] = e.order()
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ok, low int
	)
	for i := range n {
		wg.Add(1)
		go func(o uuid.UUID) {
			defer wg.Done()
			_, err := e.charge(d.DriverID, o, 50, 2)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, wallets.ErrInsufficientBalance):
				low++
			default:
				t.Errorf("charge: %v", err)
			}
		}(orders[i])
	}
	wg.Wait()
	if ok != 10 || low != n-10 {
		t.Fatalf("charged %d, refused %d; want 10 and %d", ok, low, n-10)
	}
	e.expectWallet(d.DriverID, 0, 0)
	e.expectReconciled()
}

func TestWelcomeBonusCredit(t *testing.T) {
	e := newEnv(t)
	d := e.driver(0)
	admin := e.admin()
	for range 2 {
		e.mustTx(func(tx pgx.Tx) error { return wallets.CreditWelcomeBonus(e.ctx, tx, d.DriverID, 500, admin) })
	}
	e.expectWallet(d.DriverID, 500, 0)
	if n := e.count(`SELECT count(*) FROM wallet_ledger WHERE idempotency_key = $1 AND entry_type = 'bonus_credit' AND admin_id = $2`,
		"welcome_bonus:"+d.DriverID.String(), admin); n != 1 {
		t.Fatalf("bonus entries = %d", n)
	}
	if err := e.tx(func(tx pgx.Tx) error { return wallets.CreditWelcomeBonus(e.ctx, tx, d.DriverID, 600, admin) }); !errors.Is(err, wallets.ErrIdempotencyConflict) {
		t.Fatalf("second bonus with another amount: %v", err)
	}
	if err := e.tx(func(tx pgx.Tx) error { return wallets.CreditWelcomeBonus(e.ctx, tx, d.DriverID, 0, admin) }); !errors.Is(err, wallets.ErrInvalidAmount) {
		t.Fatalf("zero bonus: %v", err)
	}
	if !wallets.ValidEntryType(wallets.EntryBonusCredit) {
		t.Fatal("bonus_credit is not a known entry type")
	}
	e.expectReconciled()
}
