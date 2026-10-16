package driverbot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi/runtime"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// TelegramAPI is the part of the Bot API the handlers use (*telegram.Client).
type TelegramAPI interface {
	runtime.MessageSender
	AnswerCallbackQuery(ctx context.Context, p telegram.AnswerCallbackQueryParams) error
	SetChatMenuButton(ctx context.Context, chatID *int64, button telegram.MenuButtonWebApp) error
}

// API is the part of the internal bot API the handlers use (*botapi.Client).
type API interface {
	SyncUser(ctx context.Context, in botapi.SyncUserRequest) (botapi.SyncUserResponse, error)
	SetLanguage(ctx context.Context, tgID int64, lang string) (string, error)
	DriverSummary(ctx context.Context, tgID int64) (botapi.DriverSummary, error)
	SetAvailability(ctx context.Context, tgID int64, online bool) (botapi.Availability, error)
	DriverOrders(ctx context.Context, tgID int64, limit int) (botapi.AvailableOrders, error)
	VerifyContact(ctx context.Context, tgID int64, phone string, contactUserID int64) (botapi.ContactResult, error)
	ReportLocation(ctx context.Context, tgID int64, in botapi.LocationReport) error
	Config(ctx context.Context) (botapi.Config, error)
}

// Menu actions.
const (
	actOnline   = "online"
	actOffline  = "offline"
	actOrders   = "orders"
	actTrip     = "trip"
	actBalance  = "balance"
	actTopup    = "topup"
	actProfile  = "profile"
	actLanguage = "language"
	actSupport  = "support"
	actPhone    = "verify_phone"
	actHelp     = "help"
)

// commands maps slash commands to actions (/offline and /help are accepted
// but not listed in the menu).
var commands = map[string]string{
	"online": actOnline, "offline": actOffline, "orders": actOrders, "trip": actTrip, "balance": actBalance,
	"topup": actTopup, "profile": actProfile, "language": actLanguage, "support": actSupport, "help": actHelp,
}

// menuCommands is the command menu in display order.
var menuCommands = []string{"start", "online", "orders", "trip", "balance", "topup", "profile", "language", "support"}

// buttonActions are the reply keyboard buttons, matched in every language.
var buttonActions = []string{actOnline, actOffline, actOrders, actTrip, actBalance, actTopup, actProfile, actLanguage, actSupport, actPhone}

// languageNames are shown in the language picker, each in its own language.
var languageNames = []struct{ code, name string }{{"kk", "Қазақша"}, {"ru", "Русский"}, {"en", "English"}}

// Live location problems are explained at most this often per chat.
const liveErrorEvery = 10 * time.Minute

// Bot handles driver bot updates.
type Bot struct {
	cfg        Config
	tg         TelegramAPI
	api        API
	log        *slog.Logger
	conf       *runtime.ConfigCache
	buttons    *runtime.Matcher
	liveErrors *runtime.Throttle
}

// New builds the bot; it fails when the texts or button labels are inconsistent.
func New(cfg Config, tg TelegramAPI, api API, log *slog.Logger) (*Bot, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := texts.Check(); err != nil {
		return nil, err
	}
	m := runtime.NewMatcher()
	for _, a := range buttonActions {
		for _, lang := range runtime.Languages {
			if err := m.Add(a, texts.T(lang, "kb_"+a)); err != nil {
				return nil, err
			}
		}
	}
	return &Bot{
		cfg: cfg, tg: tg, api: api, log: log,
		conf:       runtime.NewConfigCache(api, 5*time.Minute, log),
		buttons:    m,
		liveErrors: runtime.NewThrottle(liveErrorEvery),
	}, nil
}

// session is the context of one update.
type session struct {
	chatID int64
	tgID   int64
	lang   string
	user   botapi.SyncUserResponse
	driver botapi.DriverState
}

func (s *session) t(key string, args ...any) string { return texts.T(s.lang, key, args...) }

func (s *session) status() string {
	if s.driver.Status == nil {
		return ""
	}
	return *s.driver.Status
}

func (s *session) apply(user botapi.SyncUserResponse) {
	s.user, s.lang = user, runtime.Lang(user.Language)
	if user.Driver != nil {
		s.driver = *user.Driver
	}
}

// HandleUpdate implements runtime.Handler.
func (b *Bot) HandleUpdate(ctx context.Context, u telegram.Update) error {
	switch {
	case u.MyChatMember != nil:
		return b.onChatMember(ctx, u.MyChatMember)
	case u.CallbackQuery != nil:
		return b.onCallback(ctx, u.CallbackQuery)
	case u.EditedMessage != nil:
		if m := u.EditedMessage; m.Location != nil && private(m) && !m.Forwarded() && !m.IsVenue() {
			return b.onLiveUpdate(ctx, m)
		}
		return nil // edited texts need no reply
	case u.Message != nil:
		return b.onMessage(ctx, u.Message)
	}
	return nil
}

func private(m *telegram.Message) bool {
	return m.From != nil && !m.From.IsBot && m.Chat.Type == "private"
}

func syncRequest(from *telegram.User) botapi.SyncUserRequest {
	return botapi.SyncUserRequest{
		TelegramUserID: from.ID, FirstName: from.FirstName, LastName: from.LastName,
		Username: from.Username, LanguageCode: from.LanguageCode,
	}
}

// onChatMember records that the user blocked (kicked) or restored the bot.
func (b *Bot) onChatMember(ctx context.Context, m *telegram.ChatMemberUpdated) error {
	if m.Chat.Type != "private" || m.From.ID <= 0 {
		return nil
	}
	var blocked bool
	switch m.NewChatMember.Status {
	case "kicked", "left":
		blocked = true
	case "member":
		blocked = false
	default:
		return nil
	}
	req := syncRequest(&m.From)
	req.Blocked = &blocked
	_, err := b.api.SyncUser(ctx, req)
	return err
}

// syncSession upserts the sender and loads their language and driver state.
func (b *Bot) syncSession(ctx context.Context, msg *telegram.Message, started bool) (*session, error) {
	s := &session{chatID: msg.Chat.ID, tgID: msg.From.ID, lang: runtime.DefaultLanguage}
	req := syncRequest(msg.From)
	req.Started = started
	user, err := b.api.SyncUser(ctx, req)
	if err != nil {
		b.send(ctx, s, s.t("error"), nil)
		return nil, fmt.Errorf("sync user: %w", err)
	}
	s.apply(user)
	return s, nil
}

func (b *Bot) onMessage(ctx context.Context, msg *telegram.Message) error {
	if !private(msg) {
		return nil
	}
	cmd, payload := parseCommand(msg.Text)
	s, err := b.syncSession(ctx, msg, cmd == "start")
	if err != nil {
		return err
	}
	if s.user.IsBlocked {
		return b.blocked(ctx, s)
	}
	switch {
	case msg.Contact != nil:
		return b.onContact(ctx, s, msg)
	case msg.Location != nil && (msg.Forwarded() || msg.IsVenue()):
		// A forwarded pin or a place card is not where the driver is.
		return b.send(ctx, s, s.t("location_not_own"), b.keyboard(s))
	case msg.Location != nil:
		return b.onLocation(ctx, s, msg.Location)
	case cmd == "start":
		return b.start(ctx, s, payload)
	}
	action := commands[cmd]
	if action == "" && cmd == "" {
		action, _ = b.buttons.Match(msg.Text)
	}
	switch action {
	case actOnline:
		return b.goOnline(ctx, s)
	case actOffline:
		return b.goOffline(ctx, s)
	case actOrders:
		return b.orders(ctx, s)
	case actTrip:
		return b.trip(ctx, s)
	case actBalance:
		return b.balance(ctx, s)
	case actTopup:
		return b.topup(ctx, s)
	case actProfile:
		return b.profile(ctx, s)
	case actLanguage:
		return b.languagePicker(ctx, s)
	case actSupport:
		return b.support(ctx, s)
	case actPhone:
		// A text that looks like the button label: the client could not attach
		// the contact (for example Telegram Desktop).
		return b.send(ctx, s, s.t("phone_use_button"), b.keyboard(s))
	}
	if !s.driver.PhoneVerified && looksLikePhone(msg.Text) {
		return b.send(ctx, s, s.t("phone_use_button"), b.keyboard(s))
	}
	return b.send(ctx, s, s.t("help"), b.keyboard(s))
}

// parseCommand splits "/start payload" or "/orders@jantaxi_driver_bot".
func parseCommand(text string) (cmd, payload string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, _, _ = strings.Cut(head, "@")
	return strings.ToLower(head), strings.TrimSpace(rest)
}

// looksLikePhone reports a typed phone number (10 or more digits and nothing
// but digits, spaces, "+", "-" and parentheses).
func looksLikePhone(text string) bool {
	digits := 0
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case strings.ContainsRune(" +-()", r):
		default:
			return false
		}
	}
	return digits >= 10
}

func (b *Bot) send(ctx context.Context, s *session, text string, markup any) error {
	return runtime.SendHTML(ctx, b.tg, s.chatID, text, markup)
}

// miniAppURL returns the Mini App URL with a start parameter, preferring the
// environment and falling back to the API configuration.
func (b *Bot) miniAppURL(ctx context.Context, startParam string) string {
	base := b.cfg.MiniAppURL
	if !runtime.IsHTTPS(base) {
		if c, ok := b.conf.Get(ctx); ok {
			base = c.DriverMiniAppURL
		}
	}
	return runtime.MiniAppURL(base, startParam)
}

// appButton is an inline button opening the Mini App at startParam.
func (b *Bot) appButton(ctx context.Context, s *session, key, startParam string) []telegram.InlineKeyboardButton {
	return []telegram.InlineKeyboardButton{runtime.WebAppButton(s.t(key), b.miniAppURL(ctx, startParam))}
}

// keyboard is the persistent reply keyboard (text and contact buttons only;
// never web_app). The first button reflects the online state, and the phone
// button is shown until the number is verified.
func (b *Bot) keyboard(s *session) *telegram.ReplyKeyboardMarkup {
	t := func(k string) string { return texts.T(s.lang, k) }
	work := t("kb_online")
	if s.driver.IsOnline {
		work = t("kb_offline")
	}
	rows := [][]telegram.KeyboardButton{}
	if !s.driver.PhoneVerified {
		rows = append(rows, runtime.ContactRow(t("kb_verify_phone")))
	}
	rows = append(rows,
		runtime.Row(work),
		runtime.Row(t("kb_orders"), t("kb_trip")),
		runtime.Row(t("kb_balance"), t("kb_topup")),
		runtime.Row(t("kb_profile"), t("kb_language")),
		runtime.Row(t("kb_support")),
	)
	return runtime.ReplyKeyboard(t("kb_placeholder"), rows...)
}

func languageRow() []telegram.InlineKeyboardButton {
	row := make([]telegram.InlineKeyboardButton, 0, len(languageNames))
	for _, l := range languageNames {
		row = append(row, runtime.CallbackButton(l.name, "lang:"+l.code))
	}
	return row
}

// Profile is the command menu, descriptions and default menu button.
func (b *Bot) Profile() runtime.Profile {
	p := runtime.Profile{
		Commands:          map[string][]telegram.BotCommand{},
		Descriptions:      map[string]string{},
		ShortDescriptions: map[string]string{},
	}
	for _, lang := range runtime.Languages {
		code := lang
		if lang == runtime.DefaultLanguage {
			code = "" // Kazakh is the fallback for every other language
		}
		var cmds []telegram.BotCommand
		for _, c := range menuCommands {
			cmds = append(cmds, telegram.BotCommand{Command: c, Description: texts.T(lang, "cmd_"+c)})
		}
		p.Commands[code] = cmds
		p.Descriptions[code] = texts.T(lang, "description")
		p.ShortDescriptions[code] = texts.T(lang, "short_description")
	}
	if b.cfg.MiniAppURL != "" {
		p.MenuButton = &telegram.MenuButtonWebApp{Text: "Jan Taxi Driver", WebApp: telegram.WebAppInfo{URL: b.cfg.MiniAppURL}}
	}
	return p
}
