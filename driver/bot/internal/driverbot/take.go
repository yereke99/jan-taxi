package driverbot

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// Offers in the driver bot (ARCHITECTURE §22.4). Every offer message carries
// «✅ Тапсырысты алу» (callback "take:<order_id>"); the first driver whose tap
// reaches the API gets the order through the same atomic acceptance as the
// Mini App (dispatch.AcceptOrder). The winner's message becomes
// «✅ Сіз алдыңыз» and the API's outbox sends the trip card with the
// passenger's contact; anyone else gets an alert, and their button goes once
// the order is gone for good. The API edits the other drivers' offers through
// its outbox.

// takePrefix is the callback data prefix of the accept button.
const takePrefix = "take:"

var orderIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// onTake handles a tap on «✅ Тапсырысты алу». The callback is answered as
// soon as the API decided, before the message edits, so Telegram's spinner
// stops at once; the runtime's update claim makes a redelivered update a
// no-op, and a second tap is answered as a replay. The trip card is not sent
// here: the outbox delivers it once (also when this response or the bot
// itself is lost after the commit) and withholds it if the order was
// cancelled or reassigned meanwhile.
func (b *Bot) onTake(ctx context.Context, q *telegram.CallbackQuery, orderID string) error {
	if !orderIDRe.MatchString(orderID) {
		return b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	}
	// The API logs the acceptance under this id ("order accepted", channel bot).
	ctx = botapi.WithRequestID(ctx, "tg-cb-"+q.ID)
	user, err := b.api.SyncUser(ctx, syncRequest(&q.From))
	if err != nil {
		_ = b.alert(ctx, q, texts.T(runtime.DefaultLanguage, "take_error"))
		return fmt.Errorf("sync user: %w", err)
	}
	s := &session{chatID: q.From.ID, tgID: q.From.ID}
	if q.Message != nil {
		s.chatID = q.Message.Chat.ID
	}
	s.apply(user)
	if user.IsBlocked {
		_ = b.alert(ctx, q, s.t("take_not_allowed"))
		return b.blocked(ctx, s)
	}
	res, err := b.api.AcceptOrder(ctx, s.tgID, orderID)
	if err != nil {
		return b.takeFailed(ctx, s, q, orderID, err)
	}
	toast := s.t("take_won_toast")
	if res.Replayed {
		toast = s.t("take_replayed_toast")
	}
	if err := b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: toast}); err != nil {
		b.log.Warn("answer take callback failed", "err", err.Error())
	}
	if q.Message == nil {
		return nil
	}
	if rest, many := withoutOrder(q.Message.ReplyMarkup, orderID); many {
		// One order of an /orders list: only its row goes, the others stay takeable.
		b.setKeyboard(ctx, s, q.Message, rest)
		return nil
	}
	b.editOffer(ctx, s, q.Message, s.t("take_won"),
		runtime.Inline(b.appButton(ctx, s, "btn_open_order", "order_"+orderID)))
	return nil
}

// withoutOrder returns markup without the rows of orderID's accept button and
// whether markup offered more than one order (an /orders list); for a single
// offer the caller rewrites the whole message instead.
func withoutOrder(markup *telegram.InlineKeyboardMarkup, orderID string) (*telegram.InlineKeyboardMarkup, bool) {
	if markup == nil {
		return nil, false
	}
	takes := 0
	rest := &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{}}
	for _, row := range markup.InlineKeyboard {
		tapped := false
		for _, btn := range row {
			if strings.HasPrefix(btn.CallbackData, takePrefix) {
				takes++
				tapped = tapped || btn.CallbackData == takePrefix+orderID
			}
		}
		if !tapped {
			rest.InlineKeyboard = append(rest.InlineKeyboard, row)
		}
	}
	return rest, takes > 1
}

// setKeyboard replaces the inline keyboard of m (nil removes it).
func (b *Bot) setKeyboard(ctx context.Context, s *session, m *telegram.Message, markup *telegram.InlineKeyboardMarkup) {
	if err := b.tg.EditMessageReplyMarkup(ctx, s.chatID, m.MessageID, markup); err != nil && !telegram.IsMessageNotModified(err) {
		b.log.Warn("edit offer keyboard failed", "err", err.Error())
	}
}

// alert answers a callback with a dialog the driver has to dismiss.
func (b *Bot) alert(ctx context.Context, q *telegram.CallbackQuery, text string) error {
	return b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: text, ShowAlert: true})
}

// takeFailed explains why the order could not be taken. When the order is
// gone for good (taken, closed, out of reach) its accept button goes — the
// whole keyboard of a single offer, only its row in an /orders list; when the
// driver can fix the problem (balance, going online, location, a trip still
// running) the offer keeps its buttons for a later tap.
func (b *Bot) takeFailed(ctx context.Context, s *session, q *telegram.CallbackQuery, orderID string, err error) error {
	e, ok := botapi.AsError(err)
	if !ok {
		_ = b.alert(ctx, q, s.t("take_error"))
		return fmt.Errorf("accept order: %w", err)
	}
	var (
		text     string
		terminal bool
		followUp func() error
	)
	switch {
	case e.Code == "order_already_taken":
		text, terminal = s.t("take_taken"), true
	case e.Code == "invalid_transition":
		text, terminal = s.t("take_closed"), true
	case e.Status == 404 && e.Code == "not_found":
		text, terminal = s.t("take_unavailable"), true
	case e.Code == "insufficient_balance":
		required, _ := e.DetailInt64("required")
		available, _ := e.DetailInt64("available")
		money := func(n int64) string { return runtime.Esc(runtime.FormatMoney(n)) }
		text = s.t("take_balance", runtime.FormatMoney(required), runtime.FormatMoney(available))
		followUp = func() error {
			return b.send(ctx, s, s.t("take_balance_msg", money(required), money(available)),
				runtime.Inline(b.appButton(ctx, s, "btn_topup", "topup")))
		}
	case e.Code == "driver_offline":
		text = s.t("take_offline")
	case e.Code == "location_required":
		text = s.t("take_location")
		reason := "need_location"
		if e.DetailString("reason") == "location_stale" {
			reason = "location_stale"
		}
		followUp = func() error { return b.needLocation(ctx, s, reason) }
	case e.Code == "driver_has_active_order":
		text = s.t("take_active")
	case e.Code == "driver_not_approved", e.Code == "driver_not_registered", e.DetailString("reason") == "user_blocked":
		text, terminal = s.t("take_not_allowed"), true
	default:
		_ = b.alert(ctx, q, s.t("take_error"))
		return fmt.Errorf("accept order: %w", err)
	}
	if err := b.alert(ctx, q, text); err != nil {
		b.log.Warn("answer take callback failed", "err", err.Error())
	}
	if q.Message != nil && terminal {
		rest, many := withoutOrder(q.Message.ReplyMarkup, orderID)
		if !many {
			rest = nil
		}
		b.setKeyboard(ctx, s, q.Message, rest)
	}
	if followUp != nil {
		return followUp()
	}
	return nil
}

// editOffer puts status above the offer text and replaces its keyboard.
func (b *Bot) editOffer(ctx context.Context, s *session, m *telegram.Message, status string, markup *telegram.InlineKeyboardMarkup) {
	text := status
	if orig := strings.TrimSpace(m.Text); orig != "" {
		text += "\n\n" + runtime.Esc(orig)
	}
	err := b.tg.EditMessageText(ctx, telegram.EditMessageTextParams{
		ChatID: s.chatID, MessageID: m.MessageID, Text: text, ParseMode: "HTML", ReplyMarkup: markup,
		DisableWebPagePreview: true,
	})
	if err != nil && !telegram.IsMessageNotModified(err) {
		b.log.Warn("edit offer failed", "err", err.Error())
	}
}
