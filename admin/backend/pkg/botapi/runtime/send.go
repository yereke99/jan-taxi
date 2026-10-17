package runtime

import (
	"context"
	"errors"
	"html"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// maxSendWait bounds how long one reply waits for Telegram's retry_after.
const maxSendWait = 10 * time.Second

// replyGrace is the budget of a reply sent after the handler's deadline has
// passed (typically the error message after a timed-out API call).
const replyGrace = 5 * time.Second

// MessageSender sends Telegram messages (*telegram.Client satisfies it).
type MessageSender interface {
	SendMessage(ctx context.Context, p telegram.SendMessageParams) (*telegram.Message, error)
}

// Esc escapes text for Telegram's HTML parse mode.
func Esc(s string) string { return html.EscapeString(s) }

// SendHTML sends an HTML message with link previews disabled. A 429 is
// retried once after retry_after (up to 10s) and a transient failure once
// after a second. markup may be an inline keyboard, a reply keyboard, a
// keyboard removal or nil. When ctx has already hit its deadline the reply
// still gets a short budget of its own, so the user is not left without an
// answer.
func SendHTML(ctx context.Context, s MessageSender, chatID int64, text string, markup any) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), replyGrace)
		defer cancel()
	}
	params := telegram.SendMessageParams{
		ChatID: chatID, Text: text, ParseMode: "HTML", ReplyMarkup: cleanMarkup(markup), DisableWebPagePreview: true,
	}
	_, err := s.SendMessage(ctx, params)
	if err == nil || !telegram.IsRetryable(err) {
		return err
	}
	wait := time.Second
	if d, ok := telegram.RetryAfter(err); ok {
		if d > maxSendWait {
			return err
		}
		wait = d
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
		return err
	}
	sleep(ctx, wait)
	if ctx.Err() != nil {
		return err
	}
	_, err = s.SendMessage(ctx, params)
	return err
}

// cleanMarkup turns typed nil pointers into an untyped nil so no
// "reply_markup": null is sent.
func cleanMarkup(m any) any {
	switch v := m.(type) {
	case *telegram.InlineKeyboardMarkup:
		if v == nil || len(v.InlineKeyboard) == 0 {
			return nil
		}
	case *telegram.ReplyKeyboardMarkup:
		if v == nil {
			return nil
		}
	case *telegram.ReplyKeyboardRemove:
		if v == nil {
			return nil
		}
	}
	return m
}
