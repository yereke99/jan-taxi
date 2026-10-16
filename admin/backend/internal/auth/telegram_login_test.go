package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func (e *env) signIn(app auth.App, tgID int64) response {
	e.t.Helper()
	token := passengerBotToken
	if app == auth.AppDriver {
		token = driverBotToken
	}
	raw := testutil.SignInitData(token, tgID, "Айгүл", time.Now().Add(-time.Minute), "home")
	return e.do(nil, http.MethodPost, "/api/v1/auth/telegram/"+string(app), map[string]string{"init_data": raw}, nil)
}

func TestTelegramSignInCreatesUserWithKazakhDefault(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tg := testutil.NextTelegramID()

	res := e.signIn(auth.AppPassenger, tg)
	if res.status != http.StatusOK {
		t.Fatalf("sign-in: %d %s", res.status, res.raw)
	}
	token, _ := res.body["token"].(string)
	userID, _ := res.body["user_id"].(string)
	exp, err := time.Parse(time.RFC3339, res.body["expires_at"].(string))
	if token == "" || userID == "" || err != nil || time.Until(exp) < 11*time.Hour {
		t.Fatalf("unexpected response: %s", res.raw)
	}

	var first, tgLang, prefLang string
	var lastSeen *time.Time
	if err := e.pool.QueryRow(ctx, `
		SELECT u.first_name, u.telegram_language_code, u.last_seen_at, p.language
		FROM users u JOIN user_preferences p ON p.user_id = u.id
		WHERE u.id = $1 AND u.telegram_user_id = $2`, userID, tg).Scan(&first, &tgLang, &lastSeen, &prefLang); err != nil {
		t.Fatal(err)
	}
	// SignInitData sends language_code "ru"; the app language must still start as Kazakh.
	if first != "Айгүл" || tgLang != "ru" || prefLang != "kk" || lastSeen == nil {
		t.Fatalf("unexpected row: first=%q tgLang=%q pref=%q lastSeen=%v", first, tgLang, prefLang, lastSeen)
	}

	// A chosen language survives later sign-ins.
	if _, err := e.pool.Exec(ctx, `UPDATE user_preferences SET language = 'en' WHERE user_id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	res2 := e.signIn(auth.AppDriver, tg)
	if res2.status != http.StatusOK || res2.body["user_id"] != userID {
		t.Fatalf("second sign-in must reuse the user: %d %s", res2.status, res2.raw)
	}
	if err := e.pool.QueryRow(ctx, `SELECT language FROM user_preferences WHERE user_id = $1`, userID).Scan(&prefLang); err != nil || prefLang != "en" {
		t.Fatalf("language overwritten: %q %v", prefLang, err)
	}

	// The passenger token works on passenger and shared routes only.
	if r := e.do(nil, http.MethodGet, "/api/v1/p/whoami", nil, bearer(token)); r.status != http.StatusOK ||
		r.body["user_id"] != userID || r.body["telegram_id"].(float64) != float64(tg) || r.body["app"] != "passenger" {
		t.Fatalf("passenger route: %d %s", r.status, r.raw)
	}
	if r := e.do(nil, http.MethodGet, "/api/v1/any/whoami", nil, bearer(token)); r.status != http.StatusOK {
		t.Fatalf("shared route: %d %s", r.status, r.raw)
	}
	// A valid token of the other app is 403 forbidden (re-signing in cannot help).
	if r := e.do(nil, http.MethodGet, "/api/v1/d/whoami", nil, bearer(token)); r.status != http.StatusForbidden || r.errCode() != "forbidden" {
		t.Fatalf("passenger token on driver route: %d %s", r.status, r.raw)
	}
	driverTok := res2.body["token"].(string)
	if r := e.do(nil, http.MethodGet, "/api/v1/p/whoami", nil, bearer(driverTok)); r.status != http.StatusForbidden || r.errCode() != "forbidden" {
		t.Fatalf("driver token on passenger route: %d %s", r.status, r.raw)
	}
}

func TestTelegramSignInRejectsBadInitData(t *testing.T) {
	e := newEnv(t)
	tg := testutil.NextTelegramID()
	valid := testutil.SignInitData(passengerBotToken, tg, "A", time.Now(), "")
	q, _ := url.ParseQuery(valid)
	q.Set("user", strings.Replace(q.Get("user"), `"A"`, `"B"`, 1))
	tampered := q.Encode()

	tests := []struct {
		name, path, initData, code string
		status                     int
	}{
		{"tampered", "/api/v1/auth/telegram/passenger", tampered, "invalid_init_data", 401},
		{"expired", "/api/v1/auth/telegram/passenger", testutil.SignInitData(passengerBotToken, tg, "A", time.Now().Add(-25*time.Hour), ""), "init_data_expired", 401},
		{"future", "/api/v1/auth/telegram/passenger", testutil.SignInitData(passengerBotToken, tg, "A", time.Now().Add(5*time.Minute), ""), "invalid_init_data", 401},
		{"wrong bot", "/api/v1/auth/telegram/driver", valid, "invalid_init_data", 401},
		{"empty", "/api/v1/auth/telegram/passenger", "", "validation_failed", 422},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := e.do(nil, http.MethodPost, tt.path, map[string]string{"init_data": tt.initData}, nil)
			if r.status != tt.status || r.errCode() != tt.code {
				t.Fatalf("got %d %s", r.status, r.raw)
			}
		})
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE telegram_user_id = $1`, tg).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected sign-ins must not create users: n=%d err=%v", n, err)
	}
	// Unknown fields are rejected (strict JSON).
	if r := e.do(nil, http.MethodPost, "/api/v1/auth/telegram/passenger", map[string]any{"init_data": valid, "telegram_id": 1}, nil); r.status != 400 {
		t.Fatalf("unknown field accepted: %d", r.status)
	}
}

func TestBlockedUserIsRejected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tg := testutil.NextTelegramID()
	res := e.signIn(auth.AppPassenger, tg)
	if res.status != http.StatusOK {
		t.Fatalf("sign-in: %d", res.status)
	}
	tok := res.body["token"].(string)
	uid := uuid.MustParse(res.body["user_id"].(string))

	if _, err := e.pool.Exec(ctx, `UPDATE users SET is_blocked = true, blocked_reason = 'fraud' WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	e.svc.InvalidateUser(uid)
	if r := e.do(nil, http.MethodGet, "/api/v1/p/whoami", nil, bearer(tok)); r.status != http.StatusForbidden || r.errCode() != "user_blocked" {
		t.Fatalf("blocked user with valid token: %d %s", r.status, r.raw)
	}
	if r := e.signIn(auth.AppPassenger, tg); r.status != http.StatusForbidden || r.errCode() != "user_blocked" {
		t.Fatalf("blocked user sign-in: %d %s", r.status, r.raw)
	}
	if _, _, err := e.svc.AuthenticateAppToken(ctx, tok); err == nil {
		t.Fatal("AuthenticateAppToken accepted a blocked user")
	}
}

func TestAppTokenOverHTTP(t *testing.T) {
	e := newEnv(t)
	uid, tg := testutil.CreateUser(t, e.pool)

	expired, _, err := e.svc.IssueAppToken(uid, auth.AppPassenger, time.Now().Add(-13*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if r := e.do(nil, http.MethodGet, "/api/v1/p/whoami", nil, bearer(expired)); r.status != 401 || r.errCode() != "token_expired" {
		t.Fatalf("expired token: %d %s", r.status, r.raw)
	}
	good, _, _ := e.svc.IssueAppToken(uid, auth.AppPassenger, time.Now())
	tampered := good[:len(good)-2] + "AA"
	for name, h := range map[string]map[string]string{
		"missing header":  nil,
		"basic auth":      {"Authorization": "Basic " + good},
		"tampered":        bearer(tampered),
		"unknown user id": bearer(mustIssue(t, e.svc, uuid.New(), auth.AppPassenger)),
	} {
		if r := e.do(nil, http.MethodGet, "/api/v1/p/whoami", nil, h); r.status != 401 {
			t.Errorf("%s: got %d", name, r.status)
		}
	}
	r := e.do(nil, http.MethodGet, "/api/v1/p/whoami", nil, bearer(good))
	if r.status != 200 || r.body["telegram_id"].(float64) != float64(tg) {
		t.Fatalf("valid token: %d %s", r.status, r.raw)
	}
	p, err := e.svc.VerifyAppToken(good)
	if err != nil || p.UserID != uid || p.App != auth.AppPassenger {
		t.Fatalf("VerifyAppToken = %+v, %v", p, err)
	}
}

func mustIssue(t *testing.T, s *auth.Service, uid uuid.UUID, app auth.App) string {
	t.Helper()
	tok, _, err := s.IssueAppToken(uid, app, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestLoadDriverProfile(t *testing.T) {
	e := newEnv(t)
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{})
	res := e.signIn(auth.AppDriver, d.TelegramID)
	if res.status != 200 || res.body["user_id"] != d.UserID.String() {
		t.Fatalf("driver sign-in: %d %s", res.status, res.raw)
	}
	tok := res.body["token"].(string)
	r := e.do(nil, http.MethodGet, "/api/v1/d/whoami", nil, bearer(tok))
	if r.status != 200 || r.body["driver_id"] != d.DriverID.String() || r.body["driver_code"] != d.Code {
		t.Fatalf("driver profile not loaded: %d %s", r.status, r.raw)
	}

	// A driver-app user without a profile continues on optional routes and is
	// refused on strict ones.
	uid, _ := testutil.CreateUser(t, e.pool)
	noProfile := mustIssue(t, e.svc, uid, auth.AppDriver)
	r = e.do(nil, http.MethodGet, "/api/v1/d/whoami", nil, bearer(noProfile))
	if _, has := r.body["driver_id"]; r.status != 200 || has {
		t.Fatalf("optional profile: %d %s", r.status, r.raw)
	}
	r = e.do(nil, http.MethodGet, "/api/v1/d/strict", nil, bearer(noProfile))
	if r.status != 403 || r.errCode() != "driver_not_registered" {
		t.Fatalf("strict profile: %d %s", r.status, r.raw)
	}
	dc, ok, err := e.svc.DriverProfile(context.Background(), d.UserID)
	if err != nil || !ok || dc.ID != d.DriverID || dc.Status != "approved" {
		t.Fatalf("DriverProfile = %+v %v %v", dc, ok, err)
	}
}

func TestRequireService(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name    string
		headers map[string]string
		status  int
		bot     string
	}{
		{"no auth", map[string]string{"X-Jantaxi-Bot": "passenger"}, 401, ""},
		{"wrong secret", map[string]string{"Authorization": "Bearer nope", "X-Jantaxi-Bot": "passenger"}, 401, ""},
		{"secret prefix", map[string]string{"Authorization": "Bearer " + serviceSecret[:10], "X-Jantaxi-Bot": "passenger"}, 401, ""},
		{"no bot header", map[string]string{"Authorization": "Bearer " + serviceSecret}, 401, ""},
		{"unknown bot", map[string]string{"Authorization": "Bearer " + serviceSecret, "X-Jantaxi-Bot": "admin"}, 401, ""},
		{"passenger", map[string]string{"Authorization": "Bearer " + serviceSecret, "X-Jantaxi-Bot": "passenger"}, 200, "passenger"},
		{"driver", map[string]string{"Authorization": "Bearer " + serviceSecret, "X-Jantaxi-Bot": "driver"}, 200, "driver"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := e.do(nil, http.MethodGet, "/internal/v1/ping", nil, tt.headers)
			if r.status != tt.status || (tt.bot != "" && r.body["bot"] != tt.bot) {
				t.Fatalf("got %d %s", r.status, r.raw)
			}
		})
	}

	// An empty configured secret must never authenticate anything.
	h := auth.RequireService("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer ")
	req.Header.Set("X-Jantaxi-Bot", "driver")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("empty secret accepted: %d", rec.Code)
	}
}
