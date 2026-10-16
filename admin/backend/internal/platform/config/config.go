// Package config loads the API configuration from environment variables and
// validates it. Production is strict (real bot tokens, long secrets, https
// URLs); development and test are relaxed and report problems as warnings.
package config

import (
	"net/netip"
	"time"
)

// Environments accepted in APP_ENV.
const (
	EnvProduction  = "production"
	EnvDevelopment = "development"
	EnvTest        = "test"
)

// Geocoding providers accepted in GEOCODING_PROVIDER.
const (
	GeocoderPhoton    = "photon"
	GeocoderNominatim = "nominatim"
	GeocoderTwoGIS    = "twogis"
	GeocoderNone      = "none"
)

// MinSecretBytes is the minimum length of SESSION_SECRET and
// INTERNAL_BOT_SERVICE_SECRET in production.
const MinSecretBytes = 32

// MaxRoutePointsLimit is the hard upper bound for MAX_ROUTE_POINTS.
const MaxRoutePointsLimit = 5

// Config is the complete runtime configuration of the API process.
type Config struct {
	Env                   string // "production" | "development" | "test" (APP_ENV)
	APIPort, InternalPort int    // API_PORT=8080, INTERNAL_PORT=8081

	DatabaseURL      string
	DatabaseMaxConns int32

	PassengerBotToken, PassengerBotUsername string
	DriverBotToken, DriverBotUsername       string

	// Optional named Mini Apps; empty means the bot's Main Mini App.
	PassengerMiniAppShortName, DriverMiniAppShortName string

	PassengerMiniAppURL, DriverMiniAppURL, AdminFrontendURL, APIPublicURL string

	InternalBotServiceSecret, SessionSecret string

	TelegramInitDataMaxAge  time.Duration // 24h
	AppTokenTTL             time.Duration // 12h
	AdminSessionIdleTTL     time.Duration // 12h
	AdminSessionAbsoluteTTL time.Duration // 168h
	AdminCookieSecure       bool

	AdminBootstrapEmail, AdminBootstrapPassword string

	// Origins of the Mini App URLs and the admin URL plus CORS_ALLOWED_ORIGINS.
	CORSAllowedOrigins []string
	TrustedProxies     []netip.Prefix

	FileStorageDir string // /data/files
	MaxUploadBytes int64  // 10 MiB

	KaspiPayPaymentURL, KaspiPayRecipientName, KaspiPayPaymentInstructions string

	MapStyleURL, MapAPIKey string

	GeocodingProvider string // photon | nominatim | twogis | none
	GeocodingBaseURL  string
	GeocodingAPIKey   string

	OSRMBaseURL string
	OSRMTimeout time.Duration

	TwoGISBaseURL string // https://2gis.ru

	DefaultCity              string // city code, e.g. "EMB"
	DefaultLanguage          string // "kk"
	MaxRoutePoints           int    // 2..5
	DefaultCommissionPercent int    // 10

	DriverOnlineIdleTimeout time.Duration // 12h

	TelegramAPIBaseURL string // https://api.telegram.org

	NotificationWorkerEnabled, JobsEnabled bool

	LogLevel, LogFormat string // info, json

	// Warnings lists non-fatal problems found by Load (relaxed checks outside
	// production). The caller logs them at startup.
	Warnings []string
}

// IsProduction reports whether strict production rules apply.
func (c Config) IsProduction() bool { return c.Env == EnvProduction }

// DefaultCommissionBps returns DefaultCommissionPercent in basis points.
func (c Config) DefaultCommissionBps() int { return c.DefaultCommissionPercent * 100 }

// defaultTrustedProxies are loopback and private networks, where the reverse
// proxy (Caddy on the Docker network) lives.
var defaultTrustedProxies = []string{
	"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
}

// DefaultTrustedProxies returns a fresh copy of the default trusted proxy ranges.
func DefaultTrustedProxies() []netip.Prefix {
	out := make([]netip.Prefix, 0, len(defaultTrustedProxies))
	for _, s := range defaultTrustedProxies {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}
