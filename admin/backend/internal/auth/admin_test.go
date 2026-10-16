package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
)

const loginPath = "/api/v1/admin/auth/login"

func TestAdminLoginSessionAndCSRF(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.createAdmin("boss@jantaxi.kz", auth.RoleSuperAdmin)

	c := e.browser()
	if r := e.do(c, http.MethodPost, loginPath, map[string]string{"email": "boss@jantaxi.kz", "password": "wrong password!"}, nil); r.status != 401 || r.errCode() != "invalid_credentials" {
		t.Fatalf("wrong password: %d %s", r.status, r.raw)
	}
	if r := e.do(c, http.MethodPost, loginPath, map[string]string{"email": "nobody@jantaxi.kz", "password": adminPassword}, nil); r.status != 401 || r.errCode() != "invalid_credentials" {
		t.Fatalf("unknown e-mail: %d %s", r.status, r.raw)
	}

	r := e.do(c, http.MethodPost, loginPath, map[string]string{"email": "  BOSS@jantaxi.kz ", "password": adminPassword}, nil)
	if r.status != 200 {
		t.Fatalf("login: %d %s", r.status, r.raw)
	}
	csrf, _ := r.body["csrf_token"].(string)
	a, _ := r.body["admin"].(map[string]any)
	perms, _ := a["permissions"].([]any)
	if csrf == "" || a["id"] != admin.ID.String() || a["role"] != "super_admin" || a["email"] != "boss@jantaxi.kz" || len(perms) != 13 {
		t.Fatalf("unexpected login body: %s", r.raw)
	}
	var setCookie *http.Cookie
	for _, ck := range (&http.Response{Header: r.header}).Cookies() {
		if ck.Name == auth.AdminCookieName {
			setCookie = ck
		}
	}
	if setCookie == nil || !setCookie.HttpOnly || setCookie.SameSite != http.SameSiteStrictMode || setCookie.Path != "/api" || setCookie.MaxAge <= 0 {
		t.Fatalf("unexpected cookie: %+v", setCookie)
	}
	// Only the SHA-256 of the cookie token is stored.
	var stored int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM admin_sessions WHERE admin_id = $1 AND token_hash = sha256(decode(translate($2, '-_', '+/') || repeat('=', (4 - length($2) % 4) % 4), 'base64'))`,
		admin.ID, setCookie.Value).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("session hash not stored: %d %v", stored, err)
	}

	me := e.do(c, http.MethodGet, "/api/v1/admin/auth/me", nil, nil)
	if me.status != 200 || me.body["csrf_token"] != csrf {
		t.Fatalf("me: %d %s", me.status, me.raw)
	}
	// Login and /me report the same absolute expiry (OpenAPI AdminSession.expires_at).
	loginExp, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(r.body["expires_at"]))
	meExp, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(me.body["expires_at"]))
	if loginExp.IsZero() || !loginExp.Equal(meExp) || loginExp.Location() != time.UTC ||
		loginExp.Before(time.Now().Add(6*24*time.Hour)) || loginExp.After(time.Now().Add(8*24*time.Hour)) {
		t.Fatalf("expires_at: login %v, me %v", r.body["expires_at"], me.body["expires_at"])
	}

	// State-changing requests need the CSRF header.
	body := map[string]string{"current_password": adminPassword, "new_password": "a brand new passphrase"}
	if r := e.do(c, http.MethodPost, "/api/v1/admin/auth/password", body, nil); r.status != 403 || r.errCode() != "csrf_failed" {
		t.Fatalf("missing CSRF: %d %s", r.status, r.raw)
	}
	if r := e.do(c, http.MethodPost, "/api/v1/admin/auth/password", body, map[string]string{auth.CSRFHeader: "forged"}); r.status != 403 {
		t.Fatalf("forged CSRF: %d %s", r.status, r.raw)
	}
	short := map[string]string{"current_password": adminPassword, "new_password": "short"}
	if r := e.do(c, http.MethodPost, "/api/v1/admin/auth/password", short, map[string]string{auth.CSRFHeader: csrf}); r.status != 422 {
		t.Fatalf("short password: %d %s", r.status, r.raw)
	}

	// A second session is revoked by the password change; the current one survives.
	other, _ := e.login("boss@jantaxi.kz")
	if r := e.do(c, http.MethodPost, "/api/v1/admin/auth/password", body, map[string]string{auth.CSRFHeader: csrf}); r.status != 204 {
		t.Fatalf("password change: %d %s", r.status, r.raw)
	}
	if r := e.do(other, http.MethodGet, "/api/v1/admin/auth/me", nil, nil); r.status != 401 {
		t.Fatalf("other session survived password change: %d", r.status)
	}
	if r := e.do(c, http.MethodGet, "/api/v1/admin/auth/me", nil, nil); r.status != 200 {
		t.Fatalf("current session lost: %d", r.status)
	}

	// Logout needs CSRF, revokes, and clears the cookie.
	if r := e.do(c, http.MethodPost, "/api/v1/admin/auth/logout", nil, nil); r.status != 403 {
		t.Fatalf("logout without CSRF: %d", r.status)
	}
	if r := e.do(c, http.MethodPost, "/api/v1/admin/auth/logout", nil, map[string]string{auth.CSRFHeader: csrf}); r.status != 204 {
		t.Fatalf("logout: %d %s", r.status, r.raw)
	}
	if e.cookie(c) != nil {
		t.Fatal("cookie not cleared on logout")
	}
	// Replaying the old cookie after logout fails.
	if r := e.do(nil, http.MethodGet, "/api/v1/admin/auth/me", nil, map[string]string{"Cookie": auth.AdminCookieName + "=" + setCookie.Value}); r.status != 401 {
		t.Fatalf("revoked session accepted: %d", r.status)
	}

	var actions []string
	rows, err := e.pool.Query(ctx, `SELECT action FROM admin_audit_logs WHERE admin_id = $1 ORDER BY id`, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		actions = append(actions, a)
	}
	rows.Close()
	want := []string{"admin.create", "admin.login", "admin.login", "admin.password_change", "admin.logout"}
	if len(actions) != len(want) {
		t.Fatalf("audit actions = %v, want %v", actions, want)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("audit actions = %v, want %v", actions, want)
		}
	}
}

func TestAdminLockout(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.createAdmin("ops@jantaxi.kz", auth.RoleOperator)
	bad := map[string]string{"email": "ops@jantaxi.kz", "password": "not the password"}
	for i := 1; i <= 4; i++ {
		if r := e.do(nil, http.MethodPost, loginPath, bad, nil); r.status != 401 {
			t.Fatalf("attempt %d: %d %s", i, r.status, r.raw)
		}
	}
	r := e.do(nil, http.MethodPost, loginPath, bad, nil)
	if r.status != http.StatusLocked || r.errCode() != "account_locked" {
		t.Fatalf("fifth failure must lock: %d %s", r.status, r.raw)
	}
	good := map[string]string{"email": "ops@jantaxi.kz", "password": adminPassword}
	if r := e.do(nil, http.MethodPost, loginPath, good, nil); r.status != http.StatusLocked {
		t.Fatalf("correct password while locked: %d %s", r.status, r.raw)
	}
	var until time.Time
	if err := e.pool.QueryRow(ctx, `SELECT locked_until FROM admin_users WHERE id = $1`, admin.ID).Scan(&until); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(until); d < 14*time.Minute || d > 15*time.Minute+time.Second {
		t.Fatalf("lock duration %v", d)
	}
	// After the lock expires the correct password works and resets the counter.
	if _, err := e.pool.Exec(ctx, `UPDATE admin_users SET locked_until = now() - interval '1 second' WHERE id = $1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.do(nil, http.MethodPost, loginPath, good, nil); r.status != 200 {
		t.Fatalf("login after lock expiry: %d %s", r.status, r.raw)
	}
	var failed int
	var locked *time.Time
	if err := e.pool.QueryRow(ctx, `SELECT failed_login_count, locked_until FROM admin_users WHERE id = $1`, admin.ID).Scan(&failed, &locked); err != nil || failed != 0 || locked != nil {
		t.Fatalf("counter not reset: %d %v %v", failed, locked, err)
	}
}

func TestAdminSessionExpiry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	admin := e.createAdmin("fin@jantaxi.kz", auth.RoleFinanceManager)
	me := "/api/v1/admin/auth/me"

	tests := []struct {
		name     string
		breakSQL string
	}{
		{"idle timeout", `UPDATE admin_sessions SET last_seen_at = now() - interval '12 hours 1 minute' WHERE admin_id = $1`},
		{"absolute expiry", `UPDATE admin_sessions SET expires_at = now() - interval '1 second' WHERE admin_id = $1`},
		{"revoked", `UPDATE admin_sessions SET revoked_at = now() WHERE admin_id = $1`},
		{"admin deactivated", `UPDATE admin_users SET is_active = false WHERE id = $1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.pool.Exec(ctx, `UPDATE admin_users SET is_active = true WHERE id = $1`, admin.ID); err != nil {
				t.Fatal(err)
			}
			c, _ := e.login("fin@jantaxi.kz")
			if r := e.do(c, http.MethodGet, me, nil, nil); r.status != 200 {
				t.Fatalf("fresh session: %d", r.status)
			}
			if _, err := e.pool.Exec(ctx, tt.breakSQL, admin.ID); err != nil {
				t.Fatal(err)
			}
			if r := e.do(c, http.MethodGet, me, nil, nil); r.status != 401 {
				t.Fatalf("expected 401, got %d", r.status)
			}
		})
	}

	// Activity slides last_seen_at (at most once a minute).
	if _, err := e.pool.Exec(ctx, `UPDATE admin_users SET is_active = true WHERE id = $1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	c, _ := e.login("fin@jantaxi.kz")
	if _, err := e.pool.Exec(ctx, `UPDATE admin_sessions SET last_seen_at = now() - interval '11 hours' WHERE admin_id = $1 AND revoked_at IS NULL`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.do(c, http.MethodGet, me, nil, nil); r.status != 200 {
		t.Fatalf("active session: %d", r.status)
	}
	var fresh bool
	if err := e.pool.QueryRow(ctx, `SELECT now() - max(last_seen_at) < interval '1 minute' FROM admin_sessions WHERE admin_id = $1 AND revoked_at IS NULL`, admin.ID).Scan(&fresh); err != nil || !fresh {
		t.Fatalf("last_seen_at not slid: %v %v", fresh, err)
	}
	if err := e.svc.CleanupSessions(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAdminsManagement(t *testing.T) {
	e := newEnv(t)
	boss := e.createAdmin("boss@jantaxi.kz", auth.RoleSuperAdmin)
	c, csrf := e.login("boss@jantaxi.kz")
	h := map[string]string{auth.CSRFHeader: csrf}

	r := e.do(c, http.MethodPost, "/api/v1/admin/admins", map[string]string{
		"email": "Operator@JanTaxi.kz", "full_name": "  Дана  Оператор ", "role": "operator", "password": "operator passphrase",
	}, h)
	if r.status != 201 || r.body["email"] != "operator@jantaxi.kz" || r.body["full_name"] != "Дана Оператор" || r.body["role"] != "operator" {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	opID := r.body["id"].(string)
	if _, leaked := r.body["password_hash"]; leaked {
		t.Fatal("password hash leaked")
	}
	if r := e.do(c, http.MethodPost, "/api/v1/admin/admins", map[string]string{
		"email": "operator@jantaxi.kz", "full_name": "Dup", "role": "operator", "password": "operator passphrase",
	}, h); r.status != 409 || r.errCode() != "email_taken" {
		t.Fatalf("duplicate: %d %s", r.status, r.raw)
	}
	if r := e.do(c, http.MethodPost, "/api/v1/admin/admins", map[string]string{
		"email": "x", "full_name": "", "role": "root", "password": "short",
	}, h); r.status != 422 {
		t.Fatalf("validation: %d %s", r.status, r.raw)
	} else if f := r.body["error"].(map[string]any)["fields"].(map[string]any); f["email"] != "invalid" || f["role"] != "invalid" || f["password"] != "too_short" || f["full_name"] != "required" {
		t.Fatalf("fields: %s", r.raw)
	}

	list := e.do(c, http.MethodGet, "/api/v1/admin/admins?page=1&page_size=10", nil, nil)
	if list.status != 200 || list.body["total"].(float64) != 2 || len(list.body["items"].([]any)) != 2 {
		t.Fatalf("list: %d %s", list.status, list.raw)
	}

	// The operator lacks admins.manage.
	opClient := e.browser()
	if r := e.do(opClient, http.MethodPost, loginPath, map[string]string{"email": "operator@jantaxi.kz", "password": "operator passphrase"}, nil); r.status != 200 {
		t.Fatalf("operator login: %d", r.status)
	}
	if r := e.do(opClient, http.MethodGet, "/api/v1/admin/admins", nil, nil); r.status != 403 {
		t.Fatalf("operator listed admins: %d", r.status)
	}

	// Role change and deactivation of someone else; deactivation ends their sessions.
	if r := e.do(c, http.MethodPatch, "/api/v1/admin/admins/"+opID, map[string]any{"role": "auditor"}, h); r.status != 200 || r.body["role"] != "auditor" {
		t.Fatalf("role change: %d %s", r.status, r.raw)
	}
	if r := e.do(opClient, http.MethodGet, "/api/v1/admin/auth/me", nil, nil); r.status != 200 || r.body["admin"].(map[string]any)["role"] != "auditor" {
		t.Fatalf("role change not effective: %d %s", r.status, r.raw)
	}
	if r := e.do(c, http.MethodPatch, "/api/v1/admin/admins/"+opID, map[string]any{"is_active": false}, h); r.status != 200 || r.body["is_active"] != false {
		t.Fatalf("deactivate: %d %s", r.status, r.raw)
	}
	if r := e.do(opClient, http.MethodGet, "/api/v1/admin/auth/me", nil, nil); r.status != 401 {
		t.Fatalf("deactivated admin still signed in: %d", r.status)
	}

	// Self-protection.
	self := "/api/v1/admin/admins/" + boss.ID.String()
	for name, body := range map[string]map[string]any{
		"deactivate self": {"is_active": false},
		"demote self":     {"role": "operator"},
	} {
		if r := e.do(c, http.MethodPatch, self, body, h); r.status != 409 || r.errCode() != "cannot_modify_self" {
			t.Fatalf("%s: %d %s", name, r.status, r.raw)
		}
	}
	if r := e.do(c, http.MethodPatch, self, map[string]any{"full_name": "Бас әкімші"}, h); r.status != 200 || r.body["full_name"] != "Бас әкімші" {
		t.Fatalf("rename self: %d %s", r.status, r.raw)
	}
	if r := e.do(c, http.MethodPatch, "/api/v1/admin/admins/"+uuid.NewString(), map[string]any{"full_name": "X"}, h); r.status != 404 {
		t.Fatalf("unknown admin: %d", r.status)
	}
	if r := e.do(c, http.MethodPatch, "/api/v1/admin/admins/"+opID, map[string]any{"email": "x@y.kz"}, h); r.status != 400 {
		t.Fatalf("email is immutable: %d", r.status)
	}

	// Password reset by a super admin unlocks and revokes the target's sessions.
	if r := e.do(c, http.MethodPatch, "/api/v1/admin/admins/"+opID, map[string]any{"is_active": true, "password": "reset passphrase 1"}, h); r.status != 200 {
		t.Fatalf("reset: %d %s", r.status, r.raw)
	}
	if r := e.do(nil, http.MethodPost, loginPath, map[string]string{"email": "operator@jantaxi.kz", "password": "reset passphrase 1"}, nil); r.status != 200 {
		t.Fatalf("login with reset password: %d %s", r.status, r.raw)
	}

	var n int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM admin_audit_logs
		WHERE admin_id = $1 AND action IN ('admin.create', 'admin.update') AND entity_id = $2
		  AND NOT (coalesce(after::text, '') LIKE '%argon2%')`, boss.ID, opID).Scan(&n); err != nil || n != 4 {
		t.Fatalf("audit rows for operator = %d (%v)", n, err)
	}
}

// TestLastSuperAdminProtection drives the handler with an injected principal: via
// HTTP the only holders of admins.manage are active super admins, so the last
// one can only be targeted by itself (blocked earlier) or by a racing request.
func TestLastSuperAdminProtection(t *testing.T) {
	e := newEnv(t)
	boss := e.createAdmin("boss@jantaxi.kz", auth.RoleSuperAdmin)
	actor := e.createAdmin("actor@jantaxi.kz", auth.RoleOperator)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithAdmin(req.Context(), auth.AdminPrincipal{AdminID: actor.ID, Role: auth.RoleSuperAdmin, SessionID: uuid.New()})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	e.svc.MountAdmin(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	for name, body := range map[string]string{
		"deactivate": `{"is_active":false}`,
		"demote":     `{"role":"operator"}`,
	} {
		req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/admins/"+boss.ID.String(), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("%s last super admin: %d", name, resp.StatusCode)
		}
	}
	var active bool
	var role string
	if err := e.pool.QueryRow(context.Background(), `SELECT is_active, role FROM admin_users WHERE id = $1`, boss.ID).Scan(&active, &role); err != nil || !active || role != "super_admin" {
		t.Fatalf("last super admin changed: %v %s %v", active, role, err)
	}
}
