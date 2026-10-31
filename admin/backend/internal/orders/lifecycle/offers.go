package lifecycle

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Offer messages in the driver bot (ARCHITECTURE §22.4). Every offer is one
// order_available_driver notification with the dedupe key OfferMessageKey;
// when the order leaves the driver's hands the message is edited through the
// outbox: «✅ Сіз алдыңыз» for the driver who got it, "taken" for the others,
// "cancelled" when the order was cancelled, "expired" when its search timed
// out. An offer not sent yet is simply never sent.

// Offer message outcomes.
const (
	OfferMessageWon       = "won"
	OfferMessageTaken     = "taken"
	OfferMessageCancelled = "cancelled"
	OfferMessageExpired   = "expired"
)

// OfferMessageKey is the outbox dedupe key of driverID's offer of orderID.
func OfferMessageKey(orderID, driverID uuid.UUID) string {
	return "order_available:" + orderID.String() + ":" + driverID.String()
}

// CloseOfferMessage queues the edit of driverID's offer message of orderID
// for outcome (OfferMessageWon, OfferMessageTaken, OfferMessageCancelled or
// OfferMessageExpired).
// Drivers who never got an offer message are skipped.
func CloseOfferMessage(ctx context.Context, q db.Querier, orderID, driverID uuid.UUID, outcome string) error {
	e := notifications.Edit{
		RefDedupeKey: OfferMessageKey(orderID, driverID),
		DedupeKey:    "offer_" + outcome + ":" + orderID.String() + ":" + driverID.String(),
	}
	switch outcome {
	case OfferMessageWon:
		e.Template = notifications.TplOrderOfferWon
		e.Params = map[string]any{"order_id": orderID.String()}
	case OfferMessageTaken:
		e.Template = notifications.TplOrderOfferTaken
	case OfferMessageCancelled:
		e.Template = notifications.TplOrderOfferCancelled
	case OfferMessageExpired:
		e.Template = notifications.TplOrderOfferExpired
	default:
		return fmt.Errorf("orders: unknown offer outcome %q", outcome)
	}
	if _, err := notifications.EnqueueEdit(ctx, q, e); err != nil {
		return fmt.Errorf("orders: close offer message: %w", err)
	}
	return nil
}

// CloseOfferMessages queues outcome for every driver in driverIDs.
func CloseOfferMessages(ctx context.Context, q db.Querier, orderID uuid.UUID, driverIDs []uuid.UUID, outcome string) error {
	for _, id := range driverIDs {
		if err := CloseOfferMessage(ctx, q, orderID, id, outcome); err != nil {
			return err
		}
	}
	return nil
}
