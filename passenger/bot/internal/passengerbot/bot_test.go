package passengerbot

import (
	"context"
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
	userID   = int64(700100200)
	orderID  = "0a6f4c4e-6f1b-4b8e-9a52-3c1d2e7f9b10"
	appURL   = "https://app.example.kz/passenger/"
	drvBot   = "jantaxi_driver_bot"
	syncPath = "POST /bots/passenger/users/sync"
)

type env struct {
	tg   *bottest.FakeTelegram
	api  *bottest.FakeAPI
	bot  *Bot
	user map[string]any // sync response
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		tg:   bottest.NewFakeTelegram(t),
		api:  bottest.NewFakeAPI(t),
		user: map[string]any{"user_id": "u1", "language": "kk", "language_chosen": false, "is_new": true, "is_blocked": false},
	}
	e.api.Handle(syncPath, func(map[string]any, string) (int, any) { return 200, e.user })
	e.api.Handle("GET /config", func(map[string]any, string) (int, any) {
		return 200, map[string]any{
			"passenger_miniapp_url": appURL, "driver_miniapp_url": "https://app.example.kz/driver/",
			"passenger_bot_username": "jantaxi_bot", "driver_bot_username": drvBot, "default_language": "kk",
			"support": map[string]any{"telegram_username": "jantaxi_support", "phone": "+77011234567",
				"hours": map[string]any{"kk": "Күн сайын 08:00–23:00", "ru": "Ежедневно 08:00–23:00", "en": "Daily 08:00–23:00"}},
		}
	})
	bot, err := New(Config{MiniAppURL: appURL, DriverBotUsername: drvBot}, e.tg.Client(), e.api.Client(botapi.BotPassenger), nil)
	if err != nil {
		t.Fatal(err)
	}
	e.bot = bot
	return e
}

func (e *env) text(t *testing.T, text string) []map[string]any {
	t.Helper()
	e.tg.Reset()
	err := e.bot.HandleUpdate(context.Background(), telegram.Update{UpdateID: 1, Message: &telegram.Message{
		MessageID: 1, From: &telegram.User{ID: userID, FirstName: "Әлия", LanguageCode: "ru"},
		Chat: telegram.Chat{ID: userID, Type: "private"}, Text: text,
	}})
	if err != nil {
		t.Logf("%q: handler error (logged by the runtime): %v", text, err)
	}
	return e.tg.Messages()
}

func TestCatalogIsComplete(t *testing.T) {
	if err := texts.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestStartGreetsWithKeyboardAndMiniApp(t *testing.T) {
	e := newEnv(t)
	msgs := e.text(t, "/start")
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	calls := e.api.Calls()
	if calls[0].Method != syncPath || calls[0].Body["started"] != true || calls[0].Body["telegram_user_id"] != float64(userID) || calls[0].Body["language_code"] != "ru" {
		t.Fatalf("sync: %+v", calls[0])
	}
	if !strings.Contains(bottest.Text(msgs[0]), "Jan Taxi-ге қош келдіңіз") || msgs[0]["parse_mode"] != "HTML" {
		t.Fatalf("welcome must be Kazakh by default: %v", msgs[0])
	}
	labels, _ := bottest.ReplyButtons(msgs[0])
	want := []string{"🚕 Такси шақыру", "📦 Сәлемдеме жіберу", "📋 Менің тапсырыстарым", "🚗 Жүргізуші болу", "💬 Қолдау", "🌐 Тіл"}
	if !slices.Equal(labels, want) {
		t.Fatalf("keyboard %v, want %v", labels, want)
	}
	btns := bottest.InlineButtons(msgs[1])
	if len(btns) != 4 || bottest.WebAppURL(btns[0]) != appURL+"?startapp=home" || btns[1]["callback_data"] != "lang:kk" || btns[2]["callback_data"] != "lang:ru" {
		t.Fatalf("inline buttons: %v", btns)
	}

	// After the user chose a language the picker is not offered again, and the
	// welcome follows the stored language.
	e.user["language"], e.user["language_chosen"] = "ru", true
	msgs = e.text(t, "/start "+"order_"+orderID)
	if !strings.Contains(bottest.Text(msgs[0]), "Добро пожаловать в Jan Taxi") {
		t.Fatalf("russian welcome: %v", bottest.Text(msgs[0]))
	}
	btns = bottest.InlineButtons(msgs[1])
	if len(btns) != 1 || bottest.WebAppURL(btns[0]) != appURL+"?startapp=order_"+orderID {
		t.Fatalf("deep link button: %v", btns)
	}
	if msgs = e.text(t, "/start <script>"); bottest.WebAppURL(bottest.InlineButtons(msgs[1])[0]) != appURL+"?startapp=home" {
		t.Fatal("unknown payload must open home")
	}
}

func TestButtonsWorkInEveryLanguage(t *testing.T) {
	e := newEnv(t)
	e.api.Handle("GET /passenger/700100200/orders", func(map[string]any, string) (int, any) {
		return 200, map[string]any{"items": []any{}}
	})
	cases := map[string]func(msgs []map[string]any) bool{
		"taxi": func(m []map[string]any) bool {
			return bottest.WebAppURL(bottest.InlineButtons(m[0])[0]) == appURL+"?startapp=taxi"
		},
		"parcel": func(m []map[string]any) bool {
			return bottest.WebAppURL(bottest.InlineButtons(m[0])[0]) == appURL+"?startapp=parcel"
		},
		"orders": func(m []map[string]any) bool { return e.api.Count("GET /passenger/700100200/orders") > 0 },
		"driver": func(m []map[string]any) bool {
			b := bottest.InlineButtons(m[0])
			return len(b) == 1 && b[0]["url"] == "https://t.me/"+drvBot+"?start=register"
		},
		"support":  func(m []map[string]any) bool { return strings.Contains(bottest.Text(m[0]), "@jantaxi_support") },
		"language": func(m []map[string]any) bool { return len(bottest.InlineButtons(m[0])) == 3 },
	}
	for _, lang := range runtime.Languages {
		for action, check := range cases {
			label := texts.T(lang, "kb_"+action)
			msgs := e.text(t, label)
			if len(msgs) == 0 || !check(msgs) {
				t.Errorf("%s button %q (user language kk): %v", lang, label, msgs)
			}
		}
	}
	// Slash commands reach the same actions.
	if msgs := e.text(t, "/taxi@jantaxi_bot"); bottest.WebAppURL(bottest.InlineButtons(msgs[0])[0]) != appURL+"?startapp=taxi" {
		t.Fatalf("/taxi: %v", msgs)
	}
	// Anything else gets a short help with the keyboard.
	msgs := e.text(t, "сәлем")
	if labels, _ := bottest.ReplyButtons(msgs[0]); len(labels) != 6 || bottest.Text(msgs[0]) != texts.T("kk", "help") {
		t.Fatalf("help: %v", msgs)
	}
}

func TestOrdersListEscapesAndLinksEachOrder(t *testing.T) {
	e := newEnv(t)
	var gotQuery string
	e.api.Handle("GET /passenger/700100200/orders", func(_ map[string]any, q string) (int, any) {
		gotQuery = q
		return 200, map[string]any{"items": []any{
			map[string]any{"id": orderID, "number": 1042, "status": "in_progress", "category": "taxi", "fare": 1500, "currency": "KZT",
				"pickup_address": "Ембі <вокзал> & базар", "destination_address": "48.83010, 58.15100", "created_at": "2026-10-16T10:00:00Z"},
			map[string]any{"id": "11111111-2222-3333-4444-555555555555", "number": 1041, "status": "cancelled", "category": "parcel", "fare": 800, "currency": "KZT",
				"pickup_address": "A", "destination_address": "B", "created_at": "2026-10-15T10:00:00Z"},
		}}
	})
	msgs := e.text(t, "📋 Менің тапсырыстарым")
	if gotQuery != "limit=5" {
		t.Fatalf("query %q", gotQuery)
	}
	text := bottest.Text(msgs[0])
	for _, want := range []string{"<b>№1042</b> · Жолда · 1 500 ₸", "Ембі &lt;вокзал&gt; &amp; базар → 48.83010, 58.15100", "<b>№1041</b> · Жойылды"} {
		if !strings.Contains(text, want) {
			t.Fatalf("orders text %q lacks %q", text, want)
		}
	}
	btns := bottest.InlineButtons(msgs[0])
	if len(btns) != 3 || bottest.WebAppURL(btns[0]) != appURL+"?startapp=order_"+orderID || btns[0]["text"] != "№1042" ||
		bottest.WebAppURL(btns[2]) != appURL+"?startapp=orders" {
		t.Fatalf("order buttons: %v", btns)
	}

	e.api.Handle("GET /passenger/700100200/orders", func(map[string]any, string) (int, any) {
		return http.StatusServiceUnavailable, bottest.APIError("internal", nil)
	})
	if msgs := e.text(t, "📋 Менің тапсырыстарым"); bottest.Text(msgs[0]) != texts.T("kk", "error") {
		t.Fatalf("API failure must be explained: %v", msgs)
	}
}

func TestLanguageChoice(t *testing.T) {
	e := newEnv(t)
	var stored string
	e.api.Handle("PUT /bots/passenger/users/700100200/language", func(body map[string]any, _ string) (int, any) {
		stored, _ = body["language"].(string)
		return 200, map[string]any{"language": stored}
	})
	err := e.bot.HandleUpdate(context.Background(), telegram.Update{UpdateID: 2, CallbackQuery: &telegram.CallbackQuery{
		ID: "cb1", From: telegram.User{ID: userID, FirstName: "Әлия"}, Data: "lang:ru",
		Message: &telegram.Message{MessageID: 5, Chat: telegram.Chat{ID: userID, Type: "private"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if stored != "ru" {
		t.Fatalf("stored language %q", stored)
	}
	var answered, menu bool
	for _, c := range e.tg.Calls() {
		switch c.Method {
		case "answerCallbackQuery":
			answered = c.Body["callback_query_id"] == "cb1" && c.Body["text"] == "Язык изменён: русский."
		case "setChatMenuButton":
			mb := c.Body["menu_button"].(map[string]any)
			menu = c.Body["chat_id"] == float64(userID) && mb["text"] == "Открыть Jan Taxi" && mb["type"] == "web_app"
		}
	}
	if !answered || !menu {
		t.Fatalf("callback answered=%v menu=%v: %v", answered, menu, e.tg.Calls())
	}
	msgs := e.tg.Messages()
	if labels, _ := bottest.ReplyButtons(msgs[0]); len(labels) != 6 || labels[0] != "🚕 Вызвать такси" {
		t.Fatalf("keyboard in the new language: %v", labels)
	}
	// An unknown callback is acknowledged and ignored.
	e.tg.Reset()
	if err := e.bot.HandleUpdate(context.Background(), telegram.Update{CallbackQuery: &telegram.CallbackQuery{ID: "cb2", From: telegram.User{ID: userID}, Data: "lang:de"}}); err != nil {
		t.Fatal(err)
	}
	if calls := e.tg.Calls(); len(calls) != 1 || calls[0].Method != "answerCallbackQuery" {
		t.Fatalf("unknown callback: %v", calls)
	}
}

func TestBlockAndUnblockAreRecorded(t *testing.T) {
	e := newEnv(t)
	for status, want := range map[string]bool{"kicked": true, "member": false} {
		before := len(e.api.Calls())
		err := e.bot.HandleUpdate(context.Background(), telegram.Update{MyChatMember: &telegram.ChatMemberUpdated{
			Chat: telegram.Chat{ID: userID, Type: "private"}, From: telegram.User{ID: userID, FirstName: "Әлия"},
			NewChatMember: telegram.ChatMember{Status: status},
		}})
		if err != nil {
			t.Fatal(err)
		}
		calls := e.api.Calls()[before:]
		if len(calls) != 1 || calls[0].Body["blocked"] != want || calls[0].Body["started"] != nil {
			t.Fatalf("%s: %+v", status, calls)
		}
	}
	if len(e.tg.Messages()) != 0 {
		t.Fatal("no message may be sent to a user who blocked the bot")
	}
}

func TestBlockedUserSeesSupportOnly(t *testing.T) {
	e := newEnv(t)
	e.user["is_blocked"] = true
	msgs := e.text(t, "🚕 Такси шақыру")
	if len(msgs) != 1 || !strings.Contains(bottest.Text(msgs[0]), "Аккаунтыңыз шектелген") || !strings.Contains(bottest.Text(msgs[0]), "@jantaxi_support") {
		t.Fatalf("blocked: %v", msgs)
	}
	if b := bottest.InlineButtons(msgs[0]); len(b) != 1 || b[0]["url"] != "https://t.me/jantaxi_support" {
		t.Fatalf("support button: %v", b)
	}
}

func TestGroupsAndBotsAreIgnored(t *testing.T) {
	e := newEnv(t)
	err := e.bot.HandleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
		From: &telegram.User{ID: userID}, Chat: telegram.Chat{ID: -100, Type: "group"}, Text: "/start",
	}})
	if err != nil || len(e.api.Calls()) != 0 || len(e.tg.Calls()) != 0 {
		t.Fatalf("group message handled: %v %v", err, e.tg.Calls())
	}
}

func TestProfile(t *testing.T) {
	e := newEnv(t)
	p := e.bot.Profile()
	if len(p.Commands) != 3 || len(p.Commands[""]) != 7 || p.Commands[""][1].Description != "Такси шақыру" || p.Commands["ru"][1].Description != "Вызвать такси" {
		t.Fatalf("commands: %v", p.Commands)
	}
	for _, d := range p.Descriptions {
		if len([]rune(d)) > 512 {
			t.Fatalf("description too long: %d", len([]rune(d)))
		}
	}
	for _, d := range p.ShortDescriptions {
		if len([]rune(d)) > 120 {
			t.Fatalf("short description too long: %d", len([]rune(d)))
		}
	}
	if p.MenuButton == nil || p.MenuButton.Text != "Jan Taxi" || p.MenuButton.WebApp.URL != appURL {
		t.Fatalf("menu button: %+v", p.MenuButton)
	}
}
