package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// authTimeout is how long a Mini App client has to send its auth frame
// (a variable so tests can shorten it).
var authTimeout = 5 * time.Second

const (
	readLimitBytes   = 4 << 10
	pingEvery        = 25 * time.Second
	pingTimeout      = 10 * time.Second
	writeTimeout     = 10 * time.Second
	revalidateEvery  = time.Minute
	clientMsgRate    = 5 // client frames per second (sustained)
	clientMsgBurst   = 20
	controlQueueSize = 8
)

// Identity is an authenticated WebSocket client.
type Identity struct {
	Kind      string    // "admin" | "passenger" | "driver" (logs only)
	Scopes    []string  // event scopes the client receives
	ExpiresAt time.Time // zero: no fixed expiry
	// Revalidate is called every minute; it returns the current scopes, or an
	// error when the client is no longer authorised. Nil disables revalidation.
	Revalidate func(ctx context.Context) ([]string, error)
}

// Authenticator authenticates WebSocket clients.
type Authenticator interface {
	// FromRequest authenticates cookie-based clients (admins) at upgrade time;
	// ok=false means the client must send an auth frame.
	FromRequest(r *http.Request) (id Identity, ok bool)
	// FromToken authenticates the app token sent in the first frame.
	FromToken(ctx context.Context, token string) (Identity, error)
}

// HandlerConfig configures the WebSocket endpoint.
type HandlerConfig struct {
	Auth Authenticator
	// OriginPatterns are allowed Origin patterns (see OriginPatterns); the
	// request's own host is always allowed.
	OriginPatterns []string
	// AllowEmptyOrigin admits clients that send no Origin header (development only).
	AllowEmptyOrigin bool
}

// OriginPatterns converts allowed origins ("https://app.example.kz") into
// scheme-qualified patterns for websocket.AcceptOptions.
func OriginPatterns(origins []string) []string {
	var out []string
	for _, o := range origins {
		u, err := url.Parse(strings.TrimSpace(o))
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			continue
		}
		p := strings.ToLower(u.Scheme + "://" + u.Host)
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

type clientFrame struct {
	Type  string `json:"type"`
	Token string `json:"token,omitempty"`
}

// Handler serves GET /ws. Admins are authenticated by their session cookie at
// upgrade; Mini App clients must send {"type":"auth","token":"<app token>"}
// within 5 seconds. The server then sends {"type":"ready","scopes":[...]},
// pings every 25s, forwards events and answers {"type":"ping"} with
// {"type":"pong"}.
func (h *Hub) Handler(cfg HandlerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == "" && !cfg.AllowEmptyOrigin {
			httpx.WriteError(w, r, httpx.NewError(http.StatusForbidden, "origin_required", "Origin header required"))
			return
		}
		id, preAuthed := cfg.Auth.FromRequest(r)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: cfg.OriginPatterns})
		if err != nil {
			httpx.Logger(r.Context()).Info("websocket upgrade rejected", slog.String("err", err.Error()))
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(readLimitBytes)

		// The request context must not be relied on after hijacking; keep its
		// values (logger) but give the connection its own lifetime.
		ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
		defer cancel()
		log := httpx.Logger(r.Context())

		if !preAuthed {
			var code websocket.StatusCode
			var reason string
			id, code, reason = h.authenticate(ctx, conn, cfg.Auth)
			if code != 0 {
				_ = conn.Close(code, reason)
				return
			}
		}
		h.serve(ctx, cancel, conn, id, log)
	})
}

// authenticate reads the auth frame. A non-zero status means the connection must
// be closed with it.
func (h *Hub) authenticate(ctx context.Context, conn *websocket.Conn, a Authenticator) (Identity, websocket.StatusCode, string) {
	timer := time.AfterFunc(authTimeout, func() {
		_ = conn.Close(websocket.StatusPolicyViolation, "auth_timeout")
	})
	typ, data, err := conn.Read(ctx)
	if !timer.Stop() {
		return Identity{}, websocket.StatusPolicyViolation, "auth_timeout"
	}
	if err != nil {
		return Identity{}, websocket.StatusPolicyViolation, "unauthorized"
	}
	var f clientFrame
	if typ != websocket.MessageText || json.Unmarshal(data, &f) != nil || f.Type != "auth" || f.Token == "" {
		return Identity{}, websocket.StatusPolicyViolation, "unauthorized"
	}
	id, err := a.FromToken(ctx, f.Token)
	if err != nil {
		e := httpx.AsError(err)
		if e.Status >= 500 {
			h.log.Error("websocket authentication failed", slog.Any("err", err))
			return Identity{}, websocket.StatusInternalError, "internal"
		}
		return Identity{}, websocket.StatusPolicyViolation, closeReason(e.Code)
	}
	return id, 0, ""
}

func closeReason(code string) string {
	switch code {
	case "token_expired", "user_blocked":
		return code
	}
	return "unauthorized"
}

func (h *Hub) serve(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, id Identity, log *slog.Logger) {
	sub := h.Subscribe(id.Scopes)
	defer sub.Close()

	ready, _ := json.Marshal(struct {
		Type   string   `json:"type"`
		Scopes []string `json:"scopes"`
	}{"ready", sub.Scopes()})
	if !write(ctx, conn, ready) {
		return
	}

	control := make(chan []byte, controlQueueSize)
	go readLoop(ctx, cancel, conn, control)

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	reval := time.NewTicker(revalidateEvery)
	defer reval.Stop()
	var expired <-chan time.Time
	if !id.ExpiresAt.IsZero() {
		t := time.NewTimer(time.Until(id.ExpiresAt))
		defer t.Stop()
		expired = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-sub.C():
			if !write(ctx, conn, msg) {
				return
			}
		case msg := <-control:
			if !write(ctx, conn, msg) {
				return
			}
		case <-sub.Done():
			switch sub.Reason() {
			case ReasonShutdown:
				_ = conn.Close(websocket.StatusGoingAway, ReasonShutdown)
			case ReasonSlow:
				_ = conn.Close(websocket.StatusPolicyViolation, ReasonSlow)
			}
			return
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, pingTimeout)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		case <-expired:
			_ = conn.Close(websocket.StatusPolicyViolation, "token_expired")
			return
		case <-reval.C:
			if id.Revalidate == nil {
				continue
			}
			scopes, err := id.Revalidate(ctx)
			if err != nil {
				if httpx.AsError(err).Status >= 500 {
					log.Warn("websocket revalidation failed; keeping connection", slog.Any("err", err))
					continue
				}
				_ = conn.Close(websocket.StatusPolicyViolation, closeReason(httpx.AsError(err).Code))
				return
			}
			if !slices.Equal(slices.Compact(slices.Sorted(slices.Values(scopes))), sub.Scopes()) {
				sub.SetScopes(scopes)
			}
		}
	}
}

// readLoop handles client frames until the connection fails, then cancels ctx.
func readLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, control chan<- []byte) {
	defer cancel()
	lim := rate.NewLimiter(clientMsgRate, clientMsgBurst)
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if !lim.Allow() {
			_ = conn.Close(websocket.StatusPolicyViolation, "rate_limited")
			return
		}
		var f clientFrame
		if typ != websocket.MessageText || json.Unmarshal(data, &f) != nil {
			continue
		}
		if f.Type == "ping" {
			select {
			case control <- []byte(`{"type":"pong"}`):
			default:
			}
		}
	}
}

func write(ctx context.Context, conn *websocket.Conn, msg []byte) bool {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, msg) == nil
}
