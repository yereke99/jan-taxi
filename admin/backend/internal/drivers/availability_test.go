package drivers_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

const pathAvailability = "/api/v1/driver/availability"

func TestAvailabilityRules(t *testing.T) {
	env := driverstest.New(t)
	ctx := context.Background()

	for _, status := range []string{"draft", "pending", "changes_requested", "rejected"} {
		d := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Status: status, Balance: 1000})
		res := env.AppClient(d.UserID, auth.AppDriver).Do(http.MethodPatch, pathAvailability, `{"online":true}`)
		if res.Code != http.StatusForbidden || res.ErrorCode() != "driver_not_approved" || res.Details()["reason"] != "not_approved" {
			t.Fatalf("%s goes online: %s", status, res)
		}
	}
	suspended := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Status: "suspended", Balance: 1000})
	if _, err := drivers.SetAvailability(ctx, env.Pool, suspended.DriverID, true); err == nil {
		t.Fatal("suspended driver went online")
	}

	// Below driver.min_online_balance (seed: 50) → 402 with the amounts.
	poor := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Balance: 40})
	c := env.AppClient(poor.UserID, auth.AppDriver)
	res := c.Do(http.MethodPatch, pathAvailability, `{"online":true}`)
	if res.Code != http.StatusPaymentRequired || res.ErrorCode() != "insufficient_balance" ||
		res.Details()["required"] != float64(50) || res.Details()["available"] != float64(40) || res.Details()["topup_min_amount"] != float64(500) {
		t.Fatalf("low balance: %s", res)
	}
	if res := c.Do(http.MethodPatch, pathAvailability, `{}`); res.Code != 422 || res.Fields()["online"] != "required" {
		t.Fatalf("missing online: %s", res)
	}

	rich := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Balance: 500})
	c = env.AppClient(rich.UserID, auth.AppDriver)
	res = c.Do(http.MethodPatch, pathAvailability, `{"online":true}`)
	if res.Code != http.StatusOK || res.Body["is_online"] != true || res.Body["online_since"] == nil || res.Body["can_go_online"] != true ||
		res.Body["available"] != float64(500) || res.Body["min_online_balance"] != float64(50) || res.Body["currency"] != "KZT" {
		t.Fatalf("go online: %s", res)
	}
	if res := c.Do(http.MethodPatch, pathAvailability, `{"online":true}`); res.Code != http.StatusOK || res.Body["is_online"] != true {
		t.Fatalf("online again: %s", res)
	}
	res = c.Do(http.MethodPatch, pathAvailability, `{"online":false}`)
	if res.Code != http.StatusOK || res.Body["is_online"] != false || res.Body["online_since"] != nil {
		t.Fatalf("go offline: %s", res)
	}

	// A city that dispatches by location needs a fresh fix first.
	if _, err := env.Pool.Exec(ctx, `UPDATE cities SET require_driver_location = true, driver_location_max_age_s = 300 WHERE code = 'EMB'`); err != nil {
		t.Fatal(err)
	}
	res = c.Do(http.MethodPatch, pathAvailability, `{"online":true}`)
	if res.Code != http.StatusUnprocessableEntity || res.ErrorCode() != "location_required" || res.Details()["max_age_s"] != float64(300) {
		t.Fatalf("online without location: %s", res)
	}
	stale := time.Now().Add(-10 * time.Minute)
	if err := drivers.RecordLocation(ctx, env.Pool, rich.DriverID, drivers.LocationInput{Lat: 48.8264, Lon: 58.1442, RecordedAt: stale}, drivers.SourceBotStatic); err != nil {
		t.Fatal(err)
	}
	if res := c.Do(http.MethodPatch, pathAvailability, `{"online":true}`); res.ErrorCode() != "location_required" {
		t.Fatalf("online with stale location: %s", res)
	}
	if res := c.Do(http.MethodPost, "/api/v1/driver/location", `{"lat":48.8270,"lon":58.1450,"accuracy_m":12}`); res.Code != http.StatusNoContent {
		t.Fatalf("report location: %s", res)
	}
	if res := c.Do(http.MethodPatch, pathAvailability, `{"online":true}`); res.Code != http.StatusOK || res.Body["is_online"] != true {
		t.Fatalf("online with fresh location: %s", res)
	}
}

func TestStaleDriverJob(t *testing.T) {
	env := driverstest.New(t)
	ctx := context.Background()
	silent := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true, Balance: 100})
	active := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{Online: true, Balance: 100})
	if _, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET last_heartbeat_at = now() - interval '13 hours' WHERE id = $1`, silent.DriverID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Pool.Exec(ctx, `UPDATE driver_profiles SET last_heartbeat_at = now() - interval '1 hour' WHERE id = $1`, active.DriverID); err != nil {
		t.Fatal(err)
	}
	timeout := drivers.IdleTimeout(ctx, env.Pool, 12*time.Hour)
	if timeout != 12*time.Hour {
		t.Fatalf("idle timeout from env = %v", timeout)
	}
	n, err := drivers.SetStaleDriversOffline(ctx, env.Pool, timeout)
	if err != nil || n != 1 {
		t.Fatalf("stale job: n=%d err=%v", n, err)
	}
	if env.Count(`SELECT count(*) FROM driver_profiles WHERE id = $1 AND NOT is_online AND online_since IS NULL`, silent.DriverID) != 1 ||
		env.Count(`SELECT count(*) FROM driver_profiles WHERE id = $1 AND is_online`, active.DriverID) != 1 {
		t.Fatal("wrong drivers switched off")
	}
	// An admin override (hours) wins over the environment default.
	if _, err := env.Pool.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ('driver.online_idle_hours', '1')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	if got := drivers.IdleTimeout(ctx, env.Pool, 12*time.Hour); got != time.Hour {
		t.Fatalf("idle timeout with override = %v", got)
	}
	if n, err := drivers.SetStaleDriversOffline(ctx, env.Pool, 30*time.Minute); err != nil || n != 1 {
		t.Fatalf("second run: n=%d err=%v", n, err)
	}
}

func TestPruneLocationHistory(t *testing.T) {
	env := driverstest.New(t)
	ctx := context.Background()
	d := testutil.CreateDriver(t, env.Pool, testutil.DriverOptions{})
	if _, err := env.Pool.Exec(ctx, `
		INSERT INTO driver_location_history (driver_id, lat, lon, source, recorded_at, received_at)
		SELECT $1, 48.8, 58.1, 'miniapp', t, t FROM unnest(ARRAY[now() - interval '31 days', now() - interval '40 days', now() - interval '1 day']) t`,
		d.DriverID); err != nil {
		t.Fatal(err)
	}
	n, err := drivers.PruneLocationHistory(ctx, env.Pool, drivers.LocationHistoryRetention)
	if err != nil || n != 2 {
		t.Fatalf("prune: n=%d err=%v", n, err)
	}
	if env.Count(`SELECT count(*) FROM driver_location_history`) != 1 {
		t.Fatal("recent history removed")
	}
}
