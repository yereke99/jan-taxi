package orders_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
)

// villageArea stores a circle village commission around lat/lon.
func (e *env) villageArea(name string, bps int, lat, lon float64, radiusM int) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO commission_areas (name, commission_bps, shape, center_lat, center_lon, radius_m)
		VALUES ($1, $2, 'circle', $3, $4, $5) RETURNING id`, name, bps, lat, lon, radiusM).Scan(&id); err != nil {
		e.t.Fatalf("village area: %v", err)
	}
	return id
}

type orderCommission struct {
	Mode     string
	Bps      int
	Amount   int64
	AreaID   *uuid.UUID
	AreaName *string
}

func (e *env) orderCommission(orderID uuid.UUID) orderCommission {
	e.t.Helper()
	var c orderCommission
	if err := e.pool.QueryRow(context.Background(), `
		SELECT commission_mode, commission_bps, commission_amount, commission_area_id, commission_area_name
		FROM orders WHERE id = $1`, orderID).Scan(&c.Mode, &c.Bps, &c.Amount, &c.AreaID, &c.AreaName); err != nil {
		e.t.Fatalf("order commission: %v", err)
	}
	return c
}

// A village's own commission (§22.9): an order picked up inside it gets the
// village's percentage of the fare, snapshotted at creation and charged when
// a driver takes it; of overlapping villages the nearest centre wins; the
// order keeps its village after the area is deleted; outside every village
// the general commission (fixed 50 ₸ here) applies.
func TestVillageCommission(t *testing.T) {
	e := newEnv(t)
	pickup := embiStops[0]
	near := e.villageArea("Ембі ауылы", 700, pickup.Lat, pickup.Lon+0.005, 3000) // ~370 m from A
	e.villageArea("Солтүстік", 1000, pickup.Lat+0.03, pickup.Lon, 6000)          // ~3.3 km from A, also covers it
	drv := e.driverAt("EMB", 5000, 48.827, 58.1442)

	o, _, _ := e.order(embiStops[:2], 1000)
	c := e.orderCommission(o.ID)
	if c.Mode != "percent" || c.Bps != 700 || c.Amount != 70 || c.AreaID == nil || *c.AreaID != near || c.AreaName == nil || *c.AreaName != "Ембі ауылы" {
		t.Fatalf("village commission: %+v", c)
	}

	// The driver pays the village's amount when taking the order.
	before, _ := e.wallet(drv.DriverID)
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/accept", o.ID), nil), http.StatusOK, "")
	if after, _ := e.wallet(drv.DriverID); before-after != 70 {
		t.Fatalf("charged %d, want 70", before-after)
	}

	// The operator sees which village's commission applied.
	view := e.do(e.operator(), http.MethodGet, path("/admin/orders/%s", o.ID), nil)
	e.expect(view, http.StatusOK, "")
	var admin struct {
		CommissionArea *struct {
			ID   *uuid.UUID `json:"id"`
			Name string     `json:"name"`
		} `json:"commission_area"`
		CommissionMode string `json:"commission_mode"`
		CommissionBps  int    `json:"commission_bps"`
	}
	view.decode(t, &admin)
	if admin.CommissionArea == nil || admin.CommissionArea.Name != "Ембі ауылы" || admin.CommissionArea.ID == nil ||
		*admin.CommissionArea.ID != near || admin.CommissionMode != "percent" || admin.CommissionBps != 700 {
		t.Fatalf("admin view: %+v", admin)
	}

	// Deleting the village keeps the order's history; the next order falls
	// to the other village that still covers A.
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM commission_areas WHERE id = $1`, near); err != nil {
		t.Fatal(err)
	}
	if c := e.orderCommission(o.ID); c.AreaID != nil || c.AreaName == nil || *c.AreaName != "Ембі ауылы" || c.Amount != 70 {
		t.Fatalf("history after delete: %+v", c)
	}
	o2, _, _ := e.order(embiStops[:2], 1000)
	if c := e.orderCommission(o2.ID); c.Bps != 1000 || c.Amount != 100 || c.AreaName == nil || *c.AreaName != "Солтүстік" {
		t.Fatalf("second village: %+v", c)
	}

	// Outside every active village: the general commission.
	if _, err := e.pool.Exec(context.Background(), `UPDATE commission_areas SET is_active = false`); err != nil {
		t.Fatal(err)
	}
	o3, _, _ := e.order(embiStops[:2], 1000)
	if c := e.orderCommission(o3.ID); c.Mode != "fixed" || c.Amount != 50 || c.AreaID != nil || c.AreaName != nil {
		t.Fatalf("general commission: %+v", c)
	}
	view = e.do(e.operator(), http.MethodGet, path("/admin/orders/%s", o3.ID), nil)
	e.expect(view, http.StatusOK, "")
	admin.CommissionArea = nil
	view.decode(t, &admin)
	if admin.CommissionArea != nil {
		t.Fatalf("general commission shows a village: %+v", admin.CommissionArea)
	}
}

// The migrated launch defaults price a Zhalagash taxi from 400 ₸ and charge
// 5% to a nearby driver, including one registered in another city. The order
// retains its commission if an administrator later changes the village.
func TestZhalagashDefaultCommission(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	pickup := geo.Point{Lat: 45.0808963, Lon: 64.6809179}
	city, err := cities.Resolve(ctx, e.pool, pickup)
	if err != nil || city.Code != "ZHL" {
		t.Fatalf("Zhalagash tariff region: %+v, %v", city, err)
	}
	e.cityID = city.ID
	stops := []pricing.QuoteStop{
		{Lat: pickup.Lat, Lon: pickup.Lon, Address: "Жалағаш орталығы"},
		{Lat: pickup.Lat + 0.005, Lon: pickup.Lon, Address: "Жалағаштағы мекенжай"},
	}
	// An existing driver may work here without re-registering in Zhalagash.
	drv := e.driverAt("EMB", 1000, pickup.Lat, pickup.Lon)
	o, _, _ := e.order(stops, 400)
	c := e.orderCommission(o.ID)
	if c.Mode != "percent" || c.Bps != 500 || c.Amount != 20 || c.AreaID == nil || c.AreaName == nil || *c.AreaName != "Жалағаш" {
		t.Fatalf("seeded commission: %+v", c)
	}
	if e.count(`SELECT count(*) FROM order_offers WHERE order_id = $1 AND driver_id = $2 AND status = 'sent'`, o.ID, drv.DriverID) != 1 {
		t.Fatal("Zhalagash order not offered to nearby driver registered in Embi")
	}
	var available dispatch.Available
	r := e.do(drv.c, http.MethodGet, "/driver/orders", nil)
	e.expect(r, http.StatusOK, "")
	r.decode(t, &available)
	if !available.Eligibility.Eligible || len(available.Items) != 1 || available.Items[0].ID != o.ID || available.Items[0].Fare != 400 || available.Items[0].CommissionAmount != 20 {
		t.Fatalf("seeded available order: %+v", available)
	}

	// Changes apply to subsequent orders, including a different fare, while
	// this order keeps the original amount until it has been completed.
	if _, err := e.pool.Exec(ctx, `UPDATE commission_areas SET commission_bps = 700, name = 'Жалағаш жаңартылған' WHERE id = $1`, *c.AreaID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"accept", "start", "complete"} {
		e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/"+action, o.ID), nil), http.StatusOK, "")
		if balance, reserved := e.wallet(drv.DriverID); balance != 980 || reserved != 0 {
			t.Fatalf("wallet after %s: %d/%d, want 980/0", action, balance, reserved)
		}
	}
	e.expect(e.do(drv.c, http.MethodPost, path("/driver/orders/%s/complete", o.ID), nil), http.StatusConflict, "invalid_transition")
	if got := e.count(`SELECT count(*) FROM wallet_ledger WHERE order_id = $1 AND entry_type = 'commission_charge' AND balance_delta = -20`, o.ID); got != 1 {
		t.Fatalf("20 ₸ commission charges = %d, want 1", got)
	}
	if snapshot := e.orderCommission(o.ID); snapshot.Bps != 500 || snapshot.Amount != 20 || snapshot.AreaName == nil || *snapshot.AreaName != "Жалағаш" {
		t.Fatalf("commission snapshot changed: %+v", snapshot)
	}
	next, _, _ := e.order(stops, 800)
	if updated := e.orderCommission(next.ID); updated.Bps != 700 || updated.Amount != 56 || updated.AreaName == nil || *updated.AreaName != "Жалағаш жаңартылған" {
		t.Fatalf("updated village commission: %+v", updated)
	}
}
