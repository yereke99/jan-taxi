package botapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestUserSyncLifecycle(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	tg := testutil.NextTelegramID()
	sync := func(bot string, body map[string]any) map[string]any {
		t.Helper()
		body["telegram_user_id"] = tg
		st, b := e.call(t, bot, http.MethodPost, "/bots/"+bot+"/users/sync", body)
		if st != http.StatusOK {
			t.Fatalf("sync: %d %v", st, b)
		}
		return b
	}

	b := sync("passenger", map[string]any{"first_name": "Әлия", "language_code": "ru", "started": true})
	if b["is_new"] != true || b["language"] != "kk" || b["language_chosen"] != false || b["is_blocked"] != false {
		t.Fatalf("new user: %v", b)
	}
	if _, ok := b["driver"]; ok {
		t.Fatalf("passenger bot must not get driver state: %v", b)
	}
	var started, blocked *time.Time
	read := func() {
		t.Helper()
		if err := e.pool.QueryRow(ctx, `SELECT passenger_bot_started_at, passenger_bot_blocked_at FROM users WHERE telegram_user_id = $1`, tg).
			Scan(&started, &blocked); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if started == nil || blocked != nil {
		t.Fatalf("after /start: started=%v blocked=%v", started, blocked)
	}

	b = sync("passenger", map[string]any{"first_name": "Әлия", "blocked": true})
	if b["is_new"] != false {
		t.Fatalf("existing user reported new: %v", b)
	}
	read()
	if blocked == nil {
		t.Fatal("blocked flag not stored")
	}
	sync("passenger", map[string]any{"first_name": "Әлия", "blocked": false})
	read()
	if blocked != nil {
		t.Fatal("unblock not stored")
	}
	sync("passenger", map[string]any{"first_name": "Әлия", "blocked": true})
	sync("passenger", map[string]any{"first_name": "Әлия", "started": true})
	read()
	if blocked != nil {
		t.Fatal("/start must clear the blocked flag")
	}

	// Language: explicit choice is stored and reported.
	st, lb := e.call(t, "passenger", http.MethodPut, "/bots/passenger/users/"+itoa(tg)+"/language", map[string]any{"language": "ru"})
	if st != 200 || lb["language"] != "ru" {
		t.Fatalf("set language: %d %v", st, lb)
	}
	if b := sync("driver", map[string]any{"first_name": "Әлия"}); b["language"] != "ru" || b["language_chosen"] != true {
		t.Fatalf("after choice: %v", b)
	} else if d, _ := b["driver"].(map[string]any); d == nil || d["registered"] != false || d["phone_verified"] != false {
		t.Fatalf("driver state for a non-driver: %v", b["driver"])
	}
	if st, _ := e.call(t, "passenger", http.MethodPut, "/bots/passenger/users/"+itoa(tg)+"/language", map[string]any{"language": "de"}); st != http.StatusUnprocessableEntity {
		t.Fatalf("bad language: %d", st)
	}
	if st, _ := e.call(t, "passenger", http.MethodPut, "/bots/passenger/users/999999999999/language", map[string]any{"language": "kk"}); st != http.StatusNotFound {
		t.Fatalf("unknown user: %d", st)
	}

	// Validation.
	if st, _ := e.call(t, "passenger", http.MethodPost, "/bots/passenger/users/sync", map[string]any{"telegram_user_id": 0, "first_name": "x"}); st != http.StatusUnprocessableEntity {
		t.Fatalf("invalid id: %d", st)
	}
}

func TestUserSyncDriverState(t *testing.T) {
	e := newTestEnv(t)
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Online: true, Balance: 1000})
	st, b := e.call(t, "driver", http.MethodPost, "/bots/driver/users/sync", map[string]any{
		"telegram_user_id": d.TelegramID, "first_name": "Test", "started": true,
	})
	if st != 200 {
		t.Fatalf("sync: %d %v", st, b)
	}
	drv, _ := b["driver"].(map[string]any)
	if drv["registered"] != true || drv["code"] != d.Code || drv["status"] != "approved" || drv["is_online"] != true ||
		drv["phone_verified"] != true || drv["driver_id"] != d.DriverID.String() {
		t.Fatalf("driver state: %v", drv)
	}
	var startedAt *time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT driver_bot_started_at FROM users WHERE id = $1`, d.UserID).Scan(&startedAt); err != nil || startedAt == nil {
		t.Fatalf("driver bot start not recorded: %v %v", startedAt, err)
	}
}

func TestPassengerOrders(t *testing.T) {
	e := newTestEnv(t)
	uid, tg := testutil.CreateUser(t, e.pool)
	base := time.Now().Add(-time.Hour)
	var ids []string
	for i := range 6 {
		ids = append(ids, insertOrder(t, e.pool, uid, nil, "cancelled", 500+50*i, base.Add(time.Duration(i)*time.Minute)).String())
	}
	st, b := e.call(t, "passenger", http.MethodGet, "/passenger/"+itoa(tg)+"/orders", nil)
	if st != 200 {
		t.Fatalf("orders: %d %v", st, b)
	}
	items, _ := b["items"].([]any)
	if len(items) != 5 {
		t.Fatalf("default limit: %d items", len(items))
	}
	first := items[0].(map[string]any)
	if first["id"] != ids[5] || first["fare"] != float64(750) || first["status"] != "cancelled" || first["currency"] != "KZT" ||
		first["pickup_address"] != "Жиенқали Тілепбергенов көшесі, 12" || first["destination_address"] != "48.83010, 58.15100" {
		t.Fatalf("newest order: %v", first)
	}
	if st, b := e.call(t, "passenger", http.MethodGet, "/passenger/"+itoa(tg)+"/orders?limit=2", nil); st != 200 || len(b["items"].([]any)) != 2 {
		t.Fatalf("limit=2: %d %v", st, b)
	}
	if st, _ := e.call(t, "passenger", http.MethodGet, "/passenger/"+itoa(tg)+"/orders?limit=21", nil); st != http.StatusUnprocessableEntity {
		t.Fatalf("limit=21: %d", st)
	}
	if st, b := e.call(t, "passenger", http.MethodGet, "/passenger/999999999999/orders", nil); st != 200 || len(b["items"].([]any)) != 0 {
		t.Fatalf("unknown user: %d %v", st, b)
	}
	if st, _ := e.call(t, "passenger", http.MethodGet, "/passenger/abc/orders", nil); st != http.StatusNotFound {
		t.Fatalf("bad tg id: %d", st)
	}
}

func TestConfig(t *testing.T) {
	e := newTestEnv(t)
	st, b := e.call(t, "driver", http.MethodGet, "/config", nil)
	if st != 200 {
		t.Fatalf("config: %d %v", st, b)
	}
	if b["passenger_miniapp_url"] != "https://app.example.kz/passenger/" || b["driver_bot_username"] != "jantaxi_driver_bot" ||
		b["passenger_bot_username"] != "jantaxi_bot" || b["default_language"] != "kk" {
		t.Fatalf("config: %v", b)
	}
	sup := b["support"].(map[string]any)
	if sup["telegram_username"] != nil || sup["phone"] != nil {
		t.Fatalf("empty support contacts must be null: %v", sup)
	}
	if hours := sup["hours"].(map[string]any); hours["kk"] != "Күн сайын 08:00–23:00" {
		t.Fatalf("support hours: %v", hours)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE app_settings SET value = '"@jantaxi_support"' WHERE key = 'support.telegram_username'`); err != nil {
		t.Fatal(err)
	}
	if _, b := e.call(t, "driver", http.MethodGet, "/config", nil); b["support"].(map[string]any)["telegram_username"] != "jantaxi_support" {
		t.Fatalf("support username: %v", b["support"])
	}
}
