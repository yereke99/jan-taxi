package app

import (
	"context"
	"net/http"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
)

// wireIdentity builds the auth service, the realtime hub and the
// authentication middlewares used by every protected router.
func (a *App) wireIdentity() error {
	svc, err := auth.NewService(a.Pool, auth.Config{
		PassengerBotToken: a.Cfg.PassengerBotToken,
		DriverBotToken:    a.Cfg.DriverBotToken,
		SessionSecret:     a.Cfg.SessionSecret,
		InitDataMaxAge:    a.Cfg.TelegramInitDataMaxAge,
		AppTokenTTL:       a.Cfg.AppTokenTTL,
		AdminIdleTTL:      a.Cfg.AdminSessionIdleTTL,
		AdminAbsoluteTTL:  a.Cfg.AdminSessionAbsoluteTTL,
		CookieSecure:      a.Cfg.AdminCookieSecure,
		Audit:             auditLog,
		Log:               a.Log,
	})
	if err != nil {
		return err
	}
	a.Auth = svc
	a.Hub = realtime.NewHub(a.Log)

	// Driver profiles are loaded optionally: registration routes run before a
	// profile exists; handlers that need one call auth.RequireDriver.
	driverProfile := svc.LoadDriverProfile(true)
	a.MW = Middlewares{
		Passenger: svc.RequireApp(auth.AppPassenger),
		Driver:    chain(svc.RequireApp(auth.AppDriver), driverProfile),
		AnyApp:    chain(svc.RequireAnyApp(), driverProfile),
		Admin:     svc.RequireAdmin(),
		Service:   auth.RequireService(a.Cfg.InternalBotServiceSecret),
	}

	// Every replica keeps its own LISTEN connection, so this is a worker, not
	// an exclusive job. When it stops at shutdown, WebSocket clients are told
	// to reconnect (close 1001).
	hub, pool := a.Hub, a.Pool
	a.AddWorker("realtime_listener", func(ctx context.Context) error { return hub.Run(ctx, pool) })
	a.AddReadinessCheck("realtime", func(context.Context) error { return hub.Ready() })
	a.AddJob("session_cleanup", time.Hour, svc.CleanupSessions)
	return nil
}

// mountIdentity mounts the sign-in, admin session, administrator management
// and WebSocket routes.
func (a *App) mountIdentity() error {
	a.Auth.MountPublic(a.Public)
	a.Public.Method(http.MethodGet, "/ws", a.Hub.Handler(realtime.HandlerConfig{
		Auth:             realtime.ServiceAuthenticator{Auth: a.Auth},
		OriginPatterns:   realtime.OriginPatterns(a.Cfg.CORSAllowedOrigins),
		AllowEmptyOrigin: a.Cfg.Env == config.EnvDevelopment,
	}))
	a.Auth.MountAdminPublic(a.AdminPublic)
	a.Auth.MountAdmin(a.Admin)
	return nil
}

// auditLog adapts audit.Log to auth.AuditFunc (auth cannot import audit,
// which itself imports auth).
func auditLog(ctx context.Context, q db.Querier, r *http.Request, e auth.AuditEntry) error {
	return audit.Log(ctx, q, r, audit.Entry{
		Action: e.Action, EntityType: e.EntityType, EntityID: e.EntityID,
		Before: e.Before, After: e.After, AdminID: e.AdminID,
	})
}

// chain composes middlewares so that the first one runs first.
func chain(mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}
