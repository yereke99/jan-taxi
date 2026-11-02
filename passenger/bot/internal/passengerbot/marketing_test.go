package passengerbot

import (
	"context"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/bottest"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// TestStartWithReferralLink: "/start ref_<code>" goes to the API for the
// referral decision, and the greeting comes whatever it answers.
func TestStartWithReferralLink(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		e := newEnv(t)
		var payload any
		e.api.Handle("POST /passenger/700100200/referral", func(body map[string]any, _ string) (int, any) {
			payload = body["payload"]
			if status != 200 {
				return status, bottest.APIError("internal", nil)
			}
			return 200, map[string]any{"attributed": true, "outcome": "attributed"}
		})
		msgs := e.text(t, "/start ref_ABCD2345")
		if payload != "ref_ABCD2345" {
			t.Fatalf("status %d: payload %v", status, payload)
		}
		if len(msgs) < 2 || bottest.WebAppURL(bottest.InlineButtons(msgs[1])[0]) != appURL+"?startapp=home" {
			t.Fatalf("status %d: greeting %v", status, msgs)
		}
	}
	// Other payloads never reach the referral endpoint.
	e := newEnv(t)
	e.text(t, "/start taxi")
	if n := e.api.Count("POST /passenger/700100200/referral"); n != 0 {
		t.Fatalf("referral calls for /start taxi: %d", n)
	}
}

// TestUnsubscribeButton: the broadcast's «unsubscribe» button turns marketing
// messages off and says so in the person's language.
func TestUnsubscribeButton(t *testing.T) {
	e := newEnv(t)
	e.user["language"] = "ru"
	var optOut any
	e.api.Handle("POST /bots/passenger/users/700100200/marketing", func(body map[string]any, _ string) (int, any) {
		optOut = body["opt_out"]
		return 200, map[string]any{"marketing_subscribed": false}
	})
	if err := e.bot.HandleUpdate(context.Background(), telegram.Update{UpdateID: 3, CallbackQuery: &telegram.CallbackQuery{
		ID: "cb9", From: telegram.User{ID: userID, FirstName: "Әлия"}, Data: "mkt_off",
	}}); err != nil {
		t.Fatal(err)
	}
	if optOut != true {
		t.Fatalf("opt_out = %v", optOut)
	}
	calls := e.tg.Calls()
	if len(calls) != 1 || calls[0].Method != "answerCallbackQuery" ||
		calls[0].Body["text"] != "Рекламные рассылки отключены. Сообщения о заказах будут приходить как прежде." {
		t.Fatalf("answer: %v", calls)
	}
}
