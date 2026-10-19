package passengerbot

import (
	"context"
	"strings"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/bottest"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

const contactPath = "POST /passenger/700100200/contact"

func (e *env) contact(t *testing.T, c telegram.Contact) []map[string]any {
	t.Helper()
	e.tg.Reset()
	err := e.bot.HandleUpdate(context.Background(), telegram.Update{UpdateID: 2, Message: &telegram.Message{
		MessageID: 2, From: &telegram.User{ID: userID, FirstName: "Әлия"},
		Chat: telegram.Chat{ID: userID, Type: "private"}, Contact: &c,
	}})
	if err != nil {
		t.Logf("handler error (logged by the runtime): %v", err)
	}
	return e.tg.Messages()
}

// TestSharedContactIsStored: the passenger's own contact (Mini App
// requestContact) is stored through the internal API (§22.5).
func TestSharedContactIsStored(t *testing.T) {
	e := newEnv(t)
	var got map[string]any
	e.api.Handle(contactPath, func(body map[string]any, _ string) (int, any) {
		got = body
		return 200, map[string]any{"phone": "+77011234567", "phone_verified": true}
	})
	msgs := e.contact(t, telegram.Contact{PhoneNumber: "77011234567", FirstName: "Әлия", UserID: userID})
	if got["phone"] != "77011234567" || got["contact_user_id"] != float64(userID) {
		t.Fatalf("API body: %v", got)
	}
	if len(msgs) != 1 || !strings.Contains(strings.ReplaceAll(bottest.Text(msgs[0]), " ", " "), "+7 701 123 45 67") {
		t.Fatalf("reply: %v", msgs)
	}
}

// TestForeignContactIsRefused: someone else's contact never reaches the API;
// errors from the API are explained.
func TestForeignContactIsRefused(t *testing.T) {
	e := newEnv(t)
	msgs := e.contact(t, telegram.Contact{PhoneNumber: "77019998877", FirstName: "Басқа", UserID: 42})
	if e.api.Count(contactPath) != 0 || len(msgs) != 1 || bottest.Text(msgs[0]) != texts.T("kk", "phone_own_only") {
		t.Fatalf("foreign contact: %v", msgs)
	}
	msgs = e.contact(t, telegram.Contact{PhoneNumber: "77019998877", FirstName: "Без id"})
	if e.api.Count(contactPath) != 0 || bottest.Text(msgs[0]) != texts.T("kk", "phone_own_only") {
		t.Fatalf("contact without user id: %v", msgs)
	}
	e.api.Handle(contactPath, func(map[string]any, string) (int, any) {
		return 422, map[string]any{"error": map[string]any{"code": "validation_failed", "message": "x",
			"fields": map[string]any{"phone": "invalid"}}}
	})
	msgs = e.contact(t, telegram.Contact{PhoneNumber: "+12025550100", UserID: userID})
	if bottest.Text(msgs[0]) != texts.T("kk", "phone_invalid") {
		t.Fatalf("invalid phone: %v", msgs)
	}
}
