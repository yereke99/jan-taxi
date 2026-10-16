package reports

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
)

func intp(n int) *int { return &n }

func TestBuildDashboard(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := Period{From: f.base, To: f.base.AddDate(0, 0, 3), Loc: f.loc}

	d, err := BuildDashboard(ctx, f.pool, p, nil, f.now)
	if err != nil {
		t.Fatal(err)
	}
	wantOrders := OrderMetrics{
		Created: 6, Completed: 3, Cancelled: 2, CancelledByPassenger: 1, NoDriverFound: 1, Disputed: 1,
		SearchingNow: 1, ActiveNow: 1, ByCategory: CategoryCount{Taxi: 4, Parcel: 1, Other: 1},
	}
	if d.Orders != wantOrders {
		t.Errorf("orders = %+v\nwant     %+v", d.Orders, wantOrders)
	}
	if want := (Revenue{GMV: 3300, CommissionCharged: 420, CommissionRefunded: 150, CommissionNet: 270, Currency: "KZT"}); d.Revenue != want {
		t.Errorf("revenue = %+v, want %+v", d.Revenue, want)
	}
	if want := (DriverMetrics{Total: 5, Approved: 2, OnlineNow: 1, OfflineNow: 1, PendingReview: 1, PendingApplications: 1,
		ChangesRequested: 1, Suspended: 1}); d.Drivers != want {
		t.Errorf("drivers = %+v, want %+v", d.Drivers, want)
	}
	if want := (TopUpMetrics{PendingVerificationCount: 1, PendingVerificationAmount: 500, ConfirmedCount: 2, ConfirmedAmount: 3000,
		RefundedCount: 1, RefundedAmount: 1000}); d.TopUps != want {
		t.Errorf("topups = %+v, want %+v", d.TopUps, want)
	}
	if want := (WalletTotals{TotalBalance: 1300, TotalReserved: 100}); d.Wallets != want {
		t.Errorf("wallets = %+v, want %+v", d.Wallets, want)
	}
	if want := (Performance{AvgAcceptTimeS: intp(135), AvgTripDurationS: intp(1300), CompletionRateBps: intp(3333)}); !reflect.DeepEqual(d.Performance, want) {
		t.Errorf("performance = %v/%v/%v", *d.Performance.AvgAcceptTimeS, *d.Performance.AvgTripDurationS, *d.Performance.CompletionRateBps)
	}
	if d.Notifications != (NotificationCounts{Pending: 1, Dead: 1}) || d.Cities != (CityCounts{Total: 1, Active: 1}) {
		t.Errorf("notifications = %+v, cities = %+v", d.Notifications, d.Cities)
	}
	if d.Today.OrdersCreated != 1 || d.Today.OrdersCompleted != 0 || d.Today.CommissionNet != 0 || d.Today.TopUpsConfirmedCount != 0 {
		t.Errorf("today = %+v", d.Today)
	}
	day := func(n int) string { return f.base.AddDate(0, 0, n).Format(time.DateOnly) }
	wantDaily := []DailyRow{
		{Date: day(0), OrdersCreated: 1, OrdersCompleted: 2, GMV: 1800, Commission: 180},
		{Date: day(1), OrdersCreated: 2, OrdersCompleted: 1, GMV: 1500, Commission: 150, TopUpsConfirmedCount: 1, TopUpsConfirmedAmount: 2000},
		{Date: day(2), OrdersCreated: 3, Commission: -60, TopUpsConfirmedCount: 1, TopUpsConfirmedAmount: 1000},
	}
	if !reflect.DeepEqual(d.Daily, wantDaily) {
		t.Errorf("daily = %+v\nwant  %+v", d.Daily, wantDaily)
	}
	if d.Timezone != DefaultTimezone || !d.From.Equal(f.base) || d.From.Location() != time.UTC {
		t.Errorf("period meta = %s %s", d.Timezone, d.From)
	}

	// The same numbers for the city filter; nothing for an empty city.
	emb, err := BuildDashboard(ctx, f.pool, p, &f.cityID, f.now)
	if err != nil || emb.Orders != d.Orders || emb.Revenue != d.Revenue || emb.Drivers != d.Drivers {
		t.Fatalf("city filter: %+v, %v", emb.Orders, err)
	}
	var other uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO cities (code, name_kk, name_ru, name_en, center_lat, center_lon)
		VALUES ('AKT', 'Ақтөбе', 'Актобе', 'Aktobe', 50.2839, 57.1670) RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	empty, err := BuildDashboard(ctx, f.pool, p, &other, f.now)
	if err != nil || empty.Orders.Created != 0 || empty.Revenue.CommissionCharged != 0 || empty.Drivers.Total != 0 ||
		empty.TopUps.ConfirmedCount != 0 || empty.Cities.Total != 2 || empty.Performance.CompletionRateBps != nil {
		t.Fatalf("empty city: %+v, %v", empty, err)
	}

	// Inside one repeatable-read snapshot as the handler runs it.
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if snap, err := BuildDashboard(ctx, tx, p, nil, f.now); err != nil || snap.Orders != d.Orders {
		t.Fatalf("snapshot: %+v, %v", snap.Orders, err)
	}
}

func TestDashboardEndpoint(t *testing.T) {
	f := newFixture(t)
	h := adminHandler(t, f.pool, auth.RoleAuditor)
	from := f.base.Format(time.DateOnly)
	to := f.base.AddDate(0, 0, 2).Format(time.DateOnly)

	rec := get(h, "/dashboard?from="+from+"&to="+to+"&city_id="+f.cityID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"from", "to", "city_id", "orders", "revenue", "drivers", "topups", "wallets", "performance", "notifications", "daily", "today", "cities"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("missing %s in %s", k, rec.Body)
		}
	}
	if body["orders"].(map[string]any)["created"].(float64) != 6 || len(body["daily"].([]any)) != 3 {
		t.Fatalf("dashboard body = %s", rec.Body)
	}
	if rec := get(h, "/dashboard"); rec.Code != http.StatusOK {
		t.Fatalf("default period: %d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"from=2026-01-01&to=2026-06-01", "from=bad", "city_id=" + uuid.NewString(), "city_id=x", "from=2026-10-05&to=2026-10-01"} {
		if rec := get(h, "/dashboard?"+q); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
	for _, role := range auth.Roles {
		if rec := get(adminHandler(t, f.pool, role), "/dashboard"); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", role, rec.Code)
		}
	}
}
