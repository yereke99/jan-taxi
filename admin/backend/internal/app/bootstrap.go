package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/password"
)

var errAdminsExist = errors.New("administrators already exist")

// bootstrapAdmin creates the first super admin from ADMIN_BOOTSTRAP_EMAIL and
// ADMIN_BOOTSTRAP_PASSWORD when the admin_users table is empty. It is a no-op
// once any administrator exists, and an advisory lock keeps replicas that
// start together from racing.
func (a *App) bootstrapAdmin(ctx context.Context) error {
	email, pw := a.Cfg.AdminBootstrapEmail, a.Cfg.AdminBootstrapPassword
	if email == "" || pw == "" {
		return nil
	}
	var exists bool
	if err := a.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM admin_users)`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		a.Log.Warn("ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD are set but administrators already exist; unset them")
		return nil
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return err
	}
	var id uuid.UUID
	err = db.WithTx(ctx, a.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, db.LockKey("admin_bootstrap")); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM admin_users`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errAdminsExist
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO admin_users (email, full_name, password_hash, role)
			VALUES ($1, $2, $3, $4) RETURNING id`,
			email, "Administrator", hash, string(auth.RoleSuperAdmin)).Scan(&id)
		if err != nil {
			return fmt.Errorf("insert admin: %w", err)
		}
		return audit.Log(ctx, tx, nil, audit.Entry{
			Action:     "admin.bootstrap",
			EntityType: "admin",
			EntityID:   id.String(),
			After:      map[string]any{"email": email, "role": auth.RoleSuperAdmin, "source": "env"},
			AdminID:    &id,
		})
	})
	if errors.Is(err, errAdminsExist) {
		return nil
	}
	if err != nil {
		return err
	}
	a.Log.Warn("created the initial super admin from ADMIN_BOOTSTRAP_EMAIL; unset ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD now",
		slog.String("admin_id", id.String()))
	return nil
}
