package botapi

import (
	"net/http"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

type supportView struct {
	TelegramUsername *string            `json:"telegram_username"`
	Phone            *string            `json:"phone"`
	Hours            settings.Localized `json:"hours"`
}

// botConfig is the OpenAPI BotConfig.
type botConfig struct {
	PassengerMiniAppURL  string      `json:"passenger_miniapp_url"`
	DriverMiniAppURL     string      `json:"driver_miniapp_url"`
	PassengerBotUsername string      `json:"passenger_bot_username"`
	DriverBotUsername    string      `json:"driver_bot_username"`
	Support              supportView `json:"support"`
	DefaultLanguage      string      `json:"default_language"`
}

func nonEmpty(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}

// config returns the runtime values the bots read at start and refresh
// periodically; support contacts are admin-editable settings.
func (h *Handler) config(w http.ResponseWriter, r *http.Request) error {
	sup := settings.SupportContacts(r.Context(), h.pool)
	return httpx.OK(w, botConfig{
		PassengerMiniAppURL:  h.opts.PassengerMiniAppURL,
		DriverMiniAppURL:     h.opts.DriverMiniAppURL,
		PassengerBotUsername: strings.TrimPrefix(h.opts.PassengerBotUsername, "@"),
		DriverBotUsername:    strings.TrimPrefix(h.opts.DriverBotUsername, "@"),
		Support: supportView{
			TelegramUsername: nonEmpty(strings.TrimPrefix(strings.TrimSpace(sup.TelegramUsername), "@")),
			Phone:            nonEmpty(sup.Phone),
			Hours:            sup.Hours,
		},
		DefaultLanguage: h.opts.DefaultLanguage,
	})
}
