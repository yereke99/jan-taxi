package cities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicleclasses"
)

// Apps that may request public configuration.
var publicApps = []string{"passenger", "driver", "admin"}

// languages are the supported UI languages, Kazakh first.
var languages = []string{"kk", "ru", "en"}

// PublicOptions are the environment values the public config exposes.
type PublicOptions struct {
	PassengerBotUsername      string
	DriverBotUsername         string
	PassengerMiniAppShortName string
	DriverMiniAppShortName    string
	MapStyleURL               string
	DefaultCity               string // code ("EMB") or English name ("Embi")
	DefaultLanguage           string // "kk"
}

// PublicConfig is the runtime configuration served to the apps (§14).
type PublicConfig struct {
	App             string           `json:"app"`
	DefaultLanguage string           `json:"default_language"`
	Languages       []string         `json:"languages"`
	City            *PublicCity      `json:"city"`
	Map             PublicMap        `json:"map"`
	Routing         PublicRouting    `json:"routing"`
	Bots            PublicBots       `json:"bots"`
	Support         PublicSupport    `json:"support"`
	Delivery        PublicDelivery   `json:"delivery"`
	Navigation      PublicNavigation `json:"navigation"`
	// VehicleClasses are the active vehicle classes in display order (§21).
	VehicleClasses    []vehicleclasses.PublicClass `json:"vehicle_classes"`
	CargoSizes        []vehicleclasses.CargoSize   `json:"cargo_sizes"`
	MaxPassengerCount int                          `json:"max_passenger_count"`
	Fare              *Fare                        `json:"fare,omitempty"`  // passenger
	Kaspi             *settings.Kaspi              `json:"kaspi,omitempty"` // driver
	TopUp             *settings.TopUpLimits        `json:"topup,omitempty"` // driver
	// CancelReasons are the passenger's cancellation reasons in display
	// order (passenger, §22.3).
	CancelReasons []CancelReason `json:"cancel_reasons,omitempty"`
	// Commission is charged when a driver takes an order (driver, §22.2).
	Commission *settings.CommissionPolicy `json:"commission,omitempty"`
	// WelcomeBonus is credited once to a new driver on approval (driver,
	// §22.1; 0 when disabled).
	WelcomeBonus *int64 `json:"welcome_bonus,omitempty"`
}

// CancelReason is one passenger cancellation reason; a comment is required
// when CommentRequired.
type CancelReason struct {
	Code            string `json:"code"`
	Label           Name   `json:"label"`
	CommentRequired bool   `json:"comment_required"`
}

// cancelReasons are the reasons of ARCHITECTURE §22.3 (orders.cancel_reason_code).
var cancelReasons = []CancelReason{
	{Code: "long_wait", Label: Name{KK: "Ұзақ күттім", RU: "Долго ждать", EN: "Waited too long"}},
	{Code: "driver_issue", Label: Name{KK: "Жүргізуші ұнамады", RU: "Не понравился водитель", EN: "Didn't like the driver"}},
	{Code: "vehicle_issue", Label: Name{KK: "Көлік ұнамады", RU: "Не понравилась машина", EN: "Didn't like the car"}},
	{Code: "changed_plans", Label: Name{KK: "Жоспарым өзгерді", RU: "Планы изменились", EN: "My plans changed"}},
	{Code: "other", Label: Name{KK: "Басқа себеп", RU: "Другое", EN: "Other"}, CommentRequired: true},
}

// PublicCity is the default map city.
type PublicCity struct {
	Code   string  `json:"code"`
	Name   Name    `json:"name"`
	Center LatLon  `json:"center"`
	Zoom   float64 `json:"zoom"`
}

// PublicMap configures MapLibre.
type PublicMap struct {
	StyleURL string `json:"style_url"`
}

// PublicRouting limits route building.
type PublicRouting struct {
	MaxPoints int `json:"max_points"`
}

// PublicBots holds bot usernames and Mini App direct links.
type PublicBots struct {
	PassengerUsername  string `json:"passenger_username"`
	DriverUsername     string `json:"driver_username"`
	PassengerLink      string `json:"passenger_link"`
	DriverLink         string `json:"driver_link"`
	DriverRegisterLink string `json:"driver_register_link"`
}

// PublicSupport holds support contacts.
type PublicSupport struct {
	TelegramUsername string             `json:"telegram_username"`
	TelegramLink     string             `json:"telegram_link"`
	Phone            string             `json:"phone"`
	Hours            settings.Localized `json:"hours"`
}

// PublicDelivery lists prohibited item codes (translated by the apps).
type PublicDelivery struct {
	ProhibitedItems []string `json:"prohibited_items"`
}

// PublicNavigation configures external navigation links.
type PublicNavigation struct {
	TwoGISBaseURL string `json:"twogis_base_url"`
}

// Fare is the fare range of the default city's taxi tariff of the default
// vehicle class.
type Fare struct {
	Min  int `json:"min"`
	Max  int `json:"max"`
	Step int `json:"step"`
}

var (
	botUsernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)
	shortNameRe   = regexp.MustCompile(`^[A-Za-z0-9_]{3,30}$`)
	startParamRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// DirectLink builds a Mini App direct link that opens full screen:
// https://t.me/<bot>?startapp=<param>&mode=fullscreen for the bot's Main Mini
// App, or https://t.me/<bot>/<short>?startapp=<param>&mode=fullscreen for a
// named Mini App. An invalid start parameter is dropped (the app opens home);
// an invalid bot username yields "".
func DirectLink(botUsername, shortName, startParam string) string {
	bot := strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	if !botUsernameRe.MatchString(bot) {
		return ""
	}
	link := "https://t.me/" + bot
	if short := strings.TrimSpace(shortName); shortNameRe.MatchString(short) {
		link += "/" + short
	}
	if startParamRe.MatchString(startParam) {
		link += "?startapp=" + startParam
	} else {
		link += "?startapp"
	}
	return link + "&mode=fullscreen"
}

// PublicConfigService builds and serves GET /public/config.
type PublicConfigService struct {
	pool *pgxpool.Pool
	opts PublicOptions
}

// NewPublicConfig builds the public config service.
func NewPublicConfig(pool *pgxpool.Pool, opts PublicOptions) *PublicConfigService {
	return &PublicConfigService{pool: pool, opts: opts}
}

// MountPublic registers GET /public/config on the public router.
func (s *PublicConfigService) MountPublic(r chi.Router) {
	r.Get("/public/config", httpx.H(s.handle))
}

// Build assembles the configuration for app ("passenger" | "driver" | "admin").
func (s *PublicConfigService) Build(ctx context.Context, app string) (PublicConfig, error) {
	if !slices.Contains(publicApps, app) {
		return PublicConfig{}, httpx.Validation(map[string]string{"app": reasonInvalid})
	}
	q := s.pool
	lang := s.opts.DefaultLanguage
	if !slices.Contains(languages, lang) {
		lang = "kk"
	}
	cfg := PublicConfig{
		App: app, DefaultLanguage: lang, Languages: slices.Clone(languages),
		Map:     PublicMap{StyleURL: s.opts.MapStyleURL},
		Routing: PublicRouting{MaxPoints: settings.MaxRoutePoints(ctx, q)},
		Bots: PublicBots{
			PassengerUsername:  strings.TrimPrefix(s.opts.PassengerBotUsername, "@"),
			DriverUsername:     strings.TrimPrefix(s.opts.DriverBotUsername, "@"),
			PassengerLink:      DirectLink(s.opts.PassengerBotUsername, s.opts.PassengerMiniAppShortName, "home"),
			DriverLink:         DirectLink(s.opts.DriverBotUsername, s.opts.DriverMiniAppShortName, "home"),
			DriverRegisterLink: DirectLink(s.opts.DriverBotUsername, s.opts.DriverMiniAppShortName, "register"),
		},
		Delivery:          PublicDelivery{ProhibitedItems: nonNil(settings.ProhibitedItems(ctx, q))},
		Navigation:        PublicNavigation{TwoGISBaseURL: settings.TwoGISBaseURL(ctx, q)},
		CargoSizes:        slices.Clone(vehicleclasses.CargoSizes),
		MaxPassengerCount: vehicleclasses.MaxPassengerCount,
	}
	classes, err := vehicleclasses.Public(ctx, q)
	if err != nil {
		return PublicConfig{}, err
	}
	cfg.VehicleClasses = classes
	sup := settings.SupportContacts(ctx, q)
	cfg.Support = PublicSupport{TelegramUsername: sup.TelegramUsername, Phone: sup.Phone, Hours: sup.Hours}
	if sup.TelegramUsername != "" {
		cfg.Support.TelegramLink = "https://t.me/" + sup.TelegramUsername
	}

	city, err := Default(ctx, q, s.opts.DefaultCity)
	switch {
	case err == nil:
		cfg.City = &PublicCity{Code: city.Code, Name: city.Name, Center: toLatLon(city.Center), Zoom: city.DefaultZoom}
	case !errors.Is(err, ErrNoActiveCity):
		return PublicConfig{}, err
	}

	switch app {
	case "passenger":
		cfg.CancelReasons = slices.Clone(cancelReasons)
		if cfg.City != nil {
			fare, err := taxiFare(ctx, q, city)
			if err != nil {
				return PublicConfig{}, err
			}
			cfg.Fare = fare
		}
	case "driver":
		k := settings.KaspiPayment(ctx, q)
		t := settings.TopUp(ctx, q)
		c := settings.Commission(ctx, q)
		b := settings.WelcomeBonus(ctx, q)
		cfg.Kaspi, cfg.TopUp, cfg.Commission, cfg.WelcomeBonus = &k, &t, &c, &b
	}
	return cfg, nil
}

func taxiFare(ctx context.Context, q db.Querier, c City) (*Fare, error) {
	var f Fare
	err := q.QueryRow(ctx, `
		SELECT min_fare, max_fare, fare_step FROM tariffs
		WHERE city_id = $1 AND category = 'taxi' AND vehicle_class = $2 AND is_active`, c.ID, vehicleclasses.Default).
		Scan(&f.Min, &f.Max, &f.Step)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load taxi tariff: %w", err)
	}
	return &f, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *PublicConfigService) handle(w http.ResponseWriter, r *http.Request) error {
	cfg, err := s.Build(r.Context(), strings.TrimSpace(r.URL.Query().Get("app")))
	if err != nil {
		return err
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return httpx.Internal(err)
	}
	// Not httpx.WriteJSON: that marks every response no-store, while this
	// document is public and safe to cache briefly.
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(b, '\n'))
	return nil
}
