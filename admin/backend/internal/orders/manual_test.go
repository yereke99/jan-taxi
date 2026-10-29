package orders_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
	"github.com/yereke99/jantaxi/admin/backend/migrations"
)

// NULL storage, wire values, dispatch, acceptance, commission and history share
// the real transaction paths: no fake destination or route is introduced.
func TestManualDestinationOrderLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	pid, pc := e.passenger()
	e.setting("orders.manual_destination_enabled", "true")
	e.setting("orders.manual_destination_pricing", `"passenger_tariff_bounds"`)
	near := e.driverAt("EMB", 1000, 48.827, 58.1442)
	far := e.driverAt("EMB", 1000, 49.1, 58.1442)
	tr, err := pricing.ActiveTariff(ctx, e.pool, e.cityID, "taxi", "sedan")
	if err != nil {
		t.Fatal(err)
	}
	q, err := pricing.CreateQuote(ctx, e.pool, pricing.NewQuote{UserID: pid, CityID: e.cityID, Tariff: tr,
		Stops:    []pricing.QuoteStop{embiStops[0], {Mode: "manual", Address: "Базар жанындағы ескі мәдениет үйі"}},
		Geometry: json.RawMessage(`null`), Fare: pricing.Fare{Recommended: tr.MinFare, Min: tr.MinFare, Max: tr.MaxFare, Step: tr.FareStep, Options: pricing.Options(tr.MinFare, tr.MaxFare, tr.FareStep)}})
	if err != nil {
		t.Fatal(err)
	}
	body := taxiBody(q, 800)
	res := e.do(pc, http.MethodPost, "/orders", body)
	e.expect(res, http.StatusCreated, "")
	var raw map[string]any
	if err = json.Unmarshal(res.Body, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["distance_m"] != nil || raw["duration_s"] != nil {
		t.Fatalf("fabricated metrics: %s", res.Body)
	}
	last := raw["stops"].([]any)[1].(map[string]any)
	if last["lat"] != nil || last["lon"] != nil || last["mode"] != "manual" {
		t.Fatalf("fabricated B: %v", last)
	}
	var o orders.Order
	res.decode(t, &o)
	if e.count(`SELECT count(*) FROM order_stops WHERE order_id=$1 AND mode='manual' AND lat IS NULL AND lon IS NULL AND location IS NULL`, o.ID) != 1 {
		t.Fatal("manual B not SQL NULL")
	}
	if e.count(`SELECT count(*) FROM orders WHERE id=$1 AND distance_m IS NULL AND duration_s IS NULL AND pickup_lat=48.8264`, o.ID) != 1 {
		t.Fatal("unknown totals or valid A lost")
	}
	if e.count(`SELECT count(*) FROM order_offers WHERE order_id=$1 AND driver_id=$2`, o.ID, near.DriverID) != 1 {
		t.Fatal("near A driver not offered")
	}
	if e.count(`SELECT count(*) FROM order_offers WHERE order_id=$1 AND driver_id=$2`, o.ID, far.DriverID) != 0 {
		t.Fatal("far A driver offered")
	}

	// A schema rollback after new manual records must refuse and retain data.
	_, down := manualMigration(t)
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, down)
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "Manual destination data exists") {
		t.Fatalf("unsafe rollback allowed: %v", err)
	}
	if e.count(`SELECT count(*) FROM order_stops WHERE order_id=$1 AND mode='manual' AND lat IS NULL`, o.ID) != 1 {
		t.Fatal("rollback lost destination")
	}
	e.expect(e.do(pc, http.MethodPost, "/orders", body), http.StatusOK, "")
	accepted := e.do(near.c, http.MethodPost, "/driver/orders/"+o.ID.String()+"/accept", nil)
	e.expect(accepted, http.StatusOK, "")
	if e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id=$1 AND entry_type='commission_charge'`, o.ID) != 1 {
		t.Fatal("commission not charged exactly once")
	}
	e.expect(e.do(near.c, http.MethodPost, "/driver/orders/"+o.ID.String()+"/start", nil), http.StatusOK, "")
	e.expect(e.do(near.c, http.MethodPost, "/driver/orders/"+o.ID.String()+"/complete", nil), http.StatusOK, "")
	history := e.do(pc, http.MethodGet, "/orders", nil)
	e.expect(history, http.StatusOK, "")
	if err = json.Unmarshal(history.Body, &raw); err != nil {
		t.Fatal(err)
	}
	item := raw["items"].([]any)[0].(map[string]any)
	if item["distance_m"] != nil || item["destination"].(map[string]any)["lat"] != nil {
		t.Fatalf("history fabricated values: %s", history.Body)
	}
}

func TestManualDestinationRadiusBoundaries(t *testing.T) {
	for _, radius := range []int{5000, 10000} {
		t.Run(fmt.Sprint(radius), func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			pid, pc := e.passenger()
			e.setting("orders.manual_destination_enabled", "true")
			e.setting("dispatch.radius_base_m", fmt.Sprint(radius))
			e.setting("dispatch.radius_max_m", fmt.Sprint(radius))
			e.setting("dispatch.radius_step_m", "0")
			project := func(m int) (float64, float64) {
				var lat, lon float64
				if err := e.pool.QueryRow(ctx, `SELECT ST_Y(p::geometry),ST_X(p::geometry) FROM (SELECT ST_Project(ST_SetSRID(ST_MakePoint(58.1442,48.8264),4326)::geography,$1::float8,0::float8) p) s`, m).Scan(&lat, &lon); err != nil {
					t.Fatal(err)
				}
				return lat, lon
			}
			lat, lon := project(radius - 100)
			inside := e.driverAt("EMB", 1000, lat, lon)
			lat, lon = project(radius + 100)
			outside := e.driverAt("EMB", 1000, lat, lon)
			tr, err := pricing.ActiveTariff(ctx, e.pool, e.cityID, "taxi", "sedan")
			if err != nil {
				t.Fatal(err)
			}
			q, err := pricing.CreateQuote(ctx, e.pool, pricing.NewQuote{UserID: pid, CityID: e.cityID, Tariff: tr, Stops: []pricing.QuoteStop{embiStops[0], {Mode: "manual", Address: "Базар жанындағы ескі мектеп"}}, Geometry: json.RawMessage(`null`), Fare: pricing.Fare{Recommended: tr.MinFare, Min: tr.MinFare, Max: tr.MaxFare, Step: tr.FareStep, Options: pricing.Options(tr.MinFare, tr.MaxFare, tr.FareStep)}})
			if err != nil {
				t.Fatal(err)
			}
			res := e.do(pc, http.MethodPost, "/orders", taxiBody(q, 800))
			e.expect(res, http.StatusCreated, "")
			var o orders.Order
			res.decode(t, &o)
			if e.count(`SELECT count(*) FROM order_offers WHERE order_id=$1 AND driver_id=$2`, o.ID, inside.DriverID) != 1 {
				t.Fatalf("driver %dm from A excluded", radius-100)
			}
			if e.count(`SELECT count(*) FROM order_offers WHERE order_id=$1 AND driver_id=$2`, o.ID, outside.DriverID) != 0 {
				t.Fatalf("driver %dm from A included", radius+100)
			}
		})
	}
}

func manualMigration(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("00010_manual_destinations.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing migration down")
	}
	return strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
}

func TestManualMigrationRoundtripBeforeManualWrites(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	up, down := manualMigration(t)
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatalf("safe down before manual writes: %v", err)
	}
	if _, err = tx.Exec(ctx, up); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='order_stops' AND column_name='mode')`).Scan(&exists); err != nil || !exists {
		t.Fatalf("schema not restored: %v", err)
	}
}
