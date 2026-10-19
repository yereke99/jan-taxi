package passengerbot

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
	runtime.ChatMenuAPI
	AnswerCallbackQuery(ctx context.Context, p telegram.AnswerCallbackQueryParams) error
}

// API is the part of the internal bot API the handlers use (*botapi.Client).
type API interface {
	SyncUser(ctx context.Context, in botapi.SyncUserRequest) (botapi.SyncUserResponse, error)
	SetLanguage(ctx context.Context, tgID int64, lang string) (string, error)
	PassengerOrders(ctx context.Context, tgID int64, limit int) ([]botapi.OrderSummary, error)
	VerifyPassengerContact(ctx context.Context, tgID int64, phone string, contactUserID int64) (botapi.ContactResult, error)
	Config(ctx context.Context) (botapi.Config, error)
}

// Menu actions.
const (
	actTaxi     = "taxi"
	actParcel   = "parcel"
	actOrders   = "orders"
	actDriver   = "driver"
	actSupport  = "support"
	actLanguage = "language"
	actHelp     = "help"
)

// commands maps slash commands to actions.
var commands = map[string]string{
	"taxi": actTaxi, "parcel": actParcel, "orders": actOrders, "driver": actDriver,
	"support": actSupport, "language": actLanguage, "help": actHelp,
}

// languageNames are shown in the language picker, each in its own language.
var languageNames = []struct{ code, name string }{{"kk", "Қазақша"}, {"ru", "Русский"}, {"en", "English"}}

// Bot handles passenger bot updates.
type Bot struct {
	cfg     Config
	tg      TelegramAPI
	api     API
	log     *slog.Logger
	conf    *runtime.ConfigCache
	buttons *runtime.Matcher
	menus   *runtime.ChatMenus
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
	for _, a := range []string{actTaxi, actParcel, actOrders, actDriver, actSupport, actLanguage} {
		for _, lang := range runtime.Languages {
			if err := m.Add(a, texts.T(lang, "kb_"+a)); err != nil {
				return nil, err
			}
		}
	}
	return &Bot{
		cfg: cfg, tg: tg, api: api, log: log,
		conf:    runtime.NewConfigCache(api, 5*time.Minute, log),
		buttons: m,
		menus:   runtime.NewChatMenus(tg, log),
	}, nil
}

// session is the context of one update.
type session struct {
	chatID int64
	tgID   int64
	lang   string
	user   botapi.SyncUserResponse
}

func (s *session) t(key string, args ...any) string { return texts.T(s.lang, key, args...) }

// HandleUpdate implements runtime.Handler.
func (b *Bot) HandleUpdate(ctx context.Context, u telegram.Update) error {
	switch {
	case u.MyChatMember != nil:
		return b.onChatMember(ctx, u.MyChatMember)
	case u.CallbackQuery != nil:
		return b.onCallback(ctx, u.CallbackQuery)
	case u.Message != nil:
		return b.onMessage(ctx, u.Message)
	}
	return nil // edited messages and other updates need no reply
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

func (b *Bot) onMessage(ctx context.Context, msg *telegram.Message) error {
	if msg.From == nil || msg.From.IsBot || msg.Chat.Type != "private" {
		return nil
	}
	cmd, payload := parseCommand(msg.Text)
	req := syncRequest(msg.From)
	req.Started = cmd == "start"
	s := &session{chatID: msg.Chat.ID, tgID: msg.From.ID, lang: runtime.DefaultLanguage}
	user, err := b.api.SyncUser(ctx, req)
	if err != nil {
		b.send(ctx, s, s.t("error"), nil)
		return fmt.Errorf("sync user: %w", err)
	}
	s.user, s.lang = user, runtime.Lang(user.Language)
	if user.IsBlocked {
		return b.blocked(ctx, s)
	}
	defer b.menus.Ensure(ctx, s.chatID)

	if msg.Contact != nil {
		return b.onContact(ctx, s, msg)
	}
	if cmd == "start" {
		return b.start(ctx, s, payload)
	}
	action := commands[cmd]
	if action == "" && cmd == "" {
		action, _ = b.buttons.Match(msg.Text)
	}
	switch action {
	case actTaxi:
		return b.category(ctx, s, "taxi", "btn_taxi")
	case actParcel:
		return b.category(ctx, s, "parcel", "btn_parcel")
	case actOrders:
		return b.orders(ctx, s)
	case actDriver:
		return b.becomeDriver(ctx, s)
	case actSupport:
		return b.support(ctx, s)
	case actLanguage:
		return b.languagePicker(ctx, s)
	default:
		return b.send(ctx, s, s.t("help"), b.keyboard(s.lang))
	}
}

// parseCommand splits "/start payload" or "/orders@jantaxi_bot".
func parseCommand(text string) (cmd, payload string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, _, _ = strings.Cut(head, "@")
	return strings.ToLower(head), strings.TrimSpace(rest)
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
			base = c.PassengerMiniAppURL
		}
	}
	return runtime.MiniAppURL(base, startParam)
}

func (b *Bot) driverBotUsername(ctx context.Context) string {
	if b.cfg.DriverBotUsername != "" {
		return b.cfg.DriverBotUsername
	}
	if c, ok := b.conf.Get(ctx); ok {
		return c.DriverBotUsername
	}
	return ""
}

// keyboard is the persistent reply keyboard (text buttons only).
func (b *Bot) keyboard(lang string) *telegram.ReplyKeyboardMarkup {
	t := func(k string) string { return texts.T(lang, k) }
	return runtime.ReplyKeyboard(t("kb_placeholder"),
		runtime.Row(t("kb_taxi"), t("kb_parcel")),
		runtime.Row(t("kb_orders"), t("kb_driver")),
		runtime.Row(t("kb_support"), t("kb_language")),
	)
}

func languageRow() []telegram.InlineKeyboardButton {
	row := make([]telegram.InlineKeyboardButton, 0, len(languageNames))
	for _, l := range languageNames {
		row = append(row, runtime.CallbackButton(l.name, "lang:"+l.code))
	}
	return row
}

// Profile is the command menu, descriptions and default menu button. The
// menu button is the only one every chat uses (see runtime.ChatMenus); its
// URL comes from PASSENGER_MINIAPP_URL, else from the API configuration.
func (b *Bot) Profile(ctx context.Context) runtime.Profile {
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
		for _, c := range []string{"start", "taxi", "parcel", "orders", "driver", "support", "language"} {
			cmds = append(cmds, telegram.BotCommand{Command: c, Description: texts.T(lang, "cmd_"+c)})
		}
		p.Commands[code] = cmds
		p.Descriptions[code] = texts.T(lang, "description")
		p.ShortDescriptions[code] = texts.T(lang, "short_description")
	}
	if url := b.miniAppURL(ctx, ""); url != "" {
		p.MenuButton = &telegram.MenuButtonWebApp{Text: "Jan Taxi", WebApp: telegram.WebAppInfo{URL: url}}
	}
	return p
}
