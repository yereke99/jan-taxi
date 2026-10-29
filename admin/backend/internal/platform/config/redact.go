package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

// LogValue renders the configuration for structured logs. Secrets appear only
// as "set" or "unset"; the database URL is reduced to user@host/dbname.
func (c Config) LogValue() slog.Value { return slog.GroupValue(c.safeAttrs()...) }

// String renders the configuration without secrets (also used by %v and %+v).
func (c Config) String() string {
	var b strings.Builder
	b.WriteString("Config{")
	for i, a := range c.safeAttrs() {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s=%v", a.Key, a.Value)
	}
	b.WriteString("}")
	return b.String()
}

// GoString keeps %#v from printing secret fields.
func (c Config) GoString() string { return c.String() }

func (c Config) safeAttrs() []slog.Attr {
	proxies := make([]string, len(c.TrustedProxies))
	for i, p := range c.TrustedProxies {
		proxies[i] = p.String()
	}
	return []slog.Attr{
		slog.String("env", c.Env),
		slog.Int("api_port", c.APIPort),
		slog.Int("internal_port", c.InternalPort),
		slog.String("database", redactDatabaseURL(c.DatabaseURL)),
		slog.Int("database_max_conns", int(c.DatabaseMaxConns)),
		slog.String("passenger_bot_token", presence(c.PassengerBotToken)),
		slog.String("passenger_bot_username", c.PassengerBotUsername),
		slog.String("driver_bot_token", presence(c.DriverBotToken)),
		slog.String("driver_bot_username", c.DriverBotUsername),
		slog.String("passenger_miniapp_short_name", c.PassengerMiniAppShortName),
		slog.String("driver_miniapp_short_name", c.DriverMiniAppShortName),
		slog.String("passenger_miniapp_url", c.PassengerMiniAppURL),
		slog.String("driver_miniapp_url", c.DriverMiniAppURL),
		slog.String("admin_frontend_url", c.AdminFrontendURL),
		slog.String("api_public_url", c.APIPublicURL),
		slog.String("internal_bot_service_secret", presence(c.InternalBotServiceSecret)),
		slog.String("session_secret", presence(c.SessionSecret)),
		slog.Duration("telegram_initdata_max_age", c.TelegramInitDataMaxAge),
		slog.Duration("app_token_ttl", c.AppTokenTTL),
		slog.Duration("admin_session_idle_ttl", c.AdminSessionIdleTTL),
		slog.Duration("admin_session_absolute_ttl", c.AdminSessionAbsoluteTTL),
		slog.Bool("admin_cookie_secure", c.AdminCookieSecure),
		slog.String("admin_bootstrap_email", presence(c.AdminBootstrapEmail)),
		slog.String("admin_bootstrap_password", presence(c.AdminBootstrapPassword)),
		slog.String("cors_allowed_origins", strings.Join(c.CORSAllowedOrigins, ",")),
		slog.String("trusted_proxies", strings.Join(proxies, ",")),
		slog.String("file_storage_dir", c.FileStorageDir),
		slog.Int64("max_upload_bytes", c.MaxUploadBytes),
		slog.String("kaspi_pay_payment_url", c.KaspiPayPaymentURL),
		slog.String("kaspi_pay_recipient_name", c.KaspiPayRecipientName),
		slog.String("map_style_url", c.MapStyleURL),
		slog.String("map_api_key", presence(c.MapAPIKey)),
		slog.String("geocoding_provider", c.GeocodingProvider),
		slog.String("geocoding_base_url", c.GeocodingBaseURL),
		slog.String("geocoding_api_key", presence(c.GeocodingAPIKey)),
		slog.String("osrm_base_url", c.OSRMBaseURL),
		slog.Duration("osrm_timeout", c.OSRMTimeout),
		slog.String("twogis_base_url", c.TwoGISBaseURL),
		slog.String("default_city", c.DefaultCity),
		slog.String("default_language", c.DefaultLanguage),
		slog.Int("max_route_points", c.MaxRoutePoints),
		slog.Int("default_commission_percent", c.DefaultCommissionPercent),
		slog.Duration("driver_online_idle_timeout", c.DriverOnlineIdleTimeout),
		slog.String("telegram_api_base_url", c.TelegramAPIBaseURL),
		slog.Bool("notification_worker_enabled", c.NotificationWorkerEnabled),
		slog.Bool("jobs_enabled", c.JobsEnabled),
		slog.Bool("verification_enabled", c.VerificationEnabled),
		slog.String("verification_key", presence(c.VerificationKey)),
		slog.Bool("verification_auto_approve", c.VerificationAutoApprove),
		slog.Float64("verification_min_confidence", c.VerificationMinConfidence),
		slog.Int("verification_queue_limit", c.VerificationQueueLimit),
		slog.String("log_level", c.LogLevel),
		slog.String("log_format", c.LogFormat),
	}
}

func presence(s string) string {
	if s == "" {
		return "unset"
	}
	return "set"
}

// redactDatabaseURL keeps only user, host and database name. Passwords can sit
// in the userinfo or in query parameters, so both are dropped.
func redactDatabaseURL(raw string) string {
	if raw == "" {
		return "unset"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "set (unparsed)"
	}
	user := ""
	if u.User != nil && u.User.Username() != "" {
		user = u.User.Username() + "@"
	}
	return u.Scheme + "://" + user + u.Host + u.Path
}
