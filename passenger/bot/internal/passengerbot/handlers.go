package passengerbot

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// Start parameters the passenger Mini App understands (§18).
var (
	staticStartParams = map[string]bool{
		"home": true, "taxi": true, "parcel": true, "other": true, "orders": true, "support": true, "driver": true,
	}
	orderStartParamRe = regexp.MustCompile(`^order_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func passengerStartParam(payload string) string {
	if staticStartParams[payload] || orderStartParamRe.MatchString(payload) {
		return payload
	}
	return "home"
}

// start greets the user: the reply keyboard first, then the Mini App button
// (opening the screen named by a deep-link payload) and, until the user has
// picked a language, the language choice.
func (b *Bot) start(ctx context.Context, s *session, payload string) error {
	if err := b.send(ctx, s, s.t("welcome"), b.keyboard(s.lang)); err != nil {
		return err
	}
	rows := [][]telegram.InlineKeyboardButton{{
		runtime.WebAppButton(s.t("btn_open_app"), b.miniAppURL(ctx, passengerStartParam(payload))),
	}}
	text := s.t("welcome_open")
	if !s.user.LanguageChosen {
		rows = append(rows, languageRow())
		if runtime.Inline(rows[0]) == nil {
			text = s.t("pick_language")
		}
	}
	if kb := runtime.Inline(rows...); kb != nil {
		return b.send(ctx, s, text, kb)
	}
	return nil
}

// category answers "order a taxi" / "send a parcel" with a button that opens
// the booking flow with the category preselected.
func (b *Bot) category(ctx context.Context, s *session, startParam, buttonKey string) error {
	kb := runtime.Inline([]telegram.InlineKeyboardButton{
		runtime.WebAppButton(s.t(buttonKey), b.miniAppURL(ctx, startParam)),
	})
	if kb == nil {
		return b.send(ctx, s, s.t(startParam)+"\n\n"+s.t("error"), nil)
	}
	return b.send(ctx, s, s.t(startParam), kb)
}

// orders lists the five most recent orders, each with a button opening it.
func (b *Bot) orders(ctx context.Context, s *session) error {
	items, err := b.api.PassengerOrders(ctx, s.tgID, 5)
	if err != nil {
		_ = b.send(ctx, s, s.t("error"), nil)
		return fmt.Errorf("passenger orders: %w", err)
	}
	if len(items) == 0 {
		kb := runtime.Inline([]telegram.InlineKeyboardButton{
			runtime.WebAppButton(s.t("btn_taxi"), b.miniAppURL(ctx, "taxi")),
			runtime.WebAppButton(s.t("btn_parcel"), b.miniAppURL(ctx, "parcel")),
		})
		return b.send(ctx, s, s.t("orders_empty"), kb)
	}
	lines := []string{s.t("orders_title")}
	var rows [][]telegram.InlineKeyboardButton
	var row []telegram.InlineKeyboardButton
	for _, o := range items {
		lines = append(lines, s.t("order_line", o.Number,
			runtime.Esc(s.t("status_"+o.Status)), runtime.Esc(runtime.FormatMoney(o.Fare)),
			runtime.Esc(o.PickupAddress), runtime.Esc(o.DestinationAddress)))
		row = append(row, runtime.WebAppButton(s.t("btn_order", o.Number), b.miniAppURL(ctx, "order_"+o.ID)))
		if len(row) == 3 {
			rows, row = append(rows, row), nil
		}
	}
	rows = append(rows, row,
		[]telegram.InlineKeyboardButton{runtime.WebAppButton(s.t("btn_all_orders"), b.miniAppURL(ctx, "orders"))})
	return b.send(ctx, s, strings.Join(lines, "\n\n"), runtime.Inline(rows...))
}

// becomeDriver explains the separate driver bot and links to it.
func (b *Bot) becomeDriver(ctx context.Context, s *session) error {
	link := runtime.BotLink(b.driverBotUsername(ctx), "register")
	if link == "" {
		return b.send(ctx, s, s.t("driver_unavailable"), nil)
	}
	kb := runtime.Inline([]telegram.InlineKeyboardButton{runtime.URLButton(s.t("btn_driver_bot"), link)})
	return b.send(ctx, s, s.t("driver"), kb)
}

// supportText renders the configured support contacts.
func (b *Bot) supportText(ctx context.Context, s *session) (string, *telegram.InlineKeyboardMarkup) {
	c, ok := b.conf.Get(ctx)
	if !ok {
		return s.t("error"), nil
	}
	lines := []string{s.t("support_title")}
	var kb *telegram.InlineKeyboardMarkup
	if u := c.Support.TelegramUsername; u != nil && *u != "" {
		lines = append(lines, s.t("support_tg", runtime.Esc(*u)))
		kb = runtime.Inline([]telegram.InlineKeyboardButton{runtime.URLButton(s.t("btn_support"), runtime.BotLink(*u, ""))})
	}
	if p := c.Support.Phone; p != nil && *p != "" {
		lines = append(lines, s.t("support_phone", runtime.Esc(runtime.FormatPhone(*p))))
	}
	if len(lines) == 1 {
		lines = append(lines, s.t("support_none"))
	}
	if h := c.Support.Hours.Get(s.lang); h != "" {
		lines = append(lines, s.t("support_hours", runtime.Esc(h)))
	}
	return strings.Join(lines, "\n"), kb
}

func (b *Bot) support(ctx context.Context, s *session) error {
	text, kb := b.supportText(ctx, s)
	return b.send(ctx, s, text, kb)
}

// blocked tells an admin-blocked user to contact support and nothing else.
func (b *Bot) blocked(ctx context.Context, s *session) error {
	text, kb := b.supportText(ctx, s)
	return b.send(ctx, s, s.t("blocked")+"\n\n"+text, kb)
}

func (b *Bot) languagePicker(ctx context.Context, s *session) error {
	return b.send(ctx, s, s.t("pick_language"), runtime.Inline(languageRow()))
}

// onCallback handles inline button presses (only the language picker).
func (b *Bot) onCallback(ctx context.Context, q *telegram.CallbackQuery) error {
	lang, ok := strings.CutPrefix(q.Data, "lang:")
	if !ok || runtime.Lang(lang) != lang {
		return b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	}
	user, err := b.api.SyncUser(ctx, syncRequest(&q.From))
	if err != nil {
		_ = b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: texts.T(runtime.DefaultLanguage, "error")})
		return fmt.Errorf("sync user: %w", err)
	}
	chatID := q.From.ID
	if q.Message != nil {
		chatID = q.Message.Chat.ID
	}
	s := &session{chatID: chatID, tgID: q.From.ID, lang: runtime.Lang(user.Language), user: user}
	if user.IsBlocked {
		_ = b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
		return b.blocked(ctx, s)
	}
	if _, err := b.api.SetLanguage(ctx, q.From.ID, lang); err != nil {
		_ = b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: s.t("error")})
		return fmt.Errorf("set language: %w", err)
	}
	s.lang = lang
	_ = b.tg.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: s.t("language_set")})
	if url := b.miniAppURL(ctx, ""); runtime.IsHTTPS(url) {
		chat := s.chatID
		if err := b.tg.SetChatMenuButton(ctx, &chat, telegram.MenuButtonWebApp{
			Text: s.t("menu_button"), WebApp: telegram.WebAppInfo{URL: url},
		}); err != nil {
			b.log.Warn("set chat menu button failed", "err", err.Error())
		}
	}
	return b.send(ctx, s, s.t("language_set"), b.keyboard(lang))
}
