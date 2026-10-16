package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

const (
	// AdminCookieName is the admin session cookie.
	AdminCookieName = "jt_admin"
	// CSRFHeader carries the CSRF token on state-changing admin requests.
	CSRFHeader = "X-CSRF-Token"

	adminCookiePath    = "/api"
	sessionTokenBytes  = 32
	sessionSlideMinGap = time.Minute
)

// ErrCSRF is returned when an unsafe admin request lacks a valid CSRF token.
var ErrCSRF = httpx.NewError(http.StatusForbidden, "csrf_failed", "missing or invalid CSRF token")

// CSRFToken derives the CSRF token for a session: base64url(HMAC(secret, "csrf:"+id)).
func (s *Service) CSRFToken(sessionID uuid.UUID) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte("csrf:" + sessionID.String()))
	return b64.EncodeToString(mac.Sum(nil))
}

func (s *Service) validCSRF(r *http.Request, sessionID uuid.UUID) bool {
	got := r.Header.Get(CSRFHeader)
	return got != "" && hmac.Equal([]byte(got), []byte(s.CSRFToken(sessionID)))
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// RequireAdmin authenticates the admin session cookie, enforces CSRF on unsafe
// methods and, when perms are given, requires all of them.
func (s *Service) RequireAdmin(perms ...Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, err := s.AdminFromRequest(r)
			if err != nil {
				// Drop a dead cookie, but not on server errors (e.g. a DB outage).
				if _, cerr := r.Cookie(AdminCookieName); cerr == nil && httpx.AsError(err).Status == http.StatusUnauthorized {
					s.clearSessionCookie(w)
				}
				httpx.WriteError(w, r, err)
				return
			}
			if !safeMethod(r.Method) && !s.validCSRF(r, a.SessionID) {
				httpx.WriteError(w, r, ErrCSRF)
				return
			}
			for _, p := range perms {
				if !a.Can(p) {
					httpx.WriteError(w, r, httpx.Forbidden())
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(WithAdmin(r.Context(), a)))
		})
	}
}

// AdminFromRequest authenticates the admin session cookie without a CSRF check
// (used for the WebSocket upgrade and by RequireAdmin).
func (s *Service) AdminFromRequest(r *http.Request) (AdminPrincipal, error) {
	c, err := r.Cookie(AdminCookieName)
	if err != nil {
		return AdminPrincipal{}, httpx.Unauthorized()
	}
	raw, err := b64.DecodeString(c.Value)
	if err != nil || len(raw) != sessionTokenBytes {
		return AdminPrincipal{}, httpx.Unauthorized()
	}
	hash := sha256.Sum256(raw)
	return s.loadSession(r.Context(), hash[:], true)
}

// AdminSessionActive re-checks a session (e.g. for long-lived WebSocket
// connections) without extending it.
func (s *Service) AdminSessionActive(ctx context.Context, sessionID uuid.UUID) error {
	var hash []byte
	err := s.pool.QueryRow(ctx, `SELECT token_hash FROM admin_sessions WHERE id = $1`, sessionID).Scan(&hash)
	if db.IsNoRows(err) {
		return httpx.Unauthorized()
	}
	if err != nil {
		return httpx.Internal(err)
	}
	_, err = s.loadSession(ctx, hash, false)
	return err
}

func (s *Service) loadSession(ctx context.Context, tokenHash []byte, slide bool) (AdminPrincipal, error) {
	var (
		a         AdminPrincipal
		role      string
		lastSeen  time.Time
		expiresAt time.Time
		revokedAt *time.Time
		active    bool
	)
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.last_seen_at, s.expires_at, s.revoked_at, a.id, a.email::text, a.full_name, a.role, a.is_active
		FROM admin_sessions s JOIN admin_users a ON a.id = s.admin_id
		WHERE s.token_hash = $1`, tokenHash).
		Scan(&a.SessionID, &lastSeen, &expiresAt, &revokedAt, &a.AdminID, &a.Email, &a.FullName, &role, &active)
	if db.IsNoRows(err) {
		return AdminPrincipal{}, httpx.Unauthorized()
	}
	if err != nil {
		return AdminPrincipal{}, httpx.Internal(err)
	}
	now := s.now()
	a.Role = Role(role)
	if revokedAt != nil || !now.Before(expiresAt) || now.Sub(lastSeen) >= s.cfg.AdminIdleTTL || !active || !a.Role.Valid() {
		return AdminPrincipal{}, httpx.Unauthorized()
	}
	if slide && now.Sub(lastSeen) >= sessionSlideMinGap {
		if _, err := s.pool.Exec(ctx,
			`UPDATE admin_sessions SET last_seen_at = $2 WHERE id = $1 AND revoked_at IS NULL`, a.SessionID, now); err != nil {
			return AdminPrincipal{}, httpx.Internal(err)
		}
	}
	return a, nil
}

// createSession inserts a session for adminID and returns its id, the raw cookie
// value and the absolute expiry. Only the SHA-256 of the token is stored.
func (s *Service) createSession(ctx context.Context, tx pgx.Tx, r *http.Request, adminID uuid.UUID, now time.Time) (uuid.UUID, string, time.Time, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return uuid.Nil, "", time.Time{}, err
	}
	hash := sha256.Sum256(raw)
	expires := now.Add(s.cfg.AdminAbsoluteTTL)
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO admin_sessions (admin_id, token_hash, ip, user_agent, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3::inet, $4, $5, $5, $6) RETURNING id`,
		adminID, hash[:], clientInet(r), ua, now, expires).Scan(&id)
	if err != nil {
		return uuid.Nil, "", time.Time{}, err
	}
	return id, b64.EncodeToString(raw), expires, nil
}

func (s *Service) setSessionCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminCookieName,
		Value:    value,
		Path:     adminCookiePath,
		Expires:  expires,
		MaxAge:   int(expires.Sub(s.now()).Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Service) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminCookieName,
		Value:    "",
		Path:     adminCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clientInet returns the client IP for an inet column, or nil when unparsable.
func clientInet(r *http.Request) *string {
	a, err := netip.ParseAddr(httpx.ClientIP(r))
	if err != nil {
		return nil
	}
	v := a.Unmap().String()
	return &v
}

// CleanupSessions deletes sessions that can no longer be used. Run hourly.
func (s *Service) CleanupSessions(ctx context.Context) error {
	now := s.now()
	_, err := s.pool.Exec(ctx, `
		DELETE FROM admin_sessions
		WHERE expires_at < $1 OR last_seen_at < $2 OR revoked_at < $3`,
		now.Add(-24*time.Hour), now.Add(-s.cfg.AdminIdleTTL-24*time.Hour), now.Add(-24*time.Hour))
	return err
}
