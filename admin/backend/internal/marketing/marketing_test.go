package marketing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/marketing"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

func TestParseRules(t *testing.T) {
	tests := []struct {
		template, raw string
		field         string // "" = valid
	}{
		{marketing.TemplateFirstRideCoupon, `{"amount":400,"validity_hours":72,"min_order_fare":400,"categories":["taxi"]}`, ""},
		{marketing.TemplateFirstRideCoupon, `{"amount":400,"validity_hours":72,"min_order_fare":300,"categories":["taxi"]}`, "rules.min_order_fare"},
		{marketing.TemplateFirstRideCoupon, `{"amount":400,"validity_hours":72,"min_order_fare":400,"categories":["taxi"],"x":1}`, "rules"},
		{marketing.TemplateFirstRideCoupon, `{"amount":0,"validity_hours":72,"min_order_fare":400,"categories":["taxi"]}`, "rules.amount"},
		{marketing.TemplateFirstRideCoupon, `{"amount":400,"validity_hours":72,"min_order_fare":400,"categories":["bus"]}`, "rules.categories"},
		{marketing.TemplateReferral, `{"reward_amount":300,"categories":["taxi"],"attribution_window_minutes":60,"unlock":"coupon_redeemed","min_ride_seconds":60}`, ""},
		{marketing.TemplateReferral, `{"reward_amount":200,"categories":["taxi"],"attribution_window_minutes":60,"unlock":"immediate","min_ride_seconds":60}`, ""},
		{marketing.TemplateReferral, `{"reward_amount":300,"categories":["taxi"],"attribution_window_minutes":60,"unlock":"always","min_ride_seconds":60}`, "rules.unlock"},
		{marketing.TemplateDriverMilestone, `{"amount":1000,"rides":10,"categories":["taxi"]}`, ""},
		{marketing.TemplatePassengerCredit, `{"amount":200}`, "rules.segment_id"},
		{marketing.TemplateBroadcastOnly, `{}`, ""},
		{"lottery", `{}`, "template"},
	}
	for _, tt := range tests {
		_, fields := marketing.ParseRules(tt.template, json.RawMessage(tt.raw))
		if tt.field == "" && len(fields) > 0 || tt.field != "" && fields[tt.field] == "" {
			t.Errorf("%s %s: fields %v, want %q", tt.template, tt.raw, fields, tt.field)
		}
	}
}

func TestReferralCodeFromPayload(t *testing.T) {
	for in, want := range map[string]string{"ref_ABCD2345": "ABCD2345", "ref_abcd2345": "ABCD2345", "ref_ABC": "",
		"order_1": "", "ref_ABCD23456": "", " ref_ABCD2345 ": "ABCD2345"} {
		got, ok := marketing.ReferralCodeFromPayload(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q → %q %v", in, got, ok)
		}
	}
}

// --- admin API --------------------------------------------------------------------

type adminEnv struct {
	t       *testing.T
	pool    *pgxpool.Pool
	h       http.Handler
	adminID uuid.UUID
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	pool := testutil.NewDB(t)
	svc := marketing.New(pool, marketing.Options{Log: slog.New(slog.DiscardHandler)})
	r := chi.NewRouter()
	svc.MountAdmin(r.With(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := uuid.Parse(r.Header.Get("X-Test-Admin"))
			if err != nil {
				httpx.WriteError(w, r, httpx.Unauthorized())
				return
			}
			ctx := auth.WithAdmin(r.Context(), auth.AdminPrincipal{AdminID: id, Role: auth.Role(r.Header.Get("X-Test-Role"))})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}))
	e := &adminEnv{t: t, pool: pool, h: r}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO admin_users (email, full_name, password_hash, role) VALUES ('m@jantaxi.test', 'M', 'x', 'super_admin')
		RETURNING id`).Scan(&e.adminID); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *adminEnv) do(role auth.Role, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Admin", e.adminID.String())
	req.Header.Set("X-Test-Role", string(role))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *adminEnv) campaignID(key string) string {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM marketing_campaigns WHERE key = $1`, key).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id.String()
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

// TestAdminPermissions: viewers read, marketing managers prepare and pause,
// only approvers switch money on, with a reason and an explicit confirmation.
func TestAdminPermissions(t *testing.T) {
	e := newAdminEnv(t)
	coupon := "/marketing/campaigns/" + e.campaignID("first_ride_coupon_400")
	activate := map[string]any{"action": "activate", "reason": "Іске қосу", "confirm": true, "expected_version": 1}

	if code, body := e.do(auth.RoleAuditor, http.MethodGet, "/marketing/campaigns", nil); code != 200 || body["total"].(float64) < 3 {
		t.Fatalf("auditor list: %d %v", code, body)
	}
	if code, _ := e.do(auth.RoleDriverModerator, http.MethodGet, "/marketing/campaigns", nil); code != 403 {
		t.Fatalf("moderator list: %d", code)
	}
	for _, role := range []auth.Role{auth.RoleAuditor, auth.RoleOperator, auth.RoleMarketingManager} {
		if code, _ := e.do(role, http.MethodPost, coupon+"/transition", activate); code != 403 {
			t.Fatalf("%s activates: %d", role, code)
		}
	}
	if code, body := e.do(auth.RoleFinanceManager, http.MethodPost, coupon+"/transition",
		map[string]any{"action": "activate", "confirm": true, "expected_version": 1}); code != 422 {
		t.Fatalf("activation without a reason: %d %v", code, body)
	}
	if code, body := e.do(auth.RoleFinanceManager, http.MethodPost, coupon+"/transition",
		map[string]any{"action": "activate", "reason": "Іске қосу", "expected_version": 1}); code != 422 {
		t.Fatalf("activation without confirmation: %d %v", code, body)
	}
	if code, body := e.do(auth.RoleFinanceManager, http.MethodPost, coupon+"/transition",
		map[string]any{"action": "activate", "reason": "Іске қосу", "confirm": true}); code != 422 {
		t.Fatalf("activation without the reviewed version: %d %v", code, body)
	}
	if code, body := e.do(auth.RoleFinanceManager, http.MethodPost, coupon+"/transition", activate); code != 200 || body["status"] != "active" {
		t.Fatalf("approver activates: %d %v", code, body)
	}
	// The launch promotions are switched on and off by approvers only.
	if code, _ := e.do(auth.RoleMarketingManager, http.MethodPost, coupon+"/transition",
		map[string]any{"action": "pause"}); code != 403 {
		t.Fatalf("manager pauses a launch campaign: %d", code)
	}
	if code, body := e.do(auth.RoleFinanceManager, http.MethodPost, coupon+"/transition",
		map[string]any{"action": "pause"}); code != 200 || body["status"] != "paused" {
		t.Fatalf("approver pauses: %d %v", code, body)
	}
	if code, _ := e.do(auth.RoleMarketingManager, http.MethodPost, coupon+"/transition",
		map[string]any{"action": "resume", "reason": "Қайта қосу", "confirm": true, "expected_version": 1}); code != 403 {
		t.Fatalf("manager resumes: %d", code)
	}
	// A second live first-ride campaign is refused.
	code, created := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/campaigns", map[string]any{
		"template": "first_ride_coupon", "title": map[string]string{"kk": "Тағы", "ru": "Ещё", "en": "Another"},
		"rules": map[string]any{"amount": 300, "validity_hours": 24, "min_order_fare": 300, "categories": []string{"taxi"}},
	})
	if code != 201 || created["status"] != "draft" || created["version"].(float64) != 1 {
		t.Fatalf("create draft: %d %v", code, created)
	}
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, "/marketing/campaigns/"+created["id"].(string)+"/transition",
		activate); code != 409 || errCode(body) != "campaign_conflict" {
		t.Fatalf("second live coupon campaign: %d %v", code, body)
	}
	// Changing a live campaign's rules is an approver's call and makes a new
	// version; the old version stays.
	rules := map[string]any{"rules": map[string]any{"amount": 500, "validity_hours": 72, "min_order_fare": 500,
		"categories": []string{"taxi"}}, "change_note": "500 ₸"}
	if code, _ := e.do(auth.RoleMarketingManager, http.MethodPost, coupon+"/versions", rules); code != 403 {
		t.Fatalf("manager edits a live campaign: %d", code)
	}
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, coupon+"/versions", rules); code != 200 ||
		body["version"].(float64) != 2 || len(body["versions"].([]any)) != 2 {
		t.Fatalf("new version: %d %v", code, body)
	}
	// Budget changes need a reason; switches likewise.
	if code, _ := e.do(auth.RoleSuperAdmin, http.MethodPatch, coupon+"/budget",
		map[string]any{"budget_limit": 50000, "confirm": true}); code != 422 {
		t.Fatalf("budget without reason: %d", code)
	}
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPatch, coupon+"/budget",
		map[string]any{"budget_limit": 50000, "reason": "Пилот", "confirm": true}); code != 200 || body["budget_limit"].(float64) != 50000 {
		t.Fatalf("budget: %d %v", code, body)
	}
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPatch, "/marketing/settings",
		map[string]any{"marketing.redemptions_enabled": false, "reason": "Тексеру"}); code != 200 {
		t.Fatalf("switch: %d %v", code, body)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM admin_audit_logs WHERE action LIKE 'marketing.campaign.%'`); n < 4 {
		t.Fatalf("audit rows = %d", n)
	}
	// Manual bonus credits need finance.manage.
	uid, _ := testutil.CreateUser(t, e.pool)
	adj := map[string]any{"amount": 500, "reason": "Өтемақы", "idempotency_key": uuid.NewString(), "confirm": true}
	if code, _ := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/users/"+uid.String()+"/bonus-adjustments", adj); code != 403 {
		t.Fatalf("manager credits: %d", code)
	}
	for range 2 { // a replay credits once
		if code, body := e.do(auth.RoleFinanceManager, http.MethodPost, "/marketing/users/"+uid.String()+"/bonus-adjustments", adj); code != 200 ||
			body["balance"].(float64) != 500 {
			t.Fatalf("finance credits: %d %v", code, body)
		}
	}
	if code, body := e.do(auth.RoleFinanceManager, http.MethodPost, "/marketing/users/"+uid.String()+"/bonus-adjustments",
		map[string]any{"amount": -600, "reason": "Қате", "idempotency_key": uuid.NewString(), "confirm": true}); code != 409 ||
		errCode(body) != "insufficient_balance" {
		t.Fatalf("debit below zero: %d %v", code, body)
	}
}

func (e *adminEnv) setting(key, value string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO app_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, value); err != nil {
		e.t.Fatal(err)
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// --- broadcasts -------------------------------------------------------------------

type fakeSender struct {
	mu    sync.Mutex
	sent  map[int64]int
	fail  map[int64]error // first call per chat
	calls int
}

func (f *fakeSender) Send(_ context.Context, _ notifications.Bot, chatID int64, text string, markup *telegram.InlineKeyboardMarkup) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err, ok := f.fail[chatID]; ok {
		delete(f.fail, chatID)
		return 0, err
	}
	if text == "" || markup == nil {
		return 0, &telegram.APIError{Code: 400, Description: "empty"}
	}
	f.sent[chatID]++
	return int64(f.calls), nil
}

// TestBroadcastPipeline: the snapshot leaves out opted-out people, sending
// suppresses blocked chats, retries server errors, honours 429, never sends a
// paused broadcast and yields to queued order messages.
func TestBroadcastPipeline(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	var users []struct {
		id uuid.UUID
		tg int64
	}
	for range 5 {
		id, tg := testutil.CreateUser(t, e.pool)
		users = append(users, struct {
			id uuid.UUID
			tg int64
		}{id, tg})
	}
	if err := marketing.SetMarketingOptOut(ctx, e.pool, users[4].id, true); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{sent: map[int64]int{}, fail: map[int64]error{
		users[1].tg: &telegram.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"},
		users[2].tg: &telegram.APIError{Code: 502, Description: "Bad Gateway"},
	}}
	worker := marketing.NewBroadcastWorker(e.pool, sender, marketing.WorkerOptions{
		PassengerMiniAppURL: "https://app.jantaxi.test/passenger/", Log: slog.New(slog.DiscardHandler)})

	code, b := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts", map[string]any{
		"title": "Жаңа акция", "audience": "passengers",
		"content": map[string]string{"kk": "Сәлем! <b>400 ₸</b> купон", "ru": "Привет! Купон 400 ₸", "en": "Hi! 400 ₸ coupon"},
		"button":  map[string]any{"label": map[string]string{"kk": "Ашу", "ru": "Открыть", "en": "Open"}, "start_param": "coupons"},
	})
	if code != 201 || b["status"] != "draft" {
		t.Fatalf("create: %d %v", code, b)
	}
	id := b["id"].(string)
	if est := b["estimate"].(map[string]any); est["reachable"].(float64) != 4 || est["suppressed"].(float64) != 1 {
		t.Fatalf("estimate: %v", est)
	}
	// Test sends go only to people an approver listed, and never to someone
	// who opted out; the listed person gets it even while it is a draft.
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id+"/test",
		map[string]any{"telegram_user_id": users[0].tg}); code != 422 {
		t.Fatalf("test send to an unlisted person: %d %v", code, body)
	}
	e.setting("marketing.test_recipients", fmt.Sprintf("[%d, %d]", users[0].tg, users[4].tg))
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id+"/test",
		map[string]any{"telegram_user_id": users[4].tg}); code != 422 {
		t.Fatalf("test send to an opted-out person: %d %v", code, body)
	}
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id+"/test",
		map[string]any{"telegram_user_id": users[0].tg}); code != 201 {
		t.Fatalf("test send: %d %v", code, body)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.sent[users[0].tg] != 1 || len(sender.sent) != 1 {
		t.Fatalf("after the test send: %v", sender.sent)
	}
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id+"/transition",
		map[string]any{"action": "send_now", "confirm": true}); code != 200 || body["status"] != "sending" ||
		body["recipient_count"].(float64) != 4 {
		t.Fatalf("send: %d %v", code, body)
	}
	// Order messages first: while the outbox has a backlog nothing is sent.
	for i := range 25 {
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO notification_outbox (bot, user_id, chat_id, template, dedupe_key)
			VALUES ('passenger', $1, $2, 'order_started', $3)`, users[0].id, users[0].tg, "backlog:"+uuid.NewString()+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := worker.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("sent %d during an order backlog (%v)", n, err)
	}
	if _, err := e.pool.Exec(ctx, `DELETE FROM notification_outbox`); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := worker.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// The 502 is retried after its backoff.
	if _, err := e.pool.Exec(ctx, `UPDATE marketing_broadcast_recipients SET next_attempt_at = now() WHERE status = 'queued'`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := worker.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	stats := map[string]int{}
	rows, err := e.pool.Query(ctx, `SELECT status, count(*) FROM marketing_broadcast_recipients WHERE NOT is_test GROUP BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		var n int
		_ = rows.Scan(&s, &n)
		stats[s] = n
	}
	rows.Close()
	if stats["sent"] != 3 || stats["blocked"] != 1 || stats["suppressed"] != 1 {
		t.Fatalf("delivery: %v (sent %v)", stats, sender.sent)
	}
	if sender.sent[users[4].tg] != 0 || sender.sent[users[1].tg] != 0 {
		t.Fatalf("opted-out or blocked person got a message: %v", sender.sent)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM users WHERE id = $1 AND passenger_bot_blocked_at IS NOT NULL`, users[1].id); n != 1 {
		t.Fatal("blocked chat not recorded")
	}
	var status string
	if err := e.pool.QueryRow(ctx, `SELECT status FROM marketing_broadcasts WHERE id = $1`, id).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("broadcast status %q %v", status, err)
	}

	// A paused broadcast sends nothing; cancelling drops what is left.
	code, b2 := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts", map[string]any{
		"title": "Екінші", "audience": "passengers",
		"content": map[string]string{"kk": "Мәтін", "ru": "Текст", "en": "Text"},
	})
	if code != 201 {
		t.Fatalf("create: %d %v", code, b2)
	}
	id2 := b2["id"].(string)
	e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id2+"/transition", map[string]any{"action": "send_now", "confirm": true})
	e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id2+"/transition", map[string]any{"action": "pause"})
	before := sender.calls
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.calls != before {
		t.Fatal("a paused broadcast was sent")
	}
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id2+"/transition",
		map[string]any{"action": "cancel"}); code != 200 || body["status"] != "cancelled" {
		t.Fatalf("cancel: %d %v", code, body)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM marketing_broadcast_recipients WHERE broadcast_id = $1 AND status = 'queued'`, id2); n != 0 {
		t.Fatalf("queued after cancel: %d", n)
	}
}

// TestBroadcastApproval: an audience above the threshold needs an approver.
func TestBroadcastApproval(t *testing.T) {
	e := newAdminEnv(t)
	for range 3 {
		testutil.CreateUser(t, e.pool)
	}
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO app_settings (key, value) VALUES ('marketing.broadcast_approval_threshold', '2')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	_, b := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts", map[string]any{
		"title": "Барлығына", "audience": "passengers",
		"content": map[string]string{"kk": "Мәтін", "ru": "Текст", "en": "Text"},
	})
	id := b["id"].(string)
	send := map[string]any{"action": "send_now", "confirm": true}
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id+"/transition", send); code != 403 ||
		errCode(body) != "approval_required" {
		t.Fatalf("manager sends to 3 > 2: %d %v", code, body)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM marketing_broadcast_recipients WHERE broadcast_id = $1`, id); n != 0 {
		t.Fatalf("snapshot kept after refusal: %d", n)
	}
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, "/marketing/broadcasts/"+id+"/transition", send); code != 200 {
		t.Fatalf("approver sends: %d %v", code, body)
	}
	// Segment filters are typed per audience.
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/segments/preview",
		map[string]any{"audience": "passengers", "filters": map[string]any{"driver_statuses": []string{"approved"}}}); code != 422 {
		t.Fatalf("driver filter on passengers: %d %v", code, body)
	}
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/segments/preview",
		map[string]any{"audience": "passengers", "filters": map[string]any{"languages": []string{"kk"}, "completed_rides_max": 0}}); code != 200 ||
		body["reachable"].(float64) != 3 {
		t.Fatalf("preview: %d %v", code, body)
	}
	_ = time.Second
}

func itoa(i int) string { return string(rune('a' + i%26)) }

// TestApprovalsHoldAtSendTime: what a non-approver scheduled cannot grow past
// the approval threshold by send time (segment edits after scheduling do not
// move a broadcast), and copy for drivers may not name the commission.
func TestApprovalsHoldAtSendTime(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	e.setting("marketing.broadcast_approval_threshold", "2")
	for range 3 {
		id, _ := testutil.CreateUser(t, e.pool)
		if _, err := e.pool.Exec(ctx, `UPDATE user_preferences SET language = 'ru' WHERE user_id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	code, seg := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/segments",
		map[string]any{"name": "English", "audience": "passengers", "filters": map[string]any{"languages": []string{"en"}}})
	if code != 201 {
		t.Fatalf("segment: %d %v", code, seg)
	}
	_, b := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts", map[string]any{
		"title": "Аз адамға", "audience": "passengers", "segment_id": seg["id"],
		"content": map[string]string{"kk": "Мәтін", "ru": "Текст", "en": "Text"},
	})
	id := b["id"].(string)
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id+"/transition",
		map[string]any{"action": "schedule", "scheduled_at": time.Now().Add(time.Hour).UTC(), "confirm": true}); code != 200 {
		t.Fatalf("schedule: %d %v", code, body)
	}
	// Widening the segment afterwards does not move the scheduled broadcast.
	if code, _ := e.do(auth.RoleMarketingManager, http.MethodPatch, "/marketing/segments/"+seg["id"].(string),
		map[string]any{"filters": map[string]any{}}); code != 200 {
		t.Fatalf("segment edit: %d", code)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE marketing_broadcasts SET scheduled_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{sent: map[int64]int{}, fail: map[int64]error{}}
	worker := marketing.NewBroadcastWorker(e.pool, sender, marketing.WorkerOptions{Log: slog.New(slog.DiscardHandler)})
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	var recipients *int
	if err := e.pool.QueryRow(ctx, `SELECT status, recipient_count FROM marketing_broadcasts WHERE id = $1`, id).
		Scan(&status, &recipients); err != nil || status != "completed" || recipients == nil || *recipients != 0 {
		t.Fatalf("frozen targeting: %s %v %v", status, recipients, err)
	}

	// A broadcast whose audience grew past the threshold since a manager
	// scheduled it is not sent.
	_, b2 := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts", map[string]any{
		"title": "Өсетін аудитория", "audience": "passengers", "filters": map[string]any{"registered_within_days": 30},
		"content": map[string]string{"kk": "Мәтін", "ru": "Текст", "en": "Text"},
	})
	id2 := b2["id"].(string)
	if _, err := e.pool.Exec(ctx, `UPDATE users SET created_at = now() - interval '60 days'`); err != nil {
		t.Fatal(err)
	}
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts/"+id2+"/transition",
		map[string]any{"action": "schedule", "scheduled_at": time.Now().Add(time.Hour).UTC(), "confirm": true}); code != 200 {
		t.Fatalf("schedule an empty audience: %d %v", code, body)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE users SET created_at = now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE marketing_broadcasts SET scheduled_at = now() - interval '1 second' WHERE id = $1`, id2); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT status FROM marketing_broadcasts WHERE id = $1`, id2).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("grown audience: %s %v", status, err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent without approval: %v", sender.sent)
	}

	// Drivers never read about the commission.
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/broadcasts", map[string]any{
		"title": "Жүргізушілерге", "audience": "drivers",
		"content": map[string]string{"kk": "Бүгін комиссия 0 ₸", "ru": "Текст", "en": "Text"},
	}); code != 422 || body["error"].(map[string]any)["fields"].(map[string]any)["content.kk"] != "mentions_commission" {
		t.Fatalf("commission in driver copy: %d %v", code, body)
	}
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/campaigns", map[string]any{
		"template": "driver_milestone_bonus", "title": map[string]string{"kk": "Комиссия қайтарымы", "ru": "Бонус", "en": "Bonus"},
		"rules":     map[string]any{"amount": 1000, "rides": 10, "categories": []string{"taxi"}},
		"starts_at": time.Now().UTC(), "ends_at": time.Now().Add(240 * time.Hour).UTC(),
	}); code != 422 {
		t.Fatalf("commission in a driver campaign title: %d %v", code, body)
	}
}

// TestManualBucketAndFrozenCredits: finance's manual-adjustment bucket can
// be neither archived nor re-capped by a marketing manager, and a
// passenger_credit audience is frozen when an approver schedules it.
func TestManualBucketAndFrozenCredits(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	manual := "/marketing/campaigns/" + e.campaignID("manual_adjustments")
	if code, body := e.do(auth.RoleMarketingManager, http.MethodPost, manual+"/transition", map[string]any{"action": "archive"}); code != 403 {
		t.Fatalf("manager archives the manual bucket: %d %v", code, body)
	}
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, manual+"/transition", map[string]any{"action": "archive"}); code != 409 {
		t.Fatalf("archive the manual bucket: %d %v", code, body)
	}
	if code, _ := e.do(auth.RoleMarketingManager, http.MethodPost, manual+"/versions",
		map[string]any{"rules": map[string]any{"max_amount": 1000000}}); code != 403 {
		t.Fatalf("manager re-caps manual credits: %d", code)
	}

	for range 2 {
		testutil.CreateUser(t, e.pool)
	}
	_, seg := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/segments",
		map[string]any{"name": "Барлығы", "audience": "passengers", "filters": map[string]any{}})
	code, c := e.do(auth.RoleMarketingManager, http.MethodPost, "/marketing/campaigns", map[string]any{
		"template": "passenger_credit", "title": map[string]string{"kk": "Сыйлық", "ru": "Подарок", "en": "Gift"},
		"rules": map[string]any{"amount": 200, "segment_id": seg["id"]},
	})
	if code != 201 {
		t.Fatalf("credit campaign: %d %v", code, c)
	}
	cid := c["id"].(string)
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, "/marketing/campaigns/"+cid+"/transition", map[string]any{
		"action": "schedule", "scheduled_at": time.Now().Add(time.Hour).UTC(), "reason": "Сыйлық", "confirm": true,
		"expected_version": 1}); code != 200 {
		t.Fatalf("schedule: %d %v", code, body)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM marketing_grants WHERE campaign_id = $1 AND status = 'pending'`, cid); n != 2 {
		t.Fatalf("frozen audience = %d", n)
	}
	// People who join afterwards are not part of what was approved.
	testutil.CreateUser(t, e.pool)
	if _, err := e.pool.Exec(ctx, `UPDATE marketing_campaigns SET scheduled_at = now() - interval '1 second' WHERE id = $1`, cid); err != nil {
		t.Fatal(err)
	}
	svc := marketing.New(e.pool, marketing.Options{Log: slog.New(slog.DiscardHandler)})
	if err := svc.ActivateDue(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM marketing_grants WHERE campaign_id = $1`, cid); n != 2 {
		t.Fatalf("grants after activation = %d", n)
	}
	// A version that changed since review is refused.
	if code, body := e.do(auth.RoleSuperAdmin, http.MethodPost, "/marketing/campaigns/"+cid+"/transition", map[string]any{
		"action": "end", "reason": "Тоқтату", "confirm": true, "expected_version": 7}); code != 409 || errCode(body) != "version_conflict" {
		t.Fatalf("stale version: %d %v", code, body)
	}
}
