package passengerbot

import (
	"context"
	"fmt"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// onContact stores the phone the passenger shared (Mini App requestContact,
// ARCHITECTURE §22.5) so the driver of their orders can call them. Telegram
// vouches for the number only when it is the sender's own contact, so other
// contacts are refused here and again by the API.
func (b *Bot) onContact(ctx context.Context, s *session, msg *telegram.Message) error {
	c := msg.Contact
	if c.UserID == 0 || c.UserID != msg.From.ID {
		return b.send(ctx, s, s.t("phone_own_only"), b.keyboard(s.lang))
	}
	res, err := b.api.VerifyPassengerContact(ctx, s.tgID, c.PhoneNumber, c.UserID)
	if err != nil {
		e, ok := botapi.AsError(err)
		switch {
		case ok && e.DetailString("reason") == "contact_mismatch":
			return b.send(ctx, s, s.t("phone_own_only"), b.keyboard(s.lang))
		case ok && e.Fields["phone"] != "":
			return b.send(ctx, s, s.t("phone_invalid"), b.keyboard(s.lang))
		case ok && e.DetailString("reason") == "user_blocked":
			return b.blocked(ctx, s)
		}
		_ = b.send(ctx, s, s.t("error"), b.keyboard(s.lang))
		return fmt.Errorf("verify contact: %w", err)
	}
	return b.send(ctx, s, s.t("phone_saved", runtime.Esc(runtime.FormatPhone(res.Phone))), b.keyboard(s.lang))
}
