package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"
)

// Load reads the configuration from the process environment.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom reads the configuration through getenv (tests pass a map lookup).
// It returns every validation problem joined into one error.
func LoadFrom(getenv func(string) string) (Config, error) {
	e := &envReader{getenv: getenv}

	env := strings.ToLower(e.str("APP_ENV", EnvProduction))
	switch env {
	case EnvProduction, EnvDevelopment, EnvTest:
	default:
		return Config{}, errors.New("config: APP_ENV must be production, development or test")
	}
	prod := env == EnvProduction

	c := Config{
		Env:          env,
		APIPort:      e.integer("API_PORT", 8080),
		InternalPort: e.integer("INTERNAL_PORT", 8081),

		DatabaseURL:      e.raw("DATABASE_URL"),
		DatabaseMaxConns: int32(e.integer("DATABASE_MAX_CONNS", 25)),

		PassengerBotToken:    e.raw("PASSENGER_BOT_TOKEN"),
		PassengerBotUsername: strings.TrimPrefix(e.raw("PASSENGER_BOT_USERNAME"), "@"),
		DriverBotToken:       e.raw("DRIVER_BOT_TOKEN"),
		DriverBotUsername:    strings.TrimPrefix(e.raw("DRIVER_BOT_USERNAME"), "@"),

		PassengerMiniAppShortName: e.raw("PASSENGER_MINIAPP_SHORT_NAME"),
		DriverMiniAppShortName:    e.raw("DRIVER_MINIAPP_SHORT_NAME"),

		PassengerMiniAppURL: e.raw("PASSENGER_MINIAPP_URL"),
		DriverMiniAppURL:    e.raw("DRIVER_MINIAPP_URL"),
		AdminFrontendURL:    e.raw("ADMIN_FRONTEND_URL"),
		APIPublicURL:        e.raw("API_PUBLIC_URL"),

		InternalBotServiceSecret: e.raw("INTERNAL_BOT_SERVICE_SECRET"),
		SessionSecret:            e.raw("SESSION_SECRET"),

		TelegramInitDataMaxAge:  e.duration("TELEGRAM_INITDATA_MAX_AGE", 24*time.Hour),
		AppTokenTTL:             e.duration("APP_TOKEN_TTL", 12*time.Hour),
		AdminSessionIdleTTL:     e.duration("ADMIN_SESSION_IDLE_TTL", 12*time.Hour),
		AdminSessionAbsoluteTTL: e.duration("ADMIN_SESSION_ABSOLUTE_TTL", 168*time.Hour),
		AdminCookieSecure:       e.boolean("ADMIN_COOKIE_SECURE", prod),

		AdminBootstrapEmail:    strings.ToLower(e.raw("ADMIN_BOOTSTRAP_EMAIL")),
		AdminBootstrapPassword: e.getenv("ADMIN_BOOTSTRAP_PASSWORD"), // not trimmed: spaces are legal in passwords

		FileStorageDir: e.str("FILE_STORAGE_DIR", defaultStorageDir(prod)),
		MaxUploadBytes: e.int64("MAX_UPLOAD_BYTES", 10<<20),

		KaspiPayPaymentURL:          e.raw("KASPI_PAY_PAYMENT_URL"),
		KaspiPayRecipientName:       e.raw("KASPI_PAY_RECIPIENT_NAME"),
		KaspiPayPaymentInstructions: e.raw("KASPI_PAY_PAYMENT_INSTRUCTIONS"),

		MapStyleURL: e.str("MAP_STYLE_URL", "https://tiles.openfreemap.org/styles/positron"),
		MapAPIKey:   e.raw("MAP_API_KEY"),

		GeocodingProvider: strings.ToLower(e.str("GEOCODING_PROVIDER", GeocoderPhoton)),
		GeocodingBaseURL:  e.raw("GEOCODING_BASE_URL"),
		GeocodingAPIKey:   e.raw("GEOCODING_API_KEY"),

		OSRMBaseURL: e.str("OSRM_BASE_URL", "http://osrm:5000"),
		OSRMTimeout: e.duration("OSRM_TIMEOUT", 5*time.Second),

		TwoGISBaseURL: e.str("TWOGIS_BASE_URL", "https://2gis.ru"),

		DefaultCity:              e.str("DEFAULT_CITY", "EMB"),
		DefaultLanguage:          strings.ToLower(e.str("DEFAULT_LANGUAGE", "kk")),
		MaxRoutePoints:           e.integer("MAX_ROUTE_POINTS", MaxRoutePointsLimit),
		DefaultCommissionPercent: e.integer("DEFAULT_COMMISSION_PERCENT", 10),

		DriverOnlineIdleTimeout: e.duration("DRIVER_ONLINE_IDLE_TIMEOUT", 12*time.Hour),

		TelegramAPIBaseURL: strings.TrimRight(e.str("TELEGRAM_API_BASE_URL", "https://api.telegram.org"), "/"),

		NotificationWorkerEnabled: e.boolean("NOTIFICATION_WORKER_ENABLED", true),
		JobsEnabled:               e.boolean("JOBS_ENABLED", true),

		VerificationEnabled:       e.boolean("VERIFICATION_ENABLED", false),
		VerificationKey:           e.raw("VERIFICATION_KEY"),
		VerificationAutoApprove:   e.boolean("VERIFICATION_AUTO_APPROVE", true),
		VerificationMinConfidence: e.decimal("VERIFICATION_MIN_CONFIDENCE", 98),
		VerificationQueueLimit:    e.integer("VERIFICATION_QUEUE_LIMIT", 1000),

		LogLevel:  strings.ToLower(e.str("LOG_LEVEL", "info")),
		LogFormat: strings.ToLower(e.str("LOG_FORMAT", "json")),
	}

	if c.GeocodingBaseURL == "" {
		c.GeocodingBaseURL = defaultGeocoderURL(c.GeocodingProvider)
	}
	if !prod {
		c.applyDevDefaults()
	}

	c.TrustedProxies = parseProxies(e, e.list("TRUSTED_PROXIES"))
	extraOrigins := e.list("CORS_ALLOWED_ORIGINS")

	city, err := normalizeCity(c.DefaultCity)
	if err != nil {
		e.fail("DEFAULT_CITY", "%v", err)
	}
	c.DefaultCity = city

	v := &validator{prod: prod}
	v.errs = append(v.errs, e.errs...)
	v.check(&c)
	c.CORSAllowedOrigins = v.origins(c, extraOrigins)
	if !prod {
		c.fillDevSecrets(v)
	}

	if len(v.errs) > 0 {
		return Config{}, fmt.Errorf("config: %w", errors.Join(v.errs...))
	}
	c.Warnings = v.warns
	return c, nil
}

func defaultStorageDir(prod bool) string {
	if prod {
		return "/data/files"
	}
	return "./data/files"
}

func defaultGeocoderURL(provider string) string {
	switch provider {
	case GeocoderPhoton:
		return "https://photon.komoot.io"
	case GeocoderNominatim:
		return "https://nominatim.openstreetmap.org"
	case GeocoderTwoGIS:
		return "https://catalog.api.2gis.com"
	default:
		return ""
	}
}

// applyDevDefaults points the URLs at the local Vite dev servers so a fresh
// checkout runs without a full .env file.
func (c *Config) applyDevDefaults() {
	if c.PassengerMiniAppURL == "" {
		c.PassengerMiniAppURL = "http://localhost:5173/passenger/"
	}
	if c.DriverMiniAppURL == "" {
		c.DriverMiniAppURL = "http://localhost:5174/driver/"
	}
	if c.AdminFrontendURL == "" {
		c.AdminFrontendURL = "http://localhost:5175/"
	}
	if c.APIPublicURL == "" {
		c.APIPublicURL = fmt.Sprintf("http://localhost:%d", c.APIPort)
	}
}

// fillDevSecrets replaces missing secrets with random per-process values.
// An empty shared secret must never reach the auth code, where a naive
// comparison could accept an empty bearer token.
func (c *Config) fillDevSecrets(v *validator) {
	if c.SessionSecret == "" {
		c.SessionSecret = randomSecret()
		v.warn("SESSION_SECRET is not set; using a random per-process secret, so app tokens and admin sessions do not survive restarts")
	}
	if c.InternalBotServiceSecret == "" {
		c.InternalBotServiceSecret = randomSecret()
		v.warn("INTERNAL_BOT_SERVICE_SECRET is not set; using a random per-process secret, so the bots cannot call the internal API")
	}
}

func randomSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// normalizeCity accepts a three-letter city code or a known city name.
func normalizeCity(s string) (string, error) {
	s = strings.TrimSpace(s)
	if code, ok := knownCityNames[strings.ToLower(s)]; ok {
		return code, nil
	}
	if len(s) == 3 && isASCIILetters(s) {
		return strings.ToUpper(s), nil
	}
	return "", errors.New("must be a three-letter city code such as EMB")
}

var knownCityNames = map[string]string{
	"embi": "EMB", "emba": "EMB", "ембі": "EMB", "эмба": "EMB",
}

func isASCIILetters(s string) bool {
	for i := 0; i < len(s); i++ {
		ch := s[i] | 0x20
		if ch < 'a' || ch > 'z' {
			return false
		}
	}
	return true
}

// parseProxies parses TRUSTED_PROXIES: CIDRs or single addresses; "none"
// disables proxy trust; empty means the private-network defaults.
func parseProxies(e *envReader, items []string) []netip.Prefix {
	if len(items) == 0 {
		return DefaultTrustedProxies()
	}
	if len(items) == 1 && strings.EqualFold(items[0], "none") {
		return []netip.Prefix{}
	}
	out := make([]netip.Prefix, 0, len(items))
	for _, s := range items {
		if strings.Contains(s, "/") {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				e.fail("TRUSTED_PROXIES", "invalid CIDR %q", s)
				continue
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			e.fail("TRUSTED_PROXIES", "invalid address %q", s)
			continue
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out
}
