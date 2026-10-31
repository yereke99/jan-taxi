package lifecycle

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
)

// maxScopesPerEvent keeps a fan-out event well below the NOTIFY payload limit
// ("driver:<uuid>" is 43 bytes).
const maxScopesPerEvent = 100

// Notify enqueues a Telegram message in the caller's transaction.
func Notify(ctx context.Context, q db.Querier, bot notifications.Bot, userID uuid.UUID, template string, params map[string]any, dedupeKey string) error {
	if err := notifications.Enqueue(ctx, q, notifications.Notification{
		Bot: bot, UserID: userID, Template: template, Params: params, DedupeKey: dedupeKey,
	}); err != nil {
		return fmt.Errorf("orders: notify %s: %w", template, err)
	}
	return nil
}

// EventData is the realtime payload for an order event: ids and status only.
func EventData(c Core, extra map[string]any) map[string]any {
	d := map[string]any{
		"order_id":     c.ID.String(),
		"order_number": c.Number,
		"status":       c.Status,
	}
	if c.DriverID != nil {
		d["driver_id"] = c.DriverID.String()
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// PartyScopes are the passenger, the assigned driver (if any) and the admins.
func PartyScopes(c Core) []string {
	s := []string{realtime.UserScope(c.PassengerID), realtime.AdminScope}
	if c.DriverID != nil {
		s = append(s, realtime.DriverScope(*c.DriverID))
	}
	return s
}

// Publish sends an order event to the given scopes when the transaction commits.
func Publish(ctx context.Context, q db.Querier, typ string, data any, scopes ...string) error {
	if len(scopes) == 0 {
		return nil
	}
	for start := 0; start < len(scopes); start += maxScopesPerEvent {
		end := min(start+maxScopesPerEvent, len(scopes))
		if err := realtime.Publish(ctx, q, realtime.Event{Type: typ, Scopes: scopes[start:end], Data: data}); err != nil {
			return err
		}
	}
	return nil
}

// PublishUnavailable tells every approved driver's app that order c left the
// market (taken, cancelled or expired), so its card disappears from every
// open list at once. The event holds the order id and status only.
func PublishUnavailable(ctx context.Context, q db.Querier, c Core) error {
	return Publish(ctx, q, realtime.OrderUnavailable,
		map[string]any{"order_id": c.ID.String(), "status": c.Status}, realtime.DriversScope)
}

// DriverScopes maps driver ids to their realtime scopes.
func DriverScopes(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, realtime.DriverScope(id))
	}
	return out
}

// PublishWallet tells a driver's clients that the wallet changed.
func PublishWallet(ctx context.Context, q db.Querier, driverID uuid.UUID, orderID uuid.UUID, reason string) error {
	return Publish(ctx, q, realtime.WalletUpdated,
		map[string]any{"driver_id": driverID.String(), "order_id": orderID.String(), "reason": reason},
		realtime.DriverScope(driverID))
}
