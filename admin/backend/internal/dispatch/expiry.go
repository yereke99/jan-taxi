package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// SearchExpiryInterval is how often the search_expiry job runs (ARCHITECTURE §16).
const SearchExpiryInterval = 15 * time.Second

// expiryBatch bounds one run; the next run picks up the rest.
const expiryBatch = 200

// ExpireSearches cancels searching orders whose search window has passed
// (cancelled by system, reason no_driver_found), expires their open offers and
// tells the passenger. Each order is handled in its own short transaction; an
// order a driver accepted in the meantime is skipped. It returns how many
// orders were cancelled.
func ExpireSearches(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT id FROM orders
		WHERE status = 'searching' AND search_expires_at <= now()
		ORDER BY search_expires_at LIMIT $1`, expiryBatch)
	if err != nil {
		return 0, fmt.Errorf("dispatch: due searches: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, fmt.Errorf("dispatch: due searches: %w", err)
	}
	n := 0
	var errs []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		done, err := expireOne(ctx, pool, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if done {
			n++
		}
	}
	return n, errors.Join(errs...)
}

func expireOne(ctx context.Context, pool *pgxpool.Pool, orderID uuid.UUID) (bool, error) {
	done := false
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		// Re-check under the row lock with the database clock.
		var due bool
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(status = 'searching' AND search_expires_at <= now(), false)
			FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&due); err != nil {
			if db.IsNoRows(err) {
				return nil
			}
			return err
		}
		if !due {
			return nil
		}
		reason := lifecycle.ReasonNoDriverFound
		res, err := lifecycle.Cancel(ctx, tx, lifecycle.CancelInput{
			Change: lifecycle.Change{
				OrderID: orderID, From: []string{lifecycle.StatusSearching},
				Action: "expire", Actor: lifecycle.System(), Reason: &reason,
			},
			OfferStatus: lifecycle.OfferExpired,
		})
		if err != nil {
			return err
		}
		if err := lifecycle.Notify(ctx, tx, notifications.PassengerBot, res.After.PassengerID, notifications.TplOrderNoDriver,
			map[string]any{"order_number": res.After.Number}, "order_no_driver:"+orderID.String()); err != nil {
			return err
		}
		done = true
		return nil
	})
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Status < 500 {
			return false, nil // accepted or cancelled meanwhile
		}
		return false, fmt.Errorf("dispatch: expire %s: %w", orderID, err)
	}
	return done, nil
}
