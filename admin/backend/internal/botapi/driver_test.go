package botapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestDriverSummary(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	// Unknown user and a user without a profile.
	if st, b := e.call(t, "driver", http.MethodGet, "/driver/999999999999/summary", nil); st != 200 || b["registered"] != false || b["language"] != "kk" {
		t.Fatalf("unknown user: %d %v", st, b)
	}
	_, tg := testutil.CreateUser(t, e.pool)
	st, b := e.call(t, "driver", http.MethodGet, "/driver/"+itoa(tg)+"/summary", nil)
	if st != 200 || b["registered"] != false || b["wallet"] != nil || b["code"] != nil || b["status"] != nil || len(b["corrections"].([]any)) != 0 {
		t.Fatalf("no profile: %d %v", st, b)
	}

	// Approved, online, with an active order.
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Online: true, Balance: 1000})
	pid, _ := testutil.CreateUser(t, e.pool)
	oid := insertOrder(t, e.pool, pid, &d.DriverID, "accepted", 800, time.Now())
	pid2, _ := testutil.CreateUser(t, e.pool)
	insertOrder(t, e.pool, pid2, nil, "searching", 800, time.Now())
	st, b = e.call(t, "driver", http.MethodGet, "/driver/"+itoa(d.TelegramID)+"/summary", nil)
	if st != 200 {
		t.Fatalf("summary: %d %v", st, b)
	}
	if b["registered"] != true || b["driver_id"] != d.DriverID.String() || b["code"] != d.Code || b["status"] != "approved" ||
		b["is_online"] != true || b["phone_verified"] != true || b["first_name"] != "Test" {
		t.Fatalf("summary profile: %v", b)
	}
	w := b["wallet"].(map[string]any)
	if w["balance"] != float64(1000) || w["available"] != float64(1000) || w["currency"] != "KZT" || w["min_online_balance"] != float64(50) {
		t.Fatalf("wallet: %v", w)
	}
	ao := b["active_order"].(map[string]any)
	if ao["id"] != oid.String() || ao["status"] != "accepted" || ao["fare"] != float64(800) || ao["destination_address"] != "48.83010, 58.15100" {
		t.Fatalf("active order: %v", ao)
	}
	if v := b["vehicle"].(map[string]any); v["make"] != "Toyota" || v["model"] != "Camry" || v["plate"] == "" ||
		v["vehicle_class"] != "sedan" || v["seats"] != float64(4) || v["cargo_volume_l"] != float64(400) {
		t.Fatalf("vehicle: %v", v)
	}
	if ao := b["active_order"].(map[string]any); ao["vehicle_class"] != "sedan" || ao["passenger_count"] != float64(1) {
		t.Fatalf("active order load: %v", ao)
	}
	if b["available_orders"] != float64(0) {
		t.Fatalf("a driver with an active order has no available orders: %v", b["available_orders"])
	}

	// Approved, online, idle: the searching order is counted.
	idle := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Online: true, Balance: 1000})
	if _, b := e.call(t, "driver", http.MethodGet, "/driver/"+itoa(idle.TelegramID)+"/summary", nil); b["available_orders"] != float64(1) || b["active_order"] != nil {
		t.Fatalf("idle driver: %v", b)
	}

	// Changes requested: the latest round's corrections are listed.
	c := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "changes_requested"})
	_, err := e.pool.Exec(ctx, `
		INSERT INTO driver_applications (driver_id, round, status, snapshot, corrections) VALUES
		($1, 1, 'changes_requested', '{}', '[{"field":"avatar","reason":"other","comment":"ескі"}]'),
		($1, 2, 'changes_requested', '{}', '[{"field":"vehicle_plate","reason":"invalid_plate","comment":"Нөмір анық емес"}]')`, c.DriverID)
	if err != nil {
		t.Fatal(err)
	}
	_, b = e.call(t, "driver", http.MethodGet, "/driver/"+itoa(c.TelegramID)+"/summary", nil)
	corr := b["corrections"].([]any)
	if len(corr) != 1 || corr[0].(map[string]any)["field"] != "vehicle_plate" || corr[0].(map[string]any)["comment"] != "Нөмір анық емес" {
		t.Fatalf("corrections: %v", corr)
	}
}

func TestDriverOrdersEligibility(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	for range 3 { // one active order per passenger; commission 80 each
		pid, _ := testutil.CreateUser(t, e.pool)
		insertOrder(t, e.pool, pid, nil, "searching", 800, time.Now())
	}
	list := func(tg int64, query string) (int, map[string]any) {
		return e.call(t, "driver", http.MethodGet, "/driver/"+itoa(tg)+"/orders"+query, nil)
	}
	reason := func(b map[string]any) any { return b["eligibility"].(map[string]any)["reason"] }

	pending := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "pending"})
	if st, b := list(pending.TelegramID, ""); st != 200 || reason(b) != "not_approved" || len(b["items"].([]any)) != 0 {
		t.Fatalf("pending: %d %v", st, b)
	}
	offline := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Balance: 1000})
	if _, b := list(offline.TelegramID, ""); reason(b) != "offline" {
		t.Fatalf("offline: %v", b)
	}
	poor := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Online: true, Balance: 50})
	if _, b := list(poor.TelegramID, ""); reason(b) != "insufficient_balance" || len(b["items"].([]any)) != 0 {
		t.Fatalf("low balance: %v", b)
	}
	ok := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Online: true, Balance: 1000})
	st, b := list(ok.TelegramID, "?limit=2")
	if st != 200 || b["eligibility"].(map[string]any)["eligible"] != true || reason(b) != nil || len(b["items"].([]any)) != 2 {
		t.Fatalf("eligible: %d %v", st, b)
	}
	card := b["items"].([]any)[0].(map[string]any)
	if card["fare"] != float64(800) || card["commission_amount"] != float64(80) || len(card["stops"].([]any)) != 2 {
		t.Fatalf("order card: %v", card)
	}
	if _, has := card["recipient_phone"]; has {
		t.Fatalf("cards must not carry contact data: %v", card)
	}
	if st, b := list(ok.TelegramID, ""); st != 200 || len(b["items"].([]any)) != 3 {
		t.Fatalf("default limit: %d %v", st, b)
	}
	if st, _ := list(ok.TelegramID, "?limit=21"); st != http.StatusUnprocessableEntity {
		t.Fatalf("limit=21: %d", st)
	}

	// Orders are listed by distance from the driver's fresh location to the
	// pickup (dispatch.location_max_age_s defaults to 300 s).
	if _, err := e.pool.Exec(ctx, `DELETE FROM driver_locations WHERE driver_id = $1`, ok.DriverID); err != nil {
		t.Fatal(err)
	}
	if _, b := list(ok.TelegramID, ""); reason(b) != "location_required" || len(b["items"].([]any)) != 0 {
		t.Fatalf("location required: %v", b)
	}
	testutil.SetDriverLocation(t, e.pool, ok.DriverID, testutil.LatLon{Lat: 48.8270, Lon: 58.1450}, 10*time.Minute)
	if _, b := list(ok.TelegramID, ""); reason(b) != "location_stale" || len(b["items"].([]any)) != 0 {
		t.Fatalf("stale location: %v", b)
	}
	// 19 km north of the pickups: eligible, but no order is within reach.
	testutil.SetDriverLocation(t, e.pool, ok.DriverID, testutil.LatLon{Lat: 49.0000, Lon: 58.1442}, 0)
	if _, b := list(ok.TelegramID, ""); b["eligibility"].(map[string]any)["eligible"] != true || len(b["items"].([]any)) != 0 {
		t.Fatalf("far away: %v", b)
	}
	if _, b := e.call(t, "driver", http.MethodGet, "/driver/"+itoa(ok.TelegramID)+"/summary", nil); b["available_orders"] != float64(0) {
		t.Fatalf("far away summary: %v", b["available_orders"])
	}
	testutil.SetDriverLocation(t, e.pool, ok.DriverID, testutil.LatLon{Lat: 48.8270, Lon: 58.1450}, 0)
	_, b = list(ok.TelegramID, "")
	if b["eligibility"].(map[string]any)["eligible"] != true || len(b["items"].([]any)) != 3 {
		t.Fatalf("fresh location: %v", b)
	}
	card = b["items"].([]any)[0].(map[string]any)
	if d, _ := card["distance_to_pickup_m"].(float64); d < 50 || d > 100 {
		t.Fatalf("distance to pickup: %v", card["distance_to_pickup_m"])
	}

	_, plain := testutil.CreateUser(t, e.pool)
	if st, b := list(plain, ""); st != http.StatusForbidden || errCode(b) != "driver_not_registered" {
		t.Fatalf("no profile: %d %v", st, b)
	}
	if st, _ := list(999999999999, ""); st != http.StatusNotFound {
		t.Fatalf("unknown user: %d", st)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE users SET is_blocked = true WHERE id = $1`, ok.UserID); err != nil {
		t.Fatal(err)
	}
	if st, b := list(ok.TelegramID, ""); st != http.StatusForbidden || errDetail(b, "reason") != "user_blocked" {
		t.Fatalf("blocked user: %d %v", st, b)
	}
}

func TestDriverAvailability(t *testing.T) {
	e := newTestEnv(t)
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Balance: 1000})
	set := func(tg int64, body any) (int, map[string]any) {
		return e.call(t, "driver", http.MethodPost, "/driver/"+itoa(tg)+"/availability", body)
	}
	// Going online needs a fresh location (live location in the bot or the
	// Mini App's GPS), because dispatch is by distance to the pickup.
	st, b := set(d.TelegramID, map[string]any{"online": true})
	if st != http.StatusUnprocessableEntity || errCode(b) != "location_required" || errDetail(b, "max_age_s") != float64(300) {
		t.Fatalf("online without location: %d %v", st, b)
	}
	testutil.SetDriverLocation(t, e.pool, d.DriverID, testutil.LatLon{Lat: 48.8264, Lon: 58.1442}, 10*time.Minute)
	if st, b := set(d.TelegramID, map[string]any{"online": true}); st != http.StatusUnprocessableEntity || errCode(b) != "location_required" {
		t.Fatalf("online with a stale location: %d %v", st, b)
	}
	if st, b := e.call(t, "driver", http.MethodPost, "/driver/"+itoa(d.TelegramID)+"/location",
		map[string]any{"lat": 48.8270, "lon": 58.1450, "live": true}); st != http.StatusNoContent {
		t.Fatalf("live location: %d %v", st, b)
	}
	st, b = set(d.TelegramID, map[string]any{"online": true})
	if st != 200 || b["is_online"] != true || b["online_since"] == nil || b["can_go_online"] != true || b["blocked_reason"] != nil ||
		b["available"] != float64(1000) || b["min_online_balance"] != float64(50) || b["currency"] != "KZT" {
		t.Fatalf("go online: %d %v", st, b)
	}
	var online bool
	if err := e.pool.QueryRow(context.Background(), `SELECT is_online FROM driver_profiles WHERE id = $1`, d.DriverID).Scan(&online); err != nil || !online {
		t.Fatalf("online not stored: %v %v", online, err)
	}
	if st, b := set(d.TelegramID, map[string]any{"online": false}); st != 200 || b["is_online"] != false || b["online_since"] != nil {
		t.Fatalf("go offline: %d %v", st, b)
	}

	// The drivers package rules and errors reach the bot unchanged.
	poor := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Balance: 10})
	if st, b := set(poor.TelegramID, map[string]any{"online": true}); st != http.StatusPaymentRequired || errCode(b) != "insufficient_balance" ||
		errDetail(b, "required") != float64(50) || errDetail(b, "available") != float64(10) {
		t.Fatalf("insufficient balance: %d %v", st, b)
	}
	pending := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "pending", Balance: 1000})
	if st, b := set(pending.TelegramID, map[string]any{"online": true}); st != http.StatusForbidden || errCode(b) != "driver_not_approved" {
		t.Fatalf("pending: %d %v", st, b)
	}
	if st, _ := set(d.TelegramID, map[string]any{"online": "yes"}); st != http.StatusBadRequest {
		t.Fatalf("bad body: %d", st)
	}
	if st, b := set(d.TelegramID, map[string]any{}); st != http.StatusUnprocessableEntity || errCode(b) != "validation_failed" {
		t.Fatalf("missing online: %d %v", st, b)
	}
}

func TestDriverContact(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	uid, tg := testutil.CreateUser(t, e.pool)
	post := func(tgID int64, body map[string]any) (int, map[string]any) {
		return e.call(t, "driver", http.MethodPost, "/driver/"+itoa(tgID)+"/contact", body)
	}
	storedPhone := func() *string {
		var p *string
		if err := e.pool.QueryRow(ctx, `SELECT phone_e164 FROM users WHERE id = $1`, uid).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Someone else's contact is refused before anything is stored.
	if st, b := post(tg, map[string]any{"phone": "77071234567", "contact_user_id": tg + 1}); st != http.StatusForbidden ||
		errCode(b) != "forbidden" || errDetail(b, "reason") != "contact_mismatch" {
		t.Fatalf("contact mismatch: %d %v", st, b)
	}
	if p := storedPhone(); p != nil {
		t.Fatalf("phone stored for a mismatched contact: %v", *p)
	}
	// Not a Kazakhstan mobile.
	if st, b := post(tg, map[string]any{"phone": "+79161234567", "contact_user_id": tg}); st != http.StatusUnprocessableEntity || errCode(b) != "validation_failed" {
		t.Fatalf("russian number: %d %v", st, b)
	}

	st, b := post(tg, map[string]any{"phone": "77071234567", "contact_user_id": tg})
	if st != 200 || b["phone"] != "+77071234567" || b["phone_verified"] != true || b["driver_id"] != nil {
		t.Fatalf("verify: %d %v", st, b)
	}
	if p := storedPhone(); p == nil || *p != "+77071234567" {
		t.Fatalf("stored phone: %v", p)
	}
	var tpl, chat string
	if err := e.pool.QueryRow(ctx, `SELECT template, chat_id::text FROM notification_outbox WHERE user_id = $1`, uid).Scan(&tpl, &chat); err != nil {
		t.Fatalf("notification: %v", err)
	}
	if tpl != "phone_verified" || chat != itoa(tg) {
		t.Fatalf("notification: %s to %s", tpl, chat)
	}
	// Re-sharing the same number is idempotent (one notification).
	if st, _ := post(tg, map[string]any{"phone": "+7 707 123 45 67", "contact_user_id": tg}); st != 200 {
		t.Fatalf("repeat: %d", st)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE user_id = $1`, uid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("notifications: %d %v", n, err)
	}

	// A draft driver gets the number on the profile too.
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "draft"})
	if st, b := post(d.TelegramID, map[string]any{"phone": "87011112233", "contact_user_id": d.TelegramID}); st != 200 || b["driver_id"] != d.DriverID.String() {
		t.Fatalf("driver contact: %d %v", st, b)
	}
	var profilePhone string
	if err := e.pool.QueryRow(ctx, `SELECT phone_e164 FROM driver_profiles WHERE id = $1`, d.DriverID).Scan(&profilePhone); err != nil || profilePhone != "+77011112233" {
		t.Fatalf("profile phone: %q %v", profilePhone, err)
	}
	// A number held by another live driver is 409.
	other := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved"})
	if st, b := post(d.TelegramID, map[string]any{"phone": other.Phone, "contact_user_id": d.TelegramID}); st != http.StatusConflict || errCode(b) != "phone_taken" {
		t.Fatalf("phone taken: %d %v", st, b)
	}
	if st, _ := post(999999999999, map[string]any{"phone": "77071234567", "contact_user_id": 999999999999}); st != http.StatusNotFound {
		t.Fatalf("unknown user: %d", st)
	}
}

func TestDriverLocation(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "approved", Online: true})
	post := func(tg int64, body map[string]any) (int, map[string]any) {
		return e.call(t, "driver", http.MethodPost, "/driver/"+itoa(tg)+"/location", body)
	}
	read := func() (source string, accuracy, heading *float64) {
		t.Helper()
		if err := e.pool.QueryRow(ctx, `SELECT source, accuracy_m, heading FROM driver_locations WHERE driver_id = $1`, d.DriverID).
			Scan(&source, &accuracy, &heading); err != nil {
			t.Fatal(err)
		}
		return
	}

	// Validation happens before rate limiting.
	for _, bad := range []map[string]any{
		{"lat": 91.0, "lon": 58.0, "live": true},
		{"lat": 48.0, "lon": 181.0, "live": true},
		{"lat": 48.0, "lon": 58.0, "accuracy_m": -1, "live": true},
		{"lat": 48.0, "lon": 58.0, "heading": 400, "live": true},
		{"lat": 48.0, "lon": 58.0},
		{"lon": 58.0, "live": true},
	} {
		if st, _ := post(d.TelegramID, bad); st != http.StatusUnprocessableEntity {
			t.Fatalf("%v: %d", bad, st)
		}
	}

	if st, b := post(d.TelegramID, map[string]any{"lat": 48.83, "lon": 58.15, "accuracy_m": 12.5, "heading": 90, "live": true}); st != http.StatusNoContent {
		t.Fatalf("live location: %d %v", st, b)
	}
	if src, acc, head := read(); src != "bot_live" || acc == nil || *acc != 12.5 || head == nil || *head != 90 {
		t.Fatalf("live location stored as %s %v %v", src, acc, head)
	}
	if st, _ := post(d.TelegramID, map[string]any{"lat": 48.831, "lon": 58.151, "live": false}); st != http.StatusNoContent {
		t.Fatalf("static location: %d", st)
	}
	if src, _, _ := read(); src != "bot_static" {
		t.Fatalf("static source: %s", src)
	}
	// Outside Kazakhstan.
	if st, b := post(d.TelegramID, map[string]any{"lat": 55.75, "lon": 37.61, "live": true}); st != http.StatusUnprocessableEntity || errCode(b) != "outside_service_area" {
		t.Fatalf("outside: %d %v", st, b)
	}
	// Bursts are rate-limited like POST /driver/location.
	if st, b := post(d.TelegramID, map[string]any{"lat": 48.832, "lon": 58.152, "live": true}); st != http.StatusTooManyRequests || errCode(b) != "too_many_requests" {
		t.Fatalf("burst: %d %v", st, b)
	}

	pending := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{Status: "pending"})
	if st, b := post(pending.TelegramID, map[string]any{"lat": 48.83, "lon": 58.15, "live": true}); st != http.StatusForbidden || errCode(b) != "driver_not_approved" {
		t.Fatalf("pending driver: %d %v", st, b)
	}
}
