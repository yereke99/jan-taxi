package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

func TestTelegramSender(t *testing.T) {
	const passengerToken = "1000000001:passengerTOKENaaaaaaaaaaaaaaaaaaaaa"
	var (
		mu     sync.Mutex
		bodies = map[string]map[string]any{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies[r.URL.Path] = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if body["chat_id"] == float64(403) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
			return
		}
		if body["chat_id"] == float64(429) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":321,"chat":{"id":1,"type":"private"},"date":1}}`)
	}))
	defer srv.Close()

	s := NewTelegramSender(telegram.New(passengerToken, telegram.WithBaseURL(srv.URL)), nil)
	ctx := context.Background()
	markup := &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
		{Text: "Ашу", WebApp: &telegram.WebAppInfo{URL: "https://a.kz/passenger/?startapp=home"}},
	}}}

	id, err := s.Send(ctx, PassengerBot, 42, "<b>Сәлем</b>", markup)
	if err != nil || id != 321 {
		t.Fatalf("Send = %d, %v", id, err)
	}
	body := bodies["/bot"+passengerToken+"/sendMessage"]
	if body["parse_mode"] != "HTML" || body["text"] != "<b>Сәлем</b>" || body["chat_id"] != float64(42) {
		t.Fatalf("request body %v", body)
	}
	kb := body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)
	if kb["web_app"].(map[string]any)["url"] != "https://a.kz/passenger/?startapp=home" {
		t.Fatalf("keyboard %v", kb)
	}
	if lp, _ := body["link_preview_options"].(map[string]any); lp["is_disabled"] != true {
		t.Fatalf("link previews not disabled: %v", body)
	}

	// No markup: reply_markup must be absent, not null.
	if _, err := s.Send(ctx, PassengerBot, 43, "x", nil); err != nil {
		t.Fatal(err)
	}
	if _, present := bodies["/bot"+passengerToken+"/sendMessage"]["reply_markup"]; present {
		t.Fatal("reply_markup sent without a keyboard")
	}

	_, err = s.Send(ctx, PassengerBot, 403, "x", nil)
	if !telegram.IsBlocked(err) {
		t.Fatalf("403 not classified as blocked: %v", err)
	}
	_, err = s.Send(ctx, PassengerBot, 429, "x", nil)
	if ra, ok := telegram.RetryAfter(err); !ok || ra.Seconds() != 7 {
		t.Fatalf("429 retry_after = %v %v", ra, ok)
	}
	if strings.Contains(err.Error(), passengerToken) {
		t.Fatal("token leaked into error")
	}

	if _, err := s.Send(ctx, DriverBot, 1, "x", nil); !errors.Is(err, ErrBotNotConfigured) {
		t.Fatalf("unconfigured bot: %v", err)
	}
	if s.Configured(DriverBot) || !s.Configured(PassengerBot) {
		t.Fatal("Configured() wrong")
	}
	// An unconfigured bot is a permanent failure for the worker.
	w := NewWorker(nil, s, Options{InstanceID: "x"})
	if o := w.classify(ctx, outboxRow{attempts: 1, maxAttempts: 8}, 0, ErrBotNotConfigured); o.status != StatusDead {
		t.Fatalf("unconfigured bot outcome %+v", o)
	}
}
