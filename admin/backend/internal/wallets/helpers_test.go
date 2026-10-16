package wallets_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

type env struct {
	t        *testing.T
	pool     *pgxpool.Pool
	ctx      context.Context
	cityID   uuid.UUID
	tariffID uuid.UUID
	passID   uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewDB(t)
	e := &env{t: t, pool: pool, ctx: context.Background()}
	e.cityID = testutil.CityID(t, pool, "EMB")
	if err := pool.QueryRow(e.ctx, `SELECT id FROM tariffs WHERE city_id = $1 AND category = 'taxi' ORDER BY version DESC LIMIT 1`, e.cityID).Scan(&e.tariffID); err != nil {
		t.Fatalf("tariff: %v", err)
	}
	e.passID, _ = testutil.CreateUser(t, pool)
	return e
}

func (e *env) driver(balance int64) testutil.DriverFixture {
	e.t.Helper()
	return testutil.CreateDriver(e.t, e.pool, testutil.DriverOptions{Balance: balance})
}

// order inserts a draft order; drafts are outside the one-active-order indexes,
// so a test can create as many as it needs for one passenger.
func (e *env) order() uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(e.ctx, `
		INSERT INTO orders (passenger_id, city_id, category, service_type, cargo_type, status, fare, commission_bps,
		                    commission_amount, payment_method, tariff_id, distance_m, duration_s, stop_count,
		                    pickup_lat, pickup_lon, idempotency_key)
		VALUES ($1, $2, 'taxi', 'passenger', 'none', 'draft', 1000, 1000, 100, 'cash', $3, 1000, 120, 2,
		        48.8264, 58.1442, $4)
		RETURNING id`, e.passID, e.cityID, e.tariffID, uuid.NewString()).Scan(&id)
	if err != nil {
		e.t.Fatalf("create order: %v", err)
	}
	return id
}

func (e *env) admin() uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(e.ctx, `
		INSERT INTO admin_users (email, full_name, password_hash, role)
		VALUES ($1, 'Finance', 'x', 'finance_manager') RETURNING id`, fmt.Sprintf("fin-%s@example.test", uuid.NewString()[:8])).Scan(&id)
	if err != nil {
		e.t.Fatalf("create admin: %v", err)
	}
	return id
}

func (e *env) topup(d testutil.DriverFixture, amount int64) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(e.ctx, `
		INSERT INTO top_up_requests (reference, driver_id, wallet_id, requested_amount, idempotency_key, expires_at)
		VALUES ($1, $2, $3, $4, $5, now() + interval '48 hours') RETURNING id`,
		d.Code+"-TOPUP-"+uuid.NewString()[:6], d.DriverID, d.WalletID, amount, uuid.NewString()).Scan(&id)
	if err != nil {
		e.t.Fatalf("create top-up: %v", err)
	}
	return id
}

func (e *env) tx(fn func(tx pgx.Tx) error) error {
	return db.WithTx(e.ctx, e.pool, fn)
}

func (e *env) mustTx(fn func(tx pgx.Tx) error) {
	e.t.Helper()
	if err := e.tx(fn); err != nil {
		e.t.Fatalf("tx: %v", err)
	}
}

func (e *env) wallet(driverID uuid.UUID) wallets.Wallet {
	e.t.Helper()
	w, err := wallets.Get(e.ctx, e.pool, driverID)
	if err != nil {
		e.t.Fatalf("get wallet: %v", err)
	}
	return w
}

func (e *env) expectWallet(driverID uuid.UUID, balance, reserved int64) {
	e.t.Helper()
	w := e.wallet(driverID)
	if w.Balance != balance || w.Reserved != reserved {
		e.t.Fatalf("wallet = balance %d reserved %d, want balance %d reserved %d", w.Balance, w.Reserved, balance, reserved)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("count: %v", err)
	}
	return n
}

func (e *env) ledgerCount(key string) int {
	return e.count(`SELECT count(*) FROM wallet_ledger WHERE idempotency_key = $1`, key)
}

func (e *env) expectReconciled() {
	e.t.Helper()
	ms, err := wallets.Reconcile(e.ctx, e.pool)
	if err != nil {
		e.t.Fatalf("reconcile: %v", err)
	}
	if len(ms) != 0 {
		e.t.Fatalf("reconcile mismatches: %+v", ms)
	}
}

func timePtr(t time.Time) *time.Time { return &t }
