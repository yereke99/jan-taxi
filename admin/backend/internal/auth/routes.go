package auth

import (
	"github.com/go-chi/chi/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// MountPublic mounts the Mini App sign-in endpoints (under /api/v1):
// POST /auth/telegram/passenger and POST /auth/telegram/driver, each rate-limited
// per client IP with its own limiter.
func (s *Service) MountPublic(r chi.Router) {
	for _, app := range []App{AppPassenger, AppDriver} {
		r.With(s.tgLimiters[app].Middleware(nil)).
			Post("/auth/telegram/"+string(app), httpx.H(s.handleTelegramAuth(app)))
	}
}

// MountAdminPublic mounts the unauthenticated admin endpoints (under /api/v1/admin):
// POST /auth/login and POST /auth/logout.
func (s *Service) MountAdminPublic(r chi.Router) {
	r.Post("/auth/login", httpx.H(s.handleLogin))
	r.Post("/auth/logout", httpx.H(s.handleLogout))
}

// MountAdmin mounts the session-protected admin endpoints (under /api/v1/admin,
// on a router that already runs RequireAdmin): GET /auth/me, POST /auth/password,
// and the administrator management routes guarded by admins.manage.
func (s *Service) MountAdmin(r chi.Router) {
	r.Get("/auth/me", httpx.H(s.handleMe))
	r.Post("/auth/password", httpx.H(s.handleChangePassword))
	r.Group(func(r chi.Router) {
		r.Use(Permit(PermAdminsManage))
		r.Get("/admins", httpx.H(s.handleListAdmins))
		r.Post("/admins", httpx.H(s.handleCreateAdmin))
		r.Patch("/admins/{id}", httpx.H(s.handleUpdateAdmin))
	})
}
