// Package notifications delivers Telegram messages through a transactional
// outbox (ARCHITECTURE §8). Business code calls Enqueue inside its own
// transaction; the Worker claims due rows with a lease, renders the template in
// the recipient's language and sends it with the matching bot.
package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Bot selects which Telegram bot sends a message.
type Bot string

const (
	PassengerBot Bot = "passenger"
	DriverBot    Bot = "driver"
)

// Valid reports whether b is a known bot.
func (b Bot) Valid() bool { return b == PassengerBot || b == DriverBot }

// Languages supported by templates; DefaultLanguage is used when a user has no
// preference or an unsupported one.
const (
	LangKK          = "kk"
	LangRU          = "ru"
	LangEN          = "en"
	DefaultLanguage = LangKK
)

// Languages lists the template languages in display order.
var Languages = []string{LangKK, LangRU, LangEN}

func validLanguage(l string) bool { return l == LangKK || l == LangRU || l == LangEN }

// Outbox statuses (notification_outbox.status).
const (
	StatusPending   = "pending"
	StatusSending   = "sending"
	StatusSent      = "sent"
	StatusDead      = "dead"
	StatusCancelled = "cancelled"
)

// Notification is one message to enqueue. Params must match the template's
// documented params (see templates.go); DedupeKey makes enqueueing idempotent,
// e.g. "order_accepted_passenger:<order_id>".
type Notification struct {
	Bot       Bot
	UserID    uuid.UUID
	Template  string
	Params    map[string]any
	DedupeKey string
}

var (
	// ErrUnknownTemplate means the template key is not in the registry.
	ErrUnknownTemplate = errors.New("notifications: unknown template")
	// ErrInvalidNotification means bot, params or dedupe key are invalid.
	ErrInvalidNotification = errors.New("notifications: invalid notification")
	// ErrUserNotFound means the recipient user does not exist.
	ErrUserNotFound = errors.New("notifications: user not found")
)

const maxDedupeKeyLen = 200

// Enqueue inserts n into notification_outbox using q (normally the caller's
// business transaction, so the message exists only if the transaction commits).
// The chat id is resolved from users.telegram_user_id. A duplicate DedupeKey is
// a silent no-op. Unknown templates, params that do not match the template, and
// unknown users return errors so mistakes surface in tests.
func Enqueue(ctx context.Context, q db.Querier, n Notification) error {
	t, ok := Lookup(n.Template)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownTemplate, n.Template)
	}
	if !t.AllowsBot(n.Bot) {
		return fmt.Errorf("%w: template %s cannot be sent by bot %q", ErrInvalidNotification, t.Key, n.Bot)
	}
	key := strings.TrimSpace(n.DedupeKey)
	if key == "" || len(key) > maxDedupeKeyLen {
		return fmt.Errorf("%w: dedupe key must be 1..%d bytes", ErrInvalidNotification, maxDedupeKeyLen)
	}
	if n.UserID == uuid.Nil {
		return fmt.Errorf("%w: user id required", ErrInvalidNotification)
	}
	if err := t.ValidateParams(n.Params); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidNotification, err)
	}
	params := n.Params
	if params == nil {
		params = map[string]any{}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("%w: params: %v", ErrInvalidNotification, err)
	}

	var id uuid.UUID
	err = q.QueryRow(ctx, `
		INSERT INTO notification_outbox (bot, user_id, chat_id, template, params, dedupe_key)
		SELECT $1, u.id, u.telegram_user_id, $3, $4, $5 FROM users u WHERE u.id = $2
		ON CONFLICT (dedupe_key) DO NOTHING
		RETURNING id`, string(n.Bot), n.UserID, t.Key, raw, key).Scan(&id)
	if err == nil {
		return nil
	}
	if !db.IsNoRows(err) {
		return fmt.Errorf("notifications: enqueue %s: %w", t.Key, err)
	}
	// No row: either a duplicate key (fine) or an unknown user.
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, n.UserID).Scan(&exists); err != nil {
		return fmt.Errorf("notifications: enqueue %s: %w", t.Key, err)
	}
	if !exists {
		return ErrUserNotFound
	}
	return nil
}
