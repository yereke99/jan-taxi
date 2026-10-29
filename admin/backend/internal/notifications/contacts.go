package notifications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

var errContactAccessRevoked = errors.New("order contact access is no longer current")

// CancelOrderContacts supersedes unsent trip cards in the same transaction as
// a cancellation or new assignment. Already claimed cards are revalidated by
// the worker before sending; already delivered contacts cannot be revoked.
func CancelOrderContacts(ctx context.Context, q db.Querier, orderID uuid.UUID) error {
	_, err := q.Exec(ctx, `
		UPDATE notification_outbox SET status = 'cancelled', last_error = 'order contact access superseded'
		WHERE template = 'order_accepted_driver' AND params->>'order_id' = $1 AND status = 'pending'`, orderID.String())
	if err != nil {
		return fmt.Errorf("notifications: supersede order contacts: %w", err)
	}
	return nil
}

// lockContactOrder authorizes contact delivery against the current assignment.
// accepted_at and the trip card's created_at are both PostgreSQL now() in the
// assignment transaction. Their equality fences old messages even if the same
// driver is later assigned again, without treating arrival/start as revocation.
// The order lock prevents a cancellation/reassignment from committing between
// this check and the send. Both run under the bounded SendTimeout context.
func (w *Worker) lockContactOrder(ctx context.Context, m outboxRow) (pgx.Tx, error) {
	if m.template != TplOrderAcceptedDriver {
		return nil, nil
	}
	orderID, err := uuid.Parse(fmt.Sprint(m.params["order_id"]))
	if err != nil || m.userID == nil || m.bot != DriverBot {
		return nil, errContactAccessRevoked
	}
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	var authorized bool
	err = tx.QueryRow(ctx, `
		SELECT true FROM orders o JOIN driver_profiles d ON d.id = o.driver_id
		WHERE o.id = $1 AND d.user_id = $2
		  AND o.status IN ('accepted', 'driver_arriving', 'in_progress')
		  AND o.accepted_at = $3
		  AND EXISTS (SELECT 1 FROM notification_outbox n WHERE n.id = $4
		              AND n.status = 'sending' AND n.locked_by = $5 AND n.locked_until > clock_timestamp())
		FOR SHARE OF o`, orderID, *m.userID, m.createdAt, m.id, w.opt.InstanceID).Scan(&authorized)
	if err != nil {
		releaseContactLock(tx)
		if db.IsNoRows(err) {
			return nil, errContactAccessRevoked
		}
		return nil, err
	}
	return tx, nil
}

func releaseContactLock(tx pgx.Tx) {
	if tx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx) // read-only transaction; release the lock before recording the outcome
}
