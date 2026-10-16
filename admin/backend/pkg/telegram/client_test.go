package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "123456:AAH-secret-token_value"

type recorded struct {
	path string
	body map[string]any
}

// fakeBotAPI serves canned responses keyed by method name and records requests.
type fakeBotAPI struct {
	t        *testing.T
	mu       sync.Mutex
	requests []recorded
	handler  func(w http.ResponseWriter, method string, body map[string]any)
}

func (f *fakeBotAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := "/bot" + testToken + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.Error(w, "bad path", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		f.t.Errorf("request body is not JSON: %s", raw)
	}
	method := strings.TrimPrefix(r.URL.Path, prefix)
	f.mu.Lock()
	f.requests = append(f.requests, recorded{path: method, body: body})
	f.mu.Unlock()
	f.handler(w, method, body)
}

func (f *fakeBotAPI) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func newFake(t *testing.T, h func(w http.ResponseWriter, method string, body map[string]any)) (*fakeBotAPI, *Client) {
	t.Helper()
	f := &fakeBotAPI{t: t, handler: h}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, New(testToken, WithBaseURL(srv.URL+"/"), WithHTTPClient(srv.Client()))
}

func writeJSON(w http.ResponseWriter, status int, v string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, v)
}

func TestSendMessageSuccess(t *testing.T) {
	f, c := newFake(t, func(w http.ResponseWriter, method string, body map[string]any) {
		writeJSON(w, 200, `{"ok":true,"result":{"message_id":77,"chat":{"id":42,"type":"private"},"date":1700000000,"text":"hi"}}`)
	})
	markup := InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "Open", WebApp: &WebAppInfo{URL: "https://example.kz/passenger/?startapp=home"}},
	}}}
	m, err := c.SendMessage(context.Background(), SendMessageParams{
		ChatID: 42, Text: "<b>hi</b>", ParseMode: "HTML", ReplyMarkup: markup, DisableWebPagePreview: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.MessageID != 77 || m.Chat.ID != 42 || m.Text != "hi" {
		t.Fatalf("unexpected message: %+v", m)
	}
	req := f.last()
	if req.path != "sendMessage" {
		t.Fatalf("method = %s", req.path)
	}
	if req.body["chat_id"].(float64) != 42 || req.body["parse_mode"] != "HTML" {
		t.Fatalf("unexpected body: %v", req.body)
	}
	lp, _ := req.body["link_preview_options"].(map[string]any)
	if lp["is_disabled"] != true {
		t.Fatalf("link preview not disabled: %v", req.body)
	}
	rm := req.body["reply_markup"].(map[string]any)
	btn := rm["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)
	if btn["web_app"].(map[string]any)["url"] != "https://example.kz/passenger/?startapp=home" {
		t.Fatalf("unexpected markup: %v", rm)
	}
	if _, ok := btn["callback_data"]; ok {
		t.Fatalf("empty optional fields must be omitted: %v", btn)
	}
}

func TestGetUpdatesParamsEncoding(t *testing.T) {
	f, c := newFake(t, func(w http.ResponseWriter, method string, body map[string]any) {
		writeJSON(w, 200, `{"ok":true,"result":[
			{"update_id":10,"message":{"message_id":1,"from":{"id":5,"is_bot":false,"first_name":"A"},"chat":{"id":5,"type":"private"},"date":1,"contact":{"phone_number":"+77010000000","first_name":"A","user_id":5}}},
			{"update_id":11,"edited_message":{"message_id":2,"chat":{"id":5,"type":"private"},"date":1,"edit_date":2,"location":{"latitude":48.82,"longitude":58.14,"live_period":900}}},
			{"update_id":12,"callback_query":{"id":"cb1","from":{"id":5,"is_bot":false,"first_name":"A"},"data":"lang:kk"}},
			{"update_id":13,"my_chat_member":{"chat":{"id":5,"type":"private"},"from":{"id":5,"is_bot":false,"first_name":"A"},"new_chat_member":{"status":"kicked"}}}
		]}`)
	})
	ups, err := c.GetUpdates(context.Background(), GetUpdatesParams{
		Offset: 10, Limit: 100, Timeout: 1,
		AllowedUpdates: []string{"message", "edited_message", "callback_query", "my_chat_member"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 4 || ups[0].Message.Contact.UserID != 5 || ups[1].EditedMessage.Location.LivePeriod != 900 ||
		ups[2].CallbackQuery.Data != "lang:kk" || ups[3].MyChatMember.NewChatMember.Status != "kicked" {
		t.Fatalf("unexpected updates: %+v", ups)
	}
	body := f.last().body
	if body["offset"].(float64) != 10 || body["limit"].(float64) != 100 || body["timeout"].(float64) != 1 {
		t.Fatalf("unexpected params: %v", body)
	}
	au, ok := body["allowed_updates"].([]any)
	if !ok || len(au) != 4 || au[0] != "message" || au[3] != "my_chat_member" {
		t.Fatalf("allowed_updates must be a JSON array: %#v", body["allowed_updates"])
	}

	// nil AllowedUpdates is omitted (keeps the server setting); empty slice is sent.
	if _, err := c.GetUpdates(context.Background(), GetUpdatesParams{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.last().body["allowed_updates"]; ok {
		t.Fatal("nil allowed_updates must be omitted")
	}
	if _, err := c.GetUpdates(context.Background(), GetUpdatesParams{AllowedUpdates: []string{}}); err != nil {
		t.Fatal(err)
	}
	if v, ok := f.last().body["allowed_updates"].([]any); !ok || len(v) != 0 {
		t.Fatalf("empty allowed_updates must be sent as []: %#v", f.last().body)
	}
}

func TestAPIErrorClassification(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		blocked    bool
		conflict   bool
		retryable  bool
		retryAfter time.Duration
		code       int
	}{
		{"429 retry_after", 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`,
			false, false, true, 7 * time.Second, 429},
		{"403 blocked", 403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`,
			true, false, false, 0, 403},
		{"409 conflict", 409, `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`,
			false, true, false, 0, 409},
		{"502 html from proxy", 502, `<html>Bad Gateway</html>`, false, false, true, 0, 502},
		{"500 json", 500, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`, false, false, true, 0, 500},
		{"400 bad request", 400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, false, false, false, 0, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, c := newFake(t, func(w http.ResponseWriter, _ string, _ map[string]any) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.SendMessage(context.Background(), SendMessageParams{ChatID: 1, Text: "x"})
			if err == nil {
				t.Fatal("expected error")
			}
			var ae *APIError
			if !errors.As(err, &ae) || ae.Code != tt.code {
				t.Fatalf("expected APIError %d, got %v", tt.code, err)
			}
			if IsBlocked(err) != tt.blocked || IsConflict(err) != tt.conflict || IsRetryable(err) != tt.retryable {
				t.Fatalf("classification mismatch for %v: blocked=%v conflict=%v retryable=%v",
					err, IsBlocked(err), IsConflict(err), IsRetryable(err))
			}
			ra, ok := RetryAfter(err)
			if ra != tt.retryAfter || ok != (tt.retryAfter > 0) {
				t.Fatalf("RetryAfter = %v, %v", ra, ok)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatalf("token leaked: %v", err)
			}
		})
	}
}

func TestMessageNotModified(t *testing.T) {
	_, c := newFake(t, func(w http.ResponseWriter, _ string, _ map[string]any) {
		writeJSON(w, 400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content and reply markup are exactly the same"}`)
	})
	err := c.EditMessageText(context.Background(), EditMessageTextParams{ChatID: 1, MessageID: 2, Text: "x"})
	if !IsMessageNotModified(err) || IsRetryable(err) {
		t.Fatalf("unexpected classification: %v", err)
	}
}

func TestNetworkErrorIsRetryableAndRedacted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens there now: connection refused

	var logs bytes.Buffer
	c := New(testToken, WithBaseURL("http://"+addr), WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	_, err = c.GetMe(context.Background())
	if err == nil {
		t.Fatal("expected network error")
	}
	if !IsRetryable(err) || IsBlocked(err) || IsConflict(err) {
		t.Fatalf("network error must be retryable: %v", err)
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "/bot") {
		t.Fatalf("token or URL leaked into error: %v", err)
	}
	if strings.Contains(logs.String(), testToken) || strings.Contains(logs.String(), "/bot") {
		t.Fatalf("token or URL leaked into logs: %s", logs.String())
	}
}

func TestTimeoutIsRetryableCancelIsNot(t *testing.T) {
	release := make(chan struct{})
	_, c := newFake(t, func(w http.ResponseWriter, _ string, _ map[string]any) {
		<-release
		writeJSON(w, 200, `{"ok":true,"result":true}`)
	})
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.DeleteWebhook(ctx, true)
	if err == nil || !IsRetryable(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error should be retryable: %v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("token leaked: %v", err)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel2() }()
	err = c.DeleteWebhook(ctx2, false)
	if err == nil || IsRetryable(err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must not be retryable: %v", err)
	}
}

func TestBadBaseURLDoesNotLeakToken(t *testing.T) {
	c := New(testToken, WithBaseURL("http://bad host"))
	_, err := c.GetMe(context.Background())
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("expected redacted error, got %v", err)
	}
}

func TestOtherMethodsEncoding(t *testing.T) {
	f, c := newFake(t, func(w http.ResponseWriter, method string, _ map[string]any) {
		switch method {
		case "getMe":
			writeJSON(w, 200, `{"ok":true,"result":{"id":99,"is_bot":true,"first_name":"Jan Taxi","username":"jantaxi_bot"}}`)
		case "editMessageText":
			writeJSON(w, 200, `{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"},"date":1}}`)
		default:
			writeJSON(w, 200, `{"ok":true,"result":true}`)
		}
	})
	ctx := context.Background()
	me, err := c.GetMe(ctx)
	if err != nil || me.ID != 99 || !me.IsBot || me.Username != "jantaxi_bot" {
		t.Fatalf("GetMe = %+v, %v", me, err)
	}

	chatID := int64(42)
	if err := c.SetChatMenuButton(ctx, &chatID, MenuButtonWebApp{Text: "Ашу", WebApp: WebAppInfo{URL: "https://x.kz/p/"}}); err != nil {
		t.Fatal(err)
	}
	b := f.last().body
	mb := b["menu_button"].(map[string]any)
	if b["chat_id"].(float64) != 42 || mb["type"] != "web_app" || mb["text"] != "Ашу" || mb["web_app"].(map[string]any)["url"] != "https://x.kz/p/" {
		t.Fatalf("unexpected setChatMenuButton body: %v", b)
	}
	if err := c.SetChatMenuButton(ctx, nil, MenuButtonWebApp{Text: "Open", WebApp: WebAppInfo{URL: "https://x.kz/p/"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.last().body["chat_id"]; ok {
		t.Fatal("default menu button must omit chat_id")
	}

	if err := c.SetMyCommands(ctx, []BotCommand{{Command: "start", Description: "Бастау"}}, "kk"); err != nil {
		t.Fatal(err)
	}
	b = f.last().body
	if b["language_code"] != "kk" || b["commands"].([]any)[0].(map[string]any)["command"] != "start" {
		t.Fatalf("unexpected setMyCommands body: %v", b)
	}

	if err := c.AnswerCallbackQuery(ctx, AnswerCallbackQueryParams{CallbackQueryID: "cb", Text: "ok", ShowAlert: true}); err != nil {
		t.Fatal(err)
	}
	b = f.last().body
	if b["callback_query_id"] != "cb" || b["show_alert"] != true {
		t.Fatalf("unexpected answerCallbackQuery body: %v", b)
	}

	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{{Text: "A", CallbackData: "a"}}}}
	if err := c.EditMessageText(ctx, EditMessageTextParams{ChatID: 1, MessageID: 5, Text: "t", ReplyMarkup: kb}); err != nil {
		t.Fatal(err)
	}
	b = f.last().body
	if b["message_id"].(float64) != 5 || b["reply_markup"] == nil {
		t.Fatalf("unexpected editMessageText body: %v", b)
	}

	if err := c.SetMyDescription(ctx, "Сипаттама", "kk"); err != nil {
		t.Fatal(err)
	}
	if f.last().body["description"] != "Сипаттама" {
		t.Fatalf("unexpected setMyDescription body: %v", f.last().body)
	}
	if err := c.SetMyShortDescription(ctx, "Қысқа", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.last().body["language_code"]; ok || f.last().body["short_description"] != "Қысқа" {
		t.Fatalf("unexpected setMyShortDescription body: %v", f.last().body)
	}

	if err := c.DeleteWebhook(ctx, true); err != nil {
		t.Fatal(err)
	}
	if f.last().path != "deleteWebhook" || f.last().body["drop_pending_updates"] != true {
		t.Fatalf("unexpected deleteWebhook request: %+v", f.last())
	}

	rk := ReplyKeyboardMarkup{Keyboard: [][]KeyboardButton{{{Text: "Phone", RequestContact: true}}}, ResizeKeyboard: true}
	if _, err := c.SendMessage(ctx, SendMessageParams{ChatID: 1, Text: "x", ReplyMarkup: rk}); err == nil {
		// result is `true`, not a Message: decoding must fail loudly rather than return garbage.
		t.Fatal("expected malformed result error")
	}
	b = f.last().body
	btn := b["reply_markup"].(map[string]any)["keyboard"].([]any)[0].([]any)[0].(map[string]any)
	if btn["request_contact"] != true {
		t.Fatalf("unexpected reply keyboard: %v", b)
	}
}
