package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Event is one row of the append-only order_status_events journal.
type Event struct {
	OrderID uuid.UUID
	From    *string // nil for the creation event
	To      string
	Actor   Actor
	Reason  *string
	Meta    map[string]any
}

// InsertEvent journals a status change.
func InsertEvent(ctx context.Context, q db.Querier, e Event) error {
	meta := []byte(`{}`)
	if len(e.Meta) > 0 {
		b, err := json.Marshal(e.Meta)
		if err != nil {
			return fmt.Errorf("orders: event meta: %w", err)
		}
		meta = b
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO order_status_events (order_id, from_status, to_status, actor_type, actor_id, reason, meta)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.OrderID, e.From, e.To, e.Actor.Type, e.Actor.ID, e.Reason, meta); err != nil {
		return fmt.Errorf("orders: insert status event: %w", err)
	}
	return nil
}

func isNoRows(err error) bool { return db.IsNoRows(err) }

// StrPtr returns a pointer to s, or nil for an empty string.
func StrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
