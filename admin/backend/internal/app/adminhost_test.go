package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// The admin API must not answer on the public Mini App host (the quick
// tunnel forwards that host to the API as well).
func TestAdminAPINotServedOnMiniAppHost(t *testing.T) {
	pool := testutil.NewDB(t)
	cfg := testConfig(t)
	cfg.PassengerMiniAppURL = "https://app.jantaxi.example/passenger/"
	cfg.DriverMiniAppURL = "https://app.jantaxi.example/driver/"
	cfg.AdminFrontendURL = "https://admin.jantaxi.example/"
	a := newTestApp(t, pool, cfg)

	send := func(method, host, path string) response {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{"email":"owner@jantaxi.example","password":"x"}`))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.Handler().ServeHTTP(rec, req)
		return response{status: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
	}

	for _, tc := range []struct {
		method, host, path string
		status             int
	}{
		// Mini App host, in any spelling: nothing there, not even login.
		{http.MethodGet, "app.jantaxi.example", "/api/v1/admin/auth/me", http.StatusNotFound},
		{http.MethodPost, "app.jantaxi.example", "/api/v1/admin/auth/login", http.StatusNotFound},
		{http.MethodGet, "APP.jantaxi.example:443", "/api/v1/admin/auth/me", http.StatusNotFound},
		{http.MethodGet, "app.jantaxi.example.", "/api/v1/admin/orders", http.StatusNotFound},
		{http.MethodGet, "app.jantaxi.example", "/api/v1/admin/definitely-not-a-route", http.StatusNotFound},
		// The admin host and direct calls keep working.
		{http.MethodGet, "admin.jantaxi.example", "/api/v1/admin/auth/me", http.StatusUnauthorized},
		{http.MethodGet, "127.0.0.1:8089", "/api/v1/admin/auth/me", http.StatusUnauthorized},
		{http.MethodPost, "admin.jantaxi.example", "/api/v1/admin/auth/login", http.StatusUnauthorized},
		// The Mini App API on its own host is untouched.
		{http.MethodGet, "app.jantaxi.example", "/api/v1/public/config?app=passenger", http.StatusOK},
	} {
		res := send(tc.method, tc.host, tc.path)
		if res.status != tc.status {
			t.Errorf("%s %s%s: %d, want %d (%s)", tc.method, tc.host, tc.path, res.status, tc.status, res.body)
			continue
		}
		if tc.status == http.StatusNotFound {
			if code := errorCode(t, res.body); code != "not_found" {
				t.Errorf("%s %s%s: code %q", tc.method, tc.host, tc.path, code)
			}
		}
	}

	// When the admin panel shares the Mini App host, nothing is hidden.
	shared := testConfig(t)
	shared.PassengerMiniAppURL = "https://jantaxi.example/passenger/"
	shared.DriverMiniAppURL = "https://jantaxi.example/driver/"
	shared.AdminFrontendURL = "https://jantaxi.example/admin/"
	b := newTestApp(t, pool, shared)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/auth/me", nil)
	req.Host = "jantaxi.example"
	rec := httptest.NewRecorder()
	b.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("shared host: %d %s", rec.Code, rec.Body)
	}
}

// The edge answers 404 for the admin API on the Mini App site before its API
// proxy, so admin endpoints are reachable only on ADMIN_DOMAIN.
func TestCaddyfileKeepsAdminAPIOffMiniAppSite(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	start := strings.Index(src, "\n{$APP_DOMAIN} {")
	end := strings.Index(src, "\n{$ADMIN_DOMAIN} {")
	if start < 0 || end < start {
		t.Fatal("Caddyfile: Mini App and admin sites not found")
	}
	app, admin := src[start:end], src[end:]
	block := regexp.MustCompile(`(?m)^\thandle /api/v1/admin\* \{\n\t\trespond "Not found" 404\n\t\}`)
	loc := block.FindStringIndex(app)
	if loc == nil {
		t.Fatalf("the Mini App site must answer 404 for /api/v1/admin*:\n%s", app)
	}
	if proxy := strings.Index(app, "import api_proxy"); proxy < 0 || proxy < loc[0] {
		t.Fatal("the admin 404 must come before the Mini App site's API proxy")
	}
	if block.MatchString(admin) || !strings.Contains(admin, "import api_proxy") {
		t.Fatal("the admin site must keep serving the admin API")
	}
}
