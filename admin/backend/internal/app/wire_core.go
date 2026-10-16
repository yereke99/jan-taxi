package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/users"
)

// wireCore constructs the users, cities and settings services and mounts their routes.
func (a *App) wireCore() error {
	// Env-backed settings fall back to these values until an admin overrides them.
	settings.Configure(settings.Env{
		KaspiPaymentURL:          a.Cfg.KaspiPayPaymentURL,
		KaspiRecipientName:       a.Cfg.KaspiPayRecipientName,
		KaspiInstructions:        a.Cfg.KaspiPayPaymentInstructions,
		TwoGISBaseURL:            a.Cfg.TwoGISBaseURL,
		DriverOnlineIdleTimeout:  a.Cfg.DriverOnlineIdleTimeout,
		MaxRoutePoints:           a.Cfg.MaxRoutePoints,
		DefaultCommissionPercent: a.Cfg.DefaultCommissionPercent,
	})

	// /api/v1/me, /api/v1/me/preferences (passenger or driver token)
	users.NewHandler(a.Pool).MountApp(a.AnyApp)

	// /api/v1/public/config
	cities.NewPublicConfig(a.Pool, cities.PublicOptions{
		PassengerBotUsername:      a.Cfg.PassengerBotUsername,
		DriverBotUsername:         a.Cfg.DriverBotUsername,
		PassengerMiniAppShortName: a.Cfg.PassengerMiniAppShortName,
		DriverMiniAppShortName:    a.Cfg.DriverMiniAppShortName,
		MapStyleURL:               a.Cfg.MapStyleURL,
		DefaultCity:               a.Cfg.DefaultCity,
		DefaultLanguage:           a.Cfg.DefaultLanguage,
	}).MountPublic(a.Public)

	// /api/v1/admin/{settings,audit-logs,cities,zones,places}; permissions per route.
	settings.NewHandler(a.Pool).MountAdmin(a.Admin)
	audit.NewHandler(a.Pool).MountAdmin(a.Admin)
	cities.NewAdminHandler(a.Pool).MountAdmin(a.Admin)

	a.checkDefaultCity()
	return nil
}

// checkDefaultCity warns at startup when DEFAULT_CITY does not name an active
// city; the apps then fall back to the oldest active city.
func (a *App) checkDefaultCity() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := cities.Default(ctx, a.Pool, a.Cfg.DefaultCity)
	if err != nil {
		a.Log.Warn("no active city configured; public config has no map city", slog.Any("err", err))
		return
	}
	ref := strings.TrimSpace(a.Cfg.DefaultCity)
	if ref != "" && !strings.EqualFold(ref, c.Code) && !strings.EqualFold(ref, c.Name.EN) {
		a.Log.Warn("DEFAULT_CITY is not an active city, using fallback",
			slog.String("default_city", ref), slog.String("fallback", c.Code))
	}
}
