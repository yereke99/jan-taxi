// Package users owns Telegram users, their preferences (language) and the
// per-bot chat state (started / blocked). Mini App and bot code reach users only
// through these functions so the "new users start in Kazakh" rule lives in one place.
package users

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// DefaultLanguage is the language of every new user, whatever Telegram reports.
const DefaultLanguage = "kk"

// Languages are the supported UI languages in display order.
var Languages = []string{"kk", "ru", "en"}

// ValidLanguage reports whether lang is a supported language code.
func ValidLanguage(lang string) bool { return slices.Contains(Languages, lang) }

// Bots that own per-user chat state.
const (
	BotPassenger = "passenger"
	BotDriver    = "driver"
)

// Errors are *httpx.Error values so handlers can return them unchanged;
// compare with errors.Is.
var (
	ErrNotFound            = httpx.NewError(http.StatusNotFound, "not_found", "user not found")
	ErrInvalidLanguage     = &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "unsupported language", Fields: map[string]string{"language": "invalid"}}
	ErrInvalidBot          = httpx.BadRequest("unknown bot")
	ErrInvalidTelegramUser = httpx.BadRequest("invalid telegram user")
)

// User is a Telegram user with their preferred language.
type User struct {
	ID                    uuid.UUID
	TelegramUserID        int64
	FirstName             string
	LastName              string
	Username              string
	TelegramLanguageCode  string
	Language              string
	PhoneE164             string
	PhoneVerifiedAt       *time.Time
	PassengerBotStartedAt *time.Time
	PassengerBotBlockedAt *time.Time
	DriverBotStartedAt    *time.Time
	DriverBotBlockedAt    *time.Time
	IsBlocked             bool
	BlockedReason         string
	LastSeenAt            *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// TelegramUser is the identity received from a Telegram update or initData.
type TelegramUser struct {
	ID           int64
	FirstName    string
	LastName     string
	Username     string
	LanguageCode string
}

const selectUser = `
	SELECT u.id, u.telegram_user_id, u.first_name, u.last_name, COALESCE(u.username, ''),
	       COALESCE(u.telegram_language_code, ''), COALESCE(p.language, 'kk'), COALESCE(u.phone_e164, ''),
	       u.phone_verified_at, u.passenger_bot_started_at, u.passenger_bot_blocked_at,
	       u.driver_bot_started_at, u.driver_bot_blocked_at, u.is_blocked, COALESCE(u.blocked_reason, ''),
	       u.last_seen_at, u.created_at, u.updated_at
	FROM users u
	LEFT JOIN user_preferences p ON p.user_id = u.id`

func getUser(ctx context.Context, q db.Querier, where string, arg any) (User, error) {
	var u User
	err := q.QueryRow(ctx, selectUser+" WHERE "+where, arg).Scan(
		&u.ID, &u.TelegramUserID, &u.FirstName, &u.LastName, &u.Username,
		&u.TelegramLanguageCode, &u.Language, &u.PhoneE164,
		&u.PhoneVerifiedAt, &u.PassengerBotStartedAt, &u.PassengerBotBlockedAt,
		&u.DriverBotStartedAt, &u.DriverBotBlockedAt, &u.IsBlocked, &u.BlockedReason,
		&u.LastSeenAt, &u.CreatedAt, &u.UpdatedAt)
	if db.IsNoRows(err) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	return u, nil
}

// GetByID loads a user by id.
func GetByID(ctx context.Context, q db.Querier, id uuid.UUID) (User, error) {
	return getUser(ctx, q, "u.id = $1", id)
}

// GetByTelegramID loads a user by Telegram user id.
func GetByTelegramID(ctx context.Context, q db.Querier, tgID int64) (User, error) {
	return getUser(ctx, q, "u.telegram_user_id = $1", tgID)
}

// Language returns the user's language ("kk" when no preference row exists).
func Language(ctx context.Context, q db.Querier, userID uuid.UUID) (string, error) {
	var lang string
	err := q.QueryRow(ctx, `
		SELECT COALESCE(p.language, 'kk') FROM users u
		LEFT JOIN user_preferences p ON p.user_id = u.id
		WHERE u.id = $1`, userID).Scan(&lang)
	if db.IsNoRows(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load language: %w", err)
	}
	return lang, nil
}

// SetLanguage stores the user's explicit language choice.
func SetLanguage(ctx context.Context, q db.Querier, userID uuid.UUID, lang string) error {
	if !ValidLanguage(lang) {
		return ErrInvalidLanguage
	}
	_, err := q.Exec(ctx, `
		INSERT INTO user_preferences (user_id, language, language_chosen_at) VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET language = EXCLUDED.language, language_chosen_at = now()`,
		userID, lang)
	if db.IsForeignKeyViolation(err, "") {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("set language: %w", err)
	}
	return nil
}

// botPrefix maps a bot name to its column prefix; only these two values ever
// reach SQL text.
func botPrefix(bot string) (string, error) {
	switch bot {
	case BotPassenger:
		return "passenger_bot", nil
	case BotDriver:
		return "driver_bot", nil
	}
	return "", ErrInvalidBot
}

// SyncTelegramUser creates or refreshes a user from Telegram data. New users
// always get language "kk" regardless of Telegram's language_code. When started
// is true (the user pressed /start or opened the bot), <bot>_bot_started_at is
// set and <bot>_bot_blocked_at cleared.
func SyncTelegramUser(ctx context.Context, q db.Querier, tu TelegramUser, bot string, started bool) (User, bool, error) {
	if tu.ID <= 0 {
		return User{}, false, ErrInvalidTelegramUser
	}
	prefix, err := botPrefix(bot)
	if err != nil {
		return User{}, false, err
	}
	var id uuid.UUID
	var isNew bool
	err = q.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO users (telegram_user_id, first_name, last_name, username, telegram_language_code, %[1]s_started_at)
		VALUES ($1, $2, $3, $4, $5, CASE WHEN $6::boolean THEN now() END)
		ON CONFLICT (telegram_user_id) DO UPDATE SET
			first_name = EXCLUDED.first_name,
			last_name = EXCLUDED.last_name,
			username = EXCLUDED.username,
			telegram_language_code = COALESCE(EXCLUDED.telegram_language_code, users.telegram_language_code),
			%[1]s_started_at = CASE WHEN $6::boolean THEN now() ELSE users.%[1]s_started_at END,
			%[1]s_blocked_at = CASE WHEN $6::boolean THEN NULL ELSE users.%[1]s_blocked_at END
		RETURNING id, (xmax = 0)`, prefix),
		tu.ID, cleanName(tu.FirstName), cleanName(tu.LastName), cleanUsername(tu.Username),
		cleanLanguageCode(tu.LanguageCode), started).Scan(&id, &isNew)
	if err != nil {
		return User{}, false, fmt.Errorf("upsert user: %w", err)
	}
	if _, err := q.Exec(ctx, `INSERT INTO user_preferences (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, id); err != nil {
		return User{}, false, fmt.Errorf("ensure preferences: %w", err)
	}
	u, err := GetByID(ctx, q, id)
	return u, isNew, err
}

// MarkBotBlocked records that the user blocked (or unblocked) the bot. The
// first block time is kept when the block is reported again.
func MarkBotBlocked(ctx context.Context, q db.Querier, telegramID int64, bot string, blocked bool) error {
	prefix, err := botPrefix(bot)
	if err != nil {
		return err
	}
	value := "NULL"
	if blocked {
		value = fmt.Sprintf("COALESCE(%s_blocked_at, now())", prefix)
	}
	tag, err := q.Exec(ctx, fmt.Sprintf(`UPDATE users SET %s_blocked_at = %s WHERE telegram_user_id = $1`, prefix, value), telegramID)
	if err != nil {
		return fmt.Errorf("mark bot blocked: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

var (
	usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
	langCodeRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8}){0,3}$`)
)

// maxNameRunes matches Telegram's own 64-character limit on names.
const maxNameRunes = 64

// cleanName normalises a Telegram name and truncates it instead of failing:
// a long or odd display name must never block sign-in.
func cleanName(s string) string {
	if r := []rune(s); len(r) > 4*maxNameRunes {
		s = string(r[:4*maxNameRunes])
	}
	clean, err := textutil.CleanText(s, 4*maxNameRunes)
	if err != nil {
		return ""
	}
	if r := []rune(clean); len(r) > maxNameRunes {
		clean = strings.TrimSpace(string(r[:maxNameRunes]))
	}
	return clean
}

func cleanUsername(s string) *string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "@")
	if !usernameRe.MatchString(s) {
		return nil
	}
	return &s
}

func cleanLanguageCode(s string) *string {
	s = strings.TrimSpace(s)
	if len(s) > 35 || !langCodeRe.MatchString(s) {
		return nil
	}
	s = strings.ToLower(s)
	return &s
}

// MaskPhone renders "+77011234567" as "+7 701 *** ** 67"; other formats keep
// only the last two digits visible.
func MaskPhone(e164 string) string {
	if e164 == "" {
		return ""
	}
	if len(e164) == 12 && strings.HasPrefix(e164, "+7") {
		return "+7 " + e164[2:5] + " *** ** " + e164[10:]
	}
	if len(e164) <= 4 {
		return strings.Repeat("*", len(e164))
	}
	return strings.Repeat("*", len(e164)-2) + e164[len(e164)-2:]
}
