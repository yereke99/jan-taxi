package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

type adminEnv struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router http.Handler
	admin  auth.AdminPrincipal
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	pool := testutil.NewDB(t)
	e := &adminEnv{t: t, pool: pool}
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role)
		VALUES ('root@example.test', 'Root', 'x', 'super_admin') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.admin = auth.AdminPrincipal{AdminID: id, Email: "root@example.test", Role: auth.RoleSuperAdmin, SessionID: uuid.New()}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithAdmin(req.Context(), e.admin)))
		})
	})
	NewAdmin(pool).MountAdmin(r)
	e.router = r
	return e
}

func (e *adminEnv) do(method, path string, body any, out any) int {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			e.t.Fatalf("decode %s %s: %v (%s)", method, path, err, rec.Body.String())
		}
	}
	return rec.Code
}

func (e *adminEnv) auditCount(action string) int {
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_audit_logs WHERE action = $1 AND admin_id = $2`, action, e.admin.AdminID).Scan(&n)
	return n
}

func TestAdminListAndRetryNotifications(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	user, tg := newUser(t, e.pool, "")
	other, _ := newUser(t, e.pool, "")
	dead := enqueue(t, e.pool, user, "dead-one")
	ok := enqueue(t, e.pool, other, "ok-one")

	// Produce a real dead row and a sent row through the worker.
	fs := newFakeSender()
	fs.fail(tg, apiErr(400, "Bad Request: chat not found", 0))
	w := testWorker(e.pool, fs, "w")
	w.opt.Concurrency = 1
	if _, err := e.pool.Exec(ctx, `UPDATE notification_outbox SET created_at = created_at - interval '1 minute' WHERE id = $1`, dead); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if st := readOutbox(t, e.pool, dead); st.status != StatusDead {
		t.Fatalf("setup: %+v", st)
	}
	if st := readOutbox(t, e.pool, ok); st.status != StatusSent {
		t.Fatalf("setup: %+v", st)
	}

	var page struct {
		Items    []OutboxItem `json:"items"`
		Total    int          `json:"total"`
		Page     int          `json:"page"`
		PageSize int          `json:"page_size"`
	}
	if code := e.do("GET", "/notifications?status=dead&bot=passenger", nil, &page); code != 200 {
		t.Fatalf("list status %d", code)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != dead || page.PageSize != 25 {
		t.Fatalf("page = %+v", page)
	}
	it := page.Items[0]
	if it.AttemptsSummary.Total != 1 || it.AttemptsSummary.Fatal != 1 || it.AttemptsSummary.Last == nil ||
		it.AttemptsSummary.Last.HTTPStatus == nil || *it.AttemptsSummary.Last.HTTPStatus != 400 || it.UserName != "Test User" {
		t.Fatalf("item = %+v / %+v", it, it.AttemptsSummary.Last)
	}
	if code := e.do("GET", "/notifications?page_size=10", nil, &page); code != 200 || page.Total != 2 {
		t.Fatalf("unfiltered: %d %+v", code, page)
	}
	if code := e.do("GET", "/notifications?status=lost", nil, nil); code != 422 {
		t.Fatalf("bad status filter → %d", code)
	}

	var item OutboxItem
	if code := e.do("POST", "/notifications/"+dead.String()+"/retry", nil, &item); code != 200 {
		t.Fatalf("retry status %d", code)
	}
	if item.Status != StatusPending || item.Attempts != 0 || item.LastError != nil {
		t.Fatalf("retried item %+v", item)
	}
	if n := e.auditCount("notification.retry"); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	// The retried row is delivered on the next run.
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if st := readOutbox(t, e.pool, dead); st.status != StatusSent {
		t.Fatalf("after retry: %+v", st)
	}

	var errBody struct {
		Error struct{ Code string } `json:"error"`
	}
	if code := e.do("POST", "/notifications/"+ok.String()+"/retry", nil, &errBody); code != 409 || errBody.Error.Code != "invalid_transition" {
		t.Fatalf("retry of sent row → %d %+v", code, errBody)
	}
	if code := e.do("POST", "/notifications/"+uuid.NewString()+"/retry", nil, nil); code != 404 {
		t.Fatalf("retry of unknown row → %d", code)
	}
}

func TestAdminTemplates(t *testing.T) {
	e := newAdminEnv(t)
	var list struct {
		Items []TemplateItem `json:"items"`
	}
	if code := e.do("GET", "/notification-templates", nil, &list); code != 200 {
		t.Fatalf("list → %d", code)
	}
	if len(list.Items) != len(architectureKeys)*len(Languages) {
		t.Fatalf("template items = %d", len(list.Items))
	}
	for _, it := range list.Items {
		tpl, _ := Lookup(it.Key)
		if it.Body == "" || it.Body != it.DefaultBody || it.IsCustom || it.UpdatedAt != nil ||
			(!tpl.Edit && (it.Button == nil || it.Button.Label == "")) {
			t.Fatalf("%s/%s item %+v", it.Key, it.Language, it)
		}
	}

	path := "/notification-templates/" + TplTopUpConfirmed + "/ru"
	body := "<b>Пополнено</b>: +{{amount}}\nБаланс: {{balance}}"
	var item TemplateItem
	if code := e.do("PUT", path, map[string]string{"body": body}, &item); code != 200 {
		t.Fatalf("put → %d", code)
	}
	if !item.IsCustom || item.Body != body || item.Language != LangRU || item.UpdatedBy == nil ||
		item.UpdatedBy.ID != e.admin.AdminID || item.UpdatedBy.Email != "root@example.test" || item.DefaultBody == body {
		t.Fatalf("override item %+v", item)
	}
	if strings.Join(item.Placeholders, ",") != "reference,amount,balance" {
		t.Fatalf("placeholders %v", item.Placeholders)
	}
	if n := e.auditCount("notification_template.update"); n != 1 {
		t.Fatalf("audit = %d", n)
	}
	if code := e.do("GET", "/notification-templates", nil, &list); code != 200 {
		t.Fatal(code)
	}
	for _, it := range list.Items {
		if it.Key == TplTopUpConfirmed && it.IsCustom != (it.Language == LangRU) {
			t.Fatalf("override leaked: %s/%s custom=%v", it.Key, it.Language, it.IsCustom)
		}
	}

	// The override is what users receive.
	msg, err := RenderFor(context.Background(), e.pool, TplTopUpConfirmed, LangRU,
		map[string]any{"reference": "EMB001-TOPUP-000001", "amount": 2000, "balance": 2500}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg.Text, "<b>Пополнено</b>: +2"+nbsp+"000"+nbsp+"₸") {
		t.Fatalf("rendered %q", msg.Text)
	}

	invalid := []struct {
		name string
		body string
		code int
	}{
		{"unknown placeholder", "Сумма {{fare}}", 422},
		{"malformed placeholder", "Сумма {{amount}", 422},
		{"bad html", "<b>Сумма {{amount}}", 422},
		{"script tag", "<script>x</script>", 422},
		{"empty", "   ", 422},
		{"too long", strings.Repeat("я", MaxBodyLength+1), 422},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if code := e.do("PUT", path, map[string]string{"body": tc.body}, nil); code != tc.code {
				t.Fatalf("→ %d, want %d", code, tc.code)
			}
		})
	}
	if code := e.do("PUT", "/notification-templates/nope/ru", map[string]string{"body": "x"}, nil); code != 404 {
		t.Fatalf("unknown key → %d", code)
	}
	if code := e.do("PUT", "/notification-templates/"+TplTopUpConfirmed+"/de", map[string]string{"body": "x"}, nil); code != 404 {
		t.Fatalf("unknown language → %d", code)
	}

	if code := e.do("DELETE", path, nil, &item); code != 200 {
		t.Fatalf("delete → %d", code)
	}
	if item.IsCustom || item.Body != item.DefaultBody || item.UpdatedBy != nil {
		t.Fatalf("after revert %+v", item)
	}
	if n := e.auditCount("notification_template.revert"); n != 1 {
		t.Fatalf("revert audit = %d", n)
	}
	// Reverting again is a no-op and not audited twice.
	if code := e.do("DELETE", path, nil, nil); code != 200 || e.auditCount("notification_template.revert") != 1 {
		t.Fatalf("second revert → %d", code)
	}
}

func TestAdminPermissions(t *testing.T) {
	e := newAdminEnv(t)
	tests := []struct {
		role         auth.Role
		method, path string
		want         int
	}{
		{auth.RoleOperator, "GET", "/notifications", 200},
		{auth.RoleOperator, "GET", "/notification-templates", 403},
		{auth.RoleOperator, "PUT", "/notification-templates/order_started/kk", 403},
		{auth.RoleFinanceManager, "GET", "/notifications", 403},
		{auth.RoleAuditor, "POST", "/notifications/" + uuid.NewString() + "/retry", 403},
		{auth.RoleDriverModerator, "DELETE", "/notification-templates/order_started/kk", 403},
	}
	for _, tc := range tests {
		e.admin.Role = tc.role
		var body any
		if tc.method == "PUT" {
			body = map[string]string{"body": "x"}
		}
		if code := e.do(tc.method, tc.path, body, nil); code != tc.want {
			t.Errorf("%s %s %s → %d, want %d", tc.role, tc.method, tc.path, code, tc.want)
		}
	}
}
