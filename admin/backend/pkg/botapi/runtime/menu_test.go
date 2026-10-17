package runtime

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// scriptedBotAPI answers Bot API methods from per-method queues of
// (status, body) and records every call.
type scriptedBotAPI struct {
	mu     sync.Mutex
	queue  map[string][]string // method → queued raw error bodies
	calls  []string
	bodies []map[string]any
	srv    *httptest.Server
}

func newScriptedBotAPI(t *testing.T) *scriptedBotAPI {
	s := &scriptedBotAPI{queue: map[string][]string{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.calls = append(s.calls, method)
		s.bodies = append(s.bodies, body)
		var fail string
		if q := s.queue[method]; len(q) > 0 {
			fail, s.queue[method] = q[0], q[1:]
		}
		s.mu.Unlock()
		if fail != "" {
			var e struct {
				Code int `json:"error_code"`
			}
			_ = json.Unmarshal([]byte(fail), &e)
			writeTG(w, e.Code, fail)
			return
		}
		if method == "sendMessage" {
			writeTG(w, 200, `{"ok":true,"result":{"message_id":1,"chat":{"id":7,"type":"private"},"date":0}}`)
			return
		}
		writeTG(w, 200, `{"ok":true,"result":true}`)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *scriptedBotAPI) client() *telegram.Client {
	return telegram.New(testToken, telegram.WithBaseURL(s.srv.URL))
}

func (s *scriptedBotAPI) fail(method string, bodies ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue[method] = append(s.queue[method], bodies...)
}

func (s *scriptedBotAPI) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if c == method {
			n++
		}
	}
	return n
}

const (
	badGateway = `{"ok":false,"error_code":502,"description":"Bad Gateway"}`
	badRequest = `{"ok":false,"error_code":400,"description":"Bad Request: description is too long"}`
	forbidden  = `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`
)

func TestApplyProfileWithRetryRepeatsOnlyFailedCalls(t *testing.T) {
	saved := profileBackoff
	profileBackoff = telegram.Backoff{Min: time.Millisecond, Max: 5 * time.Millisecond}
	t.Cleanup(func() { profileBackoff = saved })

	api := newScriptedBotAPI(t)
	api.fail("setChatMenuButton", badGateway, badGateway, badGateway)
	api.fail("setMyDescription", badRequest)
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	ApplyProfileWithRetry(context.Background(), api.client(), Profile{
		Commands:     map[string][]telegram.BotCommand{"": {{Command: "start", Description: "Бастау"}}},
		Descriptions: map[string]string{"": "Сипаттама"},
		MenuButton:   &telegram.MenuButtonWebApp{Text: "Jan Taxi", WebApp: telegram.WebAppInfo{URL: "https://app.example.kz/passenger/"}},
	}, log)

	if n := api.count("setChatMenuButton"); n != 4 {
		t.Fatalf("menu button sent %d times, want 3 failures and 1 success", n)
	}
	if api.count("setMyCommands") != 1 || api.count("setMyDescription") != 1 {
		t.Fatalf("calls %v: succeeded or rejected calls must not be repeated", api.calls)
	}
	if !strings.Contains(logs.String(), "bot profile call rejected") || !strings.Contains(logs.String(), "published with rejected calls") {
		t.Fatalf("logs: %s", logs.String())
	}

	// Cancellation stops the retries.
	api.fail("setChatMenuButton", badGateway, badGateway, badGateway, badGateway, badGateway, badGateway)
	profileBackoff = telegram.Backoff{Min: time.Hour, Max: time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		ApplyProfileWithRetry(ctx, api.client(), Profile{MenuButton: &telegram.MenuButtonWebApp{
			Text: "Jan Taxi", WebApp: telegram.WebAppInfo{URL: "https://app.example.kz/passenger/"}}}, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("retries did not stop with the context")
	}
}

func TestChatMenusResetEachChatOnce(t *testing.T) {
	api := newScriptedBotAPI(t)
	m := NewChatMenus(api.client(), nil)
	ctx := context.Background()

	m.Ensure(ctx, 42)
	m.Ensure(ctx, 42)
	if n := api.count("setChatMenuButton"); n != 1 {
		t.Fatalf("chat 42 reset %d times, want once per process", n)
	}
	api.mu.Lock()
	body := api.bodies[0]
	api.mu.Unlock()
	if body["chat_id"] != float64(42) || body["menu_button"].(map[string]any)["type"] != "default" {
		t.Fatalf("reset body: %v", body)
	}

	// A retryable failure is tried again with the next update; a refusal is not.
	api.fail("setChatMenuButton", badGateway)
	m.Ensure(ctx, 43)
	m.Ensure(ctx, 43)
	api.fail("setChatMenuButton", forbidden)
	m.Ensure(ctx, 44)
	m.Ensure(ctx, 44)
	if n := api.count("setChatMenuButton"); n != 4 {
		t.Fatalf("%d resets, want 1 + 2 (retry after 502) + 1 (403 not retried)", n)
	}

	// Groups, an expired context and a nil ChatMenus do nothing.
	m.Ensure(ctx, -100)
	expired, cancel := context.WithCancel(ctx)
	cancel()
	m.Ensure(expired, 45)
	var none *ChatMenus
	none.Ensure(ctx, 46)
	if n := api.count("setChatMenuButton"); n != 4 {
		t.Fatalf("%d resets after ignored calls", n)
	}
}
