package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

const allowedOrigin = "https://app.jantaxi.test"

type wsEnv struct {
	pool *pgxpool.Pool
	hub  *Hub
	svc  *auth.Service
	srv  *httptest.Server
	url  string
}

func newWSEnv(t *testing.T, allowEmptyOrigin bool) *wsEnv {
	t.Helper()
	pool := testutil.NewDB(t)
	svc, err := auth.NewService(pool, auth.Config{
		PassengerBotToken: "1:p", DriverBotToken: "2:d",
		SessionSecret: "realtime-test-session-secret-0123456789",
		Audit: func(ctx context.Context, q db.Querier, r *http.Request, e auth.AuditEntry) error {
			return audit.Log(ctx, q, r, audit.Entry{Action: e.Action, EntityType: e.EntityType, EntityID: e.EntityID,
				Before: e.Before, After: e.After, AdminID: e.AdminID})
		},
		Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := runHub(t, pool)
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Method(http.MethodGet, "/ws", h.Handler(HandlerConfig{
			Auth:             ServiceAuthenticator{Auth: svc},
			OriginPatterns:   OriginPatterns([]string{allowedOrigin}),
			AllowEmptyOrigin: allowEmptyOrigin,
		}))
		r.Route("/admin", svc.MountAdminPublic)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &wsEnv{pool: pool, hub: h, svc: svc, srv: srv, url: "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"}
}

func (e *wsEnv) dial(t *testing.T, header http.Header) *websocket.Conn {
	t.Helper()
	if header == nil {
		header = http.Header{}
	}
	if header.Get("Origin") == "" {
		header.Set("Origin", allowedOrigin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, e.url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func readJSON(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("bad frame %s: %v", data, err)
	}
	return m
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func scopesOf(m map[string]any) []string {
	var out []string
	for _, s := range m["scopes"].([]any) {
		out = append(out, s.(string))
	}
	return out
}

func TestWebSocketPassengerFlow(t *testing.T) {
	e := newWSEnv(t, false)
	ctx := context.Background()
	uid, _ := testutil.CreateUser(t, e.pool)
	tok, _, err := e.svc.IssueAppToken(uid, auth.AppPassenger, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c := e.dial(t, nil)
	send(t, c, map[string]string{"type": "auth", "token": tok})
	ready := readJSON(t, c)
	if ready["type"] != "ready" || strings.Join(scopesOf(ready), ",") != UserScope(uid) {
		t.Fatalf("unexpected ready: %v", ready)
	}

	if err := Publish(ctx, e.pool, Event{Type: OrderAccepted, Scopes: []string{UserScope(uid)}, Data: map[string]string{"order_id": "o-1", "status": "accepted"}}); err != nil {
		t.Fatal(err)
	}
	ev := readJSON(t, c)
	if ev["type"] != OrderAccepted || ev["id"] == nil || ev["ts"] == nil || ev["data"].(map[string]any)["order_id"] != "o-1" {
		t.Fatalf("unexpected event: %v", ev)
	}
	if _, leaked := ev["scopes"]; leaked {
		t.Fatal("scopes forwarded to client")
	}

	send(t, c, map[string]string{"type": "ping"})
	if pong := readJSON(t, c); pong["type"] != "pong" {
		t.Fatalf("expected pong, got %v", pong)
	}

	// Another user's events never arrive.
	other, _ := testutil.CreateUser(t, e.pool)
	_ = Publish(ctx, e.pool, Event{Type: OrderCancelled, Scopes: []string{UserScope(other)}})
	_ = Publish(ctx, e.pool, Event{Type: OrderStarted, Scopes: []string{UserScope(uid)}})
	if ev := readJSON(t, c); ev["type"] != OrderStarted {
		t.Fatalf("scope isolation broken: %v", ev)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}

func TestWebSocketDriverScopes(t *testing.T) {
	e := newWSEnv(t, false)
	d := testutil.CreateDriver(t, e.pool, testutil.DriverOptions{})
	tok, _, _ := e.svc.IssueAppToken(d.UserID, auth.AppDriver, time.Now())
	c := e.dial(t, nil)
	send(t, c, map[string]string{"type": "auth", "token": tok})
	ready := readJSON(t, c)
	got := strings.Join(scopesOf(ready), ",")
	want := []string{UserScope(d.UserID), DriverScope(d.DriverID)}
	if !(got == want[0]+","+want[1] || got == want[1]+","+want[0]) {
		t.Fatalf("driver scopes = %s", got)
	}
	if err := Publish(context.Background(), e.pool, Event{Type: OrderAvailable, Scopes: []string{DriverScope(d.DriverID)}}); err != nil {
		t.Fatal(err)
	}
	if ev := readJSON(t, c); ev["type"] != OrderAvailable {
		t.Fatalf("driver event: %v", ev)
	}
}

func expectClose(t *testing.T, c *websocket.Conn, code websocket.StatusCode, reason string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	var ce websocket.CloseError
	if err == nil || websocket.CloseStatus(err) != code {
		t.Fatalf("expected close %d, got %v", code, err)
	}
	if reason != "" {
		if !asCloseError(err, &ce) || ce.Reason != reason {
			t.Fatalf("expected reason %q, got %v", reason, err)
		}
	}
}

func asCloseError(err error, ce *websocket.CloseError) bool {
	for err != nil {
		if v, ok := err.(websocket.CloseError); ok {
			*ce = v
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestWebSocketRejectsBadAuth(t *testing.T) {
	e := newWSEnv(t, false)
	uid, _ := testutil.CreateUser(t, e.pool)
	expired, _, _ := e.svc.IssueAppToken(uid, auth.AppPassenger, time.Now().Add(-24*time.Hour))

	tests := []struct {
		name   string
		frame  any
		reason string
	}{
		{"bad token", map[string]string{"type": "auth", "token": "garbage.token"}, "unauthorized"},
		{"expired token", map[string]string{"type": "auth", "token": expired}, "token_expired"},
		{"wrong first frame", map[string]string{"type": "ping"}, "unauthorized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := e.dial(t, nil)
			send(t, c, tt.frame)
			expectClose(t, c, websocket.StatusPolicyViolation, tt.reason)
		})
	}

	t.Run("blocked user", func(t *testing.T) {
		blocked, _ := testutil.CreateUser(t, e.pool)
		tok, _, _ := e.svc.IssueAppToken(blocked, auth.AppPassenger, time.Now())
		if _, err := e.pool.Exec(context.Background(), `UPDATE users SET is_blocked = true WHERE id = $1`, blocked); err != nil {
			t.Fatal(err)
		}
		c := e.dial(t, nil)
		send(t, c, map[string]string{"type": "auth", "token": tok})
		expectClose(t, c, websocket.StatusPolicyViolation, "user_blocked")
	})

	t.Run("auth timeout", func(t *testing.T) {
		old := authTimeout
		authTimeout = 200 * time.Millisecond
		defer func() { authTimeout = old }()
		c := e.dial(t, nil)
		expectClose(t, c, websocket.StatusPolicyViolation, "auth_timeout")
	})

	t.Run("oversized frame", func(t *testing.T) {
		c := e.dial(t, nil)
		send(t, c, map[string]string{"type": "auth", "token": strings.Repeat("a", 5000)})
		expectClose(t, c, websocket.StatusMessageTooBig, "")
	})
}

func TestWebSocketOriginPolicy(t *testing.T) {
	strict := newWSEnv(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for name, origin := range map[string]string{"foreign origin": "https://evil.example", "no origin": ""} {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		_, resp, err := websocket.Dial(ctx, strict.url, &websocket.DialOptions{HTTPHeader: h})
		if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: expected 403, got %v %v", name, err, resp)
		}
	}

	dev := newWSEnv(t, true)
	c, _, err := websocket.Dial(ctx, dev.url, nil)
	if err != nil {
		t.Fatalf("development mode must allow an empty Origin: %v", err)
	}
	c.CloseNow()
}

func TestWebSocketAdminByCookie(t *testing.T) {
	e := newWSEnv(t, false)
	if _, err := e.svc.CreateAdmin(context.Background(), "ops@jantaxi.kz", "Ops", auth.RoleOperator, "operator passphrase"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"email": "ops@jantaxi.kz", "password": "operator passphrase"})
	resp, err := http.Post(e.srv.URL+"/api/v1/admin/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == auth.AdminCookieName {
			cookie = ck
		}
	}
	if resp.StatusCode != 200 || cookie == nil {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	h := http.Header{}
	h.Set("Cookie", cookie.Name+"="+cookie.Value)
	c := e.dial(t, h)
	ready := readJSON(t, c)
	if ready["type"] != "ready" || strings.Join(scopesOf(ready), ",") != AdminScope {
		t.Fatalf("admin ready: %v", ready)
	}
	if err := Publish(context.Background(), e.pool, Event{Type: TopUpUpdated, Scopes: []string{AdminScope}}); err != nil {
		t.Fatal(err)
	}
	if ev := readJSON(t, c); ev["type"] != TopUpUpdated {
		t.Fatalf("admin event: %v", ev)
	}
}

func TestWebSocketSlowClientDisconnected(t *testing.T) {
	e := newWSEnv(t, false)
	e.hub.bufSize = 1
	uid, _ := testutil.CreateUser(t, e.pool)
	tok, _, _ := e.svc.IssueAppToken(uid, auth.AppPassenger, time.Now())
	c := e.dial(t, nil)
	send(t, c, map[string]string{"type": "auth", "token": tok})
	readJSON(t, c) // ready

	// Flood the subscription directly faster than any socket can drain it.
	payload := payloadFor(t, Event{Type: DriverLocationUpdated, Scopes: []string{UserScope(uid)}, Data: map[string]string{"pad": strings.Repeat("x", 1024)}})
	deadline := time.Now().Add(5 * time.Second)
	for e.hub.ClientCount() > 0 && time.Now().Before(deadline) {
		for i := 0; i < 50; i++ {
			e.hub.dispatch(payload)
		}
	}
	if e.hub.ClientCount() != 0 {
		t.Fatal("slow client not evicted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("expected policy violation close, got %v", err)
			}
			return
		}
	}
}
