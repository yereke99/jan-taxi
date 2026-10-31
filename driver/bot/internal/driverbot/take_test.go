package driverbot

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/bottest"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

const acceptPath = "POST " + tgPath + "/orders/" + orderID + "/accept"

// offerText is the plain text Telegram returns for an offer message.
const offerText = "Жаңа тапсырыс №1042\nТакси · Седан\nҚайдан: Абай 1\nҚайда: Базар\nБағасы: 900 ₸ · Қолма-қол"

func (e *env) tapTake(t *testing.T, data string) []map[string]any {
	t.Helper()
	return e.update(t, telegram.Update{UpdateID: 9, CallbackQuery: &telegram.CallbackQuery{
		ID: "cb-take", From: telegram.User{ID: userID, FirstName: "Ерлан"}, Data: data,
		Message: &telegram.Message{MessageID: 77, Chat: telegram.Chat{ID: userID, Type: "private"}, Text: offerText},
	}})
}

// calls returns the recorded Bot API calls of method.
func (e *env) calls(method string) []bottest.Call {
	var out []bottest.Call
	for _, c := range e.tg.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func acceptResult(replayed bool, passenger map[string]any) map[string]any {
	return map[string]any{
		"replayed": replayed, "charged": 50,
		"wallet": map[string]any{"balance": 450, "reserved": 0, "available": 450, "currency": "KZT", "min_online_balance": 50},
		"order": map[string]any{
			"id": orderID, "number": 1042, "status": "accepted", "is_mine": true, "category": "taxi",
			"vehicle_class": "sedan", "vehicle_class_name": map[string]any{"kk": "Седан", "ru": "Седан", "en": "Sedan"},
			"passenger_count": 2, "comment": "2-кіреберіс <тез>", "fare": 900, "commission_amount": 50,
			"payment_method": "cash", "distance_m": 3200, "duration_s": 420,
			"stops": []any{
				map[string]any{"seq": 0, "kind": "pickup", "lat": 48.82, "lon": 58.14, "address": "Абай 1"},
				map[string]any{"seq": 1, "kind": "intermediate", "lat": 48.83, "lon": 58.15, "address": "Мектеп"},
				map[string]any{"seq": 2, "kind": "destination", "lat": 48.84, "lon": 58.16, "address": ""},
			},
			"passenger": passenger,
		},
	}
}

// TestTakeWins: the first tap takes the order through the internal API; the
// offer becomes «✅ Сіз алдыңыз» with an «open order» button. The bot sends no
// trip card of its own: the API's outbox delivers it (once, retried, fenced
// against a later cancellation), so a lost response cannot lose it.
func TestTakeWins(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	e.api.Handle(acceptPath, func(map[string]any, string) (int, any) {
		return 200, acceptResult(false, map[string]any{"first_name": "Айгүл", "phone": "+77011234567", "phone_verified": true,
			"telegram_username": "aigul_kz", "telegram_url": "https://t.me/aigul_kz"})
	})
	msgs := e.tapTake(t, "take:"+orderID)
	if e.api.Count(acceptPath) != 1 {
		t.Fatalf("accept calls = %d", e.api.Count(acceptPath))
	}
	answers := e.calls("answerCallbackQuery")
	if len(answers) != 1 || answers[0].Body["callback_query_id"] != "cb-take" || answers[0].Body["text"] != "✅ Тапсырыс сіздікі" ||
		answers[0].Body["show_alert"] == true {
		t.Fatalf("callback answer: %+v", answers)
	}
	// The answer comes before the message changes.
	var order []string
	for _, c := range e.tg.Calls() {
		order = append(order, c.Method)
	}
	if order[0] != "answerCallbackQuery" {
		t.Fatalf("call order: %v", order)
	}
	edits := e.calls("editMessageText")
	if len(edits) != 1 || edits[0].Body["message_id"] != float64(77) || edits[0].Body["chat_id"] != float64(userID) {
		t.Fatalf("edits: %+v", edits)
	}
	edited := bottest.Text(edits[0].Body)
	if !strings.HasPrefix(edited, "<b>✅ Сіз алдыңыз</b>\n\nЖаңа тапсырыс №1042") {
		t.Fatalf("edited offer: %q", edited)
	}
	if b := bottest.InlineButtons(edits[0].Body); len(b) != 1 || bottest.WebAppURL(b[0]) != appURL+"?startapp=order_"+orderID {
		t.Fatalf("edited offer buttons: %v", b)
	}
	if len(msgs) != 0 {
		t.Fatalf("the bot sent its own trip card: %v", msgs)
	}
}

// TestTakeReplay: a repeated tap is answered as a replay and sends nothing.
func TestTakeReplay(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	e.api.Handle(acceptPath, func(map[string]any, string) (int, any) {
		return 200, acceptResult(true, map[string]any{"first_name": "Дана", "phone": nil, "phone_verified": false,
			"telegram_username": nil, "telegram_url": "tg://user?id=555"})
	})
	if msgs := e.tapTake(t, "take:"+orderID); len(msgs) != 0 {
		t.Fatalf("replay sent a message: %v", msgs)
	}
	if a := e.calls("answerCallbackQuery"); len(a) != 1 || a[0].Body["text"] != texts.T("kk", "take_replayed_toast") {
		t.Fatalf("replay answer: %+v", a)
	}
}

// TestTakeFromOrdersList: in an /orders list message only the taken order's
// row goes; the text and the other orders' buttons stay.
func TestTakeFromOrdersList(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	e.api.Handle(acceptPath, func(map[string]any, string) (int, any) {
		return 200, acceptResult(false, map[string]any{"first_name": "Айгүл", "telegram_url": "tg://user?id=1"})
	})
	other := "11111111-2222-4333-8444-555555555555"
	markup := &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{
		{{Text: "✅ №1042 тапсырысты алу", CallbackData: "take:" + orderID}, {Text: "📱", WebApp: &telegram.WebAppInfo{URL: appURL + "?startapp=order_" + orderID}}},
		{{Text: "✅ №1043 тапсырысты алу", CallbackData: "take:" + other}, {Text: "📱", WebApp: &telegram.WebAppInfo{URL: appURL + "?startapp=order_" + other}}},
		{{Text: "Барлық тапсырыстар", WebApp: &telegram.WebAppInfo{URL: appURL + "?startapp=orders"}}},
	}}
	e.update(t, telegram.Update{UpdateID: 9, CallbackQuery: &telegram.CallbackQuery{
		ID: "cb-take", From: telegram.User{ID: userID, FirstName: "Ерлан"}, Data: "take:" + orderID,
		Message: &telegram.Message{MessageID: 77, Chat: telegram.Chat{ID: userID, Type: "private"}, Text: "Бос тапсырыстар", ReplyMarkup: markup},
	}})
	if n := len(e.calls("editMessageText")); n != 0 {
		t.Fatalf("list text rewritten %d times", n)
	}
	rm := e.calls("editMessageReplyMarkup")
	if len(rm) != 1 {
		t.Fatalf("keyboard edits: %+v", rm)
	}
	rows, _ := rm[0].Body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	if len(rows) != 2 || !strings.Contains(fmt.Sprint(rows[0]), "take:"+other) {
		t.Fatalf("remaining rows: %v", rows)
	}
}

// TestTakeAfterSomeoneElse: a late tap gets an alert and the offer loses its
// whole keyboard; nothing else is sent.
func TestTakeAfterSomeoneElse(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	for code, text := range map[string]string{
		"order_already_taken": texts.T("kk", "take_taken"),
		"invalid_transition":  texts.T("kk", "take_closed"),
		"not_found":           texts.T("kk", "take_unavailable"),
	} {
		status := 409
		if code == "not_found" {
			status = 404
		}
		e.api.Handle(acceptPath, func(map[string]any, string) (int, any) { return status, bottest.APIError(code, nil) })
		msgs := e.tapTake(t, "take:"+orderID)
		a := e.calls("answerCallbackQuery")
		if len(a) != 1 || a[0].Body["show_alert"] != true || a[0].Body["text"] != text {
			t.Fatalf("%s answer: %+v", code, a)
		}
		rm := e.calls("editMessageReplyMarkup")
		if len(rm) != 1 || rm[0].Body["message_id"] != float64(77) || rm[0].Body["reply_markup"] != nil {
			t.Fatalf("%s keyboard: %+v", code, rm)
		}
		if len(msgs) != 0 {
			t.Fatalf("%s messages: %v", code, msgs)
		}
	}
}

// TestTakeInsufficientBalance: an alert with the amounts, the take button
// goes (the map button stays) and a message offers a top-up.
func TestTakeInsufficientBalance(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	e.api.Handle(acceptPath, func(map[string]any, string) (int, any) {
		return 402, bottest.APIError("insufficient_balance", map[string]any{"required": 50, "available": 30,
			"currency": "KZT", "topup_min_amount": 500})
	})
	msgs := e.tapTake(t, "take:"+orderID)
	a := e.calls("answerCallbackQuery")
	if len(a) != 1 || a[0].Body["show_alert"] != true ||
		strings.ReplaceAll(a[0].Body["text"].(string), " ", " ") != "Балансыңыз жеткіліксіз: тапсырысты алу үшін кемінде 50 ₸ керек, қолжетімді 30 ₸. Балансты толтырыңыз." {
		t.Fatalf("answer: %+v", a)
	}
	// The driver can fix this: the offer keeps its accept button for a later tap.
	if rm := e.calls("editMessageReplyMarkup"); len(rm) != 0 {
		t.Fatalf("keyboard edited after a recoverable failure: %+v", rm)
	}
	if len(msgs) != 1 || !strings.Contains(msgText(msgs[0]), "50 ₸") || !strings.Contains(msgText(msgs[0]), "30 ₸") {
		t.Fatalf("top-up message: %v", msgs)
	}
	if b := bottest.InlineButtons(msgs[0]); len(b) != 1 || bottest.WebAppURL(b[0]) != appURL+"?startapp=topup" {
		t.Fatalf("top-up button: %v", b)
	}
}

// TestTakeOfflineOrWithoutLocation: alerts tell the driver what to do; the
// location case repeats how to share GPS.
func TestTakeOfflineOrWithoutLocation(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	e.api.Handle(acceptPath, func(map[string]any, string) (int, any) { return 409, bottest.APIError("driver_offline", nil) })
	if msgs := e.tapTake(t, "take:"+orderID); len(msgs) != 0 {
		t.Fatalf("offline messages: %v", msgs)
	}
	if a := e.calls("answerCallbackQuery"); a[0].Body["text"] != texts.T("kk", "take_offline") || a[0].Body["show_alert"] != true {
		t.Fatalf("offline answer: %+v", a)
	}
	e.api.Handle(acceptPath, func(map[string]any, string) (int, any) {
		return 422, bottest.APIError("location_required", map[string]any{"reason": "location_stale", "max_age_s": 300})
	})
	msgs := e.tapTake(t, "take:"+orderID)
	if a := e.calls("answerCallbackQuery"); a[0].Body["text"] != texts.T("kk", "take_location") {
		t.Fatalf("location answer: %+v", a)
	}
	if len(msgs) != 1 || msgText(msgs[0]) != strings.ReplaceAll(texts.T("kk", "location_stale"), " ", " ") {
		t.Fatalf("location message: %v", msgs)
	}
}

// TestTakeIgnoresBadData: malformed callback data never reaches the API.
func TestTakeIgnoresBadData(t *testing.T) {
	e := newEnv(t)
	e.approved(true)
	e.tapTake(t, "take:../../admin")
	if n := len(e.api.Calls()); n != 0 {
		t.Fatalf("API calls: %v", e.api.Calls())
	}
	if a := e.calls("answerCallbackQuery"); len(a) != 1 || a[0].Body["text"] != nil {
		t.Fatalf("answer: %+v", a)
	}
}
