package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestMarshalRedactsSecretLookingKeys(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"plain", map[string]any{"amount": 500, "comment": "ok"}, `{"amount":500,"comment":"ok"}`},
		{"top level", map[string]any{"password": "hunter2", "email": "a@b.kz"}, `{"email":"a@b.kz","password":"[redacted]"}`},
		{"case and separators", map[string]any{"API_Token": "t", "session_secret": "s", "password_hash": "h", "initData": "i", "init-data": "x", "X-Api-Key": "k"},
			`{"API_Token":"[redacted]","X-Api-Key":"[redacted]","init-data":"[redacted]","initData":"[redacted]","password_hash":"[redacted]","session_secret":"[redacted]"}`},
		{"non-secret lookalikes kept", map[string]any{"key": "topup.min_amount", "value": 500, "telegram_username": "jt"},
			`{"key":"topup.min_amount","telegram_username":"jt","value":500}`},
		{"nested", map[string]any{"bot": map[string]any{"name": "x", "token": "123:abc"}}, `{"bot":{"name":"x","token":"[redacted]"}}`},
		{"arrays", []any{map[string]any{"authorization": "Bearer x"}, 1}, `[{"authorization":"[redacted]"},1]`},
		{"struct", struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		}{"n", "p"}, `{"name":"n","password":"[redacted]"}`},
		{"scalar", "token", `"token"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := marshal(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.want {
				t.Fatalf("got %s, want %s", b, tt.want)
			}
		})
	}
}

func createAdmin(t *testing.T, pool *pgxpool.Pool, email, name, role string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role) VALUES ($1, $2, 'x', $3) RETURNING id`,
		email, name, role).Scan(&id); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return id
}

func TestLogStoresRedactedRowWithRequestMetadata(t *testing.T) {
	pool := testutil.NewDB(t)
	adminID := createAdmin(t, pool, "root@jantaxi.kz", "Root", "super_admin")
	ctx := auth.WithAdmin(context.Background(), auth.AdminPrincipal{AdminID: adminID, Role: auth.RoleSuperAdmin})
	req := httptest.NewRequest(http.MethodPost, "/x", nil).WithContext(ctx)
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("User-Agent", "test-agent")

	err := Log(ctx, pool, req, Entry{
		Action: "admin.create", EntityType: "admin", EntityID: "42",
		Before: nil, After: map[string]any{"email": "x@y.kz", "password": "plain", "nested": map[string]any{"secret_key": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var gotAdmin uuid.UUID
	var after, ip, ua string
	var before *string
	if err := pool.QueryRow(context.Background(), `
		SELECT admin_id, before::text, after::text, host(ip), user_agent FROM admin_audit_logs`).Scan(&gotAdmin, &before, &after, &ip, &ua); err != nil {
		t.Fatal(err)
	}
	if gotAdmin != adminID || before != nil || ip != "203.0.113.7" || ua != "test-agent" {
		t.Fatalf("row admin=%v before=%v ip=%s ua=%s", gotAdmin, before, ip, ua)
	}
	if after != `{"email": "x@y.kz", "nested": {"secret_key": "[redacted]"}, "password": "[redacted]"}` {
		t.Fatalf("after = %s", after)
	}

	// Jobs pass AdminID explicitly and no request.
	sys := createAdmin(t, pool, "jobs@jantaxi.kz", "Jobs", "operator")
	if err := Log(context.Background(), pool, nil, Entry{Action: "topup.expire", EntityType: "topup", AdminID: &sys}); err != nil {
		t.Fatal(err)
	}
}

func seedLogs(t *testing.T, pool *pgxpool.Pool) (a1, a2 uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	a1 = createAdmin(t, pool, "alma@jantaxi.kz", "Алма Нұрлан", "super_admin")
	a2 = createAdmin(t, pool, "bek@jantaxi.kz", "Bek", "finance_manager")
	rows := []struct {
		admin                    *uuid.UUID
		action, etype, eid, when string
	}{
		{&a1, "driver.approve", "driver", "d1", "2026-10-01T10:00:00Z"},
		{&a1, "driver.reject", "driver", "d2", "2026-10-02T10:00:00Z"},
		{&a2, "topup.confirm", "topup", "t1", "2026-10-03T10:00:00Z"},
		{&a2, "topup.confirm", "topup", "t2", "2026-10-04T23:30:00Z"},
		{nil, "order.cancel", "order", "o1", "2026-10-05T10:00:00Z"},
	}
	for _, r := range rows {
		if _, err := pool.Exec(ctx, `
			INSERT INTO admin_audit_logs (admin_id, action, entity_type, entity_id, after, created_at)
			VALUES ($1, $2, $3, $4, '{"ok": true}', $5)`, r.admin, r.action, r.etype, r.eid, r.when); err != nil {
			t.Fatal(err)
		}
	}
	return a1, a2
}

func TestListFilters(t *testing.T) {
	pool := testutil.NewDB(t)
	a1, a2 := seedLogs(t, pool)
	ts := func(s string) *time.Time { v, _ := time.Parse(time.RFC3339, s); return &v }
	tests := []struct {
		name  string
		f     Filter
		total int
		first string
	}{
		{"all newest first", Filter{}, 5, "o1"},
		{"by admin", Filter{AdminID: &a1}, 2, "d2"},
		{"by entity type", Filter{EntityType: "topup"}, 2, "t2"},
		{"by entity", Filter{EntityType: "driver", EntityID: "d1"}, 1, "d1"},
		{"by action", Filter{Action: "topup.confirm", AdminID: &a2}, 2, "t2"},
		{"range", Filter{From: ts("2026-10-02T00:00:00Z"), To: ts("2026-10-04T00:00:00Z")}, 2, "t1"},
		{"no match", Filter{Action: "nothing"}, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, total, err := List(context.Background(), pool, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			if total != tt.total || len(items) != tt.total {
				t.Fatalf("total=%d items=%d, want %d", total, len(items), tt.total)
			}
			if tt.first != "" && *items[0].EntityID != tt.first {
				t.Fatalf("first = %s, want %s", *items[0].EntityID, tt.first)
			}
		})
	}
	items, _, err := List(context.Background(), pool, Filter{AdminID: &a1})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].AdminEmail == nil || *items[0].AdminEmail != "alma@jantaxi.kz" || *items[0].AdminName != "Алма Нұрлан" {
		t.Fatalf("admin join = %+v", items[0])
	}
	if string(items[0].After) != `{"ok": true}` || items[0].Before != nil {
		t.Fatalf("before/after = %s / %s", items[0].Before, items[0].After)
	}
}

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

func TestAuditLogsEndpoint(t *testing.T) {
	pool := testutil.NewDB(t)
	a1, _ := seedLogs(t, pool)
	h := adminRouter(pool, auth.AdminPrincipal{AdminID: a1, Role: auth.RoleAuditor})

	tests := []struct {
		query  string
		status int
		total  int
		items  int
		field  string
	}{
		{"", 200, 5, 5, ""},
		{"?page=2&page_size=2", 200, 5, 2, ""},
		{"?page=3&page_size=2", 200, 5, 1, ""},
		{"?admin_id=" + a1.String(), 200, 2, 2, ""},
		{"?entity_type=topup&action=topup.confirm", 200, 2, 2, ""},
		{"?from=2026-10-04&to=2026-10-04", 200, 1, 1, ""}, // date-only "to" covers the whole day
		{"?from=2026-10-01T00:00:00%2B05:00&to=2026-10-02T00:00:00Z", 200, 1, 1, ""},
		{"?admin_id=nope", 422, 0, 0, "admin_id"},
		{"?from=yesterday", 422, 0, 0, "from"},
		{"?from=2026-10-05&to=2026-10-01", 422, 0, 0, "to"},
		{"?page_size=1000", 422, 0, 0, "page_size"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit-logs"+tt.query, nil))
			if rec.Code != tt.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var body struct {
				Items []Record `json:"items"`
				Total int      `json:"total"`
				Page  int      `json:"page"`
				Error struct {
					Fields map[string]string `json:"fields"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tt.status != 200 {
				if body.Error.Fields[tt.field] == "" {
					t.Fatalf("missing field error %s: %s", tt.field, rec.Body)
				}
				return
			}
			if body.Total != tt.total || len(body.Items) != tt.items {
				t.Fatalf("total=%d items=%d: %s", body.Total, len(body.Items), rec.Body)
			}
		})
	}

	// driver_moderator lacks audit.view.
	rec := httptest.NewRecorder()
	adminRouter(pool, auth.AdminPrincipal{AdminID: a1, Role: auth.RoleDriverModerator}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit-logs", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("moderator status = %d", rec.Code)
	}
}
