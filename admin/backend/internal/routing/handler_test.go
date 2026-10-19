package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
)

// testEnv is the API surface of this package behind the real auth service:
// tokens come from POST /auth/telegram/{app} with signed initData.
type testEnv struct {
	t    *testing.T
	pool *pgxpool.Pool
	osrm *fakeOSRM
	geo  *fakeGeocoder
	h    http.Handler
}

func newTestEnv(t *testing.T, mutate func(*HandlerConfig)) *testEnv {
	t.Helper()
	pool := testutil.NewDB(t)
	env := &testEnv{t: t, pool: pool, osrm: newFakeOSRM(t), geo: &fakeGeocoder{}}
	router, err := NewOSRM(OSRMConfig{BaseURL: env.osrm.URL(), Timeout: 2 * time.Second, CacheTTL: -1})
	if err != nil {
		t.Fatal(err)
	}
	authSvc, err := auth.NewService(pool, auth.Config{
		PassengerBotToken: config.TestPassengerBotToken,
		DriverBotToken:    config.TestDriverBotToken,
		SessionSecret:     config.TestSessionSecret,
		Audit:             func(context.Context, db.Querier, *http.Request, auth.AuditEntry) error { return nil },
		Log:               quietLog,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := HandlerConfig{
		Pool: pool, Router: router, MaxRoutePoints: 5, Log: quietLog, GeoPerMinute: 600, PreviewPerMinute: 600,
		Places: NewPlaces(pool, PlacesConfig{Provider: env.geo, ProviderName: "photon", CacheTTL: -1, Log: quietLog}),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	h := NewHandler(cfg)
	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) {
		authSvc.MountPublic(api)
		h.MountPassenger(api.With(authSvc.RequireApp(auth.AppPassenger)))
		h.MountApp(api.With(authSvc.RequireAnyApp()))
	})
	env.h = r
	return env
}

// signIn returns an app token for a new Telegram user of app and the user id.
func (e *testEnv) signIn(app string) (string, uuid.UUID) {
	e.t.Helper()
	botToken := config.TestPassengerBotToken
	if app == "driver" {
		botToken = config.TestDriverBotToken
	}
	tg := testutil.NextTelegramID()
	initData := testutil.SignInitData(botToken, tg, "Айгүл", time.Now(), "")
	body, _ := json.Marshal(map[string]string{"init_data": initData})
	status, out := e.do(http.MethodPost, "/api/v1/auth/telegram/"+app, "", string(body))
	if status != http.StatusOK {
		e.t.Fatalf("sign-in failed: %d %v", status, out)
	}
	var userID uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE telegram_user_id = $1`, tg).Scan(&userID); err != nil {
		e.t.Fatal(err)
	}
	return out["token"].(string), userID
}

func (e *testEnv) do(method, path, token, body string) (int, map[string]any) {
	e.t.Helper()
	status, raw, _ := e.raw(method, path, token, body)
	var out map[string]any
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &out); err != nil {
			e.t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return status, out
}

func (e *testEnv) raw(method, path, token, body string) (int, []byte, http.Header) {
	e.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes(), rec.Header()
}

func errInfo(body map[string]any) (code string, fields, details map[string]any) {
	e, _ := body["error"].(map[string]any)
	code, _ = e["code"].(string)
	fields, _ = e["fields"].(map[string]any)
	details, _ = e["details"].(map[string]any)
	return
}

func stopJSON(p geo.Point, address string) string {
	return fmt.Sprintf(`{"lat":%v,"lon":%v,"address":%q}`, p.Lat, p.Lon, address)
}

func previewBody(category string, stops ...string) string {
	return fmt.Sprintf(`{"category":%q,"stops":[%s]}`, category, strings.Join(stops, ","))
}

func TestPreviewEndToEnd(t *testing.T) {
	env := newTestEnv(t, nil)
	token, userID := env.signIn("passenger")
	ctx := context.Background()

	body := fmt.Sprintf(`{"category":"taxi","stops":[
		{"lat":48.8264,"lon":58.1442,"address":"  Жиенқали Тілепбергенов көшесі, 12 ","address_details":"2-кіреберіс,\n3-қабат"},
		{"lat":48.8291,"lon":58.1504,"address":"Теміржол вокзалы","address_details":""},
		{"lat":48.82470004,"lon":58.1418,"address":"Орталық базар"}]}`)
	status, q := env.do(http.MethodPost, "/api/v1/routes/preview", token, body)
	if status != http.StatusOK {
		t.Fatalf("preview: %d %v", status, q)
	}

	// OSRM received the stops in the passenger's order.
	calls := env.osrm.callLog()
	if len(calls) != 1 || calls[0].coords != coordString(embi, embiStation, embiBazaar) {
		t.Fatalf("OSRM calls = %+v", calls)
	}

	stops := q["stops"].([]any)
	wantStops := []struct {
		kind, address string
		details       any
		lat           float64
	}{
		{"pickup", "Жиенқали Тілепбергенов көшесі, 12", "2-кіреберіс, 3-қабат", 48.8264},
		{"intermediate", "Теміржол вокзалы", nil, 48.8291},
		{"destination", "Орталық базар", nil, 48.8247},
	}
	if len(stops) != 3 {
		t.Fatalf("stops = %v", stops)
	}
	for i, w := range wantStops {
		s := stops[i].(map[string]any)
		if s["seq"] != float64(i) || s["kind"] != w.kind || s["address"] != w.address || s["address_details"] != w.details || s["lat"] != w.lat {
			t.Errorf("stop %d = %v", i, s)
		}
	}
	fare := q["fare"].(map[string]any)
	if fare["recommended"] != float64(500) || fare["min"] != float64(500) || fare["max"] != float64(1500) ||
		fare["step"] != float64(50) || len(fare["options"].([]any)) != 21 {
		t.Fatalf("fare = %v", fare)
	}
	city := q["city"].(map[string]any)
	if city["code"] != "EMB" || city["name"].(map[string]any)["kk"] != "Ембі" {
		t.Fatalf("city = %v", city)
	}
	pm := q["payment_methods"].([]any)
	if q["category"] != "taxi" || q["currency"] != "KZT" || q["commission_bps"] != float64(1000) || len(pm) != 2 ||
		pm[0] != "cash" || pm[1] != "bank_transfer" || len(q["legs"].([]any)) != 2 || len(q["bbox"].([]any)) != 4 ||
		q["geometry"].(map[string]any)["type"] != "LineString" || q["distance_m"].(float64) <= 0 {
		t.Fatalf("quote = %v", q)
	}
	expires, err := time.Parse(time.RFC3339, q["expires_at"].(string))
	if err != nil || !strings.HasSuffix(q["expires_at"].(string), "Z") || time.Until(expires) < 9*time.Minute || time.Until(expires) > 11*time.Minute {
		t.Fatalf("expires_at = %v (%v)", q["expires_at"], err)
	}

	// The quote is persisted and readable by the orders package contract.
	quoteID := uuid.MustParse(q["quote_id"].(string))
	var sq pricing.StoredQuote
	loadQuote := func(user uuid.UUID) error {
		sentinel := errors.New("rollback")
		err := db.WithTx(ctx, env.pool, func(tx pgx.Tx) error {
			var err error
			if sq, err = pricing.LoadQuoteForUpdate(ctx, tx, quoteID, user); err != nil {
				return err
			}
			return sentinel
		})
		if errors.Is(err, sentinel) {
			return nil
		}
		return err
	}
	if err := loadQuote(userID); err != nil {
		t.Fatalf("LoadQuoteForUpdate: %v", err)
	}
	if len(sq.Stops) != 3 || sq.Stops[0].Address != "Жиенқали Тілепбергенов көшесі, 12" || sq.Stops[0].AddressDetails != "2-кіреберіс, 3-қабат" ||
		sq.Stops[1].Address != "Теміржол вокзалы" || sq.Stops[2].Lat != 48.8247 || sq.Category != "taxi" || len(sq.Legs) != 2 ||
		sq.MinFare != 500 || sq.MaxFare != 1500 || sq.CommissionBps != 1000 || sq.DistanceM != int(q["distance_m"].(float64)) {
		t.Fatalf("stored quote = %+v", sq)
	}
	if err := sq.ValidateFare(700); err != nil {
		t.Fatal(err)
	}
	other, _ := testutil.CreateUser(t, env.pool)
	if err := loadQuote(other); asHTTP(t, err).Status != http.StatusNotFound {
		t.Fatalf("other user's quote: %v", err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE fare_quotes SET expires_at = now() - interval '1 second' WHERE id = $1`, quoteID); err != nil {
		t.Fatal(err)
	}
	if err := loadQuote(userID); asHTTP(t, err).Code != "quote_expired" {
		t.Fatalf("expired quote: %v", err)
	}
}

func TestPreviewValidation(t *testing.T) {
	env := newTestEnv(t, nil)
	token, _ := env.signIn("passenger")
	six := []string{stopJSON(embi, "A"), stopJSON(embiStation, "B"), stopJSON(embiBazaar, "C"),
		stopJSON(embiSchool, "D"), stopJSON(embiAkimat, "E"), stopJSON(embiPark, "F")}
	long := strings.Repeat("ә", 201)

	tests := []struct {
		name    string
		body    string
		code    string
		fields  map[string]string
		details map[string]any
	}{
		{"six points", previewBody("taxi", six...), "validation_failed", map[string]string{"stops": "too_many"}, map[string]any{"max": float64(5)}},
		{"one point", previewBody("taxi", six[0]), "validation_failed", map[string]string{"stops": "too_few"}, nil},
		{"no stops", `{"category":"taxi"}`, "validation_failed", map[string]string{"stops": "required"}, nil},
		{"bad category", previewBody("bus", six[:2]...), "validation_failed", map[string]string{"category": "invalid"}, nil},
		{"missing category", `{"stops":[` + strings.Join(six[:2], ",") + `]}`, "validation_failed", map[string]string{"category": "required"}, nil},
		{"outside kazakhstan", previewBody("taxi", six[0], stopJSON(orenburg, "Оренбург")), "outside_service_area", nil,
			map[string]any{"field": "stops", "index": float64(1)}},
		{"outside pickup", previewBody("parcel", stopJSON(orenburg, "Оренбург"), six[0]), "outside_service_area", nil,
			map[string]any{"field": "stops", "index": float64(0)}},
		{"duplicate consecutive", previewBody("taxi", six[0], stopJSON(embiStation, "B"), stopJSON(embiStation, "B2")), "validation_failed",
			map[string]string{"stops.2": "duplicate"}, nil},
		{"texts", `{"category":"taxi","stops":[{"lat":48.8264,"lon":58.1442,"address":"  "},
			{"lat":48.8291,"lon":58.1504,"address":"` + long + `","address_details":"` + strings.Repeat("x", 121) + `"},
			{"lon":58.1,"address":"x"},{"lat":95,"lon":58,"address":"x"}]}`, "validation_failed",
			map[string]string{"stops.0.address": "required", "stops.1.address": "too_long", "stops.1.address_details": "too_long",
				"stops.2.lat": "required", "stops.3.lat": "invalid"}, nil},
	}
	for _, tt := range tests {
		status, body := env.do(http.MethodPost, "/api/v1/routes/preview", token, tt.body)
		code, fields, details := errInfo(body)
		if status != http.StatusUnprocessableEntity || code != tt.code {
			t.Fatalf("%s: %d %v", tt.name, status, body)
		}
		for k, v := range tt.fields {
			if fields[k] != v {
				t.Errorf("%s: fields[%s] = %v, want %s (%v)", tt.name, k, fields[k], v, fields)
			}
		}
		for k, v := range tt.details {
			if details[k] != v {
				t.Errorf("%s: details[%s] = %v, want %v (%v)", tt.name, k, details[k], v, details)
			}
		}
	}
	if n := len(env.osrm.callLog()); n != 0 {
		t.Fatalf("invalid requests reached OSRM %d times", n)
	}
	var quotes int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM fare_quotes`).Scan(&quotes); err != nil || quotes != 0 {
		t.Fatalf("quotes stored for invalid requests: %d %v", quotes, err)
	}

	// Malformed JSON and unknown fields.
	if status, _ := env.do(http.MethodPost, "/api/v1/routes/preview", token, `{"category":"taxi","stops":[],"user_id":"x"}`); status != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", status)
	}
	if status, _ := env.do(http.MethodPost, "/api/v1/routes/preview", token, `{`); status != http.StatusBadRequest {
		t.Fatalf("bad JSON: %d", status)
	}

	// The admin setting routing.max_points lowers the limit.
	if _, err := env.pool.Exec(context.Background(), `UPDATE app_settings SET value = '3' WHERE key = 'routing.max_points'`); err != nil {
		t.Fatal(err)
	}
	status, body := env.do(http.MethodPost, "/api/v1/routes/preview", token, previewBody("taxi", six[:4]...))
	if _, fields, details := errInfo(body); status != 422 || fields["stops"] != "too_many" || details["max"] != float64(3) {
		t.Fatalf("max_points=3: %d %v", status, body)
	}
	if status, body := env.do(http.MethodPost, "/api/v1/routes/preview", token, previewBody("other", six[:3]...)); status != 200 {
		t.Fatalf("3 points with max_points=3: %d %v", status, body)
	}
}

func TestPreviewAuthAndCategory(t *testing.T) {
	env := newTestEnv(t, nil)
	passenger, _ := env.signIn("passenger")
	driver, _ := env.signIn("driver")
	body := previewBody("taxi", stopJSON(embi, "A"), stopJSON(embiStation, "B"))

	if status, _ := env.do(http.MethodPost, "/api/v1/routes/preview", "", body); status != http.StatusUnauthorized {
		t.Fatalf("no token: %d", status)
	}
	// The auth middleware rejects a driver token on passenger routes (it
	// currently answers 401; the contract says 403).
	if status, _ := env.do(http.MethodPost, "/api/v1/routes/preview", driver, body); status != http.StatusForbidden && status != http.StatusUnauthorized {
		t.Fatalf("driver token: %d", status)
	}
	// A category without an active tariff in the resolved city cannot be booked.
	if _, err := env.pool.Exec(context.Background(), `UPDATE tariffs SET is_active = false WHERE category = 'parcel'`); err != nil {
		t.Fatal(err)
	}
	status, out := env.do(http.MethodPost, "/api/v1/routes/preview", passenger, previewBody("parcel", stopJSON(embi, "A"), stopJSON(embiStation, "B")))
	if code, fields, _ := errInfo(out); status != 422 || code != "validation_failed" || fields["category"] != "not_allowed" {
		t.Fatalf("no parcel tariff: %d %v", status, out)
	}
	// A pickup outside every city zone is priced by the nationwide region,
	// never by the nearest city (Aktobe → Embi trip).
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", passenger, previewBody("taxi", stopJSON(aktobe, "Ақтөбе"), stopJSON(embi, "Ембі")))
	if status != 200 || out["city"].(map[string]any)["code"] != "KAZ" || out["tariff_region"].(map[string]any)["code"] != "KAZ" ||
		out["distance_m"].(float64) < 150_000 {
		t.Fatalf("intercity: %d %v", status, out)
	}
}

func TestPreviewRoutingErrors(t *testing.T) {
	env := newTestEnv(t, nil)
	token, _ := env.signIn("passenger")
	body := previewBody("taxi", stopJSON(embi, "A"), stopJSON(embiStation, "B"), stopJSON(embiBazaar, "C"))

	env.osrm.set(func(f *fakeOSRM) { f.code, f.message = "NoRoute", "No route found between points" })
	status, out := env.do(http.MethodPost, "/api/v1/routes/preview", token, body)
	if code, _, details := errInfo(out); status != 422 || code != "route_unavailable" || details["reason"] != "no_route" {
		t.Fatalf("no route: %d %v", status, out)
	}

	env.osrm.set(func(f *fakeOSRM) { f.code = ""; f.snap[2] = 2500 })
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, body)
	if code, _, details := errInfo(out); status != 422 || code != "route_unavailable" || details["reason"] != "point_not_routable" || details["index"] != float64(2) {
		t.Fatalf("not routable: %d %v", status, out)
	}

	env.osrm.set(func(f *fakeOSRM) { f.snap = map[int]float64{}; f.status = http.StatusServiceUnavailable })
	status, raw, hdr := env.raw(http.MethodPost, "/api/v1/routes/preview", token, body)
	if status != http.StatusServiceUnavailable || hdr.Get("Retry-After") == "" || !bytes.Contains(raw, []byte(`"route_unavailable"`)) {
		t.Fatalf("OSRM down: %d %s %v", status, raw, hdr)
	}
	var quotes int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM fare_quotes`).Scan(&quotes); err != nil || quotes != 0 {
		t.Fatalf("quotes stored on routing errors: %d %v", quotes, err)
	}
}

func TestGeoEndpoints(t *testing.T) {
	env := newTestEnv(t, nil)
	passenger, userID := env.signIn("passenger")
	driver, _ := env.signIn("driver")
	addPlace(t, env.pool, "Теміржол вокзалы", "Железнодорожный вокзал", "Railway station", embiStation, "вокзал")
	env.geo.search = []Place{
		{Title: "Оренбург вокзалы", Point: orenburg, Source: "photon"},
		{Title: "Ембі вокзалы", Point: geo.Point{Lat: embiStation.Lat + 0.0001, Lon: embiStation.Lon}, Source: "photon"},
		{Title: "Вокзал көшесі, 3", Subtitle: "Ембі", Point: embiSchool, Source: "photon"},
	}

	status, raw, _ := env.raw(http.MethodGet, "/api/v1/geo/search?q=%D0%B2%D0%BE%D0%BA%D0%B7%D0%B0%D0%BB&lat=48.8264&lon=58.1442", passenger, "")
	var places []map[string]any
	if err := json.Unmarshal(raw, &places); status != 200 || err != nil {
		t.Fatalf("search: %d %s", status, raw)
	}
	if len(places) != 2 || places[0]["source"] != "place" || places[0]["title"] != "Теміржол вокзалы" || places[0]["subtitle"] != "Ембі" ||
		places[0]["lat"] != embiStation.Lat || places[1]["title"] != "Вокзал көшесі, 3" || places[1]["source"] != "photon" {
		t.Fatalf("places = %v", places)
	}
	// The user's language preference is the default; ?lang= overrides it.
	if _, err := env.pool.Exec(context.Background(), `UPDATE user_preferences SET language = 'ru' WHERE user_id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	_, raw, _ = env.raw(http.MethodGet, "/api/v1/geo/search?q=%D0%B2%D0%BE%D0%BA%D0%B7%D0%B0%D0%BB", passenger, "")
	if err := json.Unmarshal(raw, &places); err != nil || len(places) == 0 || places[0]["title"] != "Железнодорожный вокзал" {
		t.Fatalf("ru preference: %s", raw)
	}
	_, raw, _ = env.raw(http.MethodGet, "/api/v1/geo/search?q=%D0%B2%D0%BE%D0%BA%D0%B7%D0%B0%D0%BB&lang=en&limit=1", driver, "")
	if err := json.Unmarshal(raw, &places); err != nil || len(places) != 1 || places[0]["title"] != "Railway station" {
		t.Fatalf("driver en: %s", raw)
	}

	for _, tt := range []struct {
		query  string
		fields map[string]string
	}{
		{"", map[string]string{"q": "required"}},
		{"q=a", map[string]string{"q": "too_short"}},
		{"q=" + strings.Repeat("x", 101), map[string]string{"q": "too_long"}},
		{"q=abc&lat=48.8", map[string]string{"lon": "required"}},
		{"q=abc&lat=x&lon=NaN", map[string]string{"lat": "invalid", "lon": "invalid"}},
		{"q=abc&lang=de", map[string]string{"lang": "invalid"}},
		{"q=abc&limit=50", map[string]string{"limit": "invalid"}},
	} {
		status, out := env.do(http.MethodGet, "/api/v1/geo/search?"+tt.query, passenger, "")
		_, fields, _ := errInfo(out)
		if status != 422 {
			t.Fatalf("%q: %d %v", tt.query, status, out)
		}
		for k, v := range tt.fields {
			if fields[k] != v {
				t.Errorf("%q: fields[%s] = %v, want %s", tt.query, k, fields[k], v)
			}
		}
	}

	// Reverse.
	status, out := env.do(http.MethodGet, "/api/v1/geo/reverse?lat=48.8264&lon=58.1442&lang=kk", passenger, "")
	if status != 200 || out["title"] != "" || out["subtitle"] != "Картадағы нүкте" || out["in_service_area"] != true ||
		out["source"] != "coordinates" || out["lat"] != 48.8264 || out["lon"] != 58.1442 {
		t.Fatalf("reverse fallback: %d %v", status, out)
	}
	status, out = env.do(http.MethodGet, "/api/v1/geo/reverse?lat=51.7682&lon=55.097", driver, "")
	if status != 200 || out["in_service_area"] != false || out["subtitle"] != "Қызмет көрсету аймағынан тыс" {
		t.Fatalf("reverse outside (driver, kk default): %d %v", status, out)
	}
	env.geo.mu.Lock()
	env.geo.reverse = &Place{Title: "Жиенқали Тілепбергенов көшесі, 12", Subtitle: "Ембі, Ақтөбе облысы", Point: embi, Source: "photon", CountryCode: "KZ"}
	env.geo.mu.Unlock()
	status, out = env.do(http.MethodGet, "/api/v1/geo/reverse?lat=48.8265&lon=58.1443", passenger, "")
	if status != 200 || out["title"] != "Жиенқали Тілепбергенов көшесі, 12" || out["subtitle"] != "Ембі, Ақтөбе облысы" || out["source"] != "photon" ||
		out["lat"] != 48.8265 || out["in_service_area"] != true {
		t.Fatalf("reverse provider: %d %v", status, out)
	}
	for _, q := range []string{"", "lat=48.8", "lat=91&lon=58", "lat=48&lon=58&lang=xx"} {
		if status, out := env.do(http.MethodGet, "/api/v1/geo/reverse?"+q, passenger, ""); status != 422 {
			t.Fatalf("reverse %q: %d %v", q, status, out)
		}
	}
	if status, _ := env.do(http.MethodGet, "/api/v1/geo/reverse?lat=48.8&lon=58.1", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("reverse without token: %d", status)
	}
}

func TestGeoRateLimit(t *testing.T) {
	env := newTestEnv(t, func(c *HandlerConfig) { c.GeoPerMinute = 3; c.PreviewPerMinute = 3 })
	a, _ := env.signIn("passenger")
	b, _ := env.signIn("passenger")
	path := "/api/v1/geo/reverse?lat=48.8264&lon=58.1442"
	if status, _ := env.do(http.MethodGet, path, a, ""); status != 200 {
		t.Fatalf("first: %d", status)
	}
	status, _, hdr := env.raw(http.MethodGet, path, a, "")
	if status != http.StatusTooManyRequests || hdr.Get("Retry-After") == "" {
		t.Fatalf("second within burst 1: %d", status)
	}
	// Limits are per user, and search shares the geo budget.
	if status, _ := env.do(http.MethodGet, path, b, ""); status != 200 {
		t.Fatalf("other user: %d", status)
	}
	if status, _ := env.do(http.MethodGet, "/api/v1/geo/search?q=abc", b, ""); status != http.StatusTooManyRequests {
		t.Fatalf("search after reverse: %d", status)
	}
	body := previewBody("taxi", stopJSON(embi, "A"), stopJSON(embiStation, "B"))
	if status, _ := env.do(http.MethodPost, "/api/v1/routes/preview", a, body); status != 200 {
		t.Fatalf("preview has its own budget: %d", status)
	}
	if status, _ := env.do(http.MethodPost, "/api/v1/routes/preview", a, body); status != http.StatusTooManyRequests {
		t.Fatalf("second preview: %d", status)
	}
}
