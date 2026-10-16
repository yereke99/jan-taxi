package reports

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// fixture is a deterministic data set relative to a local base day ten days
// before today, so period counters never overlap with "now" counters.
type fixture struct {
	pool    *pgxpool.Pool
	cityID  uuid.UUID
	loc     *time.Location
	base    time.Time // local midnight, 10 days ago
	now     time.Time
	adminID uuid.UUID
	d1, d2  testutil.DriverFixture
	orders  map[string]uuid.UUID
}

type orderSpec struct {
	cat, status                             string
	driver                                  *uuid.UUID
	fare                                    int
	created                                 time.Time
	accepted, started, completed, cancelled *time.Time
	cancelledBy, reason                     string
	pickup, destination                     string
}

func at(t time.Time) *time.Time { return &t }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testutil.NewDB(t)
	ctx := context.Background()
	f := &fixture{pool: pool, cityID: testutil.CityID(t, pool, "EMB"), loc: loadLocation(DefaultTimezone), now: time.Now(), orders: map[string]uuid.UUID{}}
	f.base = Today(f.now, f.loc).From.AddDate(0, 0, -10)
	b := func(day int, hh, mm int) time.Time {
		return f.base.AddDate(0, 0, day).Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
	}

	if err := pool.QueryRow(ctx, `INSERT INTO admin_users (email, full_name, password_hash, role)
		VALUES ('fixture@jantaxi.test', 'Fixture', 'x', 'finance_manager') RETURNING id`).Scan(&f.adminID); err != nil {
		t.Fatal(err)
	}

	// Drivers: approved online/offline, pending (with application), suspended, changes requested, draft.
	f.d1 = testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "approved", Online: true, Balance: 300})
	f.d2 = testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "approved", Balance: 1000})
	pending := testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "pending"})
	testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "suspended"})
	testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "changes_requested"})
	testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "draft"})
	exec(t, pool, `INSERT INTO driver_applications (driver_id, round, status, snapshot) VALUES ($1, 1, 'pending', '{}')`, pending.DriverID)
	exec(t, pool, `UPDATE wallets SET reserved = 100 WHERE driver_id = $1`, f.d1.DriverID)

	d1, d2 := f.d1.DriverID, f.d2.DriverID
	specs := map[string]orderSpec{
		"o1": {cat: "taxi", status: "completed", driver: &d2, fare: 1000, created: b(0, 10, 0), accepted: at(b(0, 10, 2)),
			started: at(b(0, 10, 10)), completed: at(b(0, 10, 30)),
			pickup: `=HYPERLINK("http://evil.example","click")`, destination: "Ембі, Абай көшесі 5"},
		"o2": {cat: "parcel", status: "completed", driver: &d2, fare: 1500, created: b(1, 9, 0), accepted: at(b(1, 9, 5)),
			started: at(b(1, 9, 10)), completed: at(b(1, 9, 40))},
		"o3": {cat: "other", status: "cancelled", fare: 700, created: b(1, 23, 30), cancelled: at(b(1, 23, 35)), cancelledBy: "passenger", reason: "@SUM(1)"},
		"o4": {cat: "taxi", status: "cancelled", fare: 600, created: b(2, 0, 10), cancelled: at(b(2, 0, 25)), cancelledBy: "system", reason: "no_driver_found"},
		"o5": {cat: "taxi", status: "completed", driver: &d2, fare: 800, created: b(-1, 23, 50), accepted: at(b(-1, 23, 55)),
			started: at(b(0, 0, 5)), completed: at(b(0, 0, 20))},
		"o6": {cat: "taxi", status: "searching", fare: 500, created: Today(f.now, f.loc).From.Add(time.Second)},
		"o7": {cat: "taxi", status: "in_progress", driver: &d1, fare: 1200, created: b(2, 10, 0), accepted: at(b(2, 10, 1)), started: at(b(2, 10, 5))},
		"o8": {cat: "taxi", status: "disputed", driver: &d2, fare: 900, created: b(2, 11, 0), accepted: at(b(2, 11, 1)),
			started: at(b(2, 11, 5)), completed: at(b(2, 11, 30))},
	}
	for name, s := range specs {
		f.orders[name] = f.insertOrder(t, s)
	}
	exec(t, pool, `INSERT INTO disputes (order_id, opened_by_type, reason, previous_status, created_at) VALUES ($1, 'passenger', 'late', 'completed', $2)`,
		f.orders["o8"], b(2, 12, 0))

	// Commission ledger: charges at completion, one refund.
	ledger := []struct {
		order string
		typ   string
		delta int64
		at    time.Time
	}{
		{"o1", "commission_charge", -100, b(0, 10, 30)},
		{"o5", "commission_charge", -80, b(0, 0, 20)},
		{"o2", "commission_charge", -150, b(1, 9, 40)},
		{"o8", "commission_charge", -90, b(2, 11, 30)},
		{"o2", "commission_refund", 150, b(2, 13, 0)},
	}
	for i, l := range ledger {
		exec(t, pool, `
			INSERT INTO wallet_ledger (wallet_id, entry_type, balance_delta, reserved_delta, balance_after, reserved_after,
			                           order_id, idempotency_key, description, created_at)
			VALUES ($1, $2, $3, 0, 10000, 0, $4, $5, $6, $7)`,
			f.d2.WalletID, l.typ, l.delta, f.orders[l.order], fmt.Sprintf("fixture:%d", i), "@SUM(1)", l.at)
	}

	// Top-ups: confirmed day 1, confirmed and refunded day 2, one waiting for verification.
	f.insertTopUp(t, "confirmed", 2000, at(b(1, 12, 0)), nil, b(1, 11, 0))
	f.insertTopUp(t, "refunded", 1000, at(b(2, 9, 0)), at(b(2, 15, 0)), b(2, 8, 0))
	f.insertTopUp(t, "pending_verification", 500, nil, nil, b(2, 16, 0))

	exec(t, pool, `INSERT INTO notification_outbox (bot, user_id, chat_id, template, dedupe_key, status) VALUES
		('driver', $1, 1, 'topup_created', 'fixture:1', 'pending'),
		('driver', $1, 1, 'topup_created', 'fixture:2', 'dead'),
		('driver', $1, 1, 'topup_created', 'fixture:3', 'sent')`, f.d1.UserID)
	return f
}

func (f *fixture) insertOrder(t *testing.T, s orderSpec) uuid.UUID {
	t.Helper()
	passenger, _ := testutil.CreateUser(t, f.pool)
	service, cargo := "passenger", "none"
	var desc, recipient any
	ack := false
	switch s.cat {
	case "parcel":
		service, cargo, desc, recipient, ack = "delivery", "parcel", "Құжаттар", "+77011234567", true
	case "other":
		service, cargo, desc, ack = "delivery", "other", "Кілттер", true
	}
	var cancelledBy, reason any
	if s.cancelledBy != "" {
		cancelledBy, reason = s.cancelledBy, s.reason
	}
	ctx := context.Background()
	var tariffID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM tariffs WHERE city_id = $1 AND category = $2 AND is_active`, f.cityID, s.cat).Scan(&tariffID); err != nil {
		t.Fatalf("tariff: %v", err)
	}
	var id uuid.UUID
	err := f.pool.QueryRow(ctx, `
		INSERT INTO orders (passenger_id, city_id, category, service_type, cargo_type, cargo_description, recipient_phone,
		                    prohibited_items_ack, status, driver_id, fare, commission_bps, commission_amount, payment_method,
		                    tariff_id, distance_m, duration_s, stop_count, pickup_lat, pickup_lon, idempotency_key,
		                    created_at, accepted_at, started_at, completed_at, cancelled_at, cancelled_by, cancel_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 1000, $12, 'cash',
		        $13, 3200, 540, 2, 48.8264, 58.1442, $14, $15, $16, $17, $18, $19, $20, $21)
		RETURNING id`,
		passenger, f.cityID, s.cat, service, cargo, desc, recipient, ack, s.status, s.driver, s.fare, s.fare/10,
		tariffID, uuid.NewString(), s.created, s.accepted, s.started, s.completed, s.cancelled, cancelledBy, reason).Scan(&id)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	if s.pickup != "" {
		exec(t, f.pool, `INSERT INTO order_stops (order_id, seq, kind, lat, lon, address) VALUES
			($1, 0, 'pickup', 48.8264, 58.1442, $2), ($1, 1, 'destination', 48.83, 58.15, $3)`, id, s.pickup, s.destination)
	}
	return id
}

func (f *fixture) insertTopUp(t *testing.T, status string, amount int64, confirmed, refunded *time.Time, created time.Time) {
	t.Helper()
	var verified, txn, by any
	if confirmed != nil {
		verified, txn, by = amount, "FIX-"+uuid.NewString()[:8], f.adminID
	}
	var refundedBy any
	if refunded != nil {
		refundedBy = f.adminID
	}
	var n int64
	if err := f.pool.QueryRow(context.Background(), `SELECT nextval('topup_number_seq')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	exec(t, f.pool, `
		INSERT INTO top_up_requests (number, reference, driver_id, wallet_id, requested_amount, status, verified_amount,
		                             provider_transaction_id, confirmed_by, confirmed_at, refunded_by, refunded_at,
		                             idempotency_key, expires_at, created_at, driver_payment_reference)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, '+cmd')`,
		n, fmt.Sprintf("%s-TOPUP-%06d", f.d2.Code, n), f.d2.DriverID, f.d2.WalletID, amount, status, verified, txn, by,
		confirmed, refundedBy, refunded, uuid.NewString(), created.Add(48*time.Hour), created)
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// adminHandler mounts the reports routes as an admin with role.
func adminHandler(t *testing.T, pool *pgxpool.Pool, role auth.Role) http.Handler {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO admin_users (email, full_name, password_hash, role)
		VALUES ($1, 'Reports', 'x', $2) RETURNING id`, uuid.NewString()[:8]+"@jantaxi.test", string(role)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	p := auth.AdminPrincipal{AdminID: id, Email: "reports@jantaxi.test", Role: role, SessionID: uuid.New()}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithAdmin(req.Context(), p)))
		})
	})
	NewHandler(pool).MountAdmin(r)
	return r
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}
