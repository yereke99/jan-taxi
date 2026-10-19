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
// «✅ Алу» (callback "take:<order_id>"); the first driver whose tap reaches
// the API gets the order through the same atomic acceptance as the Mini App.
// The winner's message becomes «✅ Сіз алдыңыз» and a trip card with the
// passenger's contact follows; anyone else gets an alert and loses the
// button. The API edits the other drivers' offers through its outbox.

// takePrefix is the callback data prefix of «Алу».
const takePrefix = "take:"

var orderIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// onTake handles a tap on «✅ Алу». The callback is answered as soon as the
// API decided, before the message edits, so Telegram's spinner stops at once;
// the runtime's update claim makes a redelivered update a no-op, and a
// second tap is answered as a replay.
func (b *Bot) onTake(ctx context.Context, q *telegram.CallbackQuery, orderID string) error {
	if !orderIDRe.MatchString(orderID) {
		return b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	}
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
	if q.Message != nil {
		b.editOffer(ctx, s, q.Message, s.t("take_won"),
			runtime.Inline(b.appButton(ctx, s, "btn_open_order", "order_"+orderID)))
	}
	if res.Replayed {
		return nil
	}
	return b.sendTripCard(ctx, s, res)
}

// alert answers a callback with a dialog the driver has to dismiss.
func (b *Bot) alert(ctx context.Context, q *telegram.CallbackQuery, text string) error {
	return b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: text, ShowAlert: true})
}

// takeFailed explains why the order could not be taken and removes «Алу»
// from the offer: when the order is gone for good the whole keyboard goes;
// when the driver can fix the problem (balance, going online, location) the
// map button stays.
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
	if q.Message != nil {
		var markup *telegram.InlineKeyboardMarkup
		if !terminal {
			markup = runtime.Inline([]telegram.InlineKeyboardButton{
				runtime.WebAppButton(s.t("btn_map"), b.miniAppURL(ctx, "order_"+orderID))})
		}
		if err := b.tg.EditMessageReplyMarkup(ctx, s.chatID, q.Message.MessageID, markup); err != nil &&
			!telegram.IsMessageNotModified(err) {
			b.log.Warn("remove take button failed", "err", err.Error())
		}
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

// sendTripCard sends the order the driver just took with the passenger's
// contact: name with a Telegram link, phone when shared, and buttons to open
// the order and to write in Telegram.
func (b *Bot) sendTripCard(ctx context.Context, s *session, res botapi.AcceptResult) error {
	o := res.Order
	place := func(st botapi.StopSummary) string { return runtime.Esc(stopLabel(s, st)) }
	lines := []string{s.t("trip_card_title", o.Number), runtime.Esc(s.t("category_"+o.Category)) + " · " +
		runtime.Esc(o.VehicleClassName.Get(s.lang))}
	if l := localityLine(o.Locality); l != "" {
		lines = append(lines, l)
	}
	if n := len(o.Stops); n > 0 {
		lines = append(lines, s.t("order_from", place(o.Stops[0])))
		if n > 2 {
			via := make([]string, 0, n-2)
			for _, st := range o.Stops[1 : n-1] {
				via = append(via, place(st))
			}
			lines = append(lines, s.t("trip_card_via", strings.Join(via, " → ")))
		}
		if n > 1 {
			lines = append(lines, s.t("order_to", place(o.Stops[n-1])))
		}
	}
	lines = append(lines, s.t("trip_card_fare", runtime.Esc(runtime.FormatMoney(o.Fare)),
		runtime.Esc(s.t("payment_"+o.PaymentMethod))))
	if o.PassengerCount != nil {
		lines = append(lines, s.t("trip_card_passengers", *o.PassengerCount))
	}
	if o.CargoSize != nil {
		lines = append(lines, s.t("trip_card_cargo_size", runtime.Esc(s.t("cargo_size_"+*o.CargoSize))))
	}
	if o.CargoDescription != nil && *o.CargoDescription != "" {
		lines = append(lines, s.t("order_cargo", runtime.Esc(*o.CargoDescription)))
	}
	if o.Comment != nil && *o.Comment != "" {
		lines = append(lines, s.t("order_comment", runtime.Esc(*o.Comment)))
	}
	if res.Charged > 0 && res.Wallet != nil {
		lines = append(lines, s.t("trip_card_charged", runtime.Esc(runtime.FormatMoney(res.Charged)),
			runtime.Esc(runtime.FormatMoney(res.Wallet.Available))))
	}
	rows := [][]telegram.InlineKeyboardButton{b.appButton(ctx, s, "btn_open_order", "order_"+o.ID)}
	if p := o.Passenger; p != nil {
		name := strings.TrimSpace(p.FirstName)
		if name == "" {
			name = "Telegram"
		}
		client := runtime.Esc(name)
		if strings.HasPrefix(p.TelegramURL, "https://t.me/") || strings.HasPrefix(p.TelegramURL, "tg://user?id=") {
			client = `<a href="` + runtime.Esc(p.TelegramURL) + `">` + client + `</a>`
		}
		lines = append(lines, "", s.t("trip_card_client", client))
		if p.Phone != nil && *p.Phone != "" {
			lines = append(lines, s.t("trip_card_phone", runtime.Esc(runtime.FormatPhone(*p.Phone))))
		} else {
			lines = append(lines, s.t("trip_card_no_phone"))
		}
		if strings.HasPrefix(p.TelegramURL, "https://t.me/") {
			rows = append(rows, []telegram.InlineKeyboardButton{runtime.URLButton(s.t("btn_write_tg"), p.TelegramURL)})
		}
	}
	if o.RecipientPhone != nil && *o.RecipientPhone != "" {
		lines = append(lines, s.t("trip_card_recipient", runtime.Esc(runtime.FormatPhone(*o.RecipientPhone))))
	}
	return b.send(ctx, s, strings.Join(lines, "\n"), runtime.Inline(rows...))
}
