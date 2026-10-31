package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// ExpandInterval is how often the dispatch_expand job runs (ARCHITECTURE §16).
const ExpandInterval = 15 * time.Second

// expandBatch bounds one run; the next run picks up the rest.
const expandBatch = 500

// ExpandOffers re-runs dispatch for every searching order: drivers who became
// eligible since the order was last offered (they came online, moved closer,
// topped up, finished a trip, or the order's growing search radius now
// reaches them) get an offer, an order_available_driver notification and an
// order.available event. Drivers who already have an offer for the order are
// never offered it again. Each order is handled in its own short transaction
// holding the order row lock; an order that another transaction holds right
// now (an acceptance in progress) is skipped until the next run. It returns
// the number of new offers.
func ExpandOffers(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT id FROM orders
		WHERE status = 'searching' AND (search_expires_at IS NULL OR search_expires_at > now())
		ORDER BY created_at LIMIT $1`, expandBatch)
	if err != nil {
		return 0, fmt.Errorf("dispatch: searching orders: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, fmt.Errorf("dispatch: searching orders: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	p := settings.Dispatch(ctx, pool)
	total := 0
	var errs []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := expandOne(ctx, pool, id, p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		total += n
	}
	return total, errors.Join(errs...)
}

func expandOne(ctx context.Context, pool *pgxpool.Pool, orderID uuid.UUID, p settings.DispatchSettings) (int, error) {
	n := 0
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var locked bool
		err := tx.QueryRow(ctx, `
			SELECT true FROM orders
			WHERE id = $1 AND status = 'searching' AND (search_expires_at IS NULL OR search_expires_at > now())
			FOR UPDATE SKIP LOCKED`, orderID).Scan(&locked)
		if db.IsNoRows(err) {
			return nil // accepted, cancelled or expired meanwhile, or busy
		}
		if err != nil {
			return err
		}
		o, err := lifecycle.LoadCore(ctx, tx, orderID, false)
		if err != nil {
			return err
		}
		n, err = offer(ctx, tx, o, p)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("dispatch: expand %s: %w", orderID, err)
	}
	return n, nil
}

// offerNearbyTimeout bounds OfferNearby, which runs after the caller's own
// transaction committed and must not hold the driver's request for long.
const offerNearbyTimeout = 5 * time.Second

// OfferNearby offers the searching orders already waiting around a driver who
// just became available — went online, or sent a fresh location after having
// none or a stale one — so the bot message and the order.available event
// arrive at once instead of on the next dispatch_expand run. Every candidate
// order goes through expandOne: the same eligibility rules, the same order
// row lock (an acceptance in progress is skipped) and the same
// UNIQUE(order_id, driver_id) deduplication, so nobody is offered an order
// twice. It returns the number of new offers; failures are left to the
// periodic job.
func OfferNearby(ctx context.Context, pool *pgxpool.Pool, driverID uuid.UUID) (int, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), offerNearbyTimeout)
	defer cancel()
	p := settings.Dispatch(ctx, pool)
	rows, err := pool.Query(ctx, `
		SELECT o.id FROM orders o
		JOIN driver_locations dl ON dl.driver_id = @driver_id
		WHERE o.status = 'searching' AND (o.search_expires_at IS NULL OR o.search_expires_at > now())
		  AND `+withinRadius+`
		  AND NOT EXISTS (SELECT 1 FROM order_offers f WHERE f.order_id = o.id AND f.driver_id = @driver_id)
		ORDER BY o.created_at LIMIT @batch`, policyArgs(p, pgx.NamedArgs{"driver_id": driverID, "batch": expandBatch}))
	if err != nil {
		return 0, fmt.Errorf("dispatch: orders near driver: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, fmt.Errorf("dispatch: orders near driver: %w", err)
	}
	total := 0
	var errs []error
	for _, id := range ids {
		n, err := expandOne(ctx, pool, id, p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		total += n
	}
	return total, errors.Join(errs...)
}
