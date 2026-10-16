package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/api"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/logging"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/password"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.ForTesting("")
	cfg.FileStorageDir = t.TempDir()
	return cfg
}

func newTestApp(t *testing.T, pool *pgxpool.Pool, cfg config.Config) *App {
	t.Helper()
	log := logging.NewWriter(io.Discard, "debug", "text")
	a, err := NewWithPool(context.Background(), cfg, log, pool)
	if err != nil {
		t.Fatalf("NewWithPool: %v", err)
	}
	t.Cleanup(a.Close)
	return a
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func do(t *testing.T, h http.Handler, method, path string, hdr ...string) response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return response{status: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("body is not a JSON error envelope: %v: %s", err, body)
	}
	if env.Error.Message == "" {
		t.Fatalf("error envelope without message: %s", body)
	}
	return env.Error.Code
}

func TestHealthAndReadiness(t *testing.T) {
	pool := testutil.NewDB(t)
	a := newTestApp(t, pool, testConfig(t))

	for _, h := range []http.Handler{a.Handler(), a.InternalHandler()} {
		res := do(t, h, http.MethodGet, "/healthz")
		if res.status != http.StatusOK || !strings.Contains(string(res.body), `"status":"ok"`) {
			t.Fatalf("healthz: %d %s", res.status, res.body)
		}
	}

	// Readiness may depend on background workers (e.g. the realtime LISTEN
	// connection), so serve for real and wait until the instance is ready.
	stop := serveInBackground(t, a)
	defer stop()

	var res response
	deadline := time.Now().Add(10 * time.Second)
	for {
		res = do(t, a.Handler(), http.MethodGet, "/readyz")
		if res.status == http.StatusOK || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if res.status != http.StatusOK {
		t.Fatalf("readyz: %d %s", res.status, res.body)
	}
	var ready struct {
		Status        string            `json:"status"`
		Checks        map[string]string `json:"checks"`
		SchemaVersion int64             `json:"schema_version"`
	}
	if err := json.Unmarshal(res.body, &ready); err != nil {
		t.Fatal(err)
	}
	latest, _ := db.LatestMigrationVersion()
	if ready.Status != "ready" || ready.SchemaVersion != latest || ready.Checks["database"] != "ok" || ready.Checks["migrations"] != "ok" {
		t.Fatalf("readyz body: %+v (latest %d)", ready, latest)
	}
	if res.header.Get("X-Request-ID") == "" || res.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("standard middleware headers missing: %v", res.header)
	}
}

// serveInBackground runs a.Serve on loopback listeners and returns a function
// that stops it and waits for a clean exit.
func serveInBackground(t *testing.T, a *App) func() {
	t.Helper()
	pub, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	internal, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, pub, internal) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("Serve did not stop")
		}
	}
}

func TestReadinessFailures(t *testing.T) {
	pool := testutil.NewDB(t)
	a := newTestApp(t, pool, testConfig(t))
	ctx := context.Background()

	// A registered check that fails makes the instance unready.
	failing := atomic.Bool{}
	a.checks = append(a.checks, readinessCheck{name: "test_check", fn: func(context.Context) error {
		if failing.Load() {
			return io.ErrUnexpectedEOF
		}
		return nil
	}})
	failing.Store(true)
	if res := do(t, a.Handler(), http.MethodGet, "/readyz"); res.status != http.StatusServiceUnavailable ||
		!strings.Contains(string(res.body), `"test_check":"failing"`) {
		t.Fatalf("failing check: %d %s", res.status, res.body)
	}
	failing.Store(false)

	// Schema behind the binary: the newest migration row is missing.
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = (SELECT max(version_id) FROM goose_db_version)`); err != nil {
		t.Fatal(err)
	}
	res := do(t, a.Handler(), http.MethodGet, "/readyz")
	if res.status != http.StatusServiceUnavailable || !strings.Contains(string(res.body), `"migrations":"pending"`) {
		t.Fatalf("pending migrations: %d %s", res.status, res.body)
	}

	// Draining during shutdown.
	a.draining.Store(true)
	if res := do(t, a.Handler(), http.MethodGet, "/readyz"); res.status != http.StatusServiceUnavailable {
		t.Fatalf("draining: %d", res.status)
	}
}

func TestUnknownRoutesUseJSONErrorEnvelope(t *testing.T) {
	pool := testutil.NewDB(t)
	a := newTestApp(t, pool, testConfig(t))

	tests := []struct {
		handler http.Handler
		method  string
		path    string
		status  int
		code    string
	}{
		{a.Handler(), http.MethodGet, "/api/v1/definitely-not-a-route", 404, "not_found"},
		{a.Handler(), http.MethodGet, "/api/v1/admin/definitely-not-a-route", 404, "not_found"},
		{a.Handler(), http.MethodGet, "/not-api", 404, "not_found"},
		{a.Handler(), http.MethodPost, "/healthz", 405, "method_not_allowed"},
		{a.Handler(), http.MethodDelete, "/api/v1/openapi.yaml", 405, "method_not_allowed"},
		{a.InternalHandler(), http.MethodGet, "/nope", 404, "not_found"},
		// Unknown internal routes are behind the service middleware: no route enumeration.
		{a.InternalHandler(), http.MethodGet, "/internal/v1/nope", 401, "unauthorized"},
	}
	for _, tc := range tests {
		res := do(t, tc.handler, tc.method, tc.path)
		if res.status != tc.status {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.path, res.status, tc.status)
			continue
		}
		if ct := res.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s %s: content type %q", tc.method, tc.path, ct)
		}
		if code := errorCode(t, res.body); code != tc.code {
			t.Errorf("%s %s: code %q, want %q", tc.method, tc.path, code, tc.code)
		}
	}
}

func TestOpenAPIServed(t *testing.T) {
	pool := testutil.NewDB(t)
	a := newTestApp(t, pool, testConfig(t))
	res := do(t, a.Handler(), http.MethodGet, "/api/v1/openapi.yaml")
	if res.status != http.StatusOK || res.header.Get("Content-Type") != "application/yaml" || !bytes.Equal(res.body, api.Spec) {
		t.Fatalf("openapi: %d %q", res.status, res.header.Get("Content-Type"))
	}
	etag := res.header.Get("ETag")
	if res := do(t, a.Handler(), http.MethodGet, "/api/v1/openapi.yaml", "If-None-Match", etag); res.status != http.StatusNotModified {
		t.Fatalf("conditional GET: %d", res.status)
	}
}

func TestProtectedRoutersRequireAuthentication(t *testing.T) {
	pool := testutil.NewDB(t)
	a := newTestApp(t, pool, testConfig(t))
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }
	a.Public.Get("/zz-test/public", ok)
	a.Passenger.Get("/zz-test/passenger", ok)
	a.Driver.Get("/zz-test/driver", ok)
	a.AnyApp.Get("/zz-test/any", ok)
	a.AdminPublic.Get("/zz-test/admin-public", ok)
	a.Admin.Get("/zz-test/admin", ok)
	a.Internal.Get("/zz-test/internal", ok)

	tests := []struct {
		h      http.Handler
		path   string
		status int
	}{
		{a.Handler(), "/api/v1/zz-test/public", http.StatusTeapot},
		{a.Handler(), "/api/v1/admin/zz-test/admin-public", http.StatusTeapot},
		{a.Handler(), "/api/v1/zz-test/passenger", http.StatusUnauthorized},
		{a.Handler(), "/api/v1/zz-test/driver", http.StatusUnauthorized},
		{a.Handler(), "/api/v1/zz-test/any", http.StatusUnauthorized},
		{a.Handler(), "/api/v1/admin/zz-test/admin", http.StatusUnauthorized},
		{a.InternalHandler(), "/internal/v1/zz-test/internal", http.StatusUnauthorized},
		{a.Handler(), "/internal/v1/zz-test/internal", http.StatusNotFound}, // never on the public port
	}
	for _, tc := range tests {
		if res := do(t, tc.h, http.MethodGet, tc.path); res.status != tc.status {
			t.Errorf("GET %s: %d, want %d (%s)", tc.path, res.status, tc.status, res.body)
		}
	}
}

func TestLoginRateLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	a := newTestApp(t, pool, testConfig(t))
	var last response
	for i := 0; i < authBurst+1; i++ {
		last = do(t, a.Handler(), http.MethodPost, "/api/v1/admin/auth/login")
	}
	if last.status != http.StatusTooManyRequests || errorCode(t, last.body) != "too_many_requests" {
		t.Fatalf("request %d: %d %s", authBurst+1, last.status, last.body)
	}
	// Other endpoints for the same IP keep their own, larger budget.
	if res := do(t, a.Handler(), http.MethodGet, "/api/v1/openapi.yaml"); res.status != http.StatusOK {
		t.Fatalf("general budget exhausted too early: %d", res.status)
	}
}

func TestCORS(t *testing.T) {
	pool := testutil.NewDB(t)
	a := newTestApp(t, pool, testConfig(t))
	res := do(t, a.Handler(), http.MethodOptions, "/api/v1/me",
		"Origin", "http://localhost:5173", "Access-Control-Request-Method", "GET")
	if res.status != http.StatusNoContent || res.header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("allowed origin preflight: %d %v", res.status, res.header)
	}
	res = do(t, a.Handler(), http.MethodOptions, "/api/v1/me",
		"Origin", "https://evil.example", "Access-Control-Request-Method", "GET")
	if res.status != http.StatusForbidden || res.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign origin preflight: %d %v", res.status, res.header)
	}
}

func TestServeRunsJobsAndShutsDown(t *testing.T) {
	pool := testutil.NewDB(t)
	cfg := testConfig(t)
	cfg.JobsEnabled = true
	a := newTestApp(t, pool, cfg)

	var runs, panics, workerStarts atomic.Int32
	a.AddJob("test_counter", 20*time.Millisecond, func(ctx context.Context) error {
		runs.Add(1)
		return nil
	})
	a.AddJob("test_panic", 20*time.Millisecond, func(ctx context.Context) error {
		panics.Add(1)
		panic("boom")
	})
	a.AddWorker("test_worker", func(ctx context.Context) error {
		workerStarts.Add(1)
		<-ctx.Done()
		return nil
	})
	if len(a.wireErrs) != 0 {
		t.Fatalf("registration errors: %v", a.wireErrs)
	}

	pub, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	internal, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, pub, internal) }()

	for _, addr := range []string{pub.Addr().String(), internal.Addr().String()} {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s healthz: %d", addr, resp.StatusCode)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for (runs.Load() < 2 || panics.Load() < 2 || workerStarts.Load() < 1) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if runs.Load() < 2 || panics.Load() < 2 || workerStarts.Load() != 1 {
		t.Fatalf("runs=%d panics=%d worker=%d", runs.Load(), panics.Load(), workerStarts.Load())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not stop")
	}
	if _, err := http.Get("http://" + pub.Addr().String() + "/healthz"); err == nil {
		t.Fatal("server still accepting after shutdown")
	}
	a.AddJob("late", time.Second, func(context.Context) error { return nil })
	if len(a.wireErrs) == 0 {
		t.Fatal("registering a job after Run must be rejected")
	}
}

func TestAddJobValidation(t *testing.T) {
	a := &App{}
	noop := func(context.Context) error { return nil }
	a.AddJob("a", time.Second, noop)
	a.AddJob("a", time.Second, noop)
	a.AddJob("", time.Second, noop)
	a.AddJob("b", 0, noop)
	a.AddWorker("a", noop)
	a.AddWorker("c", nil)
	if len(a.jobs) != 1 || len(a.wireErrs) != 5 {
		t.Fatalf("jobs=%d errs=%v", len(a.jobs), a.wireErrs)
	}
}

func TestJobTimingHelpers(t *testing.T) {
	for _, every := range []time.Duration{time.Millisecond, time.Second, 15 * time.Second, 24 * time.Hour} {
		if d := firstDelay(every); d < 0 || d > min(every, 5*time.Second) {
			t.Errorf("firstDelay(%v) = %v", every, d)
		}
		if d := nextDelay(every); d < every || d > every+min(every/10, time.Minute) {
			t.Errorf("nextDelay(%v) = %v", every, d)
		}
		if d := jobTimeout(every); d < time.Minute || d > 10*time.Minute {
			t.Errorf("jobTimeout(%v) = %v", every, d)
		}
	}
}

func TestBootstrapAdmin(t *testing.T) {
	pool := testutil.NewDB(t)
	cfg := testConfig(t)
	cfg.AdminBootstrapEmail = "root@jantaxi.kz"
	cfg.AdminBootstrapPassword = "correct horse battery staple"
	newTestApp(t, pool, cfg)
	newTestApp(t, pool, cfg) // second start: no duplicate

	ctx := context.Background()
	var n int
	var role, hash string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(role), max(password_hash) FROM admin_users`).Scan(&n, &role, &hash); err != nil {
		t.Fatal(err)
	}
	if n != 1 || role != "super_admin" {
		t.Fatalf("admins=%d role=%s", n, role)
	}
	if ok, err := password.Verify(cfg.AdminBootstrapPassword, hash); err != nil || !ok {
		t.Fatalf("password does not verify: %v", err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE action = 'admin.bootstrap'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows: %d %v", audits, err)
	}
}

func TestMissingMiddlewareFailsClosed(t *testing.T) {
	m := Middlewares{Passenger: func(h http.Handler) http.Handler { return h }}
	if m.validate() == nil {
		t.Fatal("incomplete middlewares must be rejected")
	}
}
