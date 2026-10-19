package botapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

const testSecret = "botapi-test-secret-0123456789abcdef"

type testEnv struct {
	pool *pgxpool.Pool
	srv  *httptest.Server
	h    *Handler
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	pool := testutil.NewDB(t)
	h, err := New(Options{
		Pool: pool, Log: slog.New(slog.DiscardHandler),
		PassengerMiniAppURL: "https://app.example.kz/passenger/", DriverMiniAppURL: "https://app.example.kz/driver/",
		PassengerBotUsername: "@jantaxi_bot", DriverBotUsername: "jantaxi_driver_bot",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	iv1 := chi.NewRouter()
	iv1.Use(auth.RequireService(testSecret))
	h.Mount(iv1.With())
	r.Mount("/internal/v1", iv1)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &testEnv{pool: pool, srv: srv, h: h}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// call sends a request as bot and decodes a JSON response into a map.
func (e *testEnv) call(t *testing.T, bot, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+"/internal/v1"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testSecret)
	if bot != "" {
		req.Header.Set(auth.BotHeader, bot)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode, out
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func errDetail(m map[string]any, key string) any {
	e, _ := m["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	return d[key]
}

// insertOrder creates an Embi taxi order with a pickup and a destination stop
// (the destination has no address, so it is shown as coordinates).
func insertOrder(t *testing.T, pool *pgxpool.Pool, passengerID uuid.UUID, driverID *uuid.UUID, status string, fare int, createdAt time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO orders (passenger_id, city_id, category, service_type, cargo_type, status, driver_id, fare,
		                    commission_bps, commission_amount, payment_method, tariff_id, distance_m, duration_s,
		                    stop_count, pickup_lat, pickup_lon, idempotency_key, accepted_at, cancelled_at,
		                    cancelled_by, search_expires_at, created_at, vehicle_class, passenger_count)
		SELECT $1, t.city_id, 'taxi', 'passenger', 'none', $2, $3, $4, 1000, ($4 * 1000 + 5000) / 10000, 'cash',
		       t.id, 1800, 300, 2, 48.8264, 58.1442, $5,
		       CASE WHEN $3::uuid IS NOT NULL THEN now() END,
		       CASE WHEN $2 = 'cancelled' THEN now() END,
		       CASE WHEN $2 = 'cancelled' THEN 'passenger' END,
		       CASE WHEN $2 = 'searching' THEN now() + interval '10 minutes' END, $6, 'sedan', 1
		FROM tariffs t JOIN cities c ON c.id = t.city_id
		WHERE c.code = 'EMB' AND t.category = 'taxi' AND t.vehicle_class = 'sedan' AND t.is_active
		RETURNING id`, passengerID, status, driverID, fare, uuid.NewString(), createdAt).Scan(&id)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO order_stops (order_id, seq, kind, lat, lon, address) VALUES
		($1, 0, 'pickup', 48.8264, 58.1442, 'Жиенқали Тілепбергенов көшесі, 12'),
		($1, 1, 'destination', 48.8301, 58.1510, '')`, id)
	if err != nil {
		t.Fatalf("insert stops: %v", err)
	}
	return id
}
