package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const (
	prodPassengerToken = "123456789:AAEpassengerTokenValue_abcdefghijklmn"
	prodDriverToken    = "987654321:AAEdriverTokenValue-abcdefghijklmnopq"
	prodSession        = "session-secret-with-at-least-32-bytes!!"
	prodService        = "service-secret-with-at-least-32-bytes!!"
	prodDBPassword     = "dbpa55word"
)

func prodEnv() map[string]string {
	return map[string]string{
		"APP_ENV":                     "production",
		"DATABASE_URL":                "postgres://jantaxi:" + prodDBPassword + "@db:5432/jantaxi?sslmode=disable",
		"PASSENGER_BOT_TOKEN":         prodPassengerToken,
		"PASSENGER_BOT_USERNAME":      "@JanTaxiBot",
		"DRIVER_BOT_TOKEN":            prodDriverToken,
		"DRIVER_BOT_USERNAME":         "JanTaxiDriverBot",
		"PASSENGER_MINIAPP_URL":       "https://app.jantaxi.kz/passenger/",
		"DRIVER_MINIAPP_URL":          "https://app.jantaxi.kz/driver/",
		"ADMIN_FRONTEND_URL":          "https://admin.jantaxi.kz/",
		"API_PUBLIC_URL":              "https://app.jantaxi.kz",
		"INTERNAL_BOT_SERVICE_SECRET": prodService,
		"SESSION_SECRET":              prodSession,
		"KASPI_PAY_PAYMENT_URL":       "https://pay.kaspi.kz/pay/example",
		"KASPI_PAY_RECIPIENT_NAME":    "ИП Jan Taxi",
	}
}

func load(env map[string]string) (Config, error) {
	return LoadFrom(func(k string) string { return env[k] })
}

func with(base map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(base)+len(kv)/2)
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func TestLoadProductionDefaults(t *testing.T) {
	c, err := load(prodEnv())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"env", c.Env, EnvProduction},
		{"api port", c.APIPort, 8080},
		{"internal port", c.InternalPort, 8081},
		{"passenger username trimmed @", c.PassengerBotUsername, "JanTaxiBot"},
		{"initdata age", c.TelegramInitDataMaxAge, 24 * time.Hour},
		{"app token ttl", c.AppTokenTTL, 12 * time.Hour},
		{"admin idle", c.AdminSessionIdleTTL, 12 * time.Hour},
		{"admin absolute", c.AdminSessionAbsoluteTTL, 168 * time.Hour},
		{"cookie secure", c.AdminCookieSecure, true},
		{"storage dir", c.FileStorageDir, "/data/files"},
		{"max upload", c.MaxUploadBytes, int64(10 << 20)},
		{"map style", c.MapStyleURL, "https://tiles.openfreemap.org/styles/positron"},
		{"geocoder", c.GeocodingProvider, GeocoderPhoton},
		{"geocoder url", c.GeocodingBaseURL, "https://photon.komoot.io"},
		{"twogis", c.TwoGISBaseURL, "https://2gis.ru"},
		{"default city", c.DefaultCity, "EMB"},
		{"default language", c.DefaultLanguage, "kk"},
		{"max route points", c.MaxRoutePoints, 5},
		{"commission", c.DefaultCommissionPercent, 10},
		{"commission bps", c.DefaultCommissionBps(), 1000},
		{"idle timeout", c.DriverOnlineIdleTimeout, 12 * time.Hour},
		{"telegram api", c.TelegramAPIBaseURL, "https://api.telegram.org"},
		{"worker", c.NotificationWorkerEnabled, true},
		{"jobs", c.JobsEnabled, true},
		{"log level", c.LogLevel, "info"},
		{"log format", c.LogFormat, "json"},
		{"osrm timeout", c.OSRMTimeout, 5 * time.Second},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	wantOrigins := []string{"https://app.jantaxi.kz", "https://admin.jantaxi.kz"}
	if fmt.Sprint(c.CORSAllowedOrigins) != fmt.Sprint(wantOrigins) {
		t.Errorf("cors origins: got %v, want %v", c.CORSAllowedOrigins, wantOrigins)
	}
	if len(c.TrustedProxies) != 6 || !c.TrustedProxies[0].Contains(netip.MustParseAddr("127.0.0.1")) {
		t.Errorf("trusted proxies: %v", c.TrustedProxies)
	}
}

func TestLoadProductionValidation(t *testing.T) {
	base := prodEnv()
	tests := []struct {
		name    string
		env     map[string]string
		wantKey string
	}{
		{"missing passenger token", with(base, "PASSENGER_BOT_TOKEN", ""), "PASSENGER_BOT_TOKEN"},
		{"missing driver username", with(base, "DRIVER_BOT_USERNAME", ""), "DRIVER_BOT_USERNAME"},
		{"malformed token", with(base, "DRIVER_BOT_TOKEN", "not-a-token"), "DRIVER_BOT_TOKEN"},
		{"username without bot suffix", with(base, "DRIVER_BOT_USERNAME", "jantaxi_driver"), "DRIVER_BOT_USERNAME"},
		{"same token for both bots", with(base, "DRIVER_BOT_TOKEN", prodPassengerToken), "DRIVER_BOT_TOKEN"},
		{"short session secret", with(base, "SESSION_SECRET", "short"), "SESSION_SECRET"},
		{"missing service secret", with(base, "INTERNAL_BOT_SERVICE_SECRET", ""), "INTERNAL_BOT_SERVICE_SECRET"},
		{"equal secrets", with(base, "INTERNAL_BOT_SERVICE_SECRET", prodSession), "INTERNAL_BOT_SERVICE_SECRET"},
		{"http miniapp url", with(base, "PASSENGER_MINIAPP_URL", "http://app.jantaxi.kz/passenger/"), "PASSENGER_MINIAPP_URL"},
		{"http api url", with(base, "API_PUBLIC_URL", "http://app.jantaxi.kz"), "API_PUBLIC_URL"},
		{"missing admin url", with(base, "ADMIN_FRONTEND_URL", ""), "ADMIN_FRONTEND_URL"},
		{"route points too many", with(base, "MAX_ROUTE_POINTS", "6"), "MAX_ROUTE_POINTS"},
		{"route points too few", with(base, "MAX_ROUTE_POINTS", "1"), "MAX_ROUTE_POINTS"},
		{"insecure cookie", with(base, "ADMIN_COOKIE_SECURE", "false"), "ADMIN_COOKIE_SECURE"},
		{"missing database", with(base, "DATABASE_URL", ""), "DATABASE_URL"},
		{"bad port", with(base, "API_PORT", "http"), "API_PORT"},
		{"same ports", with(base, "INTERNAL_PORT", "8080"), "INTERNAL_PORT"},
		{"bad duration", with(base, "APP_TOKEN_TTL", "forever"), "APP_TOKEN_TTL"},
		{"bad language", with(base, "DEFAULT_LANGUAGE", "de"), "DEFAULT_LANGUAGE"},
		{"bad provider", with(base, "GEOCODING_PROVIDER", "google"), "GEOCODING_PROVIDER"},
		{"twogis without key", with(base, "GEOCODING_PROVIDER", "twogis"), "GEOCODING_API_KEY"},
		{"wildcard cors", with(base, "CORS_ALLOWED_ORIGINS", "*"), "CORS_ALLOWED_ORIGINS"},
		{"http cors in prod", with(base, "CORS_ALLOWED_ORIGINS", "http://x.example"), "CORS_ALLOWED_ORIGINS"},
		{"bad proxy", with(base, "TRUSTED_PROXIES", "10.0.0.0/33"), "TRUSTED_PROXIES"},
		{"bootstrap email only", with(base, "ADMIN_BOOTSTRAP_EMAIL", "a@b.kz"), "ADMIN_BOOTSTRAP_EMAIL"},
		{"bootstrap short password", with(base, "ADMIN_BOOTSTRAP_EMAIL", "a@b.kz", "ADMIN_BOOTSTRAP_PASSWORD", "short"), "ADMIN_BOOTSTRAP_PASSWORD"},
		{"relative storage dir", with(base, "FILE_STORAGE_DIR", "data/files"), "FILE_STORAGE_DIR"},
		{"bad city", with(base, "DEFAULT_CITY", "Atlantis"), "DEFAULT_CITY"},
		{"bad env", with(base, "APP_ENV", "staging"), "APP_ENV"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(tc.env)
			if err == nil {
				t.Fatalf("expected an error mentioning %s", tc.wantKey)
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("error %q does not mention %s", err, tc.wantKey)
			}
			for _, secret := range []string{prodPassengerToken, prodDriverToken, prodSession, prodService, prodDBPassword} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaks a secret: %q", err)
				}
			}
		})
	}
}

func TestLoadDevelopmentIsRelaxed(t *testing.T) {
	c, err := load(map[string]string{
		"APP_ENV":      "development",
		"DATABASE_URL": "postgres://jantaxi:jantaxi@localhost:55432/jantaxi",
		"DEFAULT_CITY": "Embi",
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.PassengerBotToken != "" || c.AdminCookieSecure {
		t.Fatalf("unexpected dev values: token=%q secure=%v", presence(c.PassengerBotToken), c.AdminCookieSecure)
	}
	if len(c.SessionSecret) < MinSecretBytes || len(c.InternalBotServiceSecret) < MinSecretBytes {
		t.Fatal("dev secrets must be generated when unset")
	}
	if c.SessionSecret == c.InternalBotServiceSecret {
		t.Fatal("generated secrets must differ")
	}
	if c.DefaultCity != "EMB" {
		t.Fatalf("DEFAULT_CITY=Embi should resolve to EMB, got %q", c.DefaultCity)
	}
	if c.PassengerMiniAppURL != "http://localhost:5173/passenger/" || c.APIPublicURL != "http://localhost:8080" {
		t.Fatalf("dev URL defaults: %q %q", c.PassengerMiniAppURL, c.APIPublicURL)
	}
	joined := strings.Join(c.Warnings, "\n")
	for _, key := range []string{"PASSENGER_BOT_TOKEN", "DRIVER_BOT_USERNAME", "SESSION_SECRET", "INTERNAL_BOT_SERVICE_SECRET"} {
		if !strings.Contains(joined, key) {
			t.Errorf("expected a warning about %s, got:\n%s", key, joined)
		}
	}
	// Even in development the hard product limits hold.
	if _, err := load(map[string]string{"APP_ENV": "development", "DATABASE_URL": "postgres://x@y/z", "MAX_ROUTE_POINTS": "9"}); err == nil {
		t.Fatal("MAX_ROUTE_POINTS=9 must fail in development too")
	}
}

func TestDurationsAndLists(t *testing.T) {
	c, err := load(with(prodEnv(),
		"APP_TOKEN_TTL", "3600",
		"TELEGRAM_INITDATA_MAX_AGE", "90m",
		"CORS_ALLOWED_ORIGINS", "https://extra.example:443, https://app.jantaxi.kz",
		"TRUSTED_PROXIES", "10.1.2.3, 172.18.0.0/16",
		"DEFAULT_CITY", "emb",
	))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.AppTokenTTL != time.Hour || c.TelegramInitDataMaxAge != 90*time.Minute {
		t.Fatalf("durations: %v %v", c.AppTokenTTL, c.TelegramInitDataMaxAge)
	}
	want := "[https://app.jantaxi.kz https://admin.jantaxi.kz https://extra.example]"
	if fmt.Sprint(c.CORSAllowedOrigins) != want {
		t.Fatalf("origins: %v", c.CORSAllowedOrigins)
	}
	if fmt.Sprint(c.TrustedProxies) != "[10.1.2.3/32 172.18.0.0/16]" {
		t.Fatalf("proxies: %v", c.TrustedProxies)
	}
	if c.DefaultCity != "EMB" {
		t.Fatalf("city: %q", c.DefaultCity)
	}
	none, err := load(with(prodEnv(), "TRUSTED_PROXIES", "none"))
	if err != nil || len(none.TrustedProxies) != 0 {
		t.Fatalf("TRUSTED_PROXIES=none: %v %v", none.TrustedProxies, err)
	}
}

func TestOrigin(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://App.Example.kz/passenger/?x=1", "https://app.example.kz"},
		{"https://example.kz:443/", "https://example.kz"},
		{"http://localhost:5173/passenger/", "http://localhost:5173"},
		{"http://[::1]:8080/", "http://[::1]:8080"},
	}
	for _, tc := range tests {
		got, err := Origin(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Origin(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := Origin("ftp://x"); err == nil {
		t.Error("ftp origin must fail")
	}
}

func TestStringAndLogValueHideSecrets(t *testing.T) {
	env := with(prodEnv(), "ADMIN_BOOTSTRAP_EMAIL", "root@jantaxi.kz", "ADMIN_BOOTSTRAP_PASSWORD", "bootstrap-password-123",
		"GEOCODING_PROVIDER", "twogis", "GEOCODING_API_KEY", "geo-key-secret-value", "MAP_API_KEY", "map-key-secret-value")
	c, err := load(env)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config", "cfg", c)
	outputs := map[string]string{
		"String": c.String(),
		"%v":     fmt.Sprintf("%v", c),
		"%+v":    fmt.Sprintf("%+v", c),
		"%#v":    fmt.Sprintf("%#v", c),
		"slog":   buf.String(),
	}
	secrets := []string{prodPassengerToken, prodDriverToken, prodSession, prodService, prodDBPassword,
		"bootstrap-password-123", "geo-key-secret-value", "map-key-secret-value"}
	for name, out := range outputs {
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("%s output leaks a secret", name)
			}
		}
		if !strings.Contains(out, "jantaxi@db:5432/jantaxi") {
			t.Errorf("%s output should keep the database host: %s", name, out)
		}
	}
}

func TestForTestingIsValid(t *testing.T) {
	c := ForTesting("postgres://x@localhost/db")
	v := &validator{}
	c.APIPort, c.InternalPort = 18080, 18081
	v.check(&c)
	if len(v.errs) > 0 {
		t.Fatalf("ForTesting config is invalid: %v", v.errs)
	}
}
