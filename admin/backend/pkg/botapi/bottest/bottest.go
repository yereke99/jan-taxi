// Package bottest provides httptest fakes of the Telegram Bot API and of the
// internal bot API for the bots' tests.
package bottest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// Token is the bot token used with FakeTelegram.
const Token = "100200300:FAKE-test-token"

// Call is one recorded request.
type Call struct {
	Method string // Bot API method, or "GET /internal/v1/..." for the internal API
	Body   map[string]any
	// Raw and ContentType are the request body as sent (internal API only),
	// e.g. a multipart upload.
	Raw         []byte
	ContentType string
}

// FakeTelegram records Bot API calls and answers them successfully unless a
// failure is queued with Fail.
type FakeTelegram struct {
	mu       sync.Mutex
	calls    []Call
	failures map[string][]string // method → queued raw error responses
	files    map[string][]byte   // file_id → content (served by getFile and /file/bot…)
	srv      *httptest.Server
}

// NewFakeTelegram starts a fake Bot API server.
func NewFakeTelegram(t testing.TB) *FakeTelegram {
	f := &FakeTelegram{failures: map[string][]string{}, files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// AddFile makes a file downloadable through getFile and the file endpoint.
func (f *FakeTelegram) AddFile(fileID string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[fileID] = content
}

func filePath(fileID string) string { return "documents/" + fileID + ".pdf" }

// Client returns a Bot API client talking to the fake.
func (f *FakeTelegram) Client() *telegram.Client {
	return telegram.New(Token, telegram.WithBaseURL(f.srv.URL))
}

// Fail makes the next call of method answer with the raw JSON error body.
func (f *FakeTelegram) Fail(method, rawJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = append(f.failures[method], rawJSON)
}

func (f *FakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	if p, ok := strings.CutPrefix(r.URL.Path, "/file/bot"+Token+"/"); ok {
		f.mu.Lock()
		f.calls = append(f.calls, Call{Method: "download:" + p})
		var content []byte
		found := false
		for id, c := range f.files {
			if filePath(id) == p {
				content, found = c, true
			}
		}
		f.mu.Unlock()
		if !found {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(content)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	method, ok := strings.CutPrefix(r.URL.Path, "/bot"+Token+"/")
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	if q := f.failures[method]; len(q) > 0 {
		f.failures[method] = q[1:]
		f.mu.Unlock()
		var e struct {
			Code int `json:"error_code"`
		}
		_ = json.Unmarshal([]byte(q[0]), &e)
		w.WriteHeader(e.Code)
		_, _ = io.WriteString(w, q[0])
		return
	}
	f.calls = append(f.calls, Call{Method: method, Body: body})
	f.mu.Unlock()
	switch method {
	case "sendMessage":
		chat, _ := body["chat_id"].(float64)
		b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{
			"message_id": len(f.Calls()), "chat": map[string]any{"id": int64(chat), "type": "private"}, "date": time.Now().Unix(),
		}})
		_, _ = w.Write(b)
	case "getUpdates":
		_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
	case "getFile":
		id, _ := body["file_id"].(string)
		f.mu.Lock()
		content, found := f.files[id]
		f.mu.Unlock()
		if !found {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: invalid file_id"}`)
			return
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{
			"file_id": id, "file_unique_id": "u-" + id, "file_size": len(content), "file_path": filePath(id),
		}})
		_, _ = w.Write(b)
	default:
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}
}

// Calls returns all recorded calls.
func (f *FakeTelegram) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Messages returns the recorded sendMessage bodies.
func (f *FakeTelegram) Messages() []map[string]any {
	var out []map[string]any
	for _, c := range f.Calls() {
		if c.Method == "sendMessage" {
			out = append(out, c.Body)
		}
	}
	return out
}

// Reset forgets the recorded calls.
func (f *FakeTelegram) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// Route answers one internal API route. It receives the decoded JSON body
// (nil for GET) and the query string, and returns the HTTP status and the
// response value (encoded as JSON unless nil).
type Route func(body map[string]any, query string) (int, any)

// FakeAPI is an httptest internal bot API with per-route handlers.
type FakeAPI struct {
	mu     sync.Mutex
	routes map[string]Route // "METHOD /path" (without /internal/v1)
	calls  []Call
	srv    *httptest.Server
	t      testing.TB
}

// NewFakeAPI starts a fake internal API. Unknown routes answer 404.
func NewFakeAPI(t testing.TB) *FakeAPI {
	f := &FakeAPI{routes: map[string]Route{}, t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// Client returns an internal API client for bot talking to the fake.
func (f *FakeAPI) Client(bot string) *botapi.Client {
	c := botapi.New(f.srv.URL, "test-secret", bot)
	c.RetryBase = time.Millisecond
	return c
}

// Handle sets the handler of "METHOD /path".
func (f *FakeAPI) Handle(route string, h Route) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[route] = h
}

// Calls returns the recorded requests ("METHOD /path" with body).
func (f *FakeAPI) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Count returns how often route was called.
func (f *FakeAPI) Count(route string) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Method == route {
			n++
		}
	}
	return n
}

func (f *FakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("X-Jantaxi-Bot") == "" {
		f.t.Errorf("internal API call without service credentials: %s %s", r.Method, r.URL.Path)
	}
	route := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/internal/v1")
	var (
		body map[string]any
		raw  []byte
	)
	if r.Body != nil {
		raw, _ = io.ReadAll(r.Body)
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			_ = json.Unmarshal(raw, &body)
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, Call{Method: route, Body: body, Raw: raw, ContentType: r.Header.Get("Content-Type")})
	h := f.routes[route]
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if h == nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"no route"}}`)
		return
	}
	status, v := h(body, r.URL.RawQuery)
	w.WriteHeader(status)
	if v != nil && status != http.StatusNoContent {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// APIError builds an internal API error body.
func APIError(code string, details map[string]any) map[string]any {
	e := map[string]any{"code": code, "message": code}
	if details != nil {
		e["details"] = details
	}
	return map[string]any{"error": e}
}

// Text returns the text of a sendMessage body.
func Text(m map[string]any) string { s, _ := m["text"].(string); return s }

// InlineButtons returns the inline buttons of a sendMessage body.
func InlineButtons(m map[string]any) []map[string]any {
	var out []map[string]any
	rm, _ := m["reply_markup"].(map[string]any)
	rows, _ := rm["inline_keyboard"].([]any)
	for _, row := range rows {
		for _, b := range row.([]any) {
			out = append(out, b.(map[string]any))
		}
	}
	return out
}

// ReplyButtons returns the reply keyboard button texts of a sendMessage body
// and whether each one requests the user's contact.
func ReplyButtons(m map[string]any) (texts []string, contact []bool) {
	rm, _ := m["reply_markup"].(map[string]any)
	rows, _ := rm["keyboard"].([]any)
	for _, row := range rows {
		for _, b := range row.([]any) {
			bm := b.(map[string]any)
			s, _ := bm["text"].(string)
			c, _ := bm["request_contact"].(bool)
			if _, ok := bm["web_app"]; ok {
				texts = append(texts, "!web_app!")
			}
			texts = append(texts, s)
			contact = append(contact, c)
		}
	}
	return texts, contact
}

// WebAppURL returns the web_app URL of an inline button ("" for other kinds).
func WebAppURL(b map[string]any) string {
	wa, _ := b["web_app"].(map[string]any)
	s, _ := wa["url"].(string)
	return s
}
