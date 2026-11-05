package marketing

import (
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

func TestAutomaticAnnouncementFullscreenLink(t *testing.T) {
	for _, tc := range []struct {
		name, audience, username, shortName, target, want string
		automatic, webApp                                 bool
	}{
		{"passenger named app", AudiencePassengers, "@jan_passenger_bot", "taxi", "invite", "https://t.me/jan_passenger_bot/taxi?startapp=invite&mode=fullscreen", true, false},
		{"driver main app", AudienceDrivers, "jan_driver_bot", "", "promotions", "https://t.me/jan_driver_bot?startapp=promotions&mode=fullscreen", true, false},
		{"missing bot fallback", AudiencePassengers, "", "", "invite", "https://app.jantaxi.test/passenger/?startapp=invite", true, true},
		{"invalid bot fallback", AudienceDrivers, "bad/bot", "", "promotions", "https://app.jantaxi.test/driver/?startapp=promotions", true, true},
		{"manual stays web app", AudiencePassengers, "jan_passenger_bot", "taxi", "invite", "https://app.jantaxi.test/passenger/?startapp=invite", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := NewBroadcastWorker(nil, nil, WorkerOptions{
				PassengerMiniAppURL: "https://app.jantaxi.test/passenger/", DriverMiniAppURL: "https://app.jantaxi.test/driver/",
				PassengerBotUsername: tc.username, DriverBotUsername: tc.username,
				PassengerMiniAppShortName: tc.shortName, DriverMiniAppShortName: tc.shortName,
			})
			_, markup := worker.render(delivery{audience: tc.audience, lang: "ru", b: Broadcast{
				IsAutomatic: tc.automatic, Content: settings.Localized{RU: "Подарок"},
				Button: &BroadcastButton{Label: settings.Localized{RU: "Открыть подарок"}, StartParam: tc.target},
			}})
			if markup == nil || len(markup.InlineKeyboard) != 2 || len(markup.InlineKeyboard[0]) != 1 {
				t.Fatalf("keyboard: %+v", markup)
			}
			button := markup.InlineKeyboard[0][0]
			if button.Text != "Открыть подарок" {
				t.Fatalf("label: %q", button.Text)
			}
			if tc.webApp {
				if button.WebApp == nil || button.WebApp.URL != tc.want || button.URL != "" {
					t.Fatalf("fallback/manual button: %+v", button)
				}
			} else if button.URL != tc.want || button.WebApp != nil {
				t.Fatalf("fullscreen link button: %+v", button)
			}
			if markup.InlineKeyboard[1][0].CallbackData != OptOutCallback {
				t.Fatal("unsubscribe button missing")
			}
		})
	}
}
