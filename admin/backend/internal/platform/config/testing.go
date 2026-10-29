package config

import (
	"os"
	"path/filepath"
	"time"
)

// Fixed credentials used by ForTesting. They are well-formed but belong to no
// real bot; tests sign initData with them (e.g. testutil.SignInitData).
const (
	TestPassengerBotToken = "1000000001:TESTpassengerTOKENaaaaaaaaaaaaaaaaaaa"
	TestDriverBotToken    = "1000000002:TESTdriverTOKENbbbbbbbbbbbbbbbbbbbbbbbb"
	TestSessionSecret     = "test-session-secret-0123456789abcdef"
	TestServiceSecret     = "test-service-secret-0123456789abcdef"
)

// ForTesting returns a complete, valid configuration for tests in any package.
// Ports are 0 (callers use httptest or their own listeners); files go to a
// temporary directory unless the caller overrides FileStorageDir.
func ForTesting(databaseURL string) Config {
	return Config{
		Env:                       EnvTest,
		DatabaseURL:               databaseURL,
		DatabaseMaxConns:          10,
		PassengerBotToken:         TestPassengerBotToken,
		PassengerBotUsername:      "jantaxi_test_bot",
		DriverBotToken:            TestDriverBotToken,
		DriverBotUsername:         "jantaxi_driver_test_bot",
		PassengerMiniAppURL:       "http://localhost:5173/passenger/",
		DriverMiniAppURL:          "http://localhost:5174/driver/",
		AdminFrontendURL:          "http://localhost:5175/",
		APIPublicURL:              "http://localhost:8080",
		InternalBotServiceSecret:  TestServiceSecret,
		SessionSecret:             TestSessionSecret,
		TelegramInitDataMaxAge:    24 * time.Hour,
		AppTokenTTL:               12 * time.Hour,
		AdminSessionIdleTTL:       12 * time.Hour,
		AdminSessionAbsoluteTTL:   168 * time.Hour,
		CORSAllowedOrigins:        []string{"http://localhost:5173", "http://localhost:5174", "http://localhost:5175"},
		TrustedProxies:            DefaultTrustedProxies(),
		FileStorageDir:            filepath.Join(os.TempDir(), "jantaxi-test-files"),
		MaxUploadBytes:            10 << 20,
		KaspiPayPaymentURL:        "https://pay.example.test/jantaxi",
		KaspiPayRecipientName:     "Jan Taxi",
		MapStyleURL:               "https://tiles.openfreemap.org/styles/positron",
		GeocodingProvider:         GeocoderNone,
		OSRMBaseURL:               "http://127.0.0.1:5000",
		OSRMTimeout:               5 * time.Second,
		TwoGISBaseURL:             "https://2gis.ru",
		DefaultCity:               "EMB",
		DefaultLanguage:           "kk",
		MaxRoutePoints:            MaxRoutePointsLimit,
		DefaultCommissionPercent:  10,
		DriverOnlineIdleTimeout:   12 * time.Hour,
		TelegramAPIBaseURL:        "http://127.0.0.1:1",
		NotificationWorkerEnabled: false,
		JobsEnabled:               false,
		VerificationAutoApprove:   true,
		VerificationMinConfidence: 98,
		VerificationQueueLimit:    1000,
		LogLevel:                  "debug",
		LogFormat:                 "text",
	}
}
