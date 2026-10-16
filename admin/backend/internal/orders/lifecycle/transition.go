package lifecycle

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Actor is who performs a transition. ID is the user id for a passenger, the
// driver profile id for a driver and the admin id for an admin; nil for the
// system.
type Actor struct {
	Type string
	ID   *uuid.UUID
}

// Passenger, Driver, Admin and System build actors.
func Passenger(userID uuid.UUID) Actor { return Actor{Type: ActorPassenger, ID: &userID} }
func Driver(driverID uuid.UUID) Actor  { return Actor{Type: ActorDriver, ID: &driverID} }
func Admin(adminID uuid.UUID) Actor    { return Actor{Type: ActorAdmin, ID: &adminID} }
func System() Actor                    { return Actor{Type: ActorSystem} }

// Change describes one guarded status transition.
type Change struct {
	OrderID uuid.UUID
	From    []string // statuses the transition is allowed from
	To      string
	Action  string // reported in invalid_transition details, e.g. "cancel"
	// OnlyDriver restricts the transition to the order's assigned driver;
	// for anyone else the order does not exist (404).
	OnlyDriver *uuid.UUID
	// OnlyPassenger restricts the transition to the order's passenger (404 otherwise).
	OnlyPassenger *uuid.UUID
	Actor         Actor
	Reason        *string        // stored on the event; for cancellations also as cancel_reason
	Meta          map[string]any // stored on the event
}

// Apply locks the order, checks the caller may act on it and that its status
// is one of c.From, then moves it to c.To with a version bump and the matching
// timestamp, and journals the transition. It returns the order before and after.
func Apply(ctx context.Context, tx pgx.Tx, c Change) (before, after Core, err error) {
	before, err = LoadCore(ctx, tx, c.OrderID, true)
	if err != nil {
		return Core{}, Core{}, err
	}
	if c.OnlyDriver != nil && !before.HasDriver(*c.OnlyDriver) {
		return Core{}, Core{}, httpx.NotFound()
	}
	if c.OnlyPassenger != nil && before.PassengerID != *c.OnlyPassenger {
		return Core{}, Core{}, httpx.NotFound()
	}
	if !slices.Contains(c.From, before.Status) {
		return Core{}, Core{}, InvalidTransition(before.Status, c.Action)
	}
	after, err = update(ctx, tx, before, c)
	if err != nil {
		return Core{}, Core{}, err
	}
	if err := InsertEvent(ctx, tx, Event{OrderID: c.OrderID, From: &before.Status, To: c.To,
		Actor: c.Actor, Reason: c.Reason, Meta: c.Meta}); err != nil {
		return Core{}, Core{}, err
	}
	return before, after, nil
}

// update performs the conditional UPDATE for c. The row is already locked, so
// the status guard always matches; it stays as a second line of defence.
func update(ctx context.Context, tx pgx.Tx, before Core, c Change) (Core, error) {
	var set string
	args := []any{c.OrderID, before.Status, c.To}
	switch c.To {
	case StatusDriverArriving:
		set = `arrived_at = now()`
	case StatusInProgress:
		set = `started_at = now()`
	case StatusCompleted:
		set = `completed_at = COALESCE(completed_at, now())`
	case StatusCancelled:
		args = append(args, c.Actor.Type, c.Reason)
		set = `cancelled_at = now(), cancelled_by = $4, cancel_reason = $5, search_expires_at = NULL`
	case StatusDisputed:
		set = `updated_at = now()`
	default:
		return Core{}, fmt.Errorf("orders: no transition to %s", c.To)
	}
	sql := `UPDATE orders o SET status = $3, version = version + 1, ` + set + `
		WHERE o.id = $1 AND o.status = $2
		RETURNING ` + CoreColumns
	after, err := ScanCore(tx.QueryRow(ctx, sql, args...))
	if err != nil {
		if isNoRows(err) {
			return Core{}, InvalidTransition(before.Status, c.Action)
		}
		return Core{}, DBError(fmt.Errorf("orders: transition %s→%s: %w", before.Status, c.To, err))
	}
	return after, nil
}
