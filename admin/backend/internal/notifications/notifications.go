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
	"time"

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
	// Delay holds the message back (e.g. until an arrival time is known);
	// Release sends it earlier.
	Delay time.Duration
}

// Outbox row kinds (notification_outbox.kind).
const (
	KindSend = "send"
	KindEdit = "edit"
)

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
	if t.Edit {
		return fmt.Errorf("%w: template %s only edits messages (EnqueueEdit)", ErrInvalidNotification, t.Key)
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
		INSERT INTO notification_outbox (bot, user_id, chat_id, template, params, dedupe_key, next_attempt_at)
		SELECT $1, u.id, u.telegram_user_id, $3, $4, $5, now() + $6 * interval '1 millisecond'
		FROM users u WHERE u.id = $2
		ON CONFLICT (dedupe_key) DO NOTHING
		RETURNING id`, string(n.Bot), n.UserID, t.Key, raw, key, max(n.Delay, 0).Milliseconds()).Scan(&id)
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

// Edit rewrites the Telegram message sent for an earlier notification (the
// one enqueued with RefDedupeKey), e.g. a driver's offer once the order is
// taken or cancelled. Template must be an edit template of the same bot.
type Edit struct {
	RefDedupeKey string
	Template     string
	Params       map[string]any
	DedupeKey    string
}

// EnqueueEdit queues e in the caller's transaction and reports whether
// anything was queued. When the original message has not been sent yet it is
// cancelled instead (nothing to edit, nothing to send); when it was never
// queued, died or was cancelled, nothing happens. Otherwise an outbox row of
// kind "edit" is inserted; the worker applies it once the original is sent.
// A duplicate DedupeKey is a no-op.
func EnqueueEdit(ctx context.Context, q db.Querier, e Edit) (bool, error) {
	t, ok := Lookup(e.Template)
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrUnknownTemplate, e.Template)
	}
	if !t.Edit {
		return false, fmt.Errorf("%w: template %s does not edit messages", ErrInvalidNotification, t.Key)
	}
	key := strings.TrimSpace(e.DedupeKey)
	if key == "" || len(key) > maxDedupeKeyLen || strings.TrimSpace(e.RefDedupeKey) == "" {
		return false, fmt.Errorf("%w: dedupe keys must be 1..%d bytes", ErrInvalidNotification, maxDedupeKeyLen)
	}
	if err := t.ValidateParams(e.Params); err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidNotification, err)
	}
	params := e.Params
	if params == nil {
		params = map[string]any{}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return false, fmt.Errorf("%w: params: %v", ErrInvalidNotification, err)
	}
	// Never send what is already out of date. A worker holding the row
	// (status sending) is waited for; the edit then follows the message.
	tag, err := q.Exec(ctx, `
		UPDATE notification_outbox SET status = 'cancelled', last_error = 'superseded by ' || $2,
		       locked_until = NULL, locked_by = NULL
		WHERE dedupe_key = $1 AND kind = 'send' AND status = 'pending'`, e.RefDedupeKey, t.Key)
	if err != nil {
		return false, fmt.Errorf("notifications: cancel unsent %s: %w", e.RefDedupeKey, err)
	}
	if tag.RowsAffected() > 0 {
		return false, nil
	}
	tag, err = q.Exec(ctx, `
		INSERT INTO notification_outbox (kind, ref_outbox_id, bot, user_id, chat_id, template, params, dedupe_key)
		SELECT 'edit', r.id, r.bot, r.user_id, r.chat_id, $2, $3, $4
		FROM notification_outbox r
		WHERE r.dedupe_key = $1 AND r.kind = 'send' AND r.status IN ('sending', 'sent') AND r.bot = ANY($5)
		ON CONFLICT (dedupe_key) DO NOTHING`, e.RefDedupeKey, t.Key, raw, key, botNames(t.Bots))
	if err != nil {
		return false, fmt.Errorf("notifications: enqueue edit %s: %w", t.Key, err)
	}
	return tag.RowsAffected() > 0, nil
}

func botNames(bots []Bot) []string {
	out := make([]string, len(bots))
	for i, b := range bots {
		out[i] = string(b)
	}
	return out
}

// Release sends a delayed notification now if it is still waiting.
func Release(ctx context.Context, q db.Querier, dedupeKey string) error {
	_, err := q.Exec(ctx, `
		UPDATE notification_outbox SET next_attempt_at = now()
		WHERE dedupe_key = $1 AND status = 'pending' AND next_attempt_at > now()`, dedupeKey)
	if err != nil {
		return fmt.Errorf("notifications: release %s: %w", dedupeKey, err)
	}
	return nil
}
