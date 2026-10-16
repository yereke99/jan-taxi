package runtime

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// startParamRe is the Mini App start parameter format (§18).
var startParamRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidStartParam reports whether s may be used as a startapp parameter.
func ValidStartParam(s string) bool { return startParamRe.MatchString(s) }

// MiniAppURL returns base with ?startapp=<param> set (other query values are
// kept). An invalid param opens the Mini App home; an empty base gives "".
func MiniAppURL(base, startParam string) string {
	if strings.TrimSpace(base) == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	q := u.Query()
	if ValidStartParam(startParam) {
		q.Set("startapp", startParam)
	} else {
		q.Del("startapp")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// IsHTTPS reports whether raw is an absolute https URL (required by Telegram
// for web_app buttons).
func IsHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// Row builds one reply-keyboard row of text buttons. Reply keyboards never
// carry web_app buttons: Telegram sends no initData for them (§18).
func Row(texts ...string) []telegram.KeyboardButton {
	row := make([]telegram.KeyboardButton, 0, len(texts))
	for _, t := range texts {
		row = append(row, telegram.KeyboardButton{Text: t})
	}
	return row
}

// ContactRow is a single button that asks Telegram to share the user's own number.
func ContactRow(text string) []telegram.KeyboardButton {
	return []telegram.KeyboardButton{{Text: text, RequestContact: true}}
}

// ReplyKeyboard builds a persistent, resized reply keyboard.
func ReplyKeyboard(placeholder string, rows ...[]telegram.KeyboardButton) *telegram.ReplyKeyboardMarkup {
	kb := &telegram.ReplyKeyboardMarkup{ResizeKeyboard: true, IsPersistent: true, InputFieldPlaceholder: placeholder}
	for _, r := range rows {
		if len(r) > 0 {
			kb.Keyboard = append(kb.Keyboard, r)
		}
	}
	return kb
}

// WebAppButton opens the Mini App at url. Telegram accepts only https URLs
// for web_app buttons; otherwise the zero button is returned and dropped by Inline.
func WebAppButton(text, url string) telegram.InlineKeyboardButton {
	if !IsHTTPS(url) {
		return telegram.InlineKeyboardButton{}
	}
	return telegram.InlineKeyboardButton{Text: text, WebApp: &telegram.WebAppInfo{URL: url}}
}

// URLButton opens an http(s) or tg:// link.
func URLButton(text, raw string) telegram.InlineKeyboardButton {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "tg") {
		return telegram.InlineKeyboardButton{}
	}
	return telegram.InlineKeyboardButton{Text: text, URL: raw}
}

// CallbackButton sends data (≤64 bytes) back to the bot.
func CallbackButton(text, data string) telegram.InlineKeyboardButton {
	if data == "" || len(data) > 64 {
		return telegram.InlineKeyboardButton{}
	}
	return telegram.InlineKeyboardButton{Text: text, CallbackData: data}
}

// Inline builds an inline keyboard, dropping zero buttons and empty rows. It
// returns nil when nothing is left.
func Inline(rows ...[]telegram.InlineKeyboardButton) *telegram.InlineKeyboardMarkup {
	kb := &telegram.InlineKeyboardMarkup{}
	for _, r := range rows {
		var clean []telegram.InlineKeyboardButton
		for _, b := range r {
			if b.Text != "" && (b.URL != "" || b.CallbackData != "" || b.WebApp != nil) {
				clean = append(clean, b)
			}
		}
		if len(clean) > 0 {
			kb.InlineKeyboard = append(kb.InlineKeyboard, clean)
		}
	}
	if len(kb.InlineKeyboard) == 0 {
		return nil
	}
	return kb
}

// BotLink is https://t.me/<username>, optionally with ?start=<payload>.
func BotLink(username, startPayload string) string {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return ""
	}
	link := "https://t.me/" + username
	if startParamRe.MatchString(startPayload) {
		link += "?start=" + startPayload
	}
	return link
}
