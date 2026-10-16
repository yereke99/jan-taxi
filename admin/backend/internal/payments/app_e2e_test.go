package payments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/app"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// TestFinanceThroughApp drives the real wiring: driver sign-in, top-up and
// "I paid" in the Mini App API, then a finance admin session confirms it.
func TestFinanceThroughApp(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	cfg := config.ForTesting("")
	cfg.FileStorageDir = t.TempDir()
	a, err := app.NewWithPool(ctx, cfg, slog.New(slog.DiscardHandler), pool)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	t.Cleanup(func() { settings.Configure(settings.Env{}) }) // wireCore installs process-wide env defaults
	h := a.Handler()

	call := func(method, path string, body any, hdr map[string]string) (int, map[string]any) {
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
		if rec.Code == http.StatusOK && path == "/api/v1/admin/auth/login" {
			for _, c := range rec.Result().Cookies() {
				if c.Name == auth.AdminCookieName {
					out["_cookie"] = c.Value
				}
			}
		}
		return rec.Code, out
	}

	f := testutil.CreateDriver(t, pool, testutil.DriverOptions{Status: "approved"})
	initData := testutil.SignInitData(config.TestDriverBotToken, f.TelegramID, "Test", time.Now(), "wallet")
	code, tok := call(http.MethodPost, "/api/v1/auth/telegram/driver", map[string]any{"init_data": initData}, nil)
	if code != http.StatusOK {
		t.Fatalf("driver sign-in: %d %v", code, tok)
	}
	bearer := map[string]string{"Authorization": "Bearer " + tok["token"].(string)}

	code, w := call(http.MethodGet, "/api/v1/driver/wallet", nil, bearer)
	if code != http.StatusOK || w["balance"].(float64) != 0 {
		t.Fatalf("wallet: %d %v", code, w)
	}
	code, top := call(http.MethodPost, "/api/v1/driver/wallet/topups", map[string]any{"amount": 2000, "idempotency_key": uuid.NewString()}, bearer)
	if code != http.StatusCreated || top["payment"].(map[string]any)["url"] != cfg.KaspiPayPaymentURL {
		t.Fatalf("create top-up: %d %v", code, top)
	}
	id := top["id"].(string)
	if code, s := call(http.MethodPost, "/api/v1/driver/wallet/topups/"+id+"/submit", nil, bearer); code != http.StatusOK || s["status"] != "pending_verification" {
		t.Fatalf("submit: %d %v", code, s)
	}

	// Unauthenticated access is refused; the Kaspi webhook does not exist.
	if code, _ := call(http.MethodGet, "/api/v1/admin/topups", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("admin without session: %d", code)
	}
	if code, _ := call(http.MethodGet, "/api/v1/driver/wallet", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("wallet without token: %d", code)
	}
	if code, _ := call(http.MethodPost, "/api/v1/payments/webhooks/kaspi_manual", map[string]any{}, nil); code != http.StatusNotFound {
		t.Fatalf("webhook: %d", code)
	}

	const pw = "correct horse battery staple"
	if _, err := a.Auth.CreateAdmin(ctx, "finance@jantaxi.test", "Finance", auth.RoleFinanceManager, pw); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	code, login := call(http.MethodPost, "/api/v1/admin/auth/login", map[string]any{"email": "finance@jantaxi.test", "password": pw}, nil)
	if code != http.StatusOK || login["_cookie"] == nil {
		t.Fatalf("login: %d %v", code, login)
	}
	session := map[string]string{
		"Cookie":          auth.AdminCookieName + "=" + login["_cookie"].(string),
		auth.CSRFHeader:   login["csrf_token"].(string),
		"X-Forwarded-For": "10.0.0.1",
	}
	if code, l := call(http.MethodGet, "/api/v1/admin/topups?status=pending_verification", nil, session); code != http.StatusOK || l["total"].(float64) != 1 {
		t.Fatalf("queue: %d %v", code, l)
	}
	if code, d := call(http.MethodGet, "/api/v1/admin/dashboard", nil, session); code != http.StatusOK ||
		d["topups"].(map[string]any)["pending_verification_count"].(float64) != 1 {
		t.Fatalf("dashboard: %d %v", code, d)
	}
	body := map[string]any{"verified_amount": 2000, "provider_transaction_id": "E2E-1", "paid_at": time.Now().UTC().Format(time.RFC3339)}
	noCSRF := map[string]string{"Cookie": session["Cookie"]}
	if code, _ := call(http.MethodPost, "/api/v1/admin/topups/"+id+"/confirm", body, noCSRF); code != http.StatusForbidden {
		t.Fatalf("confirm without CSRF: %d", code)
	}
	if code, c := call(http.MethodPost, "/api/v1/admin/topups/"+id+"/confirm", body, session); code != http.StatusOK || c["status"] != "confirmed" {
		t.Fatalf("confirm: %d %v", code, c)
	}
	if code, w := call(http.MethodGet, "/api/v1/driver/wallet", nil, bearer); code != http.StatusOK || w["balance"].(float64) != 2000 {
		t.Fatalf("wallet after confirm: %d %v", code, w)
	}
}
