package driverbot

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/bottest"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

const (
	userID   = int64(700300400)
	orderID  = "0a6f4c4e-6f1b-4b8e-9a52-3c1d2e7f9b10"
	appURL   = "https://app.example.kz/driver/"
	syncPath = "POST /bots/driver/users/sync"
	tgPath   = "/driver/700300400"
)

type env struct {
	tg     *bottest.FakeTelegram
	api    *bottest.FakeAPI
	bot    *Bot
	user   map[string]any // sync response
	driver map[string]any // sync response "driver"
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{tg: bottest.NewFakeTelegram(t), api: bottest.NewFakeAPI(t)}
	e.driver = map[string]any{"registered": false, "driver_id": nil, "code": nil, "status": nil, "is_online": false, "phone_verified": false}
	e.user = map[string]any{"user_id": "u1", "language": "kk", "language_chosen": true, "is_new": false, "is_blocked": false, "driver": e.driver}
	e.api.Handle(syncPath, func(map[string]any, string) (int, any) { return 200, e.user })
	e.api.Handle("GET /config", func(map[string]any, string) (int, any) {
		return 200, map[string]any{
			"passenger_miniapp_url": "https://app.example.kz/passenger/", "driver_miniapp_url": appURL,
			"passenger_bot_username": "jantaxi_bot", "driver_bot_username": "jantaxi_driver_bot", "default_language": "kk",
			"support": map[string]any{"telegram_username": "jantaxi_support", "phone": nil,
				"hours": map[string]any{"kk": "Күн сайын 08:00–23:00", "ru": "Ежедневно 08:00–23:00", "en": "Daily 08:00–23:00"}},
		}
	})
	bot, err := New(Config{MiniAppURL: appURL}, e.tg.Client(), e.api.Client(botapi.BotDriver), nil)
	if err != nil {
		t.Fatal(err)
	}
	e.bot = bot
	return e
}

// approved makes the synced user an approved driver.
func (e *env) approved(online bool) {
	e.driver["registered"], e.driver["driver_id"], e.driver["code"] = true, "d1", "EMB007"
	e.driver["status"], e.driver["is_online"], e.driver["phone_verified"] = "approved", online, true
}

func (e *env) update(t *testing.T, u telegram.Update) []map[string]any {
	t.Helper()
	e.tg.Reset()
	if err := e.bot.HandleUpdate(context.Background(), u); err != nil {
		t.Logf("handler error (logged by the runtime): %v", err)
	}
	return e.tg.Messages()
}

func (e *env) message(t *testing.T, m telegram.Message) []map[string]any {
	t.Helper()
	m.MessageID = 1
	if m.From == nil {
		m.From = &telegram.User{ID: userID, FirstName: "Ерлан"}
	}
	m.Chat = telegram.Chat{ID: userID, Type: "private"}
	return e.update(t, telegram.Update{UpdateID: 1, Message: &m})
}

func (e *env) text(t *testing.T, text string) []map[string]any {
	return e.message(t, telegram.Message{Text: text})
}

// msgText returns a message text with no-break spaces (money, distances) as
// plain spaces.
func msgText(m map[string]any) string { return strings.ReplaceAll(bottest.Text(m), "\u00a0", " ") }

func noWebAppInReplyKeyboard(t *testing.T, msgs []map[string]any) {
	t.Helper()
	for _, m := range msgs {
		if labels, _ := bottest.ReplyButtons(m); slices.Contains(labels, "!web_app!") {
			t.Fatalf("reply keyboard with a web_app button: %v", labels)
		}
	}
}

func TestCatalogIsComplete(t *testing.T) {
	if err := texts.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestStartForNewDriver(t *testing.T) {
	e := newEnv(t)
	e.user["language_chosen"] = false
	msgs := e.text(t, "/start register")
	if calls := e.api.Calls(); calls[0].Method != syncPath || calls[0].Body["started"] != true {
		t.Fatalf("sync: %+v", calls)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want welcome, registration and language picker", len(msgs))
	}
	noWebAppInReplyKeyboard(t, msgs)
	labels, contact := bottest.ReplyButtons(msgs[0])
	want := []string{"📱 Нөмірді растау", "🚗 Жұмысқа шығу", "📋 Тапсырыстар", "🗺 Менің сапарым", "💰 Баланс", "➕ Баланс толтыру", "👤 Менің профилім", "🌐 Тіл", "💬 Қолдау"}
	if !slices.Equal(labels, want) || !contact[0] || slices.Contains(contact[1:], true) {
		t.Fatalf("keyboard %v %v, want %v with the contact button first", labels, contact, want)
	}
	if !strings.Contains(msgText(msgs[1]), "Нөмірді растау") || !strings.Contains(msgText(msgs[1]), "Jan Taxi-де жүргізуші болу") {
		t.Fatalf("registration text: %s", msgText(msgs[1]))
	}
	if b := bottest.InlineButtons(msgs[1]); len(b) != 1 || bottest.WebAppURL(b[0]) != appURL+"?startapp=register" || b[0]["text"] != "Тіркелу" {
		t.Fatalf("registration button: %v", b)
	}
	if b := bottest.InlineButtons(msgs[2]); len(b) != 3 || b[0]["callback_data"] != "lang:kk" {
		t.Fatalf("language picker: %v", b)
	}

	// With a verified phone the contact button disappears and the steps change.
	e.driver["phone_verified"] = true
	e.user["language_chosen"] = true
	msgs = e.text(t, "/start")
	if labels, _ := bottest.ReplyButtons(msgs[0]); labels[0] != "🚗 Жұмысқа шығу" || len(labels) != 8 {
		t.Fatalf("verified keyboard: %v", labels)
	}
	if !strings.Contains(msgText(msgs[1]), "Телефон нөміріңіз расталған") || len(msgs) != 2 {
		t.Fatalf("verified registration: %v", msgs)
	}

	// Work buttons explain the registration instead of calling the API.
	for _, label := range []string{"📋 Тапсырыстар", "🚗 Жұмысқа шығу", "🗺 Менің сапарым", "💰 Баланс"} {
		msgs = e.text(t, label)
		if len(msgs) != 1 || !strings.Contains(msgText(msgs[0]), "Jan Taxi-де жүргізуші болу") {
			t.Fatalf("%s for a new driver: %v", label, msgs)
		}
	}
	for _, c := range e.api.Calls() {
		if c.Method != syncPath {
			t.Fatalf("unexpected API call for an unregistered driver: %s", c.Method)
		}
	}
}

func TestStartForChangesRequested(t *testing.T) {
	e := newEnv(t)
	e.driver["registered"], e.driver["status"], e.driver["phone_verified"] = true, "changes_requested", true
	e.api.Handle("GET "+tgPath+"/summary", func(map[string]any, string) (int, any) {
		return 200, map[string]any{"registered": true, "status": "changes_requested", "corrections": []any{
			map[string]any{"field": "vehicle_plate", "reason": "invalid_plate", "comment": "Нөмір <анық> емес"},
		}}
	})
	msgs := e.text(t, "/start")
	text := msgText(msgs[1])
	for _, want := range []string{"Өтінімді түзету керек", "• Мемлекеттік нөмір — нөмір дұрыс емес", "«Нөмір &lt;анық&gt; емес»"} {
		if !strings.Contains(text, want) {
			t.Fatalf("corrections text %q lacks %q", text, want)
		}
	}
	if b := bottest.InlineButtons(msgs[1]); bottest.WebAppURL(b[0]) != appURL+"?startapp=register" || b[0]["text"] != "Өтінімді түзету" {
		t.Fatalf("fix button: %v", b)
	}
}

func TestStartForApprovedDriver(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	msgs := e.text(t, "/start order_"+orderID)
	if !strings.Contains(msgText(msgs[0]), "EMB007") || !strings.Contains(msgText(msgs[0]), "Сіз жұмыстасыз") {
		t.Fatalf("welcome: %s", msgText(msgs[0]))
	}
	if labels, contact := bottest.ReplyButtons(msgs[0]); labels[0] != "🔴 Жұмысты аяқтау" || slices.Contains(contact, true) {
		t.Fatalf("online keyboard: %v", labels)
	}
	if b := bottest.InlineButtons(msgs[1]); bottest.WebAppURL(b[0]) != appURL+"?startapp=order_"+orderID {
		t.Fatalf("deep link: %v", b)
	}
	if msgs := e.text(t, "/start ../../etc"); bottest.WebAppURL(bottest.InlineButtons(msgs[1])[0]) != appURL+"?startapp=home" {
		t.Fatal("unknown payload must open home")
	}
}

func TestButtonsWorkInEveryLanguage(t *testing.T) {
	e := newEnv(t)
	e.approved(false)
	e.api.Handle("POST "+tgPath+"/availability", func(body map[string]any, _ string) (int, any) {
		return 200, map[string]any{"is_online": body["online"], "can_go_online": true, "available": 1000, "min_online_balance": 50, "currency": "KZT"}
	})
	e.api.Handle("GET "+tgPath+"/orders", func(map[string]any, string) (int, any) {
		return 200, map[string]any{"items": []any{}, "eligibility": map[string]any{"eligible": true, "reason": nil}}
	})
	e.api.Handle("GET "+tgPath+"/summary", func(map[string]any, string) (int, any) {
		return 200, map[string]any{"registered": true, "status": "approved", "code": "EMB007", "phone_verified": true,
			"wallet":      map[string]any{"balance": 1000, "reserved": 80, "available": 920, "currency": "KZT", "min_online_balance": 50},
			"corrections": []any{}}
	})
	cases := map[string]string{
		"online": "POST " + tgPath + "/availability", "offline": "POST " + tgPath + "/availability",
		"orders": "GET " + tgPath + "/orders", "trip": "GET " + tgPath + "/summary", "balance": "GET " + tgPath + "/summary",
		"profile": "GET " + tgPath + "/summary",
	}
	for _, lang := range runtime.Languages {
		for action, route := range cases {
			before := e.api.Count(route)
			if msgs := e.text(t, texts.T(lang, "kb_"+action)); len(msgs) == 0 || e.api.Count(route) != before+1 {
				t.Errorf("%s %s button: route %s not called (%v)", lang, action, route, msgs)
			}
		}
		if msgs := e.text(t, texts.T(lang, "kb_topup")); bottest.WebAppURL(bottest.InlineButtons(msgs[0])[0]) != appURL+"?startapp=topup" {
			t.Errorf("%s top-up button: %v", lang, msgs)
		}
		if msgs := e.text(t, texts.T(lang, "kb_language")); len(bottest.InlineButtons(msgs[0])) != 3 {
			t.Errorf("%s language button: %v", lang, msgs)
		}
		if msgs := e.text(t, texts.T(lang, "kb_support")); !strings.Contains(msgText(msgs[0]), "@jantaxi_support") {
			t.Errorf("%s support button: %v", lang, msgs)
		}
	}
	// Commands reach the same actions; anything else gets help with the keyboard.
	if before := e.api.Count("GET " + tgPath + "/orders"); len(e.text(t, "/orders@jantaxi_driver_bot")) == 0 || e.api.Count("GET "+tgPath+"/orders") != before+1 {
		t.Fatal("/orders")
	}
	msgs := e.text(t, "сәлем")
	if labels, _ := bottest.ReplyButtons(msgs[0]); len(labels) != 8 || msgText(msgs[0]) != texts.T("kk", "help") {
		t.Fatalf("help: %v", msgs)
	}
}

func TestGoOnline(t *testing.T) {
	e := newEnv(t)
	e.approved(false)
	var status int
	var body any
	e.api.Handle("POST "+tgPath+"/availability", func(map[string]any, string) (int, any) { return status, body })

	status, body = 200, map[string]any{"is_online": true, "can_go_online": true, "available": 1000, "min_online_balance": 50, "currency": "KZT"}
	msgs := e.text(t, "🚗 Жұмысқа шығу")
	if len(msgs) != 2 || !strings.Contains(msgText(msgs[0]), "Сіз жұмыстасыз") || !strings.Contains(msgText(msgs[1]), "тірі геолокация") {
		t.Fatalf("online: %v", msgs)
	}
	if labels, _ := bottest.ReplyButtons(msgs[0]); labels[0] != "🔴 Жұмысты аяқтау" {
		t.Fatalf("keyboard after going online: %v", labels)
	}
	if b := bottest.InlineButtons(msgs[1]); bottest.WebAppURL(b[0]) != appURL+"?startapp=orders" {
		t.Fatalf("orders button: %v", b)
	}

	status, body = http.StatusPaymentRequired, bottest.APIError("insufficient_balance", map[string]any{"required": 50, "available": 20, "currency": "KZT"})
	msgs = e.text(t, "/online")
	if text := msgText(msgs[0]); !strings.Contains(text, "50 ₸") || !strings.Contains(text, "20 ₸") || !strings.Contains(text, "Балансыңыз жеткіліксіз") {
		t.Fatalf("insufficient balance: %s", text)
	}
	if b := bottest.InlineButtons(msgs[0]); bottest.WebAppURL(b[0]) != appURL+"?startapp=topup" {
		t.Fatalf("top-up button: %v", b)
	}

	// Dispatch is by distance to the pickup: without a fresh location the
	// driver is told how to send GPS (live location here, or the Mini App).
	status, body = http.StatusUnprocessableEntity, bottest.APIError("location_required", map[string]any{"max_age_s": 300})
	msgs = e.text(t, "/online")
	if text := msgText(msgs[0]); !strings.Contains(text, "Геолокация қажет") || !strings.Contains(text, "тірі геолокация") ||
		!strings.Contains(text, "қосымшаны ашып") {
		t.Fatalf("location required: %v", msgs)
	}
	if b := bottest.InlineButtons(msgs[0]); len(b) != 1 || bottest.WebAppURL(b[0]) != appURL+"?startapp=home" {
		t.Fatalf("location button: %v", b)
	}

	status, body = http.StatusForbidden, bottest.APIError("driver_not_approved", map[string]any{"reason": "suspended", "status": "suspended"})
	if msgs := e.text(t, "/online"); !strings.Contains(msgText(msgs[0]), "уақытша тоқтатылған") {
		t.Fatalf("suspended: %v", msgs)
	}

	// A pending driver is told about the review without calling the API.
	e.driver["status"] = "pending"
	before := e.api.Count("POST " + tgPath + "/availability")
	if msgs := e.text(t, "🚗 Жұмысқа шығу"); !strings.Contains(msgText(msgs[0]), "Өтінім тексерілуде") || e.api.Count("POST "+tgPath+"/availability") != before {
		t.Fatalf("pending: %v", msgs)
	}
}

func TestContactMustBeTheSendersOwn(t *testing.T) {
	e := newEnv(t)
	var got map[string]any
	e.api.Handle("POST "+tgPath+"/contact", func(body map[string]any, _ string) (int, any) {
		got = body
		return 200, map[string]any{"phone": "+77071234567", "phone_verified": true, "driver_id": nil}
	})

	for _, owner := range []int64{0, userID + 1} {
		msgs := e.message(t, telegram.Message{Contact: &telegram.Contact{PhoneNumber: "77071234567", FirstName: "Басқа", UserID: owner}})
		if len(msgs) != 1 || msgText(msgs[0]) != texts.T("kk", "phone_own_only") {
			t.Fatalf("contact of user %d: %v", owner, msgs)
		}
	}
	// The sender's own card forwarded from another chat carries a number typed
	// by whoever saved it, so it is refused too.
	fwd := telegram.Message{Contact: &telegram.Contact{PhoneNumber: "77071234567", FirstName: "Ерлан", UserID: userID},
		ForwardOrigin: json.RawMessage(`{"type":"hidden_user","date":1760000000,"sender_user_name":"Ерлан"}`)}
	if msgs := e.message(t, fwd); len(msgs) != 1 || msgText(msgs[0]) != texts.T("kk", "phone_own_only") {
		t.Fatalf("forwarded own contact: %v", msgs)
	}
	if got != nil || e.api.Count("POST "+tgPath+"/contact") != 0 {
		t.Fatalf("a foreign contact reached the API: %v", got)
	}

	msgs := e.message(t, telegram.Message{Contact: &telegram.Contact{PhoneNumber: "77071234567", FirstName: "Ерлан", UserID: userID}})
	if got["phone"] != "77071234567" || got["contact_user_id"] != float64(userID) {
		t.Fatalf("contact request: %v", got)
	}
	if !strings.Contains(msgText(msgs[0]), "+7 707 123 45 67") {
		t.Fatalf("confirmation: %s", msgText(msgs[0]))
	}
	if _, contact := bottest.ReplyButtons(msgs[0]); slices.Contains(contact, true) {
		t.Fatal("the contact button must disappear once the phone is verified")
	}

	e.api.Handle("POST "+tgPath+"/contact", func(map[string]any, string) (int, any) {
		return http.StatusConflict, bottest.APIError("phone_taken", nil)
	})
	if msgs := e.message(t, telegram.Message{Contact: &telegram.Contact{PhoneNumber: "77071234567", UserID: userID}}); msgText(msgs[0]) != texts.T("kk", "phone_taken") {
		t.Fatalf("phone taken: %v", msgs)
	}
	e.api.Handle("POST "+tgPath+"/contact", func(map[string]any, string) (int, any) {
		return http.StatusUnprocessableEntity, bottest.APIError("validation_failed", nil)
	})
	if msgs := e.message(t, telegram.Message{Contact: &telegram.Contact{PhoneNumber: "79161234567", UserID: userID}}); msgText(msgs[0]) != texts.T("kk", "phone_invalid") {
		t.Fatalf("invalid phone: %v", msgs)
	}
	// A typed number is not accepted.
	if msgs := e.text(t, "+7 707 123 45 67"); msgText(msgs[0]) != texts.T("kk", "phone_use_button") {
		t.Fatalf("typed phone: %v", msgs)
	}
}

func TestLocations(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	var reports []map[string]any
	var status int
	var body any
	e.api.Handle("POST "+tgPath+"/location", func(b map[string]any, _ string) (int, any) {
		reports = append(reports, b)
		return status, body
	})
	status, body = http.StatusNoContent, nil

	// Start of a live location.
	msgs := e.message(t, telegram.Message{Location: &telegram.Location{Latitude: 48.83, Longitude: 58.15, HorizontalAccuracy: 15, LivePeriod: 3600, Heading: 90}})
	if len(reports) != 1 || reports[0]["live"] != true || reports[0]["accuracy_m"] != float64(15) || reports[0]["heading"] != float64(90) {
		t.Fatalf("live start: %v", reports)
	}
	if msgText(msgs[0]) != texts.T("kk", "location_live_on") {
		t.Fatalf("live start reply: %v", msgs)
	}
	edit := func(lat float64) []map[string]any {
		return e.update(t, telegram.Update{UpdateID: 2, EditedMessage: &telegram.Message{
			MessageID: 1, From: &telegram.User{ID: userID}, Chat: telegram.Chat{ID: userID, Type: "private"},
			Location: &telegram.Location{Latitude: lat, Longitude: 58.15, LivePeriod: 3600}, EditDate: 1,
		}})
	}
	syncs := e.api.Count(syncPath)
	if msgs := edit(48.831); len(msgs) != 0 || len(reports) != 2 || reports[1]["live"] != true {
		t.Fatalf("live update must be silent: %v %v", msgs, reports)
	}
	if e.api.Count(syncPath) != syncs {
		t.Fatal("successful live updates must not sync the user")
	}

	// Outside Kazakhstan: explained once, then quiet for a while.
	status, body = http.StatusUnprocessableEntity, bottest.APIError("outside_service_area", map[string]any{"field": "location"})
	if msgs := edit(55.75); len(msgs) != 1 || msgText(msgs[0]) != texts.T("kk", "location_outside") {
		t.Fatalf("outside: %v", msgs)
	}
	for range 3 {
		if msgs := edit(55.76); len(msgs) != 0 {
			t.Fatalf("outside explained again: %v", msgs)
		}
	}
	// Rate limiting is silent.
	e.bot.liveErrors.Reset(userID)
	status, body = http.StatusTooManyRequests, bottest.APIError("too_many_requests", nil)
	if msgs := edit(48.832); len(msgs) != 0 {
		t.Fatalf("429: %v", msgs)
	}

	// A one-off pin.
	status, body = http.StatusNoContent, nil
	if msgs := e.message(t, telegram.Message{Location: &telegram.Location{Latitude: 48.83, Longitude: 58.15}}); msgText(msgs[0]) != texts.T("kk", "location_saved") ||
		reports[len(reports)-1]["live"] != false {
		t.Fatalf("static pin: %v %v", msgs, reports[len(reports)-1])
	}

	// A forwarded pin or a place card is not the driver's position: refused
	// without calling the API, also as a live-location edit.
	n := len(reports)
	forwarded := telegram.Message{Location: &telegram.Location{Latitude: 48.83, Longitude: 58.15},
		ForwardOrigin: json.RawMessage(`{"type":"user","date":1760000000,"sender_user":{"id":1,"is_bot":false,"first_name":"A"}}`)}
	venue := telegram.Message{Location: &telegram.Location{Latitude: 48.83, Longitude: 58.15},
		Venue: json.RawMessage(`{"location":{"latitude":48.83,"longitude":58.15},"title":"Базар","address":"Ембі"}`)}
	for name, m := range map[string]telegram.Message{"forwarded": forwarded, "venue": venue} {
		if msgs := e.message(t, m); len(msgs) != 1 || msgText(msgs[0]) != texts.T("kk", "location_not_own") || len(reports) != n {
			t.Fatalf("%s pin: %v (reports %d → %d)", name, msgs, n, len(reports))
		}
	}
	editedVenue := venue
	editedVenue.MessageID, editedVenue.EditDate = 1, 2
	editedVenue.From, editedVenue.Chat = &telegram.User{ID: userID}, telegram.Chat{ID: userID, Type: "private"}
	if msgs := e.update(t, telegram.Update{UpdateID: 3, EditedMessage: &editedVenue}); len(msgs) != 0 || len(reports) != n {
		t.Fatalf("edited venue: %v", msgs)
	}

	// Drivers who are not approved are answered without calling the API.
	e.driver["status"] = "pending"
	if msgs := e.message(t, telegram.Message{Location: &telegram.Location{Latitude: 48.83, Longitude: 58.15}}); msgText(msgs[0]) != texts.T("kk", "location_not_approved") || len(reports) != n {
		t.Fatalf("pending driver: %v", msgs)
	}
}

func TestOrdersList(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	var res any
	e.api.Handle("GET "+tgPath+"/orders", func(_ map[string]any, q string) (int, any) {
		if q != "limit=5" {
			t.Errorf("query %q", q)
		}
		return 200, res
	})
	res = map[string]any{"eligibility": map[string]any{"eligible": true, "reason": nil}, "items": []any{
		map[string]any{"id": orderID, "number": 1042, "category": "parcel", "fare": 1500, "currency": "KZT", "commission_amount": 150,
			"distance_m": 1800, "distance_to_pickup_m": 650, "stop_count": 3, "cargo_description": "Құжаттар <конверт>",
			"stops": []any{
				map[string]any{"seq": 0, "kind": "pickup", "lat": 48.8264, "lon": 58.1442, "address": "Ембі & вокзал"},
				map[string]any{"seq": 1, "kind": "intermediate", "lat": 48.828, "lon": 58.146, "address": "Базар"},
				map[string]any{"seq": 2, "kind": "destination", "lat": 48.8301, "lon": 58.151, "address": ""},
			}},
	}}
	msgs := e.text(t, "📋 Тапсырыстар")
	text := msgText(msgs[0])
	for _, want := range []string{"<b>№1042</b> · Сәлемдеме · <b>1 500 ₸</b>", "Қайдан: Ембі &amp; вокзал", "Қайда: Картадағы нүкте",
		"Аралық аялдамалар: 1", "Алу нүктесіне дейін: 650 м", "Маршрут: 1,8 км · Комиссия: 150 ₸", "Жүк: Құжаттар &lt;конверт&gt;"} {
		if !strings.Contains(text, want) {
			t.Fatalf("orders text %q lacks %q", text, want)
		}
	}
	if strings.Contains(text, "recipient") {
		t.Fatal("contact data in an order card")
	}
	b := bottest.InlineButtons(msgs[0])
	if len(b) != 2 || bottest.WebAppURL(b[0]) != appURL+"?startapp=order_"+orderID || bottest.WebAppURL(b[1]) != appURL+"?startapp=orders" {
		t.Fatalf("order buttons: %v", b)
	}

	for reason, want := range map[string]string{
		"offline":              texts.T("kk", "go_online_first"),
		"has_active_order":     texts.T("kk", "finish_trip_first"),
		"insufficient_balance": texts.T("kk", "orders_need_balance"),
		"location_required":    texts.T("kk", "need_location"),
		"location_stale":       texts.T("kk", "location_stale"),
	} {
		res = map[string]any{"items": []any{}, "eligibility": map[string]any{"eligible": false, "reason": reason}}
		msgs := e.text(t, "/orders")
		if msgText(msgs[0]) != want {
			t.Errorf("%s: %q", reason, msgText(msgs[0]))
		}
		if strings.HasPrefix(reason, "location_") {
			if b := bottest.InlineButtons(msgs[0]); len(b) != 1 || bottest.WebAppURL(b[0]) != appURL+"?startapp=home" {
				t.Errorf("%s button: %v", reason, b)
			}
		}
	}
	res = map[string]any{"items": []any{}, "eligibility": map[string]any{"eligible": true, "reason": nil}}
	if msgs := e.text(t, "/orders"); msgText(msgs[0]) != texts.T("kk", "orders_empty") {
		t.Fatalf("empty: %v", msgs)
	}
}

func TestTripBalanceAndProfile(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	e.user["language"] = "ru"
	summary := map[string]any{"registered": true, "status": "approved", "code": "EMB007", "is_online": true, "phone_verified": true,
		"first_name": "Ерлан", "last_name": "Сейіт", "completed_trips": 12, "rating": 4.86,
		"vehicle":     map[string]any{"make": "Toyota", "model": "Camry", "color": "Ақ", "plate": "123 ABC 04"},
		"wallet":      map[string]any{"balance": 2000, "reserved": 80, "available": 1920, "currency": "KZT", "min_online_balance": 50},
		"corrections": []any{},
		"active_order": map[string]any{"id": orderID, "number": 1042, "status": "driver_arriving", "category": "taxi", "fare": 900,
			"currency": "KZT", "pickup_address": "Ембі <вокзал>", "destination_address": "Базар"},
	}
	e.api.Handle("GET "+tgPath+"/summary", func(map[string]any, string) (int, any) { return 200, summary })

	msgs := e.text(t, "/trip")
	if text := msgText(msgs[0]); !strings.Contains(text, "Текущая поездка №1042") || !strings.Contains(text, "Ожидаете на точке подачи") ||
		!strings.Contains(text, "Ембі &lt;вокзал&gt;") || !strings.Contains(text, "900 ₸") {
		t.Fatalf("trip: %s", text)
	}
	if b := bottest.InlineButtons(msgs[0]); bottest.WebAppURL(b[0]) != appURL+"?startapp=trip" {
		t.Fatalf("trip button: %v", b)
	}

	msgs = e.text(t, "💰 Баланс")
	if text := msgText(msgs[0]); !strings.Contains(text, "Всего: 2 000 ₸") || !strings.Contains(text, "В резерве: 80 ₸") || !strings.Contains(text, "<b>1 920 ₸</b>") || !strings.Contains(text, "50 ₸") {
		t.Fatalf("balance: %s", text)
	}
	if b := bottest.InlineButtons(msgs[0]); len(b) != 2 || bottest.WebAppURL(b[0]) != appURL+"?startapp=topup" || bottest.WebAppURL(b[1]) != appURL+"?startapp=wallet" {
		t.Fatalf("balance buttons: %v", b)
	}

	msgs = e.text(t, "/profile")
	text := msgText(msgs[0])
	for _, want := range []string{"Имя: Ерлан Сейіт", "Код водителя: <b>EMB007</b>", "Статус: Одобрен", "Toyota Camry Ақ · 123 ABC 04", "Рейтинг: 4,86 · Поездок: 12", "Номер телефона подтверждён"} {
		if !strings.Contains(text, want) {
			t.Fatalf("profile %q lacks %q", text, want)
		}
	}

	summary["active_order"] = nil
	if msgs := e.text(t, "/trip"); msgText(msgs[0]) != texts.T("ru", "trip_none") {
		t.Fatalf("no trip: %v", msgs)
	}
}

func TestLanguageChoice(t *testing.T) {
	e := newEnv(t)
	e.approved(false)
	var stored string
	e.api.Handle("PUT /bots/driver/users/700300400/language", func(body map[string]any, _ string) (int, any) {
		stored, _ = body["language"].(string)
		return 200, map[string]any{"language": stored}
	})
	msgs := e.update(t, telegram.Update{UpdateID: 3, CallbackQuery: &telegram.CallbackQuery{
		ID: "cb1", From: telegram.User{ID: userID, FirstName: "Ерлан"}, Data: "lang:en",
		Message: &telegram.Message{MessageID: 5, Chat: telegram.Chat{ID: userID, Type: "private"}},
	}})
	if stored != "en" {
		t.Fatalf("stored language %q", stored)
	}
	var menu bool
	for _, c := range e.tg.Calls() {
		if c.Method == "setChatMenuButton" {
			// No per-chat button: the chat is switched back to the default one.
			mb := c.Body["menu_button"].(map[string]any)
			menu = c.Body["chat_id"] == float64(userID) && mb["type"] == "default" && mb["web_app"] == nil
		}
	}
	if !menu {
		t.Fatalf("chat menu button: %v", e.tg.Calls())
	}
	if labels, _ := bottest.ReplyButtons(msgs[0]); labels[0] != "🚗 Go online" || msgText(msgs[0]) != "Language changed: English." {
		t.Fatalf("keyboard in the new language: %v", msgs)
	}
}

func TestBlockedUsers(t *testing.T) {
	e := newEnv(t)
	// The user blocks and unblocks the bot.
	for status, want := range map[string]bool{"kicked": true, "member": false} {
		before := len(e.api.Calls())
		e.update(t, telegram.Update{MyChatMember: &telegram.ChatMemberUpdated{
			Chat: telegram.Chat{ID: userID, Type: "private"}, From: telegram.User{ID: userID, FirstName: "Ерлан"},
			NewChatMember: telegram.ChatMember{Status: status},
		}})
		if calls := e.api.Calls()[before:]; len(calls) != 1 || calls[0].Body["blocked"] != want {
			t.Fatalf("%s: %+v", status, calls)
		}
	}
	// An admin-blocked user only sees the support contact.
	e.user["is_blocked"] = true
	msgs := e.text(t, "🚗 Жұмысқа шығу")
	if len(msgs) != 1 || !strings.Contains(msgText(msgs[0]), "Аккаунтыңыз шектелген") || e.api.Count("POST "+tgPath+"/availability") != 0 {
		t.Fatalf("blocked: %v", msgs)
	}
}

func TestIgnoresGroupsAndBots(t *testing.T) {
	e := newEnv(t)
	e.update(t, telegram.Update{Message: &telegram.Message{From: &telegram.User{ID: userID}, Chat: telegram.Chat{ID: -100, Type: "group"}, Text: "/start"}})
	e.update(t, telegram.Update{Message: &telegram.Message{From: &telegram.User{ID: 5, IsBot: true}, Chat: telegram.Chat{ID: 5, Type: "private"}, Text: "/start"}})
	if len(e.api.Calls()) != 0 || len(e.tg.Calls()) != 0 {
		t.Fatalf("handled: %v %v", e.api.Calls(), e.tg.Calls())
	}
}

func TestProfileMenu(t *testing.T) {
	e := newEnv(t)
	p := e.bot.Profile(context.Background())
	if len(p.Commands) != 3 || len(p.Commands[""]) != len(menuCommands) || p.Commands[""][1].Command != "online" || p.Commands["ru"][1].Description != "Выйти на линию" {
		t.Fatalf("commands: %v", p.Commands)
	}
	for lang, d := range p.Descriptions {
		if n := len([]rune(d)); n == 0 || n > 512 {
			t.Fatalf("%q description length %d", lang, n)
		}
	}
	for lang, d := range p.ShortDescriptions {
		if n := len([]rune(d)); n == 0 || n > 120 {
			t.Fatalf("%q short description length %d", lang, n)
		}
	}
	if p.MenuButton == nil || p.MenuButton.WebApp.URL != appURL {
		t.Fatalf("menu button: %+v", p.MenuButton)
	}
}

func TestReceiptDocuments(t *testing.T) {
	const topupID = "5b0f8a52-6f7e-4f4c-9d7e-2b1a3c4d5e6f"
	e := newEnv(t)
	e.approved(false)
	pdf := []byte("%PDF-1.7 synthetic receipt")
	e.tg.AddFile("F1", pdf)
	doc := &telegram.Document{FileID: "F1", FileUniqueID: "U1", FileName: "receipt_QR18149336524.pdf", MimeType: "application/pdf", FileSize: int64(len(pdf))}
	route := "POST " + tgPath + "/topup-receipt"
	var answer func() (int, any)
	e.api.Handle(route, func(map[string]any, string) (int, any) { return answer() })

	// Credited at once.
	answer = func() (int, any) {
		return 200, map[string]any{"outcome": "credited", "reasons": []string{}, "balance": 1500,
			"topup": map[string]any{"id": topupID, "reference": "EMB007-TOPUP-000042", "amount": 1000, "verified_amount": 1000, "status": "confirmed"}}
	}
	msgs := e.message(t, telegram.Message{Document: doc})
	calls := e.tg.Calls()
	if calls[0].Method != "getFile" || calls[0].Body["file_id"] != "F1" || calls[1].Method != "download:documents/F1.pdf" {
		t.Fatalf("telegram calls: %+v", calls)
	}
	var up bottest.Call
	for _, c := range e.api.Calls() {
		if c.Method == route {
			up = c
		}
	}
	if !strings.HasPrefix(up.ContentType, "multipart/form-data") || !bytes.Contains(up.Raw, pdf) ||
		!bytes.Contains(up.Raw, []byte(`filename="receipt_QR18149336524.pdf"`)) {
		t.Fatalf("upload: %s %q", up.ContentType, up.Raw)
	}
	if len(msgs) != 1 || !strings.Contains(msgText(msgs[0]), "Баланс толтырылды") || !strings.Contains(msgText(msgs[0]), "+1 000 ₸ · EMB007-TOPUP-000042") ||
		!strings.Contains(msgText(msgs[0]), "Баланс: 1 500 ₸") {
		t.Fatalf("credited reply: %v", msgs)
	}
	if b := bottest.InlineButtons(msgs[0]); len(b) != 1 || bottest.WebAppURL(b[0]) != appURL+"?startapp=wallet" {
		t.Fatalf("credited button: %v", b)
	}

	// Left for review, in the driver's language, with the known reasons only.
	e.user["language"] = "ru"
	answer = func() (int, any) {
		return 200, map[string]any{"outcome": "pending_verification", "reasons": []string{"amount_mismatch", "something_new"}, "balance": nil,
			"topup": map[string]any{"id": topupID, "reference": "EMB007-TOPUP-000042", "amount": 5000, "verified_amount": nil, "status": "pending_verification"}}
	}
	msgs = e.message(t, telegram.Message{Document: doc})
	text := msgText(msgs[0])
	if !strings.Contains(text, "Чек получен") || !strings.Contains(text, "• сумма в чеке не совпадает с суммой заявки") || strings.Contains(text, "something_new") {
		t.Fatalf("pending reply: %s", text)
	}
	if b := bottest.InlineButtons(msgs[0]); len(b) != 1 || bottest.WebAppURL(b[0]) != appURL+"?startapp=topup_"+topupID {
		t.Fatalf("pending button: %v", b)
	}

	// Refusals.
	e.user["language"] = "kk"
	for _, tt := range []struct {
		code, want, startapp string
	}{
		{"no_open_topup", "Ашық толтыру өтінімі жоқ", "topup"},
		{"receipt_already_used", "Бұл чек бұрын пайдаланылған", ""},
		{"pdf_not_allowed", "Kaspi чегі ретінде оқу мүмкін болмады", ""},
	} {
		answer = func() (int, any) { return 409, bottest.APIError(tt.code, nil) }
		msgs = e.message(t, telegram.Message{Document: doc})
		if len(msgs) != 1 || !strings.Contains(msgText(msgs[0]), tt.want) {
			t.Fatalf("%s: %v", tt.code, msgs)
		}
		if b := bottest.InlineButtons(msgs[0]); tt.startapp != "" && (len(b) != 1 || bottest.WebAppURL(b[0]) != appURL+"?startapp="+tt.startapp) {
			t.Fatalf("%s button: %v", tt.code, b)
		}
	}

	// Not a PDF, a photo, or too large: nothing is uploaded.
	before := e.api.Count(route)
	for _, m := range []telegram.Message{
		{Document: &telegram.Document{FileID: "F2", FileName: "receipt.jpg", MimeType: "image/jpeg", FileSize: 1000}},
		{Photo: []telegram.PhotoSize{{FileID: "P1", Width: 800, Height: 1600}}},
	} {
		msgs = e.message(t, m)
		if !strings.Contains(msgText(msgs[0]), "PDF файл ретінде жіберіңіз") {
			t.Fatalf("not a pdf: %v", msgs)
		}
	}
	big := *doc
	big.FileSize = 6 << 20
	if msgs = e.message(t, telegram.Message{Document: &big}); !strings.Contains(msgText(msgs[0]), "Файл тым үлкен") {
		t.Fatalf("too large: %v", msgs)
	}
	if e.api.Count(route) != before {
		t.Fatal("refused files must not reach the API")
	}

	// Not registered yet: the registration is explained instead.
	e.driver["registered"], e.driver["status"] = false, nil
	if msgs = e.message(t, telegram.Message{Document: doc}); !strings.Contains(msgText(msgs[0]), "Jan Taxi-де жүргізуші болу") || e.api.Count(route) != before {
		t.Fatalf("unregistered: %v", msgs)
	}
}

func TestProfileMenuButtonFallsBackToAPIConfig(t *testing.T) {
	e := newEnv(t)
	bot, err := New(Config{}, e.tg.Client(), e.api.Client(botapi.BotDriver), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := bot.Profile(context.Background()); p.MenuButton == nil || p.MenuButton.WebApp.URL != appURL {
		t.Fatalf("menu button without DRIVER_MINIAPP_URL: %+v", p.MenuButton)
	}
}

func TestChatMenuButtonIsResetOncePerChat(t *testing.T) {
	e := newEnv(t)
	e.approved(false)
	count := func() (n int) {
		for _, c := range e.tg.Calls() {
			if c.Method == "setChatMenuButton" {
				n++
			}
		}
		return n
	}
	e.text(t, "/start")
	if count() != 1 {
		t.Fatalf("first update: %v", e.tg.Calls())
	}
	e.text(t, "/balance")
	if count() != 0 {
		t.Fatalf("second update: %v", e.tg.Calls())
	}
	// Live location edits arrive every few seconds and never trigger it.
	e2 := newEnv(t)
	e2.approved(true)
	e2.api.Handle("POST "+tgPath+"/location", func(map[string]any, string) (int, any) { return 204, nil })
	e2.update(t, telegram.Update{UpdateID: 9, EditedMessage: &telegram.Message{
		MessageID: 3, From: &telegram.User{ID: userID}, Chat: telegram.Chat{ID: userID, Type: "private"},
		Location: &telegram.Location{Latitude: 48.83, Longitude: 58.15, LivePeriod: 3600}, EditDate: 1,
	}})
	if len(e2.tg.Calls()) != 0 {
		t.Fatalf("live location update: %v", e2.tg.Calls())
	}
}

func TestTripWithoutAddressesShowsNoCoordinates(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	e.api.Handle("GET "+tgPath+"/summary", func(map[string]any, string) (int, any) {
		return 200, map[string]any{"registered": true, "status": "approved", "is_online": true, "phone_verified": true,
			"active_order": map[string]any{"id": orderID, "number": 7, "status": "driver_arriving", "category": "taxi", "fare": 900,
				"currency": "KZT", "pickup_address": "", "destination_address": "48.83010, 58.15100"}}
	})
	msgs := e.text(t, "/trip")
	text := msgText(msgs[0])
	if strings.Contains(text, "48.83") || !strings.Contains(text, "Қайдан: Картадағы нүкте") || !strings.Contains(text, "Қайда: Картадағы нүкте") {
		t.Fatalf("trip: %s", text)
	}
}
