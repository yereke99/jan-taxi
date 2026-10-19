package telegram

import "encoding/json"

// User is a Telegram user or bot.
type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

// Chat is the chat a message belongs to.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Contact is a phone contact shared by the user.
type Contact struct {
	PhoneNumber string `json:"phone_number"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
}

// Location is a point shared by the user; LivePeriod is set for live locations.
type Location struct {
	Latitude           float64 `json:"latitude"`
	Longitude          float64 `json:"longitude"`
	HorizontalAccuracy float64 `json:"horizontal_accuracy,omitempty"`
	LivePeriod         int     `json:"live_period,omitempty"`
	Heading            int     `json:"heading,omitempty"`
}

// Document is a general file sent to the bot (e.g. a PDF receipt).
type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// PhotoSize is one size of a photo sent to the bot.
type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// File is a file ready for download (getFile). FilePath is valid for at
// least one hour.
type File struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int64  `json:"file_size,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
}

// Message is the subset of the Bot API Message object the bots use.
type Message struct {
	MessageID int64       `json:"message_id"`
	From      *User       `json:"from,omitempty"`
	Chat      Chat        `json:"chat"`
	Date      int64       `json:"date"`
	Text      string      `json:"text,omitempty"`
	Caption   string      `json:"caption,omitempty"`
	Contact   *Contact    `json:"contact,omitempty"`
	Location  *Location   `json:"location,omitempty"`
	Document  *Document   `json:"document,omitempty"`
	Photo     []PhotoSize `json:"photo,omitempty"`
	EditDate  int64       `json:"edit_date,omitempty"`
	// ForwardOrigin is set when the message was forwarded (MessageOrigin);
	// only its presence matters here, so it is kept raw.
	ForwardOrigin json.RawMessage `json:"forward_origin,omitempty"`
	// Venue is set for a place card; such a message also carries Location,
	// which is the place's position rather than the sender's.
	Venue json.RawMessage `json:"venue,omitempty"`
}

// Forwarded reports whether the message was forwarded from another chat.
func (m *Message) Forwarded() bool {
	return len(m.ForwardOrigin) > 0 && string(m.ForwardOrigin) != "null"
}

// IsVenue reports whether the message is a place card (venue).
func (m *Message) IsVenue() bool { return len(m.Venue) > 0 && string(m.Venue) != "null" }

// CallbackQuery is an inline keyboard button press.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// ChatMember carries the member status ("member", "kicked", ...).
type ChatMember struct {
	Status string `json:"status"`
}

// ChatMemberUpdated reports that the bot was blocked or unblocked in a private chat.
type ChatMemberUpdated struct {
	Chat          Chat       `json:"chat"`
	From          User       `json:"from"`
	NewChatMember ChatMember `json:"new_chat_member"`
}

// Update is one incoming update from getUpdates.
type Update struct {
	UpdateID      int64              `json:"update_id"`
	Message       *Message           `json:"message,omitempty"`
	EditedMessage *Message           `json:"edited_message,omitempty"`
	CallbackQuery *CallbackQuery     `json:"callback_query,omitempty"`
	MyChatMember  *ChatMemberUpdated `json:"my_chat_member,omitempty"`
}

// WebAppInfo points to a Mini App URL.
type WebAppInfo struct {
	URL string `json:"url"`
}

// InlineKeyboardButton is one button of an inline keyboard; exactly one of URL,
// CallbackData or WebApp must be set.
type InlineKeyboardButton struct {
	Text         string      `json:"text"`
	URL          string      `json:"url,omitempty"`
	CallbackData string      `json:"callback_data,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
}

// InlineKeyboardMarkup is an inline keyboard attached to a message.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// KeyboardButton is a reply keyboard button. Reply keyboards never carry
// web_app buttons here: Telegram sends no initData for them.
type KeyboardButton struct {
	Text            string `json:"text"`
	RequestContact  bool   `json:"request_contact,omitempty"`
	RequestLocation bool   `json:"request_location,omitempty"`
}

// ReplyKeyboardMarkup is a custom reply keyboard.
type ReplyKeyboardMarkup struct {
	Keyboard              [][]KeyboardButton `json:"keyboard"`
	ResizeKeyboard        bool               `json:"resize_keyboard,omitempty"`
	IsPersistent          bool               `json:"is_persistent,omitempty"`
	InputFieldPlaceholder string             `json:"input_field_placeholder,omitempty"`
}

// ReplyKeyboardRemove removes the current reply keyboard.
type ReplyKeyboardRemove struct {
	RemoveKeyboard bool `json:"remove_keyboard"`
}

// BotCommand is one entry of the bot command menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// MenuButtonWebApp is the chat menu button that opens a Mini App.
type MenuButtonWebApp struct {
	Text   string     `json:"text"`
	WebApp WebAppInfo `json:"web_app"`
}

// MarshalJSON adds the "type" discriminator required by the Bot API.
func (b MenuButtonWebApp) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type   string     `json:"type"`
		Text   string     `json:"text"`
		WebApp WebAppInfo `json:"web_app"`
	}{"web_app", b.Text, b.WebApp})
}

// GetUpdatesParams are the getUpdates arguments. A nil AllowedUpdates keeps the
// server-side setting; a non-nil (even empty) slice is sent explicitly.
type GetUpdatesParams struct {
	Offset         int64
	Limit          int
	Timeout        int // long-poll timeout in seconds
	AllowedUpdates []string
}

// SendMessageParams are the sendMessage arguments. ReplyMarkup may be an
// InlineKeyboardMarkup, ReplyKeyboardMarkup or ReplyKeyboardRemove (value or pointer).
type SendMessageParams struct {
	ChatID                int64
	Text                  string
	ParseMode             string
	ReplyMarkup           any
	DisableWebPagePreview bool
}

// EditMessageTextParams are the editMessageText arguments.
type EditMessageTextParams struct {
	ChatID                int64
	MessageID             int64
	Text                  string
	ParseMode             string
	ReplyMarkup           *InlineKeyboardMarkup
	DisableWebPagePreview bool
}

// AnswerCallbackQueryParams are the answerCallbackQuery arguments.
type AnswerCallbackQueryParams struct {
	CallbackQueryID string
	Text            string
	ShowAlert       bool
}

// InputPhoto is a photo to send: the file_id of a photo Telegram already
// has, or the bytes to upload (FileID wins).
type InputPhoto struct {
	FileID   string
	Data     []byte
	FileName string
}

// SendPhotoParams are the sendPhoto arguments. The caption is at most 1024
// characters after entities are parsed.
type SendPhotoParams struct {
	ChatID      int64
	Photo       InputPhoto
	Caption     string
	ParseMode   string
	ReplyMarkup any
}

// LargestPhoto returns the file_id of the biggest size of a sent photo.
func (m *Message) LargestPhoto() string {
	best, area := "", -1
	for _, p := range m.Photo {
		if a := p.Width * p.Height; a > area {
			best, area = p.FileID, a
		}
	}
	return best
}
