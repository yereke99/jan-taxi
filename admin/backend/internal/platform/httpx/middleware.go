package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ctxKey int

const (
	ctxLogger ctxKey = iota
	ctxRequestID
	ctxClientIP
)

// Logger returns the request-scoped logger (or slog.Default()).
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxLogger).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// WithLogger stores a logger in ctx.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxLogger, l)
}

// RequestID returns the request id set by the RequestID middleware.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

// ClientIP returns the client address resolved by the RealIP middleware.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(ctxClientIP).(string); ok && ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RequestIDMiddleware assigns an X-Request-ID and a request-scoped logger.
func RequestIDMiddleware(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" || len(id) > 64 {
				var b [8]byte
				_, _ = rand.Read(b[:])
				id = hex.EncodeToString(b[:])
			}
			w.Header().Set("X-Request-ID", id)
			ctx := context.WithValue(r.Context(), ctxRequestID, id)
			ctx = WithLogger(ctx, base.With(slog.String("request_id", id)))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RealIP resolves the client IP from X-Forwarded-For only when the direct peer is
// one of the trusted proxies (e.g. Caddy on the Docker network).
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			ip := host
			if peer, err := netip.ParseAddr(host); err == nil && isTrusted(peer, trusted) {
				if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					parts := strings.Split(xff, ",")
					// Walk from the right, skipping trusted proxies.
					for i := len(parts) - 1; i >= 0; i-- {
						cand := strings.TrimSpace(parts[i])
						a, err := netip.ParseAddr(cand)
						if err != nil {
							break
						}
						ip = a.String()
						if !isTrusted(a, trusted) {
							break
						}
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxClientIP, ip)))
		})
	}
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer (needed for
// WebSocket hijacking and flushing).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog logs one line per request. Query strings are not logged because they
// may carry tokens.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		Logger(r.Context()).Info("http",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int("bytes", rec.bytes),
			slog.Duration("dur", time.Since(start)),
			slog.String("ip", ClientIP(r)),
		)
	})
}

// Recoverer converts panics into 500 responses.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				Logger(r.Context()).Error("panic", slog.Any("panic", p), slog.String("stack", string(debug.Stack())))
				WriteError(w, r, Internal(nil))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders sets conservative headers for API responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// CORS allows only the configured origins. Credentials are allowed so the admin
// cookie works when the admin panel is served from a different origin in dev.
func CORS(allowed []string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			set[o] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := set[origin]; ok {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", origin)
					h.Set("Access-Control-Allow-Credentials", "true")
					h.Add("Vary", "Origin")
					if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
						h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
						h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-CSRF-Token, X-Request-ID, Idempotency-Key")
						h.Set("Access-Control-Max-Age", "600")
						w.WriteHeader(http.StatusNoContent)
						return
					}
				} else if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimiter is a keyed token-bucket limiter with idle-entry eviction.
type RateLimiter struct {
	mu      sync.Mutex
	limit   rate.Limit
	burst   int
	entries map[string]*limiterEntry
}

type limiterEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewRateLimiter allows `perMinute` events per key per minute with the given burst.
func NewRateLimiter(perMinute float64, burst int) *RateLimiter {
	rl := &RateLimiter{limit: rate.Limit(perMinute / 60), burst: burst, entries: map[string]*limiterEntry{}}
	return rl
}

// Allow reports whether an event for key may happen now.
func (rl *RateLimiter) Allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	e, ok := rl.entries[key]
	if !ok {
		if len(rl.entries) > 50000 {
			rl.evictLocked(now)
		}
		e = &limiterEntry{lim: rate.NewLimiter(rl.limit, rl.burst)}
		rl.entries[key] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1)
}

func (rl *RateLimiter) evictLocked(now time.Time) {
	for k, e := range rl.entries {
		if now.Sub(e.seen) > 10*time.Minute {
			delete(rl.entries, k)
		}
	}
}

// Middleware limits by the key returned from keyFn (defaults to client IP).
func (rl *RateLimiter) Middleware(keyFn func(r *http.Request) string) func(http.Handler) http.Handler {
	if keyFn == nil {
		keyFn = ClientIP
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.Allow(keyFn(r)) {
				w.Header().Set("Retry-After", "10")
				WriteError(w, r, TooManyRequests())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
