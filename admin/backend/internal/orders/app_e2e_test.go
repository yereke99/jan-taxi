package orders_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/app"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// TestOrderThroughApp drives an order through the real application wiring:
// Mini App sign-in for both apps, booking, the driver's trip and an operator
// session with CSRF.
func TestOrderThroughApp(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	cfg := config.ForTesting("")
	cfg.FileStorageDir = t.TempDir()
	a, err := app.NewWithPool(ctx, cfg, slog.New(slog.DiscardHandler), pool)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	t.Cleanup(func() { settings.Configure(settings.Env{}) })
	h := a.Handler()

	call := func(method, path string, body any, hdr map[string]string) (int, map[string]any, *httptest.ResponseRecorder) {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, path, &buf)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out, rec
	}
	signIn := func(app string, token string, tgID int64) map[string]string {
		t.Helper()
		code, out, _ := call(http.MethodPost, "/api/v1/auth/telegram/"+app,
			map[string]any{"init_data": testutil.SignInitData(token, tgID, "Айгүл", time.Now(), "home")}, nil)
		if code != http.StatusOK {
			t.Fatalf("%s sign-in: %d %v", app, code, out)
		}
		return map[string]string{"Authorization": "Bearer " + out["token"].(string)}
	}

	// Passenger books a ride.
	pTG := testutil.NextTelegramID()
	passenger := signIn("passenger", config.TestPassengerBotToken, pTG)
	var pid uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE telegram_user_id = $1`, pTG).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	cityID := testutil.CityID(t, pool, "EMB")
	tariff, err := pricing.ActiveTariff(ctx, pool, cityID, "taxi", "sedan")
	if err != nil {
		t.Fatal(err)
	}
	q, err := pricing.CreateQuote(ctx, pool, pricing.NewQuote{
		UserID: pid, CityID: cityID, Tariff: tariff, Stops: embiStops[:2], DistanceM: 1840, DurationS: 260,
		Legs:     []pricing.Leg{{DistanceM: 1840, DurationS: 260}},
		Geometry: json.RawMessage(`{"type":"LineString","coordinates":[[58.1442,48.8264],[58.1504,48.8291]]}`),
		BBox:     [4]float64{58.1442, 48.8264, 58.1504, 48.8291}, Fare: pricing.Compute(tariff, 1840, 260, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	drv := testutil.CreateDriver(t, pool, testutil.DriverOptions{Online: true, Balance: 500})
	driver := signIn("driver", config.TestDriverBotToken, drv.TelegramID)

	code, o, _ := call(http.MethodPost, "/api/v1/orders", taxiBody(q, 800), passenger)
	if code != http.StatusCreated || o["status"] != "searching" {
		t.Fatalf("create: %d %v", code, o)
	}
	id := o["id"].(string)
	number := strconv.FormatInt(int64(o["number"].(float64)), 10)

	// Tokens of one app do not open the other app's routes.
	if code, _, _ := call(http.MethodGet, "/api/v1/orders/"+id, nil, driver); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("driver token on passenger route: %d", code)
	}
	if code, _, _ := call(http.MethodGet, "/api/v1/driver/orders", nil, passenger); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("passenger token on driver route: %d", code)
	}

	// Driver trip.
	code, avail, _ := call(http.MethodGet, "/api/v1/driver/orders", nil, driver)
	if code != http.StatusOK || len(avail["items"].([]any)) != 1 {
		t.Fatalf("available: %d %v", code, avail)
	}
	for _, step := range []string{"accept", "arriving", "start", "complete"} {
		code, d, _ := call(http.MethodPost, "/api/v1/driver/orders/"+id+"/"+step, nil, driver)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %v", step, code, d)
		}
	}
	if code, p, _ := call(http.MethodGet, "/api/v1/orders/"+id, nil, passenger); code != http.StatusOK || p["status"] != "completed" {
		t.Fatalf("passenger view: %d %v", code, p)
	}

	// Operator session.
	const pw = "correct horse battery staple"
	if _, err := a.Auth.CreateAdmin(ctx, "ops@jantaxi.test", "Operator", auth.RoleOperator, pw); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	code, login, rec := call(http.MethodPost, "/api/v1/admin/auth/login", map[string]any{"email": "ops@jantaxi.test", "password": pw}, nil)
	if code != http.StatusOK {
		t.Fatalf("login: %d %v", code, login)
	}
	var cookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.AdminCookieName {
			cookie = c.Value
		}
	}
	session := map[string]string{"Cookie": auth.AdminCookieName + "=" + cookie, auth.CSRFHeader: login["csrf_token"].(string)}
	if code, l, _ := call(http.MethodGet, "/api/v1/admin/orders?q=%23"+number, nil, session); code != http.StatusOK || l["total"].(float64) != 1 {
		t.Fatalf("admin search: %d %v", code, l)
	}
	if code, d, _ := call(http.MethodGet, "/api/v1/admin/orders/"+id, nil, session); code != http.StatusOK || d["commission_amount"].(float64) != 50 ||
		d["commission_mode"] != "fixed" {
		t.Fatalf("admin detail: %d %v", code, d)
	}
	reason := map[string]any{"reason": "Жолаушы шағымы"}
	if code, _, _ := call(http.MethodPost, "/api/v1/admin/orders/"+id+"/dispute", reason, map[string]string{"Cookie": session["Cookie"]}); code != http.StatusForbidden {
		t.Fatalf("dispute without CSRF: %d", code)
	}
	if code, d, _ := call(http.MethodPost, "/api/v1/admin/orders/"+id+"/dispute", reason, session); code != http.StatusOK || d["status"] != "disputed" {
		t.Fatalf("dispute: %d %v", code, d)
	}
	if code, l, _ := call(http.MethodGet, "/api/v1/admin/disputes?status=open", nil, session); code != http.StatusOK || l["total"].(float64) != 1 {
		t.Fatalf("disputes: %d %v", code, l)
	}
	if code, l, _ := call(http.MethodGet, "/api/v1/admin/drivers/"+drv.DriverID.String()+"/orders", nil, session); code != http.StatusOK || l["total"].(float64) != 1 {
		t.Fatalf("driver orders: %d %v", code, l)
	}
	if code, _, _ := call(http.MethodGet, "/api/v1/admin/orders", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("admin without session: %d", code)
	}
}
