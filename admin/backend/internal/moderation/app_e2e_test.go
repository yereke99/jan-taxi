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
// sign-in, the two-page registration with the profile photo, the submission
// that approves at once with the welcome bonus, an administrator session with
// CSRF seeing the masked IIN, and going online after a location report.
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

	// Driver signs in with Telegram initData and fills in both pages.
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
		"first_name": "Ерлан", "last_name": "Бекенов", "iin": "950101300027",
	}, bearer)
	if res.code != http.StatusUnprocessableEntity || res.body["error"].(map[string]any)["fields"].(map[string]any)["iin"] != "checksum" {
		t.Fatalf("IIN with a wrong check digit: %d %v", res.code, res.body)
	}
	res = call(http.MethodPut, pathRegistration, map[string]any{
		"first_name": "Ерлан", "last_name": "Бекенов", "iin": "951212300459",
	}, bearer)
	if res.code != http.StatusOK || res.body["code"] != "EMB001" || res.body["iin"] != "951212300459" {
		t.Fatalf("save page 1: %d %v", res.code, res.body)
	}
	driverID := uuid.MustParse(res.body["id"].(string))
	if up := upload("avatar", 1, bearer); up.code != http.StatusCreated {
		t.Fatalf("upload avatar: %d %v", up.code, up.body)
	}
	res = call(http.MethodPut, pathRegistration, map[string]any{
		"vehicle": map[string]any{"make": "Toyota", "model": "Camry", "color": "Ақ", "plate": "777 аве 04", "vehicle_class": "sedan"},
	}, bearer)
	if res.code != http.StatusOK || res.body["vehicle"].(map[string]any)["vehicle_class"] != "sedan" || res.body["first_name"] != "Ерлан" {
		t.Fatalf("save page 2: %d %v", res.code, res.body)
	}
	// The driver bot verifies the phone through a Telegram contact.
	if err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return drivers.ApplyVerifiedPhone(ctx, tx, userID, "+77015550101")
	}); err != nil {
		t.Fatal(err)
	}
	res = call(http.MethodPost, pathRegister, map[string]any{"pd_consent": true, "pd_consent_version": "driver-pd-2026-10-v3"}, bearer)
	if res.code != http.StatusOK || res.body["status"] != "approved" {
		t.Fatalf("submit: %d %v", res.code, res.body)
	}

	// An administrator signs in to the admin panel.
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
	if res := call(http.MethodGet, "/api/v1/admin/drivers?status=approved", nil, nil); res.code != http.StatusUnauthorized {
		t.Fatalf("list without session: %d", res.code)
	}
	res = call(http.MethodGet, "/api/v1/admin/drivers?status=approved&q=777ABE04", nil, session)
	if res.code != http.StatusOK || res.body["total"] != float64(1) {
		t.Fatalf("approved drivers: %d %v", res.code, res.body)
	}
	if res := call(http.MethodGet, "/api/v1/admin/drivers?status=pending", nil, session); res.code != http.StatusOK || res.body["total"] != float64(0) {
		t.Fatalf("nothing waits for a moderator: %d %v", res.code, res.body)
	}
	detail := call(http.MethodGet, adminPath(driverID, ""), nil, session)
	if detail.code != http.StatusOK || detail.body["iin_masked"] != "********0459" || detail.body["approved_at"] == nil {
		t.Fatalf("detail: %d %v", detail.code, detail.body)
	}
	docs := detail.body["documents"].(map[string]any)
	if docs["id_front"] != nil || docs["id_back"] != nil {
		t.Fatalf("documents of a new driver: %v", docs)
	}
	// The first approval credits the welcome bonus (§22.1).
	if w := detail.body["wallet"].(map[string]any); w["balance"] != float64(500) || w["available"] != float64(500) {
		t.Fatalf("wallet after approval: %v", w)
	}
	if b, _ := detail.body["welcome_bonus"].(map[string]any); b == nil || b["amount"] != float64(500) || b["granted_at"] == nil {
		t.Fatalf("welcome bonus: %v", detail.body["welcome_bonus"])
	}
	// State changes need the CSRF token.
	if res := call(http.MethodPost, adminPath(driverID, "approve"), nil, map[string]string{"Cookie": session["Cookie"]}); res.code != http.StatusForbidden {
		t.Fatalf("approve without CSRF: %d", res.code)
	}
	if res := call(http.MethodPost, adminPath(driverID, "approve"), nil, session); res.code != http.StatusConflict {
		t.Fatalf("approve an approved driver: %d %v", res.code, res.body)
	}
	if res := call(http.MethodGet, "/api/v1/admin/drivers/locations", nil, session); res.code != http.StatusOK || res.body["items"] == nil {
		t.Fatalf("live map: %d %v", res.code, res.body)
	}

	// With the bonus the driver works at once; going online only needs a location.
	res = call(http.MethodPatch, "/api/v1/driver/availability", map[string]any{"online": true}, bearer)
	if res.code != http.StatusUnprocessableEntity || res.body["error"].(map[string]any)["code"] != "location_required" {
		t.Fatalf("go online without location: %d %v", res.code, res.body)
	}
	if res := call(http.MethodPost, "/api/v1/driver/location", map[string]any{"lat": 48.8264, "lon": 58.1442}, bearer); res.code != http.StatusNoContent {
		t.Fatalf("location: %d %v", res.code, res.body)
	}
	if res := call(http.MethodPatch, "/api/v1/driver/availability", map[string]any{"online": true}, bearer); res.code != http.StatusOK || res.body["is_online"] != true {
		t.Fatalf("go online: %d %v", res.code, res.body)
	}
	res = call(http.MethodGet, "/api/v1/driver/profile", nil, bearer)
	if res.code != http.StatusOK || res.body["status"] != "approved" || res.body["vehicle"].(map[string]any)["plate"] != "777 ABE 04" ||
		res.body["iin_masked"] != "********0459" {
		t.Fatalf("profile: %d %v", res.code, res.body)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM files WHERE owner_user_id = $1 AND purpose <> 'driver_avatar'`, userID); n != 0 {
		t.Fatalf("%d documents or vehicle photos stored", n)
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
