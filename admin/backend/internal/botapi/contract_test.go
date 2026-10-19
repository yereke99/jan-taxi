package botapi_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/app"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
)

// TestClientAgainstWiredApp drives the internal listener of the fully wired
// API with the bots' typed client, so routes, authentication and JSON field
// names are checked end to end.
func TestClientAgainstWiredApp(t *testing.T) {
	pool := testutil.NewDB(t)
	cfg := config.ForTesting("")
	cfg.FileStorageDir = t.TempDir()
	cfg.PassengerMiniAppURL = "https://app.example.kz/passenger/"
	a, err := app.NewWithPool(context.Background(), cfg, slog.New(slog.DiscardHandler), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	srv := httptest.NewServer(a.InternalHandler())
	t.Cleanup(srv.Close)
	ctx := context.Background()

	drv := botapi.New(srv.URL, config.TestServiceSecret, botapi.BotDriver)
	pas := botapi.New(srv.URL, config.TestServiceSecret, botapi.BotPassenger)

	// Wrong secret.
	bad := botapi.New(srv.URL, "wrong-secret-0123456789abcdef0123", botapi.BotDriver)
	if _, err := bad.Config(ctx); botapi.ErrorCode(err) != "unauthorized" {
		t.Fatalf("wrong secret: %v", err)
	}

	// Lease, claim, offset.
	l, err := drv.AcquireLease(ctx, "driver-bot-a", 30*time.Second)
	if err != nil || !l.Acquired || l.LastUpdateID != 0 || l.LeaseExpiresAt == nil {
		t.Fatalf("lease: %+v %v", l, err)
	}
	if l, err := drv.AcquireLease(ctx, "driver-bot-b", 30*time.Second); err != nil || l.Acquired {
		t.Fatalf("second instance: %+v %v", l, err)
	}
	if ok, err := drv.ClaimUpdate(ctx, 901); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, err := drv.ClaimUpdate(ctx, 901); err != nil || ok {
		t.Fatalf("second claim: %v %v", ok, err)
	}
	if n, err := drv.SaveOffset(ctx, "driver-bot-a", 901); err != nil || n != 901 {
		t.Fatalf("offset: %d %v", n, err)
	}
	if _, err := drv.SaveOffset(ctx, "driver-bot-b", 950); botapi.ErrorCode(err) != "conflict" {
		t.Fatalf("non-holder offset: %v", err)
	}

	// Users and language.
	d := testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "approved", Balance: 1000})
	u, err := drv.SyncUser(ctx, botapi.SyncUserRequest{TelegramUserID: d.TelegramID, FirstName: "Test", Started: true})
	if err != nil || u.Language != "kk" || u.Driver == nil || !u.Driver.Registered || u.Driver.Code == nil || *u.Driver.Code != d.Code {
		t.Fatalf("sync: %+v %v", u, err)
	}
	if lang, err := drv.SetLanguage(ctx, d.TelegramID, "en"); err != nil || lang != "en" {
		t.Fatalf("language: %q %v", lang, err)
	}

	// Driver actions. Going online needs a fresh location: a live location
	// shared in the bot provides it.
	_, err = drv.SetAvailability(ctx, d.TelegramID, true)
	if e, ok := botapi.AsError(err); !ok || e.Status != 422 || e.Code != "location_required" {
		t.Fatalf("online without location: %v", err)
	} else if age, ok := e.DetailInt64("max_age_s"); !ok || age != 300 {
		t.Fatalf("location max age: %v", e.Details)
	}
	if err := drv.ReportLocation(ctx, d.TelegramID, botapi.LocationReport{Lat: 48.8264, Lon: 58.1442, Live: true}); err != nil {
		t.Fatalf("location: %v", err)
	}
	av, err := drv.SetAvailability(ctx, d.TelegramID, true)
	if err != nil || !av.IsOnline || !av.CanGoOnline || av.Available != 1000 {
		t.Fatalf("availability: %+v %v", av, err)
	}
	s, err := drv.DriverSummary(ctx, d.TelegramID)
	if err != nil || !s.Registered || s.StatusValue() != "approved" || !s.IsOnline || s.Language != "en" || s.Wallet == nil || s.Wallet.Available != 1000 || s.Vehicle == nil {
		t.Fatalf("summary: %+v %v", s, err)
	}
	orders, err := drv.DriverOrders(ctx, d.TelegramID, 5)
	if err != nil || !orders.Eligibility.Eligible || orders.Items == nil {
		t.Fatalf("orders: %+v %v", orders, err)
	}
	_, err = drv.VerifyContact(ctx, d.TelegramID, "77071234567", d.TelegramID+1)
	if e, ok := botapi.AsError(err); !ok || e.Status != 403 || e.DetailString("reason") != "contact_mismatch" {
		t.Fatalf("contact mismatch: %v", err)
	}
	poor := testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "approved", Balance: 10})
	_, err = drv.SetAvailability(ctx, poor.TelegramID, true)
	if e, ok := botapi.AsError(err); !ok || e.Code != "insufficient_balance" {
		t.Fatalf("insufficient balance: %v", err)
	} else if req, ok := e.DetailInt64("required"); !ok || req != 50 {
		t.Fatalf("required amount: %v", e.Details)
	}

	// «Алу» on an order the driver cannot see.
	if _, err := drv.AcceptOrder(ctx, d.TelegramID, "0a6f4c4e-6f1b-4b8e-9a52-3c1d2e7f9b10"); botapi.ErrorCode(err) != "not_found" {
		t.Fatalf("accept unknown order: %v", err)
	}

	// Passenger side and the X-Jantaxi-Bot route restrictions.
	if items, err := pas.PassengerOrders(ctx, d.TelegramID, 5); err != nil || len(items) != 0 {
		t.Fatalf("passenger orders: %v %v", items, err)
	}
	_, ptg := testutil.CreateUser(t, pool)
	if res, err := pas.VerifyPassengerContact(ctx, ptg, "+77015556677", ptg); err != nil || res.Phone != "+77015556677" || !res.PhoneVerified {
		t.Fatalf("passenger contact: %+v %v", res, err)
	}
	if _, err := drv.VerifyPassengerContact(ctx, ptg, "+77015556677", ptg); botapi.ErrorCode(err) != "forbidden" {
		t.Fatalf("driver bot on a passenger route: %v", err)
	}
	if _, err := pas.DriverSummary(ctx, d.TelegramID); botapi.ErrorCode(err) != "forbidden" {
		t.Fatalf("passenger bot on a driver route: %v", err)
	}
	c, err := pas.Config(ctx)
	if err != nil || c.PassengerMiniAppURL != "https://app.example.kz/passenger/" || c.DriverBotUsername != cfg.DriverBotUsername || c.DefaultLanguage != "kk" {
		t.Fatalf("config: %+v %v", c, err)
	}
}
