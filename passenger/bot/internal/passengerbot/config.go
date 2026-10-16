// Package passengerbot is the Jan Taxi passenger Telegram bot: a thin entry
// point that greets users, answers menu buttons and opens the passenger Mini
// App. All state lives in the API (internal bot API).
package passengerbot

import (
	"errors"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// Config is the passenger bot configuration (environment variables).
type Config struct {
	Token              string // PASSENGER_BOT_TOKEN
	Username           string // PASSENGER_BOT_USERNAME
	DriverBotUsername  string // DRIVER_BOT_USERNAME
	MiniAppURL         string // PASSENGER_MINIAPP_URL
	InternalAPIURL     string // INTERNAL_API_URL
	ServiceSecret      string // INTERNAL_BOT_SERVICE_SECRET
	HealthPort         string // HEALTH_PORT
	InstanceID         string // INSTANCE_ID
	TelegramAPIBaseURL string // TELEGRAM_API_BASE_URL
}

// LoadConfig reads the configuration from the environment.
func LoadConfig() (Config, error) {
	c := Config{
		Token:              runtime.Getenv("PASSENGER_BOT_TOKEN", ""),
		Username:           strings.TrimPrefix(runtime.Getenv("PASSENGER_BOT_USERNAME", ""), "@"),
		DriverBotUsername:  strings.TrimPrefix(runtime.Getenv("DRIVER_BOT_USERNAME", ""), "@"),
		MiniAppURL:         runtime.Getenv("PASSENGER_MINIAPP_URL", ""),
		InternalAPIURL:     runtime.Getenv("INTERNAL_API_URL", "http://api:8081"),
		ServiceSecret:      runtime.Getenv("INTERNAL_BOT_SERVICE_SECRET", ""),
		HealthPort:         runtime.Getenv("HEALTH_PORT", runtime.DefaultHealthPort),
		InstanceID:         runtime.InstanceID(),
		TelegramAPIBaseURL: runtime.Getenv("TELEGRAM_API_BASE_URL", telegram.DefaultBaseURL),
	}
	var missing []string
	if c.Token == "" {
		missing = append(missing, "PASSENGER_BOT_TOKEN")
	}
	if c.ServiceSecret == "" {
		missing = append(missing, "INTERNAL_BOT_SERVICE_SECRET")
	}
	if len(missing) > 0 {
		return c, errors.New("missing required environment variables: " + strings.Join(missing, ", "))
	}
	return c, nil
}

// Warnings lists settings that make features unavailable.
func (c Config) Warnings() []string {
	var w []string
	if !runtime.IsHTTPS(c.MiniAppURL) {
		w = append(w, "PASSENGER_MINIAPP_URL is not an https URL: Mini App buttons are taken from the API config or omitted")
	}
	if c.DriverBotUsername == "" {
		w = append(w, "DRIVER_BOT_USERNAME is empty: the driver bot link comes from the API config")
	}
	return w
}
