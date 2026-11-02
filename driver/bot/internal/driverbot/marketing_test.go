package driverbot

import (
	"context"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// TestUnsubscribeButton: the driver bot handles the broadcast's
// «unsubscribe» button like the passenger bot.
func TestUnsubscribeButton(t *testing.T) {
	e := newEnv(t)
	var optOut any
	e.api.Handle("POST /bots/driver/users/700100300/marketing", func(body map[string]any, _ string) (int, any) {
		optOut = body["opt_out"]
		return 200, map[string]any{"marketing_subscribed": false}
	})
	if err := e.bot.HandleUpdate(context.Background(), telegram.Update{UpdateID: 4, CallbackQuery: &telegram.CallbackQuery{
		ID: "cb7", From: telegram.User{ID: 700100300, FirstName: "Ерлан"}, Data: "mkt_off",
	}}); err != nil {
		t.Fatal(err)
	}
	if optOut != true {
		t.Fatalf("opt_out = %v", optOut)
	}
	calls := e.tg.Calls()
	if len(calls) != 1 || calls[0].Method != "answerCallbackQuery" ||
		calls[0].Body["text"] != "Жарнамалық хабарламалар өшірілді. Тапсырыстар бұрынғыдай келеді." {
		t.Fatalf("answer: %v", calls)
	}
}
