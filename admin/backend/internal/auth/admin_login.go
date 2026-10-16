package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/password"
)

const (
	maxFailedLogins  = 5
	lockoutDuration  = 15 * time.Minute
	minPasswordRunes = 12
	maxPasswordBytes = 1024
)

var (
	errInvalidCredentials = httpx.NewError(http.StatusUnauthorized, "invalid_credentials", "invalid e-mail or password")
)

func accountLocked(until time.Time) *httpx.Error {
	return httpx.NewError(http.StatusLocked, "account_locked", "too many failed sign-in attempts").
		WithDetails(map[string]any{"locked_until": until.UTC().Format(time.RFC3339)})
}

// AdminView is the admin as returned by the auth endpoints.
type AdminView struct {
	ID          uuid.UUID    `json:"id"`
	Email       string       `json:"email"`
	FullName    string       `json:"full_name"`
	Role        Role         `json:"role"`
	Permissions []Permission `json:"permissions"`
}

type sessionResponse struct {
	Admin     AdminView `json:"admin"`
	CSRFToken string    `json:"csrf_token"`
	// ExpiresAt is the absolute session expiry; the session also ends after
	// AdminIdleTTL without requests.
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Service) sessionBody(a AdminPrincipal, expiresAt time.Time) sessionResponse {
	return sessionResponse{
		Admin:     AdminView{ID: a.AdminID, Email: a.Email, FullName: a.FullName, Role: a.Role, Permissions: a.Role.Permissions()},
		CSRFToken: s.CSRFToken(a.SessionID),
		ExpiresAt: expiresAt.UTC(),
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type adminCredentials struct {
	id          uuid.UUID
	email       string
	fullName    string
	hash        string
	role        Role
	active      bool
	lockedUntil *time.Time
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	fields := map[string]string{}
	if email == "" {
		fields["email"] = "required"
	}
	if req.Password == "" {
		fields["password"] = "required"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	if !s.loginIPLimit.Allow(httpx.ClientIP(r)) || !s.loginMailLim.Allow(email) {
		w.Header().Set("Retry-After", "60")
		return httpx.TooManyRequests()
	}
	ctx := r.Context()
	now := s.now()

	if len(req.Password) > maxPasswordBytes || len(email) > 254 {
		return errInvalidCredentials
	}
	cred, found, err := s.loadCredentials(ctx, email)
	if err != nil {
		return httpx.Internal(err)
	}
	if !found {
		if h := s.dummyPasswordHash(); h != "" {
			_, _ = password.Verify(req.Password, h)
		}
		return errInvalidCredentials
	}
	if cred.lockedUntil != nil && now.Before(*cred.lockedUntil) {
		return accountLocked(*cred.lockedUntil)
	}
	ok, err := password.Verify(req.Password, cred.hash)
	if err != nil {
		httpx.Logger(ctx).Error("admin password hash is malformed", slog.String("admin_id", cred.id.String()))
		ok = false
	}
	if !ok {
		return s.recordFailedLogin(ctx, r, cred.id, now)
	}
	if !cred.active {
		return errInvalidCredentials
	}

	var rehash *string
	if password.NeedsRehash(cred.hash) {
		if h, err := password.Hash(req.Password); err == nil {
			rehash = &h
		}
	}
	var (
		sessionID uuid.UUID
		cookie    string
		expires   time.Time
	)
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE admin_users
			SET failed_login_count = 0, locked_until = NULL, last_login_at = $2,
			    password_hash = COALESCE($3::text, password_hash)
			WHERE id = $1`, cred.id, now, rehash); err != nil {
			return err
		}
		var err error
		sessionID, cookie, expires, err = s.createSession(ctx, tx, r, cred.id, now)
		if err != nil {
			return err
		}
		id := cred.id
		return s.audit(ctx, tx, r, AuditEntry{Action: "admin.login", EntityType: "admin", EntityID: id.String(), AdminID: &id})
	})
	if err != nil {
		return httpx.Internal(err)
	}
	s.setSessionCookie(w, cookie, expires)
	return httpx.OK(w, s.sessionBody(AdminPrincipal{
		AdminID: cred.id, Email: cred.email, FullName: cred.fullName, Role: cred.role, SessionID: sessionID,
	}, expires))
}

func (s *Service) loadCredentials(ctx context.Context, email string) (adminCredentials, bool, error) {
	var c adminCredentials
	var role string
	err := s.pool.QueryRow(ctx, `
		SELECT id, email::text, full_name, password_hash, role, is_active, locked_until
		FROM admin_users WHERE email = $1`, email).
		Scan(&c.id, &c.email, &c.fullName, &c.hash, &role, &c.active, &c.lockedUntil)
	if db.IsNoRows(err) {
		return adminCredentials{}, false, nil
	}
	if err != nil {
		return adminCredentials{}, false, err
	}
	c.role = Role(role)
	return c, true, nil
}

// recordFailedLogin counts a failure and locks the account on the fifth one.
// The counter restarts after a lock so each lock period allows five new attempts.
func (s *Service) recordFailedLogin(ctx context.Context, r *http.Request, adminID uuid.UUID, now time.Time) error {
	var lockedUntil *time.Time
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			UPDATE admin_users SET
				locked_until = CASE WHEN failed_login_count + 1 >= $2 THEN $3 ELSE locked_until END,
				failed_login_count = CASE WHEN failed_login_count + 1 >= $2 THEN 0 ELSE failed_login_count + 1 END
			WHERE id = $1
			RETURNING locked_until`, adminID, maxFailedLogins, now.Add(lockoutDuration)).Scan(&lockedUntil); err != nil {
			return err
		}
		if lockedUntil != nil && lockedUntil.After(now) {
			return s.audit(ctx, tx, r, AuditEntry{Action: "admin.locked", EntityType: "admin", EntityID: adminID.String(),
				AdminID: &adminID, After: map[string]any{"locked_until": lockedUntil.UTC()}})
		}
		return nil
	})
	if err != nil {
		return httpx.Internal(err)
	}
	if lockedUntil != nil && lockedUntil.After(now) {
		httpx.Logger(ctx).Warn("admin account locked after failed sign-ins", slog.String("admin_id", adminID.String()))
		return accountLocked(*lockedUntil)
	}
	return errInvalidCredentials
}

// handleLogout revokes the current session. It is mounted without RequireAdmin so
// an already-expired session still gets its cookie cleared.
func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) error {
	a, err := s.AdminFromRequest(r)
	if err != nil {
		s.clearSessionCookie(w)
		return httpx.NoContent(w)
	}
	if !s.validCSRF(r, a.SessionID) {
		return ErrCSRF
	}
	ctx := WithAdmin(r.Context(), a)
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`,
			a.SessionID, s.now()); err != nil {
			return err
		}
		return s.audit(ctx, tx, r, AuditEntry{Action: "admin.logout", EntityType: "admin", EntityID: a.AdminID.String()})
	})
	if err != nil {
		return httpx.Internal(err)
	}
	s.clearSessionCookie(w)
	return httpx.NoContent(w)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) error {
	a, err := RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	var expiresAt time.Time
	err = s.pool.QueryRow(r.Context(), `SELECT expires_at FROM admin_sessions WHERE id = $1`, a.SessionID).Scan(&expiresAt)
	if db.IsNoRows(err) {
		return httpx.Unauthorized()
	}
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, s.sessionBody(a, expiresAt))
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleChangePassword changes the caller's password and revokes their other sessions.
func (s *Service) handleChangePassword(w http.ResponseWriter, r *http.Request) error {
	a, err := RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	var req changePasswordRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.CurrentPassword == "" {
		return httpx.Validation(map[string]string{"current_password": "required"})
	}
	if reason := passwordProblem(req.NewPassword); reason != "" {
		return httpx.Validation(map[string]string{"new_password": reason})
	}
	if req.NewPassword == req.CurrentPassword {
		return httpx.Validation(map[string]string{"new_password": "same_as_current"})
	}
	if !s.passwordLimit.Allow(a.AdminID.String()) {
		return httpx.TooManyRequests()
	}
	ctx := r.Context()
	var hash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM admin_users WHERE id = $1`, a.AdminID).Scan(&hash); err != nil {
		return httpx.Internal(err)
	}
	if ok, _ := password.Verify(req.CurrentPassword, hash); !ok || len(req.CurrentPassword) > maxPasswordBytes {
		return httpx.Validation(map[string]string{"current_password": "invalid"})
	}
	newHash, err := password.Hash(req.NewPassword)
	if err != nil {
		return httpx.Internal(err)
	}
	now := s.now()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE admin_users SET password_hash = $2, password_changed_at = $3, failed_login_count = 0, locked_until = NULL
			WHERE id = $1`, a.AdminID, newHash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE admin_sessions SET revoked_at = $3
			WHERE admin_id = $1 AND id <> $2 AND revoked_at IS NULL`, a.AdminID, a.SessionID, now); err != nil {
			return err
		}
		return s.audit(ctx, tx, r, AuditEntry{Action: "admin.password_change", EntityType: "admin", EntityID: a.AdminID.String()})
	})
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}

// passwordProblem returns a validation reason code, or "" if pw is acceptable.
func passwordProblem(pw string) string {
	switch {
	case pw == "":
		return "required"
	case !utf8.ValidString(pw):
		return "invalid"
	case utf8.RuneCountInString(pw) < minPasswordRunes:
		return "too_short"
	case len(pw) > maxPasswordBytes || utf8.RuneCountInString(pw) > 256:
		return "too_long"
	case strings.TrimSpace(pw) == "":
		return "invalid"
	}
	return ""
}
