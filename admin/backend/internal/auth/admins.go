package auth

import (
	"context"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/password"
)

const maxFullNameRunes = 120

// AdminRecord is an administrator as listed in the admin panel.
type AdminRecord struct {
	ID          uuid.UUID    `json:"id"`
	Email       string       `json:"email"`
	FullName    string       `json:"full_name"`
	Role        Role         `json:"role"`
	Permissions []Permission `json:"permissions"`
	IsActive    bool         `json:"is_active"`
	LastLoginAt *time.Time   `json:"last_login_at"`
	LockedUntil *time.Time   `json:"locked_until"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

const adminColumns = `id, email::text, full_name, role, is_active, last_login_at, locked_until, created_at, updated_at`

func scanAdmin(row pgx.Row) (AdminRecord, error) {
	var a AdminRecord
	var role string
	if err := row.Scan(&a.ID, &a.Email, &a.FullName, &role, &a.IsActive, &a.LastLoginAt, &a.LockedUntil, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return AdminRecord{}, err
	}
	a.Role = Role(role)
	a.Permissions = a.Role.Permissions()
	return a, nil
}

func (s *Service) handleListAdmins(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := httpx.Page[AdminRecord]{Items: []AdminRecord{}, Page: page.Page, PageSize: page.PageSize}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_users`).Scan(&out.Total); err != nil {
		return httpx.Internal(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+adminColumns+` FROM admin_users ORDER BY created_at, id LIMIT $1 OFFSET $2`,
		page.PageSize, page.Offset())
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return httpx.Internal(err)
		}
		out.Items = append(out.Items, a)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, out)
}

type createAdminRequest struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

func (s *Service) handleCreateAdmin(w http.ResponseWriter, r *http.Request) error {
	var req createAdminRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	email, reason := normalizeEmail(req.Email)
	if reason != "" {
		fields["email"] = reason
	}
	fullName, reason := validFullName(req.FullName)
	if reason != "" {
		fields["full_name"] = reason
	}
	if !Role(req.Role).Valid() {
		fields["role"] = "invalid"
	}
	if reason := passwordProblem(req.Password); reason != "" {
		fields["password"] = reason
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	hash, err := password.Hash(req.Password)
	if err != nil {
		return httpx.Internal(err)
	}
	ctx := r.Context()
	var rec AdminRecord
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		rec, err = scanAdmin(tx.QueryRow(ctx, `
			INSERT INTO admin_users (email, full_name, password_hash, role) VALUES ($1, $2, $3, $4)
			RETURNING `+adminColumns, email, fullName, hash, req.Role))
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, r, AuditEntry{Action: "admin.create", EntityType: "admin", EntityID: rec.ID.String(), After: auditView(rec)})
	})
	if db.IsUniqueViolation(err, "") {
		return httpx.Conflict("email_taken", "an administrator with this e-mail already exists")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.Created(w, rec)
}

type updateAdminRequest struct {
	FullName *string `json:"full_name"`
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
	Password *string `json:"password"`
}

func (s *Service) handleUpdateAdmin(w http.ResponseWriter, r *http.Request) error {
	actor, err := RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req updateAdminRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.FullName == nil && req.Role == nil && req.IsActive == nil && req.Password == nil {
		return httpx.BadRequest("nothing to update")
	}
	fields := map[string]string{}
	var fullName *string
	if req.FullName != nil {
		v, reason := validFullName(*req.FullName)
		if reason != "" {
			fields["full_name"] = reason
		}
		fullName = &v
	}
	if req.Role != nil && !Role(*req.Role).Valid() {
		fields["role"] = "invalid"
	}
	var newHash *string
	if req.Password != nil {
		if reason := passwordProblem(*req.Password); reason != "" {
			fields["password"] = reason
		} else {
			h, err := password.Hash(*req.Password)
			if err != nil {
				return httpx.Internal(err)
			}
			newHash = &h
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}

	ctx := r.Context()
	now := s.now()
	var after AdminRecord
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		// Lock every super admin first (stable order) so concurrent demotions
		// cannot both pass the last-super-admin check or deadlock.
		if _, err := tx.Exec(ctx, `SELECT id FROM admin_users WHERE role = 'super_admin' ORDER BY id FOR UPDATE`); err != nil {
			return err
		}
		before, err := scanAdmin(tx.QueryRow(ctx, `SELECT `+adminColumns+` FROM admin_users WHERE id = $1 FOR UPDATE`, id))
		if db.IsNoRows(err) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		demote := req.Role != nil && Role(*req.Role) != before.Role
		deactivate := req.IsActive != nil && !*req.IsActive && before.IsActive
		if id == actor.AdminID && (demote || deactivate) {
			return httpx.Conflict("cannot_modify_self", "you cannot deactivate yourself or change your own role")
		}
		if before.Role == RoleSuperAdmin && before.IsActive && (demote || deactivate) {
			var others int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM admin_users WHERE role = 'super_admin' AND is_active AND id <> $1`, id).Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return httpx.Conflict("last_super_admin", "at least one active super admin must remain")
			}
		}
		after, err = scanAdmin(tx.QueryRow(ctx, `
			UPDATE admin_users SET
				full_name = COALESCE($2::text, full_name),
				role = COALESCE($3::text, role),
				is_active = COALESCE($4::boolean, is_active),
				password_hash = COALESCE($5::text, password_hash),
				password_changed_at = CASE WHEN $5::text IS NOT NULL THEN $6 ELSE password_changed_at END,
				failed_login_count = CASE WHEN $5::text IS NOT NULL THEN 0 ELSE failed_login_count END,
				locked_until = CASE WHEN $5::text IS NOT NULL THEN NULL ELSE locked_until END
			WHERE id = $1
			RETURNING `+adminColumns, id, fullName, req.Role, req.IsActive, newHash, now))
		if err != nil {
			return err
		}
		if deactivate || newHash != nil {
			// Credentials changed or access removed: end the target's sessions
			// (keeping the caller's own current session when they edit themselves).
			if _, err := tx.Exec(ctx, `
				UPDATE admin_sessions SET revoked_at = $3
				WHERE admin_id = $1 AND id <> $2 AND revoked_at IS NULL`, id, actor.SessionID, now); err != nil {
				return err
			}
		}
		afterAudit := auditView(after)
		if newHash != nil {
			afterAudit["credentials_reset"] = true
		}
		return s.audit(ctx, tx, r, AuditEntry{Action: "admin.update", EntityType: "admin", EntityID: id.String(),
			Before: auditView(before), After: afterAudit})
	})
	if err != nil {
		return httpx.AsError(err)
	}
	return httpx.OK(w, after)
}

func auditView(a AdminRecord) map[string]any {
	return map[string]any{"email": a.Email, "full_name": a.FullName, "role": a.Role, "is_active": a.IsActive}
}

func normalizeEmail(raw string) (string, string) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" {
		return "", "required"
	}
	if len(e) > 254 {
		return "", "too_long"
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || addr.Name != "" || !strings.Contains(e[strings.LastIndexByte(e, '@')+1:], ".") {
		return "", "invalid"
	}
	return e, ""
}

func validFullName(raw string) (string, string) {
	v := cleanName(raw, maxFullNameRunes+1)
	switch {
	case v == "":
		return "", "required"
	case len([]rune(v)) > maxFullNameRunes:
		return "", "too_long"
	}
	return v, ""
}

// CreateAdmin inserts an administrator (used by the CLI and the first-start
// bootstrap). It validates like the HTTP endpoint and never logs the password.
func (s *Service) CreateAdmin(ctx context.Context, email, fullName string, role Role, pw string) (AdminRecord, error) {
	fields := map[string]string{}
	e, reason := normalizeEmail(email)
	if reason != "" {
		fields["email"] = reason
	}
	name := cleanName(fullName, maxFullNameRunes)
	if !role.Valid() {
		fields["role"] = "invalid"
	}
	if reason := passwordProblem(pw); reason != "" {
		fields["password"] = reason
	}
	if len(fields) > 0 {
		return AdminRecord{}, httpx.Validation(fields)
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return AdminRecord{}, err
	}
	var rec AdminRecord
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		rec, err = scanAdmin(tx.QueryRow(ctx, `
			INSERT INTO admin_users (email, full_name, password_hash, role) VALUES ($1, $2, $3, $4)
			RETURNING `+adminColumns, e, name, hash, string(role)))
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, nil, AuditEntry{Action: "admin.create", EntityType: "admin", EntityID: rec.ID.String(),
			After: auditView(rec), AdminID: &rec.ID})
	})
	if db.IsUniqueViolation(err, "") {
		return AdminRecord{}, httpx.Conflict("email_taken", "an administrator with this e-mail already exists")
	}
	return rec, err
}
