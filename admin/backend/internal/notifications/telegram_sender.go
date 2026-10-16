package notifications

import (
	"context"
	"errors"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// ErrBotNotConfigured is returned for a bot without a client (its token is
// unset, which config allows only outside production). It is not retryable, so
// such messages become dead instead of looping.
var ErrBotNotConfigured = errors.New("notifications: bot is not configured")

// TelegramSender sends through the Bot API with one client per bot token.
type TelegramSender struct {
	clients map[Bot]*telegram.Client
}

// NewTelegramSender builds a Sender; a nil client leaves that bot unconfigured.
func NewTelegramSender(passenger, driver *telegram.Client) *TelegramSender {
	s := &TelegramSender{clients: make(map[Bot]*telegram.Client, 2)}
	if passenger != nil {
		s.clients[PassengerBot] = passenger
	}
	if driver != nil {
		s.clients[DriverBot] = driver
	}
	return s
}

// Configured reports whether bot has a client.
func (s *TelegramSender) Configured(bot Bot) bool { return s.clients[bot] != nil }

// Send implements Sender with sendMessage in HTML parse mode and link previews off.
func (s *TelegramSender) Send(ctx context.Context, bot Bot, chatID int64, text string, markup *telegram.InlineKeyboardMarkup) (int64, error) {
	c := s.clients[bot]
	if c == nil {
		return 0, ErrBotNotConfigured
	}
	p := telegram.SendMessageParams{ChatID: chatID, Text: text, ParseMode: "HTML", DisableWebPagePreview: true}
	if markup != nil {
		// Assign only a non-nil pointer: a typed nil in the interface would be
		// serialised as "reply_markup": null.
		p.ReplyMarkup = markup
	}
	msg, err := c.SendMessage(ctx, p)
	if err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}
