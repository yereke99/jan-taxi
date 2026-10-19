package orders_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
)

// Embi centre, where the class drivers stand and the orders start.
const embiLat, embiLon = 48.8264, 58.1442

// classDriver creates an online driver at the Embi centre whose vehicle is of
// class with seats (0: class default) and cargo litres (nil: class default).
func (e *env) classDriver(class string, seats int, cargoL *int) testDriver {
	e.t.Helper()
	f := testutil.CreateDriver(e.t, e.pool, testutil.DriverOptions{Online: true, Balance: 1000,
		VehicleClass: class, Seats: seats, CargoVolumeL: cargoL, Location: &testutil.LatLon{Lat: embiLat, Lon: embiLon}})
	return testDriver{DriverFixture: f, c: e.token(f.UserID, auth.AppDriver)}
}

// classOrder places an order of category and vehicle class for a new
// passenger through POST /orders, from a quote stored as POST /routes/preview
// stores one option.
func (e *env) classOrder(category, class string, load pricing.Load) (map[string]any, uuid.UUID, caller) {
	e.t.Helper()
	ctx := context.Background()
	pid, pc := e.passenger()
	tariff, err := pricing.ActiveTariff(ctx, e.pool, e.cityID, category, class)
	if err != nil {
		e.t.Fatalf("tariff %s/%s: %v", category, class, err)
	}
	stops := embiStops[:2]
	q, err := pricing.CreateQuote(ctx, e.pool, pricing.NewQuote{
		UserID: pid, CityID: e.cityID, Tariff: tariff, Load: load, Stops: stops, DistanceM: 1000, DurationS: 130,
		Legs:     []pricing.Leg{{DistanceM: 1000, DurationS: 130}},
		Geometry: json.RawMessage(`{"type":"LineString","coordinates":[[58.1442,48.8264],[58.1504,48.8291]]}`),
		BBox:     [4]float64{58.1442, 48.8264, 58.1504, 48.8291}, Fare: pricing.Compute(tariff, 1000, 130, 2),
	})
	if err != nil {
		e.t.Fatalf("quote: %v", err)
	}
	body := map[string]any{"quote_id": q.ID.String(), "fare": q.MinFare, "payment_method": "cash",
		"idempotency_key": uuid.NewString()}
	if category != pricing.CategoryTaxi {
		body["cargo_description"] = "Кітаптар салынған қорап"
		body["prohibited_items_ack"] = true
		if category == pricing.CategoryParcel {
			body["recipient_phone"] = "+77015550101"
		}
	}
	r := e.do(pc, http.MethodPost, "/orders", body)
	e.expect(r, http.StatusCreated, "")
	var o map[string]any
	r.decode(e.t, &o)
	return o, uuid.MustParse(o["id"].(string)), pc
}

func intPtr(n int) *int       { return &n }
func strPtr(s string) *string { return &s }

func (e *env) sees(d testDriver, orderID uuid.UUID) bool {
	e.t.Helper()
	for _, c := range e.available(d).Items {
		if c.ID == orderID {
			return true
		}
	}
	return false
}

func TestMinivanOrderReachesOnlyMinivanDrivers(t *testing.T) {
	e := newEnv(t)
	sedan := e.classDriver("sedan", 0, nil)
	universal := e.classDriver("universal", 0, nil)
	minivan := e.classDriver("minivan", 0, nil)      // 7 seats
	smallMinivan := e.classDriver("minivan", 6, nil) // too few seats for 7
	microbus := e.classDriver("microbus", 0, nil)

	o, id, _ := e.classOrder("taxi", "minivan", pricing.Load{PassengerCount: intPtr(7)})
	name, _ := o["vehicle_class_name"].(map[string]any)
	if o["vehicle_class"] != "minivan" || o["passenger_count"] != float64(7) || o["cargo_size"] != nil ||
		name["kk"] != "Минивэн" || name["ru"] != "Минивэн" || name["en"] != "Minivan" {
		t.Fatalf("order load = %v", o)
	}
	var stored struct {
		class string
		count *int16
		size  *string
		vol   int
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT vehicle_class, passenger_count, cargo_size, cargo_volume_l FROM orders WHERE id = $1`, id).
		Scan(&stored.class, &stored.count, &stored.size, &stored.vol); err != nil || stored.class != "minivan" ||
		stored.count == nil || *stored.count != 7 || stored.size != nil || stored.vol != 0 {
		t.Fatalf("stored load = %+v %v", stored, err)
	}

	if e.offers(id, minivan.DriverID) != 1 {
		t.Fatal("the minivan driver got no offer")
	}
	for name, d := range map[string]testDriver{"sedan": sedan, "universal": universal, "6-seat minivan": smallMinivan, "microbus": microbus} {
		if e.offers(id, d.DriverID) != 0 {
			t.Errorf("%s driver was offered a 7-passenger minivan order", name)
		}
		if e.sees(d, id) {
			t.Errorf("%s driver lists the minivan order", name)
		}
		if r := e.do(d.c, http.MethodGet, path("/driver/orders/%s", id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s driver opens the minivan order: %d", name, r.Status)
		}
		if r := e.do(d.c, http.MethodPost, path("/driver/orders/%s/accept", id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s driver accepts the minivan order: %d %s", name, r.Status, r.Body)
		}
	}
	list := e.available(minivan)
	if len(list.Items) != 1 || list.Items[0].ID != id || list.Items[0].VehicleClass != "minivan" ||
		list.Items[0].PassengerCount == nil || *list.Items[0].PassengerCount != 7 || list.Items[0].VehicleClassName.KK != "Минивэн" {
		t.Fatalf("minivan driver list = %+v", list)
	}
	r := e.do(minivan.c, http.MethodPost, path("/driver/orders/%s/accept", id), nil)
	e.expect(r, http.StatusOK, "")
	var detail map[string]any
	e.expect(e.do(minivan.c, http.MethodGet, path("/driver/orders/%s", id), nil), http.StatusOK, "")
	e.do(minivan.c, http.MethodGet, path("/driver/orders/%s", id), nil).decode(t, &detail)
	if detail["vehicle_class"] != "minivan" || detail["passenger_count"] != float64(7) {
		t.Fatalf("driver detail load = %v", detail)
	}
}

func TestUniversalServesSedanOrders(t *testing.T) {
	e := newEnv(t)
	sedan := e.classDriver("sedan", 0, nil)
	universal := e.classDriver("universal", 0, nil)
	minivan := e.classDriver("minivan", 0, nil)

	_, sedanOrder, pc := e.classOrder("taxi", "sedan", pricing.Load{})
	if e.offers(sedanOrder, sedan.DriverID) != 1 || e.offers(sedanOrder, universal.DriverID) != 1 ||
		e.offers(sedanOrder, minivan.DriverID) != 0 {
		t.Fatal("a sedan order goes to sedans and estates only")
	}
	_, estateOrder, _ := e.classOrder("taxi", "universal", pricing.Load{})
	if e.offers(estateOrder, universal.DriverID) != 1 || e.offers(estateOrder, sedan.DriverID) != 0 ||
		e.offers(estateOrder, minivan.DriverID) != 0 {
		t.Fatal("an estate order goes to estates only")
	}
	if !e.sees(universal, sedanOrder) || !e.sees(universal, estateOrder) || e.sees(sedan, estateOrder) {
		t.Fatal("available lists do not follow the classes")
	}

	// The estate takes the sedan order; the passenger sees both classes.
	e.expect(e.do(universal.c, http.MethodPost, path("/driver/orders/%s/accept", sedanOrder), nil), http.StatusOK, "")
	var o map[string]any
	e.do(pc, http.MethodGet, path("/orders/%s", sedanOrder), nil).decode(t, &o)
	vehicle, _ := o["vehicle"].(map[string]any)
	if o["vehicle_class"] != "sedan" || vehicle["vehicle_class"] != "universal" || vehicle["seats"] != float64(4) ||
		vehicle["cargo_volume_l"] != float64(600) || vehicle["vehicle_class_name"].(map[string]any)["en"] != "Estate" {
		t.Fatalf("passenger order = %v", o)
	}
	var list map[string]any
	e.do(pc, http.MethodGet, "/orders?scope=active", nil).decode(t, &list)
	item := list["items"].([]any)[0].(map[string]any)
	if item["vehicle_class"] != "sedan" || item["passenger_count"] != float64(1) {
		t.Fatalf("passenger list item = %v", item)
	}
}

func TestSeatsAndCargoSpaceLimitDispatch(t *testing.T) {
	e := newEnv(t)
	sedan := e.classDriver("sedan", 0, nil)              // 4 seats, 400 L
	smallSedan := e.classDriver("sedan", 3, intPtr(300)) // 3 seats, 300 L
	estate := e.classDriver("universal", 0, nil)         // 4 seats, 600 L
	noClass := e.classDriver("sedan", 0, nil)
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE vehicles SET class_code = NULL, seats = NULL, cargo_volume_l = NULL WHERE id = $1`, noClass.VehicleID); err != nil {
		t.Fatal(err)
	}

	// Four passengers: the 3-seat sedan is left out.
	_, four, _ := e.classOrder("taxi", "sedan", pricing.Load{PassengerCount: intPtr(4)})
	if e.offers(four, sedan.DriverID) != 1 || e.offers(four, estate.DriverID) != 1 || e.offers(four, smallSedan.DriverID) != 0 {
		t.Fatal("4 passengers: seats not respected")
	}
	if r := e.do(smallSedan.c, http.MethodPost, path("/driver/orders/%s/accept", four), nil); r.Status != http.StatusNotFound {
		t.Fatalf("3-seat sedan accepts 4 passengers: %d", r.Status)
	}
	// A large parcel (400 L): the 300 L boot is too small.
	o, large, _ := e.classOrder("parcel", "sedan", pricing.Load{CargoSize: strPtr("l")})
	if o["cargo_size"] != "l" || o["cargo_volume_l"] != float64(400) || o["passenger_count"] != nil {
		t.Fatalf("parcel load = %v", o)
	}
	if e.offers(large, sedan.DriverID) != 1 || e.offers(large, estate.DriverID) != 1 || e.offers(large, smallSedan.DriverID) != 0 {
		t.Fatal("large parcel: cargo space not respected")
	}
	// A small parcel fits the small boot.
	_, small, _ := e.classOrder("other", "sedan", pricing.Load{CargoSize: strPtr("s")})
	if e.offers(small, smallSedan.DriverID) != 1 {
		t.Fatal("small parcel not offered to the small sedan")
	}
	// A vehicle without a declared class takes nothing.
	for _, id := range []uuid.UUID{four, large, small} {
		if e.offers(id, noClass.DriverID) != 0 || e.sees(noClass, id) {
			t.Fatal("a vehicle without a class was offered an order")
		}
	}
}

func TestAdminAssignRequiresAFittingVehicle(t *testing.T) {
	e := newEnv(t)
	sedan := e.classDriver("sedan", 0, nil)
	minivan := e.classDriver("minivan", 0, nil)
	_, id, _ := e.classOrder("taxi", "minivan", pricing.Load{PassengerCount: intPtr(6)})

	r := e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/assign", id), map[string]any{"driver_id": sedan.DriverID})
	e.expect(r, http.StatusUnprocessableEntity, "validation_failed")
	if er := r.err(t).Error; er.Fields["driver_id"] != "not_allowed" || er.Details["reason"] != "vehicle_mismatch" {
		t.Fatalf("assign sedan to a minivan order: %s", r.Body)
	}
	r = e.do(e.operator(), http.MethodPost, path("/admin/orders/%s/assign", id), map[string]any{"driver_id": minivan.DriverID})
	e.expect(r, http.StatusOK, "")

	var ao map[string]any
	e.do(e.operator(), http.MethodGet, path("/admin/orders/%s", id), nil).decode(t, &ao)
	if ao["vehicle_class"] != "minivan" || ao["passenger_count"] != float64(6) ||
		ao["vehicle"].(map[string]any)["vehicle_class"] != "minivan" {
		t.Fatalf("admin order = %v", ao)
	}
	var list map[string]any
	e.do(e.operator(), http.MethodGet, "/admin/orders", nil).decode(t, &list)
	if item := list["items"].([]any)[0].(map[string]any); item["vehicle_class"] != "minivan" ||
		item["vehicle_class_name"].(map[string]any)["kk"] != "Минивэн" {
		t.Fatalf("admin list item = %v", item)
	}
	var history map[string]any
	e.do(minivan.c, http.MethodGet, "/driver/orders/history", nil).decode(t, &history)
	if items := history["items"].([]any); len(items) != 0 {
		t.Fatalf("active order in history: %v", items)
	}
}
