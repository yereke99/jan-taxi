package orders_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
)

// env is a real database plus the order routes behind the real app-token
// middlewares. Admin routes get the admin principal from test headers (the
// session/CSRF layer belongs to auth and is tested there).
type env struct {
	t       *testing.T
	pool    *pgxpool.Pool
	auth    *auth.Service
	handler http.Handler
	cityID  uuid.UUID
	adminID uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewDB(t)
	log := slog.New(slog.DiscardHandler)
	as, err := auth.NewService(pool, auth.Config{
		PassengerBotToken: config.TestPassengerBotToken,
		DriverBotToken:    config.TestDriverBotToken,
		SessionSecret:     config.TestSessionSecret,
		Audit: func(context.Context, db.Querier, *http.Request, auth.AuditEntry) error {
			return nil
		},
		Log: log,
	})
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	svc := orders.New(pool, log)

	r := chi.NewRouter()
	svc.MountPassenger(r.With(as.RequireApp(auth.AppPassenger)))
	svc.MountDriver(r.With(as.RequireApp(auth.AppDriver), as.LoadDriverProfile(true)))
	admin := chi.NewRouter()
	svc.MountAdmin(admin.With(testAdmin))
	r.Mount("/admin", admin)

	e := &env{t: t, pool: pool, auth: as, handler: r, cityID: testutil.CityID(t, pool, "EMB")}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role)
		VALUES ('ops@jantaxi.test', 'Operator Test', 'x', 'super_admin') RETURNING id`).Scan(&e.adminID); err != nil {
		t.Fatalf("admin: %v", err)
	}
	return e
}

func testAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.Header.Get("X-Test-Admin"))
		if err != nil {
			httpx.WriteError(w, r, httpx.Unauthorized())
			return
		}
		role := auth.Role(r.Header.Get("X-Test-Role"))
		if role == "" {
			role = auth.RoleSuperAdmin
		}
		ctx := auth.WithAdmin(r.Context(), auth.AdminPrincipal{AdminID: id, Role: role, Email: "ops@jantaxi.test"})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// caller identifies who sends a request.
type caller struct {
	token string
	admin uuid.UUID
	role  auth.Role
}

func (e *env) token(userID uuid.UUID, app auth.App) caller {
	e.t.Helper()
	tok, _, err := e.auth.IssueAppToken(userID, app, time.Now())
	if err != nil {
		e.t.Fatalf("issue token: %v", err)
	}
	return caller{token: tok}
}

func (e *env) passenger() (uuid.UUID, caller) {
	e.t.Helper()
	id, _ := testutil.CreateUser(e.t, e.pool)
	return id, e.token(id, auth.AppPassenger)
}

type testDriver struct {
	testutil.DriverFixture
	c caller
}

func (e *env) driver(balance int64, online bool) testDriver {
	e.t.Helper()
	f := testutil.CreateDriver(e.t, e.pool, testutil.DriverOptions{Online: online, Balance: balance})
	return testDriver{DriverFixture: f, c: e.token(f.UserID, auth.AppDriver)}
}

func (e *env) operator() caller { return caller{admin: e.adminID, role: auth.RoleSuperAdmin} }

// response is a decoded HTTP response.
type response struct {
	Status int
	Body   []byte
}

func (r response) decode(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, dst); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
}

type apiError struct {
	Error struct {
		Code    string            `json:"code"`
		Fields  map[string]string `json:"fields"`
		Details map[string]any    `json:"details"`
	} `json:"error"`
}

func (r response) err(t *testing.T) apiError {
	t.Helper()
	var e apiError
	r.decode(t, &e)
	return e
}

func (e *env) do(c caller, method, path string, body any) response {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("encode: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.admin != uuid.Nil {
		req.Header.Set("X-Test-Admin", c.admin.String())
		req.Header.Set("X-Test-Role", string(c.role))
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	if m := zonedTime.Find(rec.Body.Bytes()); m != nil {
		e.t.Errorf("%s %s: timestamp %s is not UTC", method, path, m)
	}
	return response{Status: rec.Code, Body: rec.Body.Bytes()}
}

// zonedTime matches an RFC 3339 timestamp with a numeric offset; the API
// always writes UTC ("Z").
var zonedTime = regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?[+-]\d{2}:\d{2}"`)

// expect asserts the status (and, for errors, the code) of a response.
func (e *env) expect(r response, status int, code string) {
	e.t.Helper()
	if r.Status != status {
		e.t.Fatalf("status %d, want %d: %s", r.Status, status, r.Body)
	}
	if code != "" {
		if got := r.err(e.t).Error.Code; got != code {
			e.t.Fatalf("code %q, want %q: %s", got, code, r.Body)
		}
	}
}

// Embi points (all inside the seeded 15 km zone).
var embiStops = []pricing.QuoteStop{
	{Lat: 48.8264, Lon: 58.1442, Address: "Жиенқали Тілепбергенов көшесі, 12", AddressDetails: "2-кіреберіс"},
	{Lat: 48.8291, Lon: 58.1504, Address: "Теміржол вокзалы"},
	{Lat: 48.8310, Lon: 58.1550, Address: "Орталық базар"},
	{Lat: 48.8330, Lon: 58.1600, Address: "Аудандық аурухана"},
	{Lat: 48.8350, Lon: 58.1650, Address: "Мектеп №3"},
}

// quote stores a fare quote for userID through the pricing package, as
// POST /routes/preview would.
func (e *env) quote(userID uuid.UUID, category string, stops []pricing.QuoteStop) pricing.StoredQuote {
	e.t.Helper()
	ctx := context.Background()
	tariff, err := pricing.ActiveTariff(ctx, e.pool, e.cityID, category)
	if err != nil {
		e.t.Fatalf("tariff: %v", err)
	}
	legs := make([]pricing.Leg, len(stops)-1)
	coords := make([][2]float64, len(stops))
	dist, dur := 0, 0
	for i := range stops {
		coords[i] = [2]float64{stops[i].Lon, stops[i].Lat}
		if i > 0 {
			legs[i-1] = pricing.Leg{DistanceM: 900 + 100*i, DurationS: 120 + 10*i}
			dist += legs[i-1].DistanceM
			dur += legs[i-1].DurationS
		}
	}
	geometry, _ := json.Marshal(map[string]any{"type": "LineString", "coordinates": coords})
	q, err := pricing.CreateQuote(ctx, e.pool, pricing.NewQuote{
		UserID: userID, CityID: e.cityID, Tariff: tariff, Stops: stops, DistanceM: dist, DurationS: dur,
		Legs: legs, Geometry: geometry, BBox: [4]float64{58.1442, 48.8264, 58.1650, 48.8350},
		Fare: pricing.Compute(tariff, dist, dur, len(stops)),
	})
	if err != nil {
		e.t.Fatalf("quote: %v", err)
	}
	return q
}

func taxiBody(q pricing.StoredQuote, fare int) map[string]any {
	return map[string]any{
		"quote_id": q.ID.String(), "fare": fare, "payment_method": "cash",
		"idempotency_key": uuid.NewString(), "comment": "Кіреберіс алдында күтемін",
	}
}

// order creates a taxi order with the given stops and fare for a new passenger.
func (e *env) order(stops []pricing.QuoteStop, fare int) (orders.Order, uuid.UUID, caller) {
	e.t.Helper()
	pid, pc := e.passenger()
	q := e.quote(pid, "taxi", stops)
	r := e.do(pc, http.MethodPost, "/orders", taxiBody(q, fare))
	e.expect(r, http.StatusCreated, "")
	var o orders.Order
	r.decode(e.t, &o)
	return o, pid, pc
}

func (e *env) wallet(driverID uuid.UUID) (balance, reserved int64) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(),
		`SELECT balance, reserved FROM wallets WHERE driver_id = $1`, driverID).Scan(&balance, &reserved); err != nil {
		e.t.Fatalf("wallet: %v", err)
	}
	return balance, reserved
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func (e *env) status(orderID uuid.UUID) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1`, orderID).Scan(&s); err != nil {
		e.t.Fatalf("status: %v", err)
	}
	return s
}

func (e *env) outbox(template string, userID uuid.UUID) int {
	return e.count(`SELECT count(*) FROM notification_outbox WHERE template = $1 AND user_id = $2`, template, userID)
}

func path(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
