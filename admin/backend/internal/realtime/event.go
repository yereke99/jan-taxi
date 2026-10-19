// Package realtime delivers small "something changed" hints to Mini App and admin
// clients. Publishers call Publish inside their business transaction, which runs
// pg_notify so the event leaves PostgreSQL only on commit. Every API instance
// LISTENs on the channel (Hub.Run) and fans events out to its WebSocket clients
// by scope.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Channel is the PostgreSQL NOTIFY channel.
const Channel = "jt_events"

// MaxPayloadBytes keeps the NOTIFY payload safely below PostgreSQL's 8000-byte limit.
const MaxPayloadBytes = 7900

// AdminScope addresses every connected admin session.
const AdminScope = "admin"

// Event types (ARCHITECTURE §9).
const (
	OrderCreated              = "order.created"
	OrderAvailable            = "order.available"
	OrderTaken                = "order.taken"
	OrderAccepted             = "order.accepted"
	OrderCancelled            = "order.cancelled"
	OrderDriverArriving       = "order.driver_arriving"
	OrderStarted              = "order.started"
	OrderStopCompleted        = "order.stop_completed"
	OrderCompleted            = "order.completed"
	OrderDisputed             = "order.disputed"
	DriverLocationUpdated     = "driver.location_updated"
	DriverStatusChanged       = "driver.status_changed"
	WalletUpdated             = "wallet.updated"
	TopUpUpdated              = "topup.updated"
	TopUpConfirmed            = "topup.confirmed"
	ModerationSubmitted       = "moderation.submitted"
	ModerationChangesRequired = "moderation.changes_requested"
	ModerationApproved        = "moderation.approved"
	ModerationRejected        = "moderation.rejected"
	DriverPhoneVerified       = "driver.phone_verified"
	// UserPhoneVerified tells the passenger app that the phone shared with
	// the passenger bot was stored (§22.5).
	UserPhoneVerified = "user.phone_verified"
)

// UserScope addresses a user (passenger app, and the driver app's user).
func UserScope(id uuid.UUID) string { return "user:" + id.String() }

// DriverScope addresses a driver profile.
func DriverScope(id uuid.UUID) string { return "driver:" + id.String() }

// Event is a hint for clients; they re-fetch authoritative state over REST.
// Data should hold only ids and statuses.
type Event struct {
	Type   string
	Scopes []string
	Data   any
}

// ErrPayloadTooLarge is returned when the encoded event does not fit in a NOTIFY.
var ErrPayloadTooLarge = errors.New("realtime: event payload too large")

// envelope is the NOTIFY payload.
type envelope struct {
	Type   string          `json:"type"`
	ID     uuid.UUID       `json:"id"`
	TS     time.Time       `json:"ts"`
	Scopes []string        `json:"scopes"`
	Data   json.RawMessage `json:"data"`
}

var eventTypeRe = regexp.MustCompile(`^[a-z][a-z_]*(\.[a-z][a-z_]*)+$`)

// Publish queues ev for delivery when the transaction behind q commits. Called
// with a pool instead of a transaction it is delivered immediately.
func Publish(ctx context.Context, q db.Querier, ev Event) error {
	payload, err := encode(ev, uuid.New(), time.Now())
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `SELECT pg_notify($1, $2)`, Channel, string(payload)); err != nil {
		return fmt.Errorf("realtime publish %s: %w", ev.Type, err)
	}
	return nil
}

func encode(ev Event, id uuid.UUID, ts time.Time) ([]byte, error) {
	if !eventTypeRe.MatchString(ev.Type) {
		return nil, fmt.Errorf("realtime: invalid event type %q", ev.Type)
	}
	if len(ev.Scopes) == 0 {
		return nil, errors.New("realtime: event has no scopes")
	}
	for _, s := range ev.Scopes {
		if !validScope(s) {
			return nil, fmt.Errorf("realtime: invalid scope %q", s)
		}
	}
	data := json.RawMessage(`{}`)
	if ev.Data != nil {
		b, err := json.Marshal(ev.Data)
		if err != nil {
			return nil, fmt.Errorf("realtime: encode %s data: %w", ev.Type, err)
		}
		data = b
	}
	payload, err := json.Marshal(envelope{Type: ev.Type, ID: id, TS: ts.UTC(), Scopes: ev.Scopes, Data: data})
	if err != nil {
		return nil, err
	}
	if len(payload) >= MaxPayloadBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes", ErrPayloadTooLarge, ev.Type, len(payload))
	}
	return payload, nil
}

func validScope(s string) bool {
	if s == AdminScope {
		return true
	}
	kind, id, ok := strings.Cut(s, ":")
	if !ok || (kind != "user" && kind != "driver") {
		return false
	}
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id // canonical form only, as produced by UserScope/DriverScope
}
