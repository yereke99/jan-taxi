package driverbot

import (
	"context"
	"fmt"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// onContact verifies a phone number shared with the "📱" button or by the
// Mini App (WebApp.requestContact). Telegram vouches for the number only
// when the sender shares their own contact directly (contact.user_id ==
// from.id, not forwarded); any other contact (someone from the address book,
// or a forwarded card, whose number was typed by whoever created it) is
// refused before anything is sent to the API.
func (b *Bot) onContact(ctx context.Context, s *session, msg *telegram.Message) error {
	c := msg.Contact
	if c.UserID == 0 || c.UserID != msg.From.ID || msg.Forwarded() {
		return b.send(ctx, s, s.t("phone_own_only"), b.keyboard(s))
	}
	res, err := b.api.VerifyContact(ctx, s.tgID, c.PhoneNumber, c.UserID)
	if err != nil {
		e, ok := botapi.AsError(err)
		switch {
		case !ok:
		case e.DetailString("reason") == "contact_mismatch":
			return b.send(ctx, s, s.t("phone_own_only"), b.keyboard(s))
		case e.DetailString("reason") == "user_blocked":
			return b.blocked(ctx, s)
		case e.Code == "phone_taken":
			return b.send(ctx, s, s.t("phone_taken"), b.keyboard(s))
		case e.Code == "validation_failed":
			return b.send(ctx, s, s.t("phone_invalid"), b.keyboard(s))
		}
		_ = b.send(ctx, s, s.t("error"), b.keyboard(s))
		return fmt.Errorf("verify contact: %w", err)
	}
	s.driver.PhoneVerified = true
	return b.send(ctx, s, s.t("phone_verified", runtime.Esc(runtime.FormatPhone(res.Phone))), b.keyboard(s))
}
