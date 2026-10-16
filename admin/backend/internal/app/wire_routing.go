package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
	"github.com/yereke99/jantaxi/admin/backend/internal/routing"
)

// embiCenter is the map default when no active city is configured yet.
var embiCenter = geo.Point{Lat: 48.8264, Lon: 58.1442}

// Unused fare quotes are deleted this long after they expire.
const quoteRetention = 24 * time.Hour

// wireRouting constructs the routing, geocoding and pricing services and mounts their routes.
func (a *App) wireRouting() error {
	osrm, err := routing.NewOSRM(routing.OSRMConfig{
		BaseURL: a.Cfg.OSRMBaseURL,
		Timeout: a.Cfg.OSRMTimeout,
	})
	if err != nil {
		return err
	}

	provider, name, err := routing.NewProvider(routing.ProviderConfig{
		Provider:    a.Cfg.GeocodingProvider,
		BaseURL:     a.Cfg.GeocodingBaseURL,
		APIKey:      a.Cfg.GeocodingAPIKey,
		DefaultBias: a.defaultMapCenter(),
	})
	if err != nil {
		if a.Cfg.IsProduction() {
			return fmt.Errorf("geocoding: %w", err)
		}
		a.Log.Warn("geocoding provider disabled; only curated places and coordinates are available", slog.Any("err", err))
		provider, name = routing.None{}, config.GeocoderNone
	}
	if name == routing.SourcePhoton && isPublicPhoton(a.Cfg.GeocodingBaseURL) && a.Cfg.IsProduction() {
		a.Log.Warn("GEOCODING_BASE_URL is the public Photon server, a fair-use service without SLA; self-host Photon for production")
	}

	places := routing.NewPlaces(a.Pool, routing.PlacesConfig{Provider: provider, ProviderName: name, Log: a.Log})
	h := routing.NewHandler(routing.HandlerConfig{
		Pool: a.Pool, Router: osrm, Places: places, MaxRoutePoints: a.Cfg.MaxRoutePoints, Log: a.Log,
	})
	h.MountApp(a.AnyApp)          // GET /geo/search, GET /geo/reverse
	h.MountPassenger(a.Passenger) // POST /routes/preview

	pricing.NewAdminHandler(a.Pool).MountAdmin(a.Admin) // /admin/tariffs

	a.AddJob("quote_cleanup", time.Hour, func(ctx context.Context) error {
		n, err := pricing.CleanupQuotes(ctx, a.Pool, quoteRetention)
		if n > 0 {
			a.Log.Info("deleted expired fare quotes", slog.Int64("count", n))
		}
		return err
	})
	return nil
}

// defaultMapCenter returns the default city's centre (Embi when none).
func (a *App) defaultMapCenter() geo.Point {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c, err := cities.Default(ctx, a.Pool, a.Cfg.DefaultCity); err == nil {
		return c.Center
	}
	return embiCenter
}

func isPublicPhoton(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.EqualFold(u.Hostname(), "photon.komoot.io")
}
