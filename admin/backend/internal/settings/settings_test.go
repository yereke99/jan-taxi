package settings

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestGettersReadSeed(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()

	if got := Int(ctx, pool, "topup.min_amount", 1); got != 500 {
		t.Errorf("Int = %d, want 500", got)
	}
	if got := Int(ctx, pool, "missing.key", 7); got != 7 {
		t.Errorf("Int default = %d, want 7", got)
	}
	if got := Bool(ctx, pool, "driver.require_phone_verification", false); !got {
		t.Error("Bool = false, want true")
	}
	if got := Strings(ctx, pool, "delivery.prohibited_items", nil); len(got) != 8 || got[0] != "weapons" {
		t.Errorf("Strings = %v", got)
	}
	if got := String(ctx, pool, "support.phone", "x"); got != "" {
		t.Errorf("String = %q, want empty", got)
	}
	if got := Text(ctx, pool, "support.hours", Localized{}); got.KK != "Күн сайын 08:00–23:00" {
		t.Errorf("Text = %+v", got)
	}
	var presets []int64
	found, err := JSON(ctx, pool, "topup.presets", &presets)
	if err != nil || !found || !reflect.DeepEqual(presets, []int64{1000, 2000, 5000, 10000}) {
		t.Errorf("JSON = %v %v %v", presets, found, err)
	}
	found, err = JSON(ctx, pool, "missing.key", &presets)
	if err != nil || found {
		t.Errorf("JSON missing = %v %v", found, err)
	}
	// Malformed stored values fall back to the default instead of failing.
	if _, err := pool.Exec(ctx, `UPDATE app_settings SET value = '"many"' WHERE key = 'topup.min_amount'`); err != nil {
		t.Fatal(err)
	}
	if got := Int(ctx, pool, "topup.min_amount", 42); got != 42 {
		t.Errorf("Int malformed = %d, want 42", got)
	}
}

func TestDispatchSettings(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	want := DispatchSettings{RadiusBaseM: 5000, RadiusStepM: 2500, RadiusStepS: 60, RadiusMaxM: 10000, LocationMaxAgeS: 300}
	if got := Dispatch(ctx, pool); got != want {
		t.Fatalf("defaults = %+v", got)
	}
	for k, v := range map[string]any{"dispatch.radius_base_m": 3000, "dispatch.radius_step_m": 2000,
		"dispatch.radius_step_s": 30, "dispatch.radius_max_m": 15000, "dispatch.location_max_age_s": 120} {
		if err := Set(ctx, pool, k, v, uuid.Nil); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	want = DispatchSettings{RadiusBaseM: 3000, RadiusStepM: 2000, RadiusStepS: 30, RadiusMaxM: 15000, LocationMaxAgeS: 120}
	if got := Dispatch(ctx, pool); got != want {
		t.Fatalf("stored = %+v", got)
	}
	// A maximum below the base (written around the admin API) never shrinks
	// the base radius.
	if _, err := pool.Exec(ctx, `UPDATE app_settings SET value = '1000' WHERE key = 'dispatch.radius_max_m'`); err != nil {
		t.Fatal(err)
	}
	if got := Dispatch(ctx, pool); got.RadiusMaxM != 3000 || got.RadiusAt(time.Hour) != 3000 {
		t.Fatalf("max below base = %+v", got)
	}
}

func TestSetAndReset(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	adminID := createAdmin(t, pool, "super_admin")

	if err := Set(ctx, pool, "topup.presets", []int{3000, 1000}, adminID); err != nil {
		t.Fatal(err)
	}
	var presets []int64
	if _, err := JSON(ctx, pool, "topup.presets", &presets); err != nil || !reflect.DeepEqual(presets, []int64{1000, 3000}) {
		t.Fatalf("presets = %v (%v)", presets, err)
	}
	var by uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT updated_by FROM app_settings WHERE key = 'topup.presets'`).Scan(&by); err != nil || by != adminID {
		t.Fatalf("updated_by = %v (%v)", by, err)
	}

	err := Set(ctx, pool, "routing.max_points", 9, uuid.Nil)
	var he *httpx.Error
	if !errors.As(err, &he) || he.Fields["routing.max_points"] != ReasonOutOfRange {
		t.Fatalf("out of range error = %v", err)
	}
	if err := Set(ctx, pool, "no.such.key", 1, uuid.Nil); !errors.As(err, &he) || he.Fields["no.such.key"] != ReasonUnknown {
		t.Fatalf("unknown key error = %v", err)
	}

	if err := Reset(ctx, pool, "topup.presets"); err != nil {
		t.Fatal(err)
	}
	if found, _ := JSON(ctx, pool, "topup.presets", &presets); found {
		t.Fatal("reset left the row")
	}
	if got := TopUp(ctx, pool).Presets; !reflect.DeepEqual(got, []int64{1000, 2000, 5000, 10000}) {
		t.Fatalf("presets after reset = %v", got)
	}
}

func TestCache(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	SetCacheTTL(time.Minute)
	t.Cleanup(func() { SetCacheTTL(0) })

	if got := Int(ctx, pool, "topup.max_amount", 0); got != 100000 {
		t.Fatalf("initial = %d", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE app_settings SET value = '200000' WHERE key = 'topup.max_amount'`); err != nil {
		t.Fatal(err)
	}
	if got := Int(ctx, pool, "topup.max_amount", 0); got != 100000 {
		t.Fatalf("cached read = %d, want stale 100000", got)
	}
	// Transactions bypass the cache and never populate it.
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if got := Int(ctx, tx, "topup.max_amount", 0); got != 200000 {
			t.Errorf("tx read = %d, want 200000", got)
		}
		if _, err := tx.Exec(ctx, `UPDATE app_settings SET value = '300000' WHERE key = 'topup.max_amount'`); err != nil {
			return err
		}
		if got := Int(ctx, tx, "topup.max_amount", 0); got != 300000 {
			t.Errorf("tx read own write = %d", got)
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	Invalidate("topup.max_amount")
	if got := Int(ctx, pool, "topup.max_amount", 0); got != 200000 {
		t.Fatalf("after invalidate = %d, want 200000", got)
	}
	// Set invalidates its own key.
	if err := Set(ctx, pool, "topup.max_amount", 150000, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if got := Int(ctx, pool, "topup.max_amount", 0); got != 150000 {
		t.Fatalf("after Set = %d, want 150000", got)
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	SetCacheTTL(time.Millisecond)
	t.Cleanup(func() { SetCacheTTL(0) })
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 50 {
				if got := Int(ctx, pool, "topup.expiry_hours", 0); got != 48 {
					t.Errorf("read %d", got)
					return
				}
				if (i+j)%10 == 0 {
					Invalidate()
				}
			}
		}()
	}
	wg.Wait()
}

func TestEffectiveHelpers(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	Configure(Env{
		KaspiPaymentURL: "https://pay.kaspi.kz/pay/env", KaspiRecipientName: "ИП Env",
		TwoGISBaseURL: "https://2gis.kz", DriverOnlineIdleTimeout: 6 * time.Hour, MaxRoutePoints: 5,
	})
	t.Cleanup(func() { Configure(Env{}) })

	// The payee seeded by migration 00006 wins over the environment.
	k := KaspiPayment(ctx, pool)
	if k.PaymentURL != "https://pay.kaspi.kz/pay/xbp1imhd" || k.RecipientName != `ИП "JAN-TAXI"` {
		t.Fatalf("seeded kaspi = %+v", k)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM app_settings WHERE key IN ('kaspi.payment_url', 'kaspi.recipient_name')`); err != nil {
		t.Fatal(err)
	}

	k = KaspiPayment(ctx, pool)
	if k.PaymentURL != "https://pay.kaspi.kz/pay/env" || k.RecipientName != "ИП Env" || k.Instructions.KK == "" {
		t.Fatalf("env kaspi = %+v", k)
	}
	if err := Set(ctx, pool, "kaspi.payment_url", "https://pay.kaspi.kz/pay/db", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if got := KaspiPayment(ctx, pool).PaymentURL; got != "https://pay.kaspi.kz/pay/db" {
		t.Fatalf("db override = %q", got)
	}
	if got := TwoGISBaseURL(ctx, pool); got != "https://2gis.kz" {
		t.Errorf("2gis = %q", got)
	}
	if got := DriverOnlineIdleTimeout(ctx, pool); got != 6*time.Hour {
		t.Errorf("idle = %v", got)
	}
	if got := MaxRoutePoints(ctx, pool); got != 5 {
		t.Errorf("max points = %d", got)
	}
	if got := MinOnlineBalance(ctx, pool); got != 50 {
		t.Errorf("min online balance = %d", got)
	}
	if got := TopUpExpiry(ctx, pool); got != 48*time.Hour {
		t.Errorf("topup expiry = %v", got)
	}
	// An invalid stored value is ignored in favour of the default.
	if _, err := pool.Exec(ctx, `UPDATE app_settings SET value = '99' WHERE key = 'routing.max_points'`); err != nil {
		t.Fatal(err)
	}
	if got := MaxRoutePoints(ctx, pool); got != 5 {
		t.Errorf("max points with invalid row = %d", got)
	}
	// Presets outside [min, max] are not offered.
	if _, err := pool.Exec(ctx, `UPDATE app_settings SET value = '2000' WHERE key = 'topup.min_amount'`); err != nil {
		t.Fatal(err)
	}
	if got := TopUp(ctx, pool); got.Min != 2000 || !reflect.DeepEqual(got.Presets, []int64{2000, 5000, 10000}) {
		t.Errorf("topup = %+v", got)
	}
}

func createAdmin(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, role string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role)
		VALUES ($1, 'Test Admin', 'x', $2) RETURNING id`, uuid.NewString()+"@example.com", role).Scan(&id)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return id
}

func TestPayeeAndAutoConfirmDefaults(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	c := CompanyDetails(ctx, pool)
	want := Company{Name: `ИП "JAN-TAXI"`, BIN: "000809500289", IBAN: "KZ05722S000057176693", Bank: `АО "Kaspi Bank"`,
		BIC: "CASPKZKA", KBe: "19", Address: "Жалагашский район, с. Жалагаш, ул. Нығмет Мырзалиев, 94"}
	if c != want {
		t.Fatalf("company = %+v", c)
	}
	a := ReceiptAutoConfirm(ctx, pool)
	if !a.Enabled || a.MaxPerDay != 3 || a.MaxAmountPerDay != 50000 || a.ReceiptMaxAge != 48*time.Hour {
		t.Fatalf("auto confirm = %+v", a)
	}
	// The built-in defaults match the seed, so a reset changes nothing.
	for _, k := range []string{"company.bin", "company.iban", "topup.auto_confirm_enabled", "topup.auto_max_per_day"} {
		if err := Reset(ctx, pool, k); err != nil {
			t.Fatal(err)
		}
	}
	if CompanyDetails(ctx, pool) != want || ReceiptAutoConfirm(ctx, pool) != a {
		t.Fatal("defaults differ from the seed")
	}
	if err := Set(ctx, pool, "topup.auto_confirm_enabled", false, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if ReceiptAutoConfirm(ctx, pool).Enabled {
		t.Fatal("kill switch ignored")
	}
}
