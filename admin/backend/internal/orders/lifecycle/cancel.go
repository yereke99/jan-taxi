package lifecycle

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// CancelInput describes a cancellation. Change.To is always cancelled.
type CancelInput struct {
	Change
	// OfferStatus closes the offers still waiting for an answer
	// (OfferCancelled, or OfferExpired for the search timeout).
	OfferStatus string
}

// CancelResult reports what a cancellation did.
type CancelResult struct {
	Before, After Core
	// Released is a reservation (earlier commission model) returned to the
	// assigned driver's available balance.
	Released int64
	// OfferedDrivers are drivers whose open offer was closed.
	OfferedDrivers []uuid.UUID
}

// Cancel moves an order to cancelled, closes open offers (their bot messages
// are marked cancelled) and publishes order.cancelled. The commission charged
// at acceptance is never returned here (§22.2: a passenger's cancellation
// keeps it; operators refund explicitly); a reservation left by an
// acceptance under the earlier model is released (plus wallet.updated).
// Notifications are the caller's job because they depend on who cancelled.
func Cancel(ctx context.Context, tx pgx.Tx, in CancelInput) (CancelResult, error) {
	in.To = StatusCancelled
	if in.Action == "" {
		in.Action = "cancel"
	}
	before, after, err := Apply(ctx, tx, in.Change)
	if err != nil {
		return CancelResult{}, err
	}
	res := CancelResult{Before: before, After: after}
	if err := notifications.CancelOrderContacts(ctx, tx, after.ID); err != nil {
		return CancelResult{}, err
	}
	note := "order cancelled by " + in.Actor.Type
	if res.Released, err = wallets.ReleaseForOrder(ctx, tx, in.OrderID, note); err != nil {
		return CancelResult{}, DBError(err)
	}
	offerStatus := in.OfferStatus
	if offerStatus == "" {
		offerStatus = OfferCancelled
	}
	if res.OfferedDrivers, err = CloseOpenOffers(ctx, tx, in.OrderID, offerStatus, uuid.Nil); err != nil {
		return CancelResult{}, err
	}
	message := OfferMessageCancelled
	if offerStatus == OfferExpired {
		message = OfferMessageExpired
	}
	if err := CloseOfferMessages(ctx, tx, in.OrderID, res.OfferedDrivers, message); err != nil {
		return CancelResult{}, err
	}
	data := EventData(after, map[string]any{"cancelled_by": in.Actor.Type})
	scopes := append(PartyScopes(after), DriverScopes(res.OfferedDrivers)...)
	if err := Publish(ctx, tx, realtime.OrderCancelled, data, scopes...); err != nil {
		return CancelResult{}, err
	}
	if before.Status == StatusSearching {
		if err := PublishUnavailable(ctx, tx, after); err != nil {
			return CancelResult{}, err
		}
	}
	if after.DriverID != nil && res.Released > 0 {
		if err := PublishWallet(ctx, tx, *after.DriverID, after.ID, "commission_release"); err != nil {
			return CancelResult{}, err
		}
	}
	return res, nil
}

// CloseOpenOffers closes the order's offers that are still "sent" (except the
// one of exceptDriver) with status and returns the affected drivers.
func CloseOpenOffers(ctx context.Context, q db.Querier, orderID uuid.UUID, status string, exceptDriver uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `
		UPDATE order_offers SET status = $2, closed_at = now()
		WHERE order_id = $1 AND status = 'sent' AND driver_id <> $3
		RETURNING driver_id`, orderID, status, exceptDriver)
	if err != nil {
		return nil, fmt.Errorf("orders: close offers: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("orders: close offers: %w", err)
	}
	return ids, nil
}
