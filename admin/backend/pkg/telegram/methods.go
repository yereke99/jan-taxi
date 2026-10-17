package telegram

import (
	"context"
	"encoding/json"
	"time"
)

// GetMe returns the bot's own user.
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", struct{}{}, &u, 0); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUpdates long-polls for updates (timeout at most MaxPollTimeout). The
// request deadline is the poll timeout plus the request timeout (15s), so a
// healthy long poll never hits it.
func (c *Client) GetUpdates(ctx context.Context, p GetUpdatesParams) ([]Update, error) {
	p.Timeout = min(p.Timeout, MaxPollTimeout)
	body := map[string]any{}
	if p.Offset != 0 {
		body["offset"] = p.Offset
	}
	if p.Limit > 0 {
		body["limit"] = p.Limit
	}
	if p.Timeout > 0 {
		body["timeout"] = p.Timeout
	}
	if p.AllowedUpdates != nil {
		body["allowed_updates"] = p.AllowedUpdates
	}
	var out []Update
	if err := c.call(ctx, "getUpdates", body, &out, time.Duration(max(p.Timeout, 0))*time.Second); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteWebhook removes any webhook so getUpdates can be used.
func (c *Client) DeleteWebhook(ctx context.Context, dropPending bool) error {
	body := map[string]any{}
	if dropPending {
		body["drop_pending_updates"] = true
	}
	return c.call(ctx, "deleteWebhook", body, nil, 0)
}

type linkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

// SendMessage sends a text message.
func (c *Client) SendMessage(ctx context.Context, p SendMessageParams) (*Message, error) {
	body := struct {
		ChatID             int64               `json:"chat_id"`
		Text               string              `json:"text"`
		ParseMode          string              `json:"parse_mode,omitempty"`
		ReplyMarkup        any                 `json:"reply_markup,omitempty"`
		LinkPreviewOptions *linkPreviewOptions `json:"link_preview_options,omitempty"`
	}{ChatID: p.ChatID, Text: p.Text, ParseMode: p.ParseMode, ReplyMarkup: p.ReplyMarkup}
	if p.DisableWebPagePreview {
		body.LinkPreviewOptions = &linkPreviewOptions{IsDisabled: true}
	}
	var m Message
	if err := c.call(ctx, "sendMessage", body, &m, 0); err != nil {
		return nil, err
	}
	return &m, nil
}

// EditMessageText replaces the text (and inline keyboard) of a sent message.
func (c *Client) EditMessageText(ctx context.Context, p EditMessageTextParams) error {
	body := struct {
		ChatID      int64                 `json:"chat_id"`
		MessageID   int64                 `json:"message_id"`
		Text        string                `json:"text"`
		ParseMode   string                `json:"parse_mode,omitempty"`
		ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	}{p.ChatID, p.MessageID, p.Text, p.ParseMode, p.ReplyMarkup}
	var ignored json.RawMessage
	return c.call(ctx, "editMessageText", body, &ignored, 0)
}

// AnswerCallbackQuery acknowledges an inline button press.
func (c *Client) AnswerCallbackQuery(ctx context.Context, p AnswerCallbackQueryParams) error {
	body := struct {
		CallbackQueryID string `json:"callback_query_id"`
		Text            string `json:"text,omitempty"`
		ShowAlert       bool   `json:"show_alert,omitempty"`
	}{p.CallbackQueryID, p.Text, p.ShowAlert}
	return c.call(ctx, "answerCallbackQuery", body, nil, 0)
}

// SetMyCommands sets the command menu; an empty languageCode sets the fallback.
func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand, languageCode string) error {
	if commands == nil {
		commands = []BotCommand{}
	}
	body := struct {
		Commands     []BotCommand `json:"commands"`
		LanguageCode string       `json:"language_code,omitempty"`
	}{commands, languageCode}
	return c.call(ctx, "setMyCommands", body, nil, 0)
}

// SetChatMenuButton sets the menu button of one private chat, or the default
// button when chatID is nil.
func (c *Client) SetChatMenuButton(ctx context.Context, chatID *int64, button MenuButtonWebApp) error {
	body := struct {
		ChatID     *int64           `json:"chat_id,omitempty"`
		MenuButton MenuButtonWebApp `json:"menu_button"`
	}{chatID, button}
	return c.call(ctx, "setChatMenuButton", body, nil, 0)
}

// ResetChatMenuButton makes a private chat use the bot's default menu button
// again, clearing a button set for that chat alone.
func (c *Client) ResetChatMenuButton(ctx context.Context, chatID int64) error {
	body := struct {
		ChatID     int64             `json:"chat_id"`
		MenuButton map[string]string `json:"menu_button"`
	}{chatID, map[string]string{"type": "default"}}
	return c.call(ctx, "setChatMenuButton", body, nil, 0)
}

// SetMyDescription sets the text shown in an empty chat with the bot.
func (c *Client) SetMyDescription(ctx context.Context, description, languageCode string) error {
	body := struct {
		Description  string `json:"description"`
		LanguageCode string `json:"language_code,omitempty"`
	}{description, languageCode}
	return c.call(ctx, "setMyDescription", body, nil, 0)
}

// SetMyShortDescription sets the text shown on the bot's profile page.
func (c *Client) SetMyShortDescription(ctx context.Context, short, languageCode string) error {
	body := struct {
		ShortDescription string `json:"short_description"`
		LanguageCode     string `json:"language_code,omitempty"`
	}{short, languageCode}
	return c.call(ctx, "setMyShortDescription", body, nil, 0)
}

// GetFile prepares a file for download and returns its path.
func (c *Client) GetFile(ctx context.Context, fileID string) (*File, error) {
	var f File
	if err := c.call(ctx, "getFile", map[string]string{"file_id": fileID}, &f, 0); err != nil {
		return nil, err
	}
	return &f, nil
}
