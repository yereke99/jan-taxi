package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/commissionareas"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/config"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
	"github.com/yereke99/jantaxi/admin/backend/internal/routing"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicleclasses"
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

	// Arrival estimates of drivers who took an order (§22.6): the road time
	// from the driver's latest fix to pickup A, recomputed while they drive.
	a.ETA = dispatch.NewArrivalETA(a.Pool, func(ctx context.Context, from, to geo.Point) (int, error) {
		r, err := osrm.Route(ctx, []geo.Point{from, to})
		return r.DurationS, err
	}, a.Log)
	eta := a.ETA
	a.AddJob("driver_eta", dispatch.ETAJobInterval, func(ctx context.Context) error {
		_, err := eta.RefreshDue(ctx)
		return err
	})

	pricing.NewAdminHandler(a.Pool).MountAdmin(a.Admin)        // /admin/tariffs
	vehicleclasses.NewAdminHandler(a.Pool).MountAdmin(a.Admin) // /admin/vehicle-classes
	// /admin/commission-areas: villages with their own driver commission.
	commissionareas.NewAdminHandler(a.Pool, commissionGeocoder{places}).MountAdmin(a.Admin)

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

// commissionGeocoder gives the village commission editor the map search:
// places by name and the settlements around a point.
type commissionGeocoder struct{ places *routing.Places }

func (g commissionGeocoder) SearchPlaces(ctx context.Context, query, lang string, limit int) ([]commissionareas.Place, error) {
	found, err := g.places.Search(ctx, query, nil, lang, limit)
	if err != nil {
		return nil, err
	}
	out := make([]commissionareas.Place, 0, len(found))
	for _, p := range found {
		out = append(out, commissionareas.Place{Title: p.Title, Subtitle: p.Subtitle, Lat: p.Point.Lat, Lon: p.Point.Lon})
	}
	return out, nil
}

func (g commissionGeocoder) SettlementsNear(ctx context.Context, p geo.Point, radiusKm float64, limit int) ([]commissionareas.Settlement, error) {
	list := g.places.Settlements(ctx, p, radiusKm, limit)
	out := make([]commissionareas.Settlement, 0, len(list))
	for _, s := range list {
		c := commissionareas.Settlement{Name: s.Name, Kind: s.Kind, Point: s.Point}
		if len(s.Extent) == 4 {
			c.Extent = &[4]float64{s.Extent[0], s.Extent[1], s.Extent[2], s.Extent[3]}
		}
		out = append(out, c)
	}
	return out, nil
}

func isPublicPhoton(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.EqualFold(u.Hostname(), "photon.komoot.io")
}
