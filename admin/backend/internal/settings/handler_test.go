package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func adminRouter(pool *pgxpool.Pool, a auth.AdminPrincipal) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithAdmin(req.Context(), a)))
		})
	})
	NewHandler(pool).MountAdmin(r)
	return r
}

func call(t *testing.T, h http.Handler, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return rec, out
}

func itemsByKey(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, it := range body["items"].([]any) {
		m := it.(map[string]any)
		out[m["key"].(string)] = m
	}
	return out
}

func TestAdminListSettings(t *testing.T) {
	pool := testutil.NewDB(t)
	Configure(Env{KaspiRecipientName: "ИП Jan Taxi", TwoGISBaseURL: "https://2gis.ru"})
	t.Cleanup(func() { Configure(Env{}) })
	admin := auth.AdminPrincipal{AdminID: createAdmin(t, pool, "super_admin"), Role: auth.RoleSuperAdmin}

	rec, body := call(t, adminRouter(pool, admin), http.MethodGet, "/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	items := itemsByKey(t, body)
	if len(items) != len(Defs()) {
		t.Fatalf("got %d items, want %d", len(items), len(Defs()))
	}
	tests := []struct {
		key, source       string
		hasEnv, overrides bool
		value             string
	}{
		{"topup.min_amount", "database", false, false, `500`},
		{"routing.max_points", "database", true, true, `5`},
		// Seeded with the payee (migration 00006): the database row wins over the env default.
		{"kaspi.recipient_name", "database", true, true, `"ИП \"JAN-TAXI\""`},
		{"kaspi.payment_url", "database", true, true, `"https://pay.kaspi.kz/pay/xbp1imhd"`},
		{"company.bin", "database", false, false, `"000809500289"`},
		{"topup.auto_confirm_enabled", "database", false, false, `true`},
		{"driver.online_idle_hours", "default", true, false, `12`},
	}
	for _, tt := range tests {
		it := items[tt.key]
		v, _ := json.Marshal(it["value"])
		if it["source"] != tt.source || it["has_env_default"] != tt.hasEnv || it["overrides_env"] != tt.overrides || string(v) != tt.value {
			t.Errorf("%s = %v", tt.key, it)
		}
	}
	desc := items["support.hours"]["description"].(map[string]any)
	if desc["kk"] == "" || desc["ru"] == "" || desc["en"] == "" {
		t.Errorf("description = %v", desc)
	}
	if items["topup.presets"]["type"] != string(KindIntList) {
		t.Errorf("type = %v", items["topup.presets"]["type"])
	}
}

func TestAdminPatchSettings(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	adminID := createAdmin(t, pool, "super_admin")
	h := adminRouter(pool, auth.AdminPrincipal{AdminID: adminID, Role: auth.RoleSuperAdmin})

	rec, body := call(t, h, http.MethodPatch, "/settings",
		`{"support.telegram_username":"@jantaxi_help","topup.presets":[3000,1000],"kaspi.payment_url":"https://pay.kaspi.kz/pay/q1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	items := itemsByKey(t, body)
	if items["support.telegram_username"]["value"] != "jantaxi_help" {
		t.Errorf("username = %v", items["support.telegram_username"]["value"])
	}
	if items["kaspi.payment_url"]["overrides_env"] != true || items["kaspi.payment_url"]["updated_by"] != adminID.String() {
		t.Errorf("kaspi item = %v", items["kaspi.payment_url"])
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE admin_id = $1 AND action = 'settings.update' AND entity_type = 'app_setting'`, adminID).Scan(&n); err != nil || n != 3 {
		t.Fatalf("audit rows = %d (%v)", n, err)
	}
	var before, after string
	if err := pool.QueryRow(ctx, `SELECT before::text, after::text FROM admin_audit_logs WHERE entity_id = 'topup.presets'`).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	if before != `{"value": [1000, 2000, 5000, 10000]}` || after != `{"value": [1000, 3000]}` {
		t.Errorf("audit before=%s after=%s", before, after)
	}

	// null resets to the default and is audited as a reset.
	rec, body = call(t, h, http.MethodPatch, "/settings", `{"kaspi.payment_url":null}`)
	if rec.Code != http.StatusOK || itemsByKey(t, body)["kaspi.payment_url"]["source"] != "default" {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs WHERE action = 'settings.reset'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reset audit rows = %d (%v)", n, err)
	}
}

func TestAdminPatchSettingsRejects(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	h := adminRouter(pool, auth.AdminPrincipal{AdminID: createAdmin(t, pool, "super_admin"), Role: auth.RoleSuperAdmin})

	tests := []struct {
		name, body string
		status     int
		fields     map[string]string
	}{
		{"unknown key", `{"payments.secret":"x","topup.min_amount":600}`, 422, map[string]string{"payments.secret": ReasonUnknown}},
		{"bad value", `{"routing.max_points":7}`, 422, map[string]string{"routing.max_points": ReasonOutOfRange}},
		{"min above max", `{"topup.min_amount":200000}`, 422, map[string]string{"topup.min_amount": "greater_than_max"}},
		{"preset outside range", `{"topup.presets":[100]}`, 422, map[string]string{"topup.presets": ReasonOutOfRange}},
		{"radius max below base", `{"dispatch.radius_max_m":4000}`, 422, map[string]string{"dispatch.radius_max_m": "less_than_base"}},
		{"radius base above max", `{"dispatch.radius_base_m":12000}`, 422, map[string]string{"dispatch.radius_max_m": "less_than_base"}},
		{"radius base too small", `{"dispatch.radius_base_m":100}`, 422, map[string]string{"dispatch.radius_base_m": ReasonOutOfRange}},
		{"empty", `{}`, 400, nil},
		{"not an object", `[1]`, 400, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, body := call(t, h, http.MethodPatch, "/settings", tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			for k, v := range tt.fields {
				got := body["error"].(map[string]any)["fields"].(map[string]any)[k]
				if got != v {
					t.Errorf("field %s = %v, want %s", k, got, v)
				}
			}
		})
	}
	// Nothing was written by the rejected patches.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("audit rows = %d (%v)", n, err)
	}
	if got := Int(ctx, pool, "topup.min_amount", 0); got != 500 {
		t.Fatalf("min amount changed to %d", got)
	}

	// Raising min and max together is consistent and accepted.
	rec, _ := call(t, h, http.MethodPatch, "/settings", `{"topup.min_amount":2000,"topup.presets":[2000,5000]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("consistent patch: %d %s", rec.Code, rec.Body)
	}
	// So is raising the base and the maximum radius together.
	rec, body := call(t, h, http.MethodPatch, "/settings", `{"dispatch.radius_base_m":12000,"dispatch.radius_max_m":20000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("consistent radius patch: %d %s", rec.Code, rec.Body)
	}
	items := itemsByKey(t, body)
	if items["dispatch.radius_base_m"]["value"] != float64(12000) || items["dispatch.radius_max_m"]["source"] != "database" {
		t.Fatalf("radius items: %v %v", items["dispatch.radius_base_m"], items["dispatch.radius_max_m"])
	}
	c := items["dispatch.radius_max_m"]["constraints"].(map[string]any)
	if c["min"] != float64(500) || c["max"] != float64(100000) {
		t.Fatalf("radius constraints: %v", c)
	}
	if got := Dispatch(ctx, pool); got.RadiusBaseM != 12000 || got.RadiusMaxM != 20000 {
		t.Fatalf("dispatch after patch: %+v", got)
	}
}

func TestAdminSettingsRequireConfigManage(t *testing.T) {
	pool := testutil.NewDB(t)
	h := adminRouter(pool, auth.AdminPrincipal{AdminID: uuid.New(), Role: auth.RoleOperator})
	for _, m := range []string{http.MethodGet, http.MethodPatch} {
		rec, _ := call(t, h, m, "/settings", `{"topup.min_amount":600}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", m, rec.Code)
		}
	}
}
