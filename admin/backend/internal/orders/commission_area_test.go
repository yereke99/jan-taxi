package orders_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
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
