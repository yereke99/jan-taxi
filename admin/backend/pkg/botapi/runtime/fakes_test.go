package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

const testToken = "123456:TEST-token-never-logged"

// fakeTelegram is an httptest Bot API: getUpdates serves queued updates from
// the requested offset; failures can be injected.
type fakeTelegram struct {
	mu            sync.Mutex
	updates       []telegram.Update
	getCalls      int
	getTimes      []time.Time
	offsets       []int64
	lastParams    map[string]any
	conflicts     int // answer the next N getUpdates with 409
	webhookActive bool
	tooMany       int // answer the next N getUpdates with 429 retry_after 1
	deleteCalls   int
	sent          []map[string]any
	sendFailures  int // answer the next N sendMessage with 429 retry_after 1
	srv           *httptest.Server
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegram) client() *telegram.Client {
	return telegram.New(testToken, telegram.WithBaseURL(f.srv.URL))
}

func (f *fakeTelegram) push(us ...telegram.Update) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, us...)
}

func writeTG(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/bot"+testToken+"/") {
		writeTG(w, 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/bot"+testToken+"/")
	var params map[string]any
	_ = json.NewDecoder(r.Body).Decode(&params)
	f.mu.Lock()
	switch method {
	case "deleteWebhook":
		f.deleteCalls++
		f.webhookActive = false
		f.mu.Unlock()
		writeTG(w, 200, `{"ok":true,"result":true}`)
	case "sendMessage":
		if f.sendFailures > 0 {
			f.sendFailures--
			f.mu.Unlock()
			writeTG(w, 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`)
			return
		}
		f.sent = append(f.sent, params)
		f.mu.Unlock()
		writeTG(w, 200, `{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"},"date":0}}`)
	case "getUpdates":
		f.getCalls++
		f.getTimes = append(f.getTimes, time.Now())
		f.lastParams = params
		offset := int64(0)
		if v, ok := params["offset"].(float64); ok {
			offset = int64(v)
		}
		f.offsets = append(f.offsets, offset)
		switch {
		case f.webhookActive:
			f.mu.Unlock()
			writeTG(w, 409, `{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first"}`)
			return
		case f.conflicts > 0:
			f.conflicts--
			f.mu.Unlock()
			writeTG(w, 409, `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request; make sure that only one bot instance is running"}`)
			return
		case f.tooMany > 0:
			f.tooMany--
			f.mu.Unlock()
			writeTG(w, 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`)
			return
		}
		var out []telegram.Update
		for _, u := range f.updates {
			if u.UpdateID >= offset {
				out = append(out, u)
			}
		}
		f.mu.Unlock()
		if len(out) == 0 {
			time.Sleep(10 * time.Millisecond) // a short "long poll"
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "result": append([]telegram.Update{}, out...)})
		writeTG(w, 200, string(b))
	default:
		f.mu.Unlock()
		writeTG(w, 404, `{"ok":false,"error_code":404,"description":"Not Found"}`)
	}
}

func (f *fakeTelegram) stats() (calls int, deletes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getCalls, f.deleteCalls
}

// fakeState is an httptest internal API implementing the lease/offset/claim
// protocol in memory.
type fakeState struct {
	mu         sync.Mutex
	holder     string
	expires    time.Time
	lastUpdate int64
	claims     map[int64]bool
	leaseCalls int
	offsets    []int64
	claimFail  int // fail the next N claims with 503
	srv        *httptest.Server
}

func newFakeState(t *testing.T) *fakeState {
	f := &fakeState{claims: map[int64]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeState) client() *botapi.Client {
	c := botapi.New(f.srv.URL, "secret", botapi.BotDriver)
	c.RetryBase = time.Millisecond
	c.MaxAttempts = 1
	return c
}

func (f *fakeState) serve(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/internal/v1/bots/driver")
	switch {
	case path == "/lease":
		f.leaseCalls++
		id, _ := body["instance_id"].(string)
		ttl, _ := body["ttl_s"].(float64)
		acquired := f.holder == "" || f.holder == id || time.Now().After(f.expires)
		if acquired {
			f.holder, f.expires = id, time.Now().Add(time.Duration(ttl)*time.Second)
		}
		writeTG(w, 200, `{"acquired":`+strconv.FormatBool(acquired)+`,"last_update_id":`+strconv.FormatInt(f.lastUpdate, 10)+`,"lease_expires_at":null}`)
	case path == "/offset":
		id, _ := body["instance_id"].(string)
		if id != f.holder {
			writeTG(w, 409, `{"error":{"code":"conflict","message":"not holder","details":{"reason":"not_lease_holder"}}}`)
			return
		}
		v, _ := body["last_update_id"].(float64)
		f.lastUpdate = max(f.lastUpdate, int64(v))
		f.offsets = append(f.offsets, int64(v))
		writeTG(w, 200, `{"last_update_id":`+strconv.FormatInt(f.lastUpdate, 10)+`}`)
	case strings.HasPrefix(path, "/updates/") && strings.HasSuffix(path, "/claim"):
		if f.claimFail > 0 {
			f.claimFail--
			writeTG(w, 503, `{"error":{"code":"internal","message":"down"}}`)
			return
		}
		id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(path, "/updates/"), "/claim"), 10, 64)
		claimed := !f.claims[id]
		f.claims[id] = true
		writeTG(w, 200, `{"claimed":`+strconv.FormatBool(claimed)+`}`)
	default:
		writeTG(w, 404, `{"error":{"code":"not_found","message":"nope"}}`)
	}
}

func (f *fakeState) snapshot() (holder string, last int64, leaseCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holder, f.lastUpdate, f.leaseCalls
}

// recorder is a Handler that records update ids.
type recorder struct {
	mu   sync.Mutex
	seen []int64
	fn   func(u telegram.Update)
}

func (r *recorder) handler() Handler {
	return HandlerFunc(func(_ context.Context, u telegram.Update) error {
		if r.fn != nil {
			r.fn(u)
		}
		r.mu.Lock()
		r.seen = append(r.seen, u.UpdateID)
		r.mu.Unlock()
		return nil
	})
}

func (r *recorder) ids() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.seen...)
}

func msgUpdate(id int64) telegram.Update {
	return telegram.Update{UpdateID: id, Message: &telegram.Message{MessageID: id, Chat: telegram.Chat{ID: 1, Type: "private"}, Text: "hi"}}
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
