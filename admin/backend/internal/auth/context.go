// Package auth authenticates Mini App users (Telegram initData → app token),
// administrators (password → session cookie + CSRF) and the bots (shared service
// secret), and carries the resulting principal through request contexts.
package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// App identifies which Mini App (and therefore which bot token) a principal
// authenticated through.
type App string

const (
	AppPassenger App = "passenger"
	AppDriver    App = "driver"
)

// Valid reports whether a is a known app.
func (a App) Valid() bool { return a == AppPassenger || a == AppDriver }

// Principal is an authenticated Mini App user.
type Principal struct {
	UserID     uuid.UUID
	TelegramID int64
	App        App
}

// DriverContext is the driver profile attached to driver-app requests by
// RequireDriverProfile.
type DriverContext struct {
	ID     uuid.UUID
	Code   string
	Status string // draft | pending | approved | changes_requested | rejected | suspended
	CityID uuid.UUID
}

// Role is an administrator role.
type Role string

const (
	RoleSuperAdmin      Role = "super_admin"
	RoleOperator        Role = "operator"
	RoleDriverModerator Role = "driver_moderator"
	RoleFinanceManager  Role = "finance_manager"
	RoleAuditor         Role = "auditor"
)

// Roles lists all roles in display order.
var Roles = []Role{RoleSuperAdmin, RoleOperator, RoleDriverModerator, RoleFinanceManager, RoleAuditor}

// Permission is a capability checked on admin routes.
type Permission string

const (
	PermDashboardView   Permission = "dashboard.view"
	PermOrdersView      Permission = "orders.view"
	PermOrdersManage    Permission = "orders.manage"
	PermDriversView     Permission = "drivers.view"
	PermDriversModerate Permission = "drivers.moderate"
	PermDriversManage   Permission = "drivers.manage"
	PermDocumentsView   Permission = "documents.view"
	PermFinanceView     Permission = "finance.view"
	PermFinanceManage   Permission = "finance.manage"
	PermConfigManage    Permission = "config.manage"
	PermAdminsManage    Permission = "admins.manage"
	PermAuditView       Permission = "audit.view"
	PermReportsExport   Permission = "reports.export"
)

var rolePermissions = map[Role]map[Permission]bool{
	RoleSuperAdmin: set(PermDashboardView, PermOrdersView, PermOrdersManage, PermDriversView, PermDriversModerate,
		PermDriversManage, PermDocumentsView, PermFinanceView, PermFinanceManage, PermConfigManage, PermAdminsManage,
		PermAuditView, PermReportsExport),
	RoleOperator: set(PermDashboardView, PermOrdersView, PermOrdersManage, PermDriversView, PermDriversManage,
		PermAuditView, PermReportsExport),
	RoleDriverModerator: set(PermDashboardView, PermOrdersView, PermDriversView, PermDriversModerate, PermDriversManage,
		PermDocumentsView),
	RoleFinanceManager: set(PermDashboardView, PermOrdersView, PermDriversView, PermFinanceView, PermFinanceManage,
		PermAuditView, PermReportsExport),
	RoleAuditor: set(PermDashboardView, PermOrdersView, PermDriversView, PermFinanceView, PermAuditView,
		PermReportsExport),
}

func set(ps ...Permission) map[Permission]bool {
	m := make(map[Permission]bool, len(ps))
	for _, p := range ps {
		m[p] = true
	}
	return m
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { _, ok := rolePermissions[r]; return ok }

// Has reports whether the role grants p.
func (r Role) Has(p Permission) bool { return rolePermissions[r][p] }

// Permissions returns the role's permissions (for the admin UI).
func (r Role) Permissions() []Permission {
	out := make([]Permission, 0, len(rolePermissions[r]))
	for _, p := range []Permission{PermDashboardView, PermOrdersView, PermOrdersManage, PermDriversView,
		PermDriversModerate, PermDriversManage, PermDocumentsView, PermFinanceView, PermFinanceManage,
		PermConfigManage, PermAdminsManage, PermAuditView, PermReportsExport} {
		if r.Has(p) {
			out = append(out, p)
		}
	}
	return out
}

// AdminPrincipal is an authenticated administrator.
type AdminPrincipal struct {
	AdminID   uuid.UUID
	Email     string
	FullName  string
	Role      Role
	SessionID uuid.UUID
}

// Can reports whether the admin holds permission p.
func (a AdminPrincipal) Can(p Permission) bool { return a.Role.Has(p) }

type ctxKey int

const (
	ctxPrincipal ctxKey = iota
	ctxDriver
	ctxAdmin
	ctxBot
)

// WithPrincipal stores a Mini App principal in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxPrincipal, p)
}

// PrincipalFrom returns the Mini App principal, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(Principal)
	return p, ok
}

// RequirePrincipal returns the principal or a 401 error.
func RequirePrincipal(r *http.Request) (Principal, error) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		return Principal{}, httpx.Unauthorized()
	}
	return p, nil
}

// WithDriver stores the driver profile in ctx.
func WithDriver(ctx context.Context, d DriverContext) context.Context {
	return context.WithValue(ctx, ctxDriver, d)
}

// DriverFrom returns the driver profile attached by RequireDriverProfile.
func DriverFrom(ctx context.Context) (DriverContext, bool) {
	d, ok := ctx.Value(ctxDriver).(DriverContext)
	return d, ok
}

// RequireDriver returns the driver profile or a 403 error.
func RequireDriver(r *http.Request) (DriverContext, error) {
	d, ok := DriverFrom(r.Context())
	if !ok {
		return DriverContext{}, httpx.NewError(http.StatusForbidden, "driver_not_registered", "driver profile required")
	}
	return d, nil
}

// WithAdmin stores an admin principal in ctx.
func WithAdmin(ctx context.Context, a AdminPrincipal) context.Context {
	return context.WithValue(ctx, ctxAdmin, a)
}

// AdminFrom returns the admin principal, if any.
func AdminFrom(ctx context.Context) (AdminPrincipal, bool) {
	a, ok := ctx.Value(ctxAdmin).(AdminPrincipal)
	return a, ok
}

// RequireAdminPrincipal returns the admin principal or a 401 error.
func RequireAdminPrincipal(r *http.Request) (AdminPrincipal, error) {
	a, ok := AdminFrom(r.Context())
	if !ok {
		return AdminPrincipal{}, httpx.Unauthorized()
	}
	return a, nil
}

// Permit allows the request only when the admin principal (already loaded by
// RequireAdmin on the admin router) holds every listed permission.
func Permit(perms ...Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := AdminFrom(r.Context())
			if !ok {
				httpx.WriteError(w, r, httpx.Unauthorized())
				return
			}
			for _, p := range perms {
				if !a.Can(p) {
					httpx.WriteError(w, r, httpx.Forbidden())
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WithBot stores the calling bot name ("passenger" | "driver") for internal routes.
func WithBot(ctx context.Context, bot string) context.Context {
	return context.WithValue(ctx, ctxBot, bot)
}

// BotFrom returns the bot name set by RequireService.
func BotFrom(ctx context.Context) string {
	b, _ := ctx.Value(ctxBot).(string)
	return b
}
