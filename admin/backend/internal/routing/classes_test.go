package routing

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// classBody is a preview request with extra JSON members (e.g. `"passenger_count":7`).
func classBody(category, extra string) string {
	body := previewBody(category, stopJSON(embi, "Жиенқали Тілепбергенов көшесі, 12"), stopJSON(embiStation, "Теміржол вокзалы"))
	if extra == "" {
		return body
	}
	return strings.TrimSuffix(body, "}") + "," + extra + "}"
}

func optionClasses(t *testing.T, out map[string]any) []string {
	t.Helper()
	opts, ok := out["options"].([]any)
	if !ok {
		t.Fatalf("no options: %v", out)
	}
	codes := make([]string, len(opts))
	for i, o := range opts {
		codes[i] = o.(map[string]any)["vehicle_class"].(string)
	}
	return codes
}

func option(out map[string]any, class string) map[string]any {
	for _, o := range out["options"].([]any) {
		if m := o.(map[string]any); m["vehicle_class"] == class {
			return m
		}
	}
	return nil
}

func TestPreviewOffersOneQuotePerCapableClass(t *testing.T) {
	env := newTestEnv(t, nil)
	token, userID := env.signIn("passenger")
	ctx := context.Background()

	// Taxi without a passenger count: one passenger, every passenger class.
	status, out := env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("taxi", ""))
	if status != http.StatusOK {
		t.Fatalf("taxi preview: %d %v", status, out)
	}
	if got := fmt.Sprint(optionClasses(t, out)); got != "[sedan universal minivan microbus]" {
		t.Fatalf("taxi classes = %s", got)
	}
	first := out["options"].([]any)[0].(map[string]any)
	if out["quote_id"] != first["quote_id"] || out["vehicle_class"] != "sedan" || out["passenger_count"] != float64(1) ||
		out["cargo_size"] != nil || fmt.Sprint(out["fare"]) != fmt.Sprint(first["fare"]) {
		t.Fatalf("top level must repeat the first option: %v", out)
	}
	minivan := option(out, "minivan")
	name := minivan["name"].(map[string]any)
	if minivan["seats_max"] != float64(8) || minivan["icon"] != "minivan" || name["kk"] != "Минивэн" ||
		minivan["examples"] != "Toyota Alphard, Toyota Sienna" || minivan["eta_s"] != nil ||
		minivan["fare"].(map[string]any)["min"] != float64(1000) || minivan["fare"].(map[string]any)["max"] != float64(3000) {
		t.Fatalf("minivan option = %v", minivan)
	}
	var quotes int
	if err := env.pool.QueryRow(ctx, `
		SELECT count(*) FROM fare_quotes
		WHERE user_id = $1 AND passenger_count = 1 AND cargo_size IS NULL AND cargo_volume_l = 0
		  AND id = ANY ($2::uuid[])`, userID, []string{
		first["quote_id"].(string), minivan["quote_id"].(string), option(out, "universal")["quote_id"].(string),
		option(out, "microbus")["quote_id"].(string),
	}).Scan(&quotes); err != nil || quotes != 4 {
		t.Fatalf("stored quotes = %d %v", quotes, err)
	}
	var class string
	if err := env.pool.QueryRow(ctx, `SELECT vehicle_class FROM fare_quotes WHERE id = $1`, minivan["quote_id"]).Scan(&class); err != nil || class != "minivan" {
		t.Fatalf("minivan quote class = %q %v", class, err)
	}
	if n := len(env.osrm.callLog()); n != 1 {
		t.Fatalf("route computed %d times for one preview", n)
	}

	// Seven passengers fit only a minivan or a minibus.
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("taxi", `"passenger_count":7`))
	if got := fmt.Sprint(optionClasses(t, out)); status != http.StatusOK || got != "[minivan microbus]" ||
		out["vehicle_class"] != "minivan" || out["passenger_count"] != float64(7) {
		t.Fatalf("7 passengers: %d %s %v", status, got, out)
	}
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("taxi", `"passenger_count":9`))
	if got := fmt.Sprint(optionClasses(t, out)); status != http.StatusOK || got != "[microbus]" {
		t.Fatalf("9 passengers: %d %s", status, got)
	}

	// Deliveries: the cargo size decides.
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("parcel", ""))
	if got := fmt.Sprint(optionClasses(t, out)); status != http.StatusOK || got != "[sedan universal minivan microbus cargo_van truck]" ||
		out["cargo_size"] != "s" || out["passenger_count"] != nil {
		t.Fatalf("small parcel: %d %s %v", status, got, out)
	}
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("other", `"cargo_size":"xl"`))
	if got := fmt.Sprint(optionClasses(t, out)); status != http.StatusOK || got != "[microbus cargo_van truck]" {
		t.Fatalf("xl cargo: %d %s", status, got)
	}
	if opt := option(out, "truck"); opt["cargo_volume_l"] != float64(15000) || opt["fare"].(map[string]any)["min"] != float64(5000) {
		t.Fatalf("truck option = %v", opt)
	}
	var size string
	var volume int
	if err := env.pool.QueryRow(ctx, `SELECT cargo_size, cargo_volume_l FROM fare_quotes WHERE id = $1`, out["quote_id"]).
		Scan(&size, &volume); err != nil || size != "xl" || volume != 1500 {
		t.Fatalf("xl quote load = %q %d %v", size, volume, err)
	}
}

func TestPreviewClassValidation(t *testing.T) {
	env := newTestEnv(t, nil)
	token, _ := env.signIn("passenger")
	for _, tt := range []struct {
		name, category, extra, field, reason string
	}{
		{"no passengers", "taxi", `"passenger_count":0`, "passenger_count", "too_small"},
		{"too many passengers", "taxi", `"passenger_count":16`, "passenger_count", "too_large"},
		{"cargo size on a taxi", "taxi", `"cargo_size":"s"`, "cargo_size", "not_allowed"},
		{"passengers on a parcel", "parcel", `"passenger_count":2`, "passenger_count", "not_allowed"},
		{"unknown cargo size", "other", `"cargo_size":"xxl"`, "cargo_size", "invalid"},
	} {
		status, out := env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody(tt.category, tt.extra))
		if _, fields, _ := errInfo(out); status != http.StatusUnprocessableEntity || fields[tt.field] != tt.reason {
			t.Errorf("%s: %d %v", tt.name, status, out)
		}
	}
	if n := len(env.osrm.callLog()); n != 0 {
		t.Fatalf("invalid requests reached OSRM %d times", n)
	}
}

func TestPreviewClassAvailability(t *testing.T) {
	env := newTestEnv(t, nil)
	token, _ := env.signIn("passenger")
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := env.pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}

	// A class without an active tariff in the city is not offered.
	exec(`UPDATE tariffs SET is_active = false WHERE category = 'taxi' AND vehicle_class = 'minivan'`)
	status, out := env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("taxi", `"passenger_count":7`))
	if got := fmt.Sprint(optionClasses(t, out)); status != http.StatusOK || got != "[microbus]" {
		t.Fatalf("minivan without tariff: %d %s", status, got)
	}
	// A deactivated class is not offered either.
	exec(`UPDATE vehicle_classes SET is_active = false WHERE code = 'sedan'`)
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("taxi", ""))
	if got := fmt.Sprint(optionClasses(t, out)); status != http.StatusOK || got != "[universal microbus]" || out["vehicle_class"] != "universal" {
		t.Fatalf("sedan deactivated: %d %s", status, got)
	}
	// Classes that could carry the passengers exist but none is priced.
	exec(`UPDATE tariffs SET is_active = false WHERE category = 'taxi' AND vehicle_class = 'microbus'`)
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("taxi", `"passenger_count":7`))
	if _, fields, _ := errInfo(out); status != http.StatusUnprocessableEntity || fields["category"] != "not_allowed" {
		t.Fatalf("no priced class: %d %v", status, out)
	}
	// No active class can carry that many people at all.
	exec(`UPDATE vehicle_classes SET is_active = false WHERE code IN ('minivan', 'microbus')`)
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("taxi", `"passenger_count":7`))
	if _, fields, _ := errInfo(out); status != http.StatusUnprocessableEntity || fields["passenger_count"] != "too_large" {
		t.Fatalf("no class for 7: %d %v", status, out)
	}
	exec(`UPDATE vehicle_classes SET is_active = false WHERE code IN ('cargo_van', 'truck')`)
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("other", `"cargo_size":"xl"`))
	if _, fields, _ := errInfo(out); status != http.StatusUnprocessableEntity || fields["cargo_size"] != "too_large" {
		t.Fatalf("no class for xl: %d %v", status, out)
	}
}

func TestPreviewEtaFromNearestFittingDriver(t *testing.T) {
	env := newTestEnv(t, nil)
	token, _ := env.signIn("passenger")

	// An online sedan 0 m from the pickup serves sedan orders only.
	testutil.CreateDriver(t, env.pool, testutil.DriverOptions{Online: true, Balance: 500,
		Location: &testutil.LatLon{Lat: embi.Lat, Lon: embi.Lon}})
	// A minivan with 6 seats is too small for 7 passengers; an offline minibus does not count.
	testutil.CreateDriver(t, env.pool, testutil.DriverOptions{Online: true, Balance: 500, VehicleClass: "minivan", Seats: 6,
		Location: &testutil.LatLon{Lat: embiStation.Lat, Lon: embiStation.Lon}})
	testutil.CreateDriver(t, env.pool, testutil.DriverOptions{VehicleClass: "microbus",
		Location: &testutil.LatLon{Lat: embi.Lat, Lon: embi.Lon}})
	// An estate whose balance does not cover the fixed commission gets no offers, so it gives no estimate.
	testutil.CreateDriver(t, env.pool, testutil.DriverOptions{Online: true, Balance: 30, VehicleClass: "universal",
		Location: &testutil.LatLon{Lat: embi.Lat, Lon: embi.Lon}})

	status, out := env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("taxi", ""))
	if status != http.StatusOK {
		t.Fatalf("preview: %d %v", status, out)
	}
	// Coarse on purpose (no distance oracle): a driver right at the pickup
	// is "within 5 minutes".
	if eta := option(out, "sedan")["eta_s"]; eta != float64(300) {
		t.Fatalf("sedan eta = %v", eta)
	}
	if option(out, "universal")["eta_s"] != nil || option(out, "microbus")["eta_s"] != nil {
		t.Fatalf("classes without a fitting driver must have no eta: %v", out["options"])
	}
	if eta, ok := option(out, "minivan")["eta_s"].(float64); !ok || eta < 300 || int(eta)%300 != 0 {
		t.Fatalf("minivan eta for up to 6 people = %v", option(out, "minivan")["eta_s"])
	}
	// Moving pickup A a little (inside one ~500 m grid cell) never changes
	// the estimates, so repeated previews cannot trace the drivers.
	sedan, minivan := option(out, "sedan")["eta_s"], option(out, "minivan")["eta_s"]
	cell := dispatch.SnapETAPickup(embi)
	for _, d := range [][2]float64{{0.001, 0.001}, {-0.002, 0.0025}, {0.0015, -0.003}} {
		p := geo.Point{Lat: cell.Lat + d[0], Lon: cell.Lon + d[1]}
		body := previewBody("taxi", stopJSON(p, "A"), stopJSON(embiStation, "B"))
		status, moved := env.do(http.MethodPost, "/api/v1/routes/preview", token, body)
		if status != http.StatusOK || option(moved, "sedan")["eta_s"] != sedan || option(moved, "minivan")["eta_s"] != minivan {
			t.Fatalf("pickup %v: %d sedan %v minivan %v, want %v %v", p, status,
				option(moved, "sedan")["eta_s"], option(moved, "minivan")["eta_s"], sedan, minivan)
		}
	}
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, classBody("taxi", `"passenger_count":7`))
	if status != http.StatusOK || option(out, "minivan")["eta_s"] != nil {
		t.Fatalf("6-seat minivan counted for 7 passengers: %d %v", status, out)
	}
}

// A city zone with tariffs for some classes only must not switch the other
// classes or categories off inside it: they keep the nationwide (KAZ)
// tariffs that price them everywhere else, and every option says which
// region priced it.
func TestPreviewFallsBackToNationwideTariffs(t *testing.T) {
	env := newTestEnv(t, nil)
	token, userID := env.signIn("passenger")
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := env.pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	almaty := geo.Point{Lat: 43.2389, Lon: 76.8897}
	dostyk := geo.Point{Lat: 43.2567, Lon: 76.9286}
	// An admin creates Almaty, draws its zone and adds only a taxi sedan tariff.
	exec(`INSERT INTO cities (code, name_kk, name_ru, name_en, center_lat, center_lon, default_zoom, timezone, search_timeout_s)
		VALUES ('ALA', 'Алматы', 'Алматы', 'Almaty', 43.2389, 76.8897, 12, 'Asia/Almaty', 600)`)
	exec(`INSERT INTO service_zones (city_id, name, geom)
		SELECT id, 'Алматы', ST_Multi(ST_Buffer(center, 20000)::geometry) FROM cities WHERE code = 'ALA'`)
	exec(`INSERT INTO tariffs (city_id, category, vehicle_class, version, min_fare, max_fare, fare_step, base_fare, commission_bps)
		SELECT id, 'taxi', 'sedan', 1, 900, 2500, 50, 900, 1000 FROM cities WHERE code = 'ALA'`)
	body := func(category, extra string) string {
		b := previewBody(category, stopJSON(almaty, "Абай даңғылы, 10"), stopJSON(dostyk, "Достық даңғылы, 50"))
		if extra != "" {
			b = strings.TrimSuffix(b, "}") + "," + extra + "}"
		}
		return b
	}
	region := func(m map[string]any) string {
		r, _ := m["tariff_region"].(map[string]any)
		code, _ := r["code"].(string)
		return code
	}
	quoteCity := func(id any) string {
		t.Helper()
		var code string
		if err := env.pool.QueryRow(ctx, `SELECT c.code FROM fare_quotes q JOIN cities c ON c.id = q.city_id
			WHERE q.id = $1 AND q.user_id = $2`, id, userID).Scan(&code); err != nil {
			t.Fatal(err)
		}
		return code
	}

	// One passenger: the sedan uses the Almaty tariff, the other classes KAZ.
	status, out := env.do(http.MethodPost, "/api/v1/routes/preview", token, body("taxi", ""))
	if got := fmt.Sprint(optionClasses(t, out)); status != http.StatusOK || got != "[sedan universal minivan microbus]" {
		t.Fatalf("taxi in Almaty: %d %s %v", status, got, out)
	}
	sedan, universal := option(out, "sedan"), option(out, "universal")
	if region(sedan) != "ALA" || sedan["fare"].(map[string]any)["min"] != float64(900) || quoteCity(sedan["quote_id"]) != "ALA" {
		t.Fatalf("sedan option = %v", sedan)
	}
	if region(universal) != "KAZ" || universal["fare"].(map[string]any)["min"] != float64(600) || quoteCity(universal["quote_id"]) != "KAZ" {
		t.Fatalf("universal option = %v", universal)
	}
	if region(option(out, "microbus")) != "KAZ" || region(out) != "ALA" || out["city"].(map[string]any)["code"] != "ALA" {
		t.Fatalf("regions: top %v city %v microbus %v", out["tariff_region"], out["city"], option(out, "microbus")["tariff_region"])
	}

	// Seven passengers: no Almaty tariff fits, KAZ prices both classes.
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, body("taxi", `"passenger_count":7`))
	if got := fmt.Sprint(optionClasses(t, out)); status != http.StatusOK || got != "[minivan microbus]" || region(out) != "KAZ" {
		t.Fatalf("7 passengers in Almaty: %d %s %v", status, got, out)
	}

	// Deliveries have no Almaty tariff at all: still served, by KAZ.
	for _, category := range []string{"parcel", "other"} {
		status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, body(category, ""))
		if status != http.StatusOK || region(out) != "KAZ" || len(optionClasses(t, out)) == 0 {
			t.Fatalf("%s in Almaty: %d %v", category, status, out)
		}
	}

	// Without a nationwide tariff for a class, the class is not offered.
	exec(`UPDATE tariffs SET is_active = false WHERE category = 'taxi' AND vehicle_class = 'universal'
		AND city_id = (SELECT id FROM cities WHERE code = 'KAZ')`)
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, body("taxi", ""))
	if got := fmt.Sprint(optionClasses(t, out)); status != http.StatusOK || got != "[sedan minivan microbus]" {
		t.Fatalf("no KAZ universal: %d %s", status, got)
	}
}
