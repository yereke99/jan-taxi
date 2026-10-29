package config

import (
	"encoding/base64"
	"fmt"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	botTokenRe    = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{30,}$`)
	botUsernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)
	shortNameRe   = regexp.MustCompile(`^[A-Za-z0-9_]{3,30}$`)
)

// validator collects errors (fatal) and warnings (logged). In production every
// problem is an error; outside production some become warnings.
type validator struct {
	prod  bool
	errs  []error
	warns []string
}

func (v *validator) fail(key, format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf("%s: %s", key, fmt.Sprintf(format, args...)))
}

func (v *validator) warn(msg string) { v.warns = append(v.warns, msg) }

// strict reports a production error or a development warning.
func (v *validator) strict(key, msg string) {
	if v.prod {
		v.fail(key, "%s", msg)
		return
	}
	v.warn(key + ": " + msg)
}

func (v *validator) check(c *Config) {
	v.checkPorts(c)
	if c.DatabaseURL == "" {
		v.fail("DATABASE_URL", "is required")
	}
	if c.DatabaseMaxConns < 2 || c.DatabaseMaxConns > 1000 {
		v.fail("DATABASE_MAX_CONNS", "must be between 2 and 1000")
	}
	v.checkBots(c)
	v.checkURLs(c)
	v.checkSecrets(c)
	v.checkDurations(c)
	v.checkBootstrap(c)
	v.checkStorage(c)
	v.checkProduct(c)
	v.checkIntegrations(c)
	v.checkVerification(c)

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		v.fail("LOG_LEVEL", "must be debug, info, warn or error")
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		v.fail("LOG_FORMAT", "must be json or text")
	}
}

func (v *validator) checkVerification(c *Config) {
	if c.VerificationMinConfidence < 90 || c.VerificationMinConfidence > 100 {
		v.fail("VERIFICATION_MIN_CONFIDENCE", "must be between 90 and 100")
	}
	if c.VerificationQueueLimit < 1 || c.VerificationQueueLimit > 100000 {
		v.fail("VERIFICATION_QUEUE_LIMIT", "must be between 1 and 100000")
	}
	if c.VerificationEnabled || c.VerificationKey != "" {
		key, err := base64.StdEncoding.DecodeString(c.VerificationKey)
		if err != nil || len(key) != 32 {
			v.fail("VERIFICATION_KEY", "must be a base64 encoded 32-byte key when verification is enabled")
		}
	}
}

func (v *validator) checkPorts(c *Config) {
	if c.APIPort < 1 || c.APIPort > 65535 {
		v.fail("API_PORT", "must be between 1 and 65535")
	}
	if c.InternalPort < 1 || c.InternalPort > 65535 {
		v.fail("INTERNAL_PORT", "must be between 1 and 65535")
	}
	if c.APIPort == c.InternalPort {
		v.fail("INTERNAL_PORT", "must differ from API_PORT")
	}
}

func (v *validator) checkBots(c *Config) {
	bots := []struct{ tokenKey, token, userKey, user string }{
		{"PASSENGER_BOT_TOKEN", c.PassengerBotToken, "PASSENGER_BOT_USERNAME", c.PassengerBotUsername},
		{"DRIVER_BOT_TOKEN", c.DriverBotToken, "DRIVER_BOT_USERNAME", c.DriverBotUsername},
	}
	for _, b := range bots {
		switch {
		case b.token == "":
			v.strict(b.tokenKey, "is not set; Mini App login and notifications for this bot are disabled")
		case !botTokenRe.MatchString(b.token):
			v.fail(b.tokenKey, "is not a valid Telegram bot token")
		}
		switch {
		case b.user == "":
			v.strict(b.userKey, "is not set")
		case !botUsernameRe.MatchString(b.user) || !strings.HasSuffix(strings.ToLower(b.user), "bot"):
			v.fail(b.userKey, "must be a Telegram bot username (5-32 characters, ending in \"bot\")")
		}
	}
	// One token for both apps would let a passenger initData authenticate as a driver.
	if c.PassengerBotToken != "" && c.PassengerBotToken == c.DriverBotToken {
		v.strict("DRIVER_BOT_TOKEN", "must belong to a different bot than PASSENGER_BOT_TOKEN")
	}
	for key, name := range map[string]string{
		"PASSENGER_MINIAPP_SHORT_NAME": c.PassengerMiniAppShortName,
		"DRIVER_MINIAPP_SHORT_NAME":    c.DriverMiniAppShortName,
	} {
		if name != "" && !shortNameRe.MatchString(name) {
			v.fail(key, "must be 3-30 characters of letters, digits and underscores")
		}
	}
}

func (v *validator) checkURLs(c *Config) {
	public := []struct{ key, val string }{
		{"PASSENGER_MINIAPP_URL", c.PassengerMiniAppURL},
		{"DRIVER_MINIAPP_URL", c.DriverMiniAppURL},
		{"ADMIN_FRONTEND_URL", c.AdminFrontendURL},
		{"API_PUBLIC_URL", c.APIPublicURL},
	}
	for _, p := range public {
		if p.val == "" {
			v.fail(p.key, "is required")
			continue
		}
		v.checkURL(p.key, p.val, v.prod)
	}
	if c.KaspiPayPaymentURL != "" {
		v.checkURL("KASPI_PAY_PAYMENT_URL", c.KaspiPayPaymentURL, v.prod)
	}
	v.checkURL("MAP_STYLE_URL", c.MapStyleURL, v.prod)
	v.checkURL("TWOGIS_BASE_URL", c.TwoGISBaseURL, false)
	v.checkURL("OSRM_BASE_URL", c.OSRMBaseURL, false)
	v.checkURL("TELEGRAM_API_BASE_URL", c.TelegramAPIBaseURL, false)
	if c.GeocodingBaseURL != "" {
		v.checkURL("GEOCODING_BASE_URL", c.GeocodingBaseURL, false)
	}
}

// checkURL requires an absolute http(s) URL without credentials or fragment;
// requireHTTPS additionally demands https.
func (v *validator) checkURL(key, raw string, requireHTTPS bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		v.fail(key, "must be an absolute http(s) URL")
		return
	}
	if u.User != nil {
		v.fail(key, "must not contain credentials")
	}
	if u.Fragment != "" {
		v.fail(key, "must not contain a fragment")
	}
	if requireHTTPS && u.Scheme != "https" {
		v.fail(key, "must use https in production")
	}
}

func (v *validator) checkSecrets(c *Config) {
	for _, s := range []struct{ key, val string }{
		{"SESSION_SECRET", c.SessionSecret},
		{"INTERNAL_BOT_SERVICE_SECRET", c.InternalBotServiceSecret},
	} {
		switch {
		case s.val == "" && v.prod:
			v.fail(s.key, "is required")
		case s.val != "" && len(s.val) < MinSecretBytes:
			v.strict(s.key, fmt.Sprintf("must be at least %d bytes", MinSecretBytes))
		}
	}
	if c.SessionSecret != "" && c.SessionSecret == c.InternalBotServiceSecret {
		v.strict("INTERNAL_BOT_SERVICE_SECRET", "must differ from SESSION_SECRET")
	}
	if v.prod && !c.AdminCookieSecure {
		v.fail("ADMIN_COOKIE_SECURE", "must be true in production")
	}
}

func (v *validator) checkDurations(c *Config) {
	if c.AdminSessionIdleTTL > c.AdminSessionAbsoluteTTL {
		v.fail("ADMIN_SESSION_IDLE_TTL", "must not exceed ADMIN_SESSION_ABSOLUTE_TTL")
	}
	if c.OSRMTimeout > time.Minute {
		v.fail("OSRM_TIMEOUT", "must not exceed 60s")
	}
}

func (v *validator) checkBootstrap(c *Config) {
	email, pw := c.AdminBootstrapEmail, c.AdminBootstrapPassword
	if email == "" && pw == "" {
		return
	}
	if email == "" || pw == "" {
		v.fail("ADMIN_BOOTSTRAP_EMAIL", "ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD must be set together")
		return
	}
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		v.fail("ADMIN_BOOTSTRAP_EMAIL", "must be a plain email address")
	}
	if n := len([]rune(pw)); n < 12 || n > 256 {
		v.fail("ADMIN_BOOTSTRAP_PASSWORD", "must be 12-256 characters")
	}
}

func (v *validator) checkStorage(c *Config) {
	if c.FileStorageDir == "" {
		v.fail("FILE_STORAGE_DIR", "is required")
	} else if v.prod && !filepath.IsAbs(c.FileStorageDir) {
		v.fail("FILE_STORAGE_DIR", "must be an absolute path in production")
	}
	if c.MaxUploadBytes < 64<<10 || c.MaxUploadBytes > 64<<20 {
		v.fail("MAX_UPLOAD_BYTES", "must be between 64 KiB and 64 MiB")
	}
}

func (v *validator) checkProduct(c *Config) {
	if c.MaxRoutePoints < 2 || c.MaxRoutePoints > MaxRoutePointsLimit {
		v.fail("MAX_ROUTE_POINTS", "must be between 2 and %d", MaxRoutePointsLimit)
	}
	if c.DefaultCommissionPercent < 0 || c.DefaultCommissionPercent > 50 {
		v.fail("DEFAULT_COMMISSION_PERCENT", "must be between 0 and 50")
	}
	switch c.DefaultLanguage {
	case "kk", "ru", "en":
	default:
		v.fail("DEFAULT_LANGUAGE", "must be kk, ru or en")
	}
}

func (v *validator) checkIntegrations(c *Config) {
	switch c.GeocodingProvider {
	case GeocoderPhoton, GeocoderNominatim, GeocoderNone:
	case GeocoderTwoGIS:
		if c.GeocodingAPIKey == "" {
			v.strict("GEOCODING_API_KEY", "is required for the twogis provider")
		}
	default:
		v.fail("GEOCODING_PROVIDER", "must be photon, nominatim, twogis or none")
	}
	if c.KaspiPayPaymentURL == "" || c.KaspiPayRecipientName == "" {
		v.warn("KASPI_PAY_PAYMENT_URL or KASPI_PAY_RECIPIENT_NAME is not set; drivers will not see Kaspi payment details for top-ups")
	}
}

// origins derives the CORS allow-list from the frontend URLs plus extra origins.
func (v *validator) origins(c Config, extra []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(o string) {
		if o != "" && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	for _, raw := range []string{c.PassengerMiniAppURL, c.DriverMiniAppURL, c.AdminFrontendURL} {
		if o, err := Origin(raw); err == nil {
			add(o)
		}
	}
	for _, raw := range extra {
		if raw == "*" {
			v.fail("CORS_ALLOWED_ORIGINS", "wildcard origins are not allowed")
			continue
		}
		o, err := Origin(raw)
		if err != nil {
			v.fail("CORS_ALLOWED_ORIGINS", "invalid origin %q", raw)
			continue
		}
		if u, _ := url.Parse(raw); u != nil && strings.TrimRight(u.Path, "/") != "" {
			v.fail("CORS_ALLOWED_ORIGINS", "origin %q must not contain a path", raw)
			continue
		}
		if v.prod && strings.HasPrefix(o, "http://") {
			v.fail("CORS_ALLOWED_ORIGINS", "origin %q must use https in production", raw)
			continue
		}
		add(o)
	}
	return out
}

// Origin returns the browser origin (scheme://host[:port]) of an absolute
// http(s) URL, lower-cased and without default ports.
func Origin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("not an absolute http(s) URL")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		return scheme + "://" + host + ":" + port, nil
	}
	return scheme + "://" + host, nil
}
