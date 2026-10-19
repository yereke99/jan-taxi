package orders_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
)

// Almaty has no city zone in the seed: its orders are priced by the
// nationwide region KAZ and named by the geocoded locality.
var almatyStops = []pricing.QuoteStop{
	{Lat: 43.2389, Lon: 76.8897, Address: "Абай даңғылы, 10"},
	{Lat: 43.2567, Lon: 76.9286, Address: "Достық даңғылы, 50"},
}

// quoteIn stores a sedan taxi quote priced by the tariff of the city with
// code region, carrying the locality the preview found for the pickup.
func (e *env) quoteIn(userID uuid.UUID, region string, stops []pricing.QuoteStop, locality *string) pricing.StoredQuote {
	e.t.Helper()
	ctx := context.Background()
	city, err := cities.GetByCode(ctx, e.pool, region)
	if err != nil {
		e.t.Fatalf("region %s: %v", region, err)
	}
	tariff, err := pricing.ActiveTariff(ctx, e.pool, city.ID, pricing.CategoryTaxi, "sedan")
	if err != nil {
		e.t.Fatalf("tariff: %v", err)
	}
	legs := []pricing.Leg{{DistanceM: 4200, DurationS: 600}}
	geometry, _ := json.Marshal(map[string]any{"type": "LineString",
		"coordinates": [][2]float64{{stops[0].Lon, stops[0].Lat}, {stops[1].Lon, stops[1].Lat}}})
	q, err := pricing.CreateQuote(ctx, e.pool, pricing.NewQuote{
		UserID: userID, CityID: city.ID, Tariff: tariff, Stops: stops, DistanceM: 4200, DurationS: 600, Legs: legs,
		Geometry: geometry, BBox: [4]float64{stops[0].Lon, stops[0].Lat, stops[1].Lon, stops[1].Lat},
		Fare: pricing.Compute(tariff, 4200, 600, len(stops)), Locality: locality,
	})
	if err != nil {
		e.t.Fatalf("quote: %v", err)
	}
	return q
}

// placeOf extracts locality and tariff_region from an order DTO.
func placeOf(t *testing.T, r response) (locality any, region map[string]any) {
	t.Helper()
	var m map[string]any
	r.decode(t, &m)
	return placeFields(t, m)
}

func placeFields(t *testing.T, m map[string]any) (any, map[string]any) {
	t.Helper()
	loc, ok := m["locality"]
	if !ok {
		t.Fatalf("no locality in %v", m)
	}
	region, ok := m["tariff_region"].(map[string]any)
	if !ok {
		t.Fatalf("no tariff_region in %v", m)
	}
	return loc, region
}

func wantKAZ(t *testing.T, where string, region map[string]any) {
	t.Helper()
	name, _ := region["name"].(map[string]any)
	if region["code"] != "KAZ" || name["kk"] != "Қазақстан" || name["ru"] != "Казахстан" || name["en"] != "Kazakhstan" {
		t.Errorf("%s: tariff_region = %v", where, region)
	}
}

func firstItem(t *testing.T, r response, id uuid.UUID) map[string]any {
	t.Helper()
	var page struct {
		Items []map[string]any `json:"items"`
	}
	r.decode(t, &page)
	for _, it := range page.Items {
		if it["id"] == id.String() {
			return it
		}
	}
	t.Fatalf("order %s not listed: %s", id, r.Body)
	return nil
}

func TestOrderLocalityAndTariffRegion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	almaty := "Алматы"

	// A driver near the Almaty pickup, registered in Embi: dispatch is by
	// distance, the offer names the locality.
	d := e.driverAt("EMB", 5000, 43.2400, 76.8900)

	pid, pc := e.passenger()
	q := e.quoteIn(pid, "KAZ", almatyStops, &almaty)
	r := e.do(pc, http.MethodPost, "/orders", taxiBody(q, q.RecommendedFare))
	e.expect(r, http.StatusCreated, "")
	loc, region := placeOf(t, r)
	if loc != almaty {
		t.Fatalf("created order locality = %v", loc)
	}
	wantKAZ(t, "created order", region)
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	r.decode(t, &created)
	id := created.ID

	var stored *string
	if err := e.pool.QueryRow(ctx, `SELECT locality FROM orders WHERE id = $1`, id).Scan(&stored); err != nil ||
		stored == nil || *stored != almaty {
		t.Fatalf("orders.locality = %v %v", stored, err)
	}
	if e.count(`SELECT count(*) FROM notification_outbox WHERE template = 'order_available_driver'
		AND user_id = $1 AND params->>'locality' = $2`, d.UserID, almaty) != 1 {
		t.Error("the offer does not name the locality")
	}

	// Passenger views.
	r = e.do(pc, http.MethodGet, path("/orders/%s", id), nil)
	e.expect(r, http.StatusOK, "")
	if loc, region := placeOf(t, r); loc != almaty {
		t.Errorf("passenger order locality = %v", loc)
	} else {
		wantKAZ(t, "passenger order", region)
	}
	r = e.do(pc, http.MethodGet, "/orders?scope=active", nil)
	e.expect(r, http.StatusOK, "")
	if loc, region := placeFields(t, firstItem(t, r, id)); loc != almaty {
		t.Errorf("passenger list locality = %v", loc)
	} else {
		wantKAZ(t, "passenger list", region)
	}

	// Driver: the available card, the order detail and, after accepting,
	// the active order and the accepted-order message.
	r = e.do(d.c, http.MethodGet, "/driver/orders", nil)
	e.expect(r, http.StatusOK, "")
	if loc, region := placeFields(t, firstItem(t, r, id)); loc != almaty {
		t.Errorf("driver card locality = %v", loc)
	} else {
		wantKAZ(t, "driver card", region)
	}
	r = e.do(d.c, http.MethodGet, path("/driver/orders/%s", id), nil)
	e.expect(r, http.StatusOK, "")
	if loc, region := placeOf(t, r); loc != almaty {
		t.Errorf("driver order locality = %v", loc)
	} else {
		wantKAZ(t, "driver order", region)
	}
	e.expect(e.do(d.c, http.MethodPost, path("/driver/orders/%s/accept", id), nil), http.StatusOK, "")
	r = e.do(d.c, http.MethodGet, "/driver/orders/active", nil)
	e.expect(r, http.StatusOK, "")
	if loc, region := placeOf(t, r); loc != almaty {
		t.Errorf("active order locality = %v", loc)
	} else {
		wantKAZ(t, "active order", region)
	}
	if e.count(`SELECT count(*) FROM notification_outbox WHERE template = 'order_accepted_driver'
		AND user_id = $1 AND params->>'locality' = $2`, d.UserID, almaty) != 1 {
		t.Error("the accepted-order message does not name the locality")
	}

	// Admin: detail, list, and q matching part of the locality in any case.
	op := e.operator()
	e.expect(e.do(op, http.MethodPost, path("/admin/orders/%s/cancel", id), map[string]any{"reason": "Тексеру"}),
		http.StatusOK, "")
	r = e.do(d.c, http.MethodGet, "/driver/orders/history", nil)
	e.expect(r, http.StatusOK, "")
	if loc, region := placeFields(t, firstItem(t, r, id)); loc != almaty {
		t.Errorf("driver history locality = %v", loc)
	} else {
		wantKAZ(t, "driver history", region)
	}
	r = e.do(op, http.MethodGet, path("/admin/orders/%s", id), nil)
	e.expect(r, http.StatusOK, "")
	if loc, region := placeOf(t, r); loc != almaty {
		t.Errorf("admin order locality = %v", loc)
	} else {
		wantKAZ(t, "admin order", region)
	}
	r = e.do(op, http.MethodGet, "/admin/orders?q="+url.QueryEscape("алма"), nil)
	e.expect(r, http.StatusOK, "")
	if loc, region := placeFields(t, firstItem(t, r, id)); loc != almaty {
		t.Errorf("admin list locality = %v", loc)
	} else {
		wantKAZ(t, "admin list", region)
	}
	// LIKE wildcards in q are literal.
	r = e.do(op, http.MethodGet, "/admin/orders?q="+url.QueryEscape("%"), nil)
	e.expect(r, http.StatusOK, "")
	var none struct {
		Items []map[string]any `json:"items"`
	}
	r.decode(t, &none)
	if len(none.Items) != 0 {
		t.Errorf("q=%% matched %d orders", len(none.Items))
	}

	// An Embi order without a geocoded locality: null, priced by EMB.
	pid2, pc2 := e.passenger()
	q2 := e.quoteIn(pid2, "EMB", embiStops[:2], nil)
	r = e.do(pc2, http.MethodPost, "/orders", taxiBody(q2, q2.RecommendedFare))
	e.expect(r, http.StatusCreated, "")
	loc, region = placeOf(t, r)
	if loc != nil || region["code"] != "EMB" || region["name"].(map[string]any)["kk"] != "Ембі" {
		t.Errorf("embi order: locality %v, region %v", loc, region)
	}
	// The locality search does not match it.
	r = e.do(op, http.MethodGet, "/admin/orders?q="+url.QueryEscape("Алматы"), nil)
	e.expect(r, http.StatusOK, "")
	var page struct {
		Items []map[string]any `json:"items"`
	}
	r.decode(t, &page)
	if len(page.Items) != 1 || page.Items[0]["id"] != id.String() {
		t.Errorf("q=Алматы listed %d orders", len(page.Items))
	}
}
