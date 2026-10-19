package moderation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/app"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers/driverstest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

type e2eResponse struct {
	code   int
	header http.Header
	body   map[string]any
	cookie string
}

// TestDriverOnboardingThroughApp drives the production wiring: Telegram
// sign-in, registration with uploads, submission, a moderator session with
// CSRF approving the driver, an audited document view and the first attempt
// to go online.
func TestDriverOnboardingThroughApp(t *testing.T) {
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

	send := func(req *http.Request, hdr map[string]string) e2eResponse {
		t.Helper()
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		res := e2eResponse{code: rec.Code, header: rec.Header()}
		if rec.Header().Get("Content-Type") == "application/json" || bytes.HasPrefix(bytes.TrimSpace(rec.Body.Bytes()), []byte("{")) {
			_ = json.Unmarshal(rec.Body.Bytes(), &res.body)
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.AdminCookieName {
				res.cookie = c.Value
			}
		}
		return res
	}
	call := func(method, path string, body any, hdr map[string]string) e2eResponse {
		t.Helper()
		var rd io.Reader = http.NoBody
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rd = bytes.NewReader(raw)
		}
		req := httptest.NewRequest(method, path, rd)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return send(req, hdr)
	}
	upload := func(kind string, seed uint8, hdr map[string]string) e2eResponse {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("kind", kind)
		fw, _ := mw.CreateFormFile("file", kind+".jpg")
		_, _ = fw.Write(driverstest.JPEG(t, 48, 36, seed))
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/driver/registration/files", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return send(req, hdr)
	}

	// Driver signs in with Telegram initData and fills in the application.
	tgID := testutil.NextTelegramID()
	signIn := call(http.MethodPost, "/api/v1/auth/telegram/driver",
		map[string]any{"init_data": testutil.SignInitData(config.TestDriverBotToken, tgID, "Ерлан", time.Now(), "register")}, nil)
	if signIn.code != http.StatusOK {
		t.Fatalf("sign-in: %d %v", signIn.code, signIn.body)
	}
	bearer := map[string]string{"Authorization": "Bearer " + signIn.body["token"].(string)}
	userID := uuid.MustParse(signIn.body["user_id"].(string))

	if res := call(http.MethodGet, pathRegistration, nil, bearer); res.code != http.StatusOK || res.body["status"] != "draft" || res.body["id"] != nil {
		t.Fatalf("empty registration: %d %v", res.code, res.body)
	}
	res := call(http.MethodPut, pathRegistration, map[string]any{
		"first_name": "Ерлан", "last_name": "Бекенов",
		"vehicle": map[string]any{"make": "Toyota", "model": "Camry", "color": "Ақ", "plate": "777 аве 04", "vehicle_class": "sedan"},
	}, bearer)
	if res.code != http.StatusOK || res.body["code"] != "EMB001" {
		t.Fatalf("save registration: %d %v", res.code, res.body)
	}
	driverID := uuid.MustParse(res.body["id"].(string))
	for i, kind := range []string{"avatar", "id_front", "id_back", "vehicle_main", "vehicle_side"} {
		if up := upload(kind, uint8(i+1), bearer); up.code != http.StatusCreated {
			t.Fatalf("upload %s: %d %v", kind, up.code, up.body)
		}
	}
	// The driver bot verifies the phone through a Telegram contact.
	if err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return drivers.ApplyVerifiedPhone(ctx, tx, userID, "+77015550101")
	}); err != nil {
		t.Fatal(err)
	}
	res = call(http.MethodPost, pathRegister, map[string]any{"pd_consent": true, "pd_consent_version": "2026-10"}, bearer)
	if res.code != http.StatusOK || res.body["status"] != "pending" {
		t.Fatalf("submit: %d %v", res.code, res.body)
	}

	// A moderator signs in to the admin panel.
	const pw = "correct horse battery staple"
	if _, err := a.Auth.CreateAdmin(ctx, "moderator@jantaxi.test", "Moderator", auth.RoleDriverModerator, pw); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	login := call(http.MethodPost, "/api/v1/admin/auth/login", map[string]any{"email": "moderator@jantaxi.test", "password": pw}, nil)
	if login.code != http.StatusOK || login.cookie == "" {
		t.Fatalf("login: %d %v", login.code, login.body)
	}
	session := map[string]string{
		"Cookie":        auth.AdminCookieName + "=" + login.cookie,
		auth.CSRFHeader: login.body["csrf_token"].(string),
	}
	if res := call(http.MethodGet, "/api/v1/admin/drivers?status=pending", nil, nil); res.code != http.StatusUnauthorized {
		t.Fatalf("list without session: %d", res.code)
	}
	res = call(http.MethodGet, "/api/v1/admin/drivers?status=pending&q=777ABE04", nil, session)
	if res.code != http.StatusOK || res.body["total"] != float64(1) {
		t.Fatalf("pending queue: %d %v", res.code, res.body)
	}
	detail := call(http.MethodGet, adminPath(driverID, ""), nil, session)
	if detail.code != http.StatusOK || detail.body["documents"] == nil {
		t.Fatalf("detail: %d %v", detail.code, detail.body)
	}
	frontURL := detail.body["documents"].(map[string]any)["id_front"].(map[string]any)["url"].(string)
	if doc := call(http.MethodGet, frontURL, nil, session); doc.code != http.StatusOK || doc.header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("document: %d", doc.code)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM admin_audit_logs WHERE action = 'document.view' AND entity_id = $1`, driverID.String()); n != 1 {
		t.Fatalf("document views audited: %d", n)
	}

	// State changes need the CSRF token.
	if res := call(http.MethodPost, adminPath(driverID, "approve"), nil, map[string]string{"Cookie": session["Cookie"]}); res.code != http.StatusForbidden {
		t.Fatalf("approve without CSRF: %d", res.code)
	}
	res = call(http.MethodPost, adminPath(driverID, "approve"), map[string]any{"comment": "Құжаттар тексерілді"}, session)
	if res.code != http.StatusOK || res.body["status"] != "approved" || res.body["wallet"] == nil {
		t.Fatalf("approve: %d %v", res.code, res.body)
	}
	// The first approval credits the welcome bonus (§22.1).
	if w := res.body["wallet"].(map[string]any); w["balance"] != float64(500) || w["available"] != float64(500) {
		t.Fatalf("wallet after approval: %v", w)
	}
	if b, _ := res.body["welcome_bonus"].(map[string]any); b == nil || b["amount"] != float64(500) || b["granted_at"] == nil {
		t.Fatalf("welcome bonus: %v", res.body["welcome_bonus"])
	}
	if res := call(http.MethodGet, "/api/v1/admin/drivers/locations", nil, session); res.code != http.StatusOK || res.body["items"] == nil {
		t.Fatalf("live map: %d %v", res.code, res.body)
	}

	// With the bonus the driver may work at once; going online only needs a
	// location.
	res = call(http.MethodPatch, "/api/v1/driver/availability", map[string]any{"online": true}, bearer)
	if res.code != http.StatusUnprocessableEntity || res.body["error"].(map[string]any)["code"] != "location_required" {
		t.Fatalf("go online without location: %d %v", res.code, res.body)
	}
	res = call(http.MethodGet, "/api/v1/driver/profile", nil, bearer)
	if res.code != http.StatusOK || res.body["status"] != "approved" || res.body["vehicle"].(map[string]any)["plate"] != "777 ABE 04" {
		t.Fatalf("profile: %d %v", res.code, res.body)
	}
}

func countRows(t *testing.T, q db.Querier, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := q.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
