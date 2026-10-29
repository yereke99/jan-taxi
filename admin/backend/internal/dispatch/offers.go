package dispatch

import (
	"context"
	"fmt"
	"maps"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

type offeredDriver struct {
	DriverID uuid.UUID
	UserID   uuid.UUID
}

type offerTarget struct {
	DriverID  uuid.UUID
	UserID    uuid.UUID
	DistanceM *int
}

// OfferNewOrder runs in the order-creation transaction: every free driver
// whose fresh location is within the base radius of the pickup (§7) gets an
// order_offers row, a Telegram notification (order_available_driver) and an
// order.available event. A new order's effective radius is the base radius;
// the dispatch_expand job reaches further as the search goes on. It returns
// the number of drivers offered.
func OfferNewOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (int, error) {
	o, err := lifecycle.LoadCore(ctx, tx, orderID, false)
	if err != nil {
		return 0, err
	}
	if o.Status != lifecycle.StatusSearching {
		return 0, nil
	}
	return offer(ctx, tx, o, settings.Dispatch(ctx, tx))
}

// offer creates offers for the searching order o for every eligible driver
// within its current effective radius who has none yet, then notifies and
// tells exactly those drivers. The UNIQUE(order_id, driver_id) constraint
// keeps a driver from being offered the same order twice.
func offer(ctx context.Context, tx pgx.Tx, o lifecycle.Core, p settings.DispatchSettings) (int, error) {
	rows, err := tx.Query(ctx, `
		WITH eligible AS (
			SELECT d.id AS driver_id, d.user_id, `+distanceToPickup+` AS dist
			FROM orders o
			JOIN driver_locations dl ON `+withinRadius+`
			JOIN driver_profiles d ON d.id = dl.driver_id AND d.status = 'approved' AND d.is_online
			JOIN users u ON u.id = d.user_id AND NOT u.is_blocked
			LEFT JOIN wallets w ON w.driver_id = d.id
			WHERE o.id = @order_id AND o.status = 'searching'
			  AND `+freshLocation+`
			  AND d.user_id <> o.passenger_id
			  AND `+vehicleFits+`
			  AND COALESCE(w.balance - w.reserved, 0) >= o.commission_amount
			  AND NOT EXISTS (SELECT 1 FROM orders a WHERE a.driver_id = d.id
			                  AND a.status IN ('accepted', 'driver_arriving', 'in_progress'))
		), inserted AS (
			INSERT INTO order_offers (order_id, driver_id, distance_to_pickup_m)
			SELECT @order_id, driver_id, dist FROM eligible
			ON CONFLICT (order_id, driver_id) DO NOTHING
			RETURNING driver_id, distance_to_pickup_m
		)
		SELECT e.driver_id, e.user_id, i.distance_to_pickup_m
		FROM inserted i JOIN eligible e ON e.driver_id = i.driver_id
		ORDER BY i.distance_to_pickup_m ASC, e.driver_id`, policyArgs(p, pgx.NamedArgs{"order_id": o.ID}))
	if err != nil {
		return 0, fmt.Errorf("dispatch: offer order %s: %w", o.ID, err)
	}
	drivers, err := pgx.CollectRows(rows, pgx.RowToStructByPos[offerTarget])
	if err != nil {
		return 0, fmt.Errorf("dispatch: offer order %s: %w", o.ID, err)
	}
	if err := warnUnaffordable(ctx, tx, o, p); err != nil {
		return 0, err
	}
	if len(drivers) == 0 {
		return 0, nil
	}
	base, err := offerParams(ctx, tx, o)
	if err != nil {
		return 0, err
	}
	ids := make([]uuid.UUID, len(drivers))
	for i, d := range drivers {
		ids[i] = d.DriverID
		params := maps.Clone(base)
		if d.DistanceM != nil {
			params["pickup_distance"] = *d.DistanceM
		}
		if err := lifecycle.Notify(ctx, tx, notifications.DriverBot, d.UserID, notifications.TplOrderAvailableDriver,
			params, lifecycle.OfferMessageKey(o.ID, d.DriverID)); err != nil {
			return 0, err
		}
	}
	data := map[string]any{
		"order_id": o.ID.String(), "order_number": o.Number, "status": o.Status,
		"category": o.Category, "fare": o.Fare,
	}
	if err := lifecycle.Publish(ctx, tx, realtime.OrderAvailable, data, lifecycle.DriverScopes(ids)...); err != nil {
		return 0, err
	}
	return len(drivers), nil
}

// warnUnaffordable sends wallet_low_balance (with a top-up button, at most
// once per driver and day) to the drivers order o would have been offered
// but whose available balance does not cover its commission (§22.2): they
// receive no offers until they top up.
func warnUnaffordable(ctx context.Context, tx pgx.Tx, o lifecycle.Core, p settings.DispatchSettings) error {
	if o.CommissionAmount <= 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT d.id, d.user_id, COALESCE(w.balance - w.reserved, 0)
		FROM orders o
		JOIN driver_locations dl ON `+withinRadius+`
		JOIN driver_profiles d ON d.id = dl.driver_id AND d.status = 'approved' AND d.is_online
		JOIN users u ON u.id = d.user_id AND NOT u.is_blocked
		LEFT JOIN wallets w ON w.driver_id = d.id
		WHERE o.id = @order_id AND o.status = 'searching'
		  AND `+freshLocation+`
		  AND d.user_id <> o.passenger_id
		  AND `+vehicleFits+`
		  AND COALESCE(w.balance - w.reserved, 0) < o.commission_amount
		  AND NOT EXISTS (SELECT 1 FROM orders a WHERE a.driver_id = d.id
		                  AND a.status IN ('accepted', 'driver_arriving', 'in_progress'))`,
		policyArgs(p, pgx.NamedArgs{"order_id": o.ID}))
	if err != nil {
		return fmt.Errorf("dispatch: low balance drivers: %w", err)
	}
	type poorDriver struct {
		DriverID, UserID uuid.UUID
		Available        int64
	}
	poor, err := pgx.CollectRows(rows, pgx.RowToStructByPos[poorDriver])
	if err != nil {
		return fmt.Errorf("dispatch: low balance drivers: %w", err)
	}
	for _, d := range poor {
		if err := lifecycle.Notify(ctx, tx, notifications.DriverBot, d.UserID, notifications.TplWalletLowBalance,
			map[string]any{"available": max(d.Available, 0), "required": o.CommissionAmount}, lowBalanceKey(d.DriverID)); err != nil {
			return err
		}
	}
	return nil
}

// offerParams are the order_available_driver params shared by every driver
// offered order o (§22.4); each driver adds the distance to the pickup.
func offerParams(ctx context.Context, tx pgx.Tx, o lifecycle.Core) (map[string]any, error) {
	var (
		category, payment         string
		distanceM, durationS      int
		passengers                *int
		cargoSize, cargo, comment *string
		classKK, classRU, classEN string
		locality                  *string
	)
	err := tx.QueryRow(ctx, `
		SELECT o.category, o.payment_method, COALESCE(o.distance_m,0), COALESCE(o.duration_s,0), o.passenger_count, o.cargo_size,
		       o.cargo_description, o.comment, vc.name_kk, vc.name_ru, vc.name_en, o.locality
		FROM orders o JOIN vehicle_classes vc ON vc.code = o.vehicle_class
		WHERE o.id = $1`, o.ID).Scan(&category, &payment, &distanceM, &durationS, &passengers, &cargoSize,
		&cargo, &comment, &classKK, &classRU, &classEN, &locality)
	if err != nil {
		return nil, fmt.Errorf("dispatch: offer details: %w", err)
	}
	route, err := lifecycle.LoadRoute(ctx, tx, o.ID)
	if err != nil {
		return nil, err
	}
	params := map[string]any{
		"order_id":       o.ID.String(),
		"order_number":   o.Number,
		"category":       category,
		"vehicle_class":  map[string]string{"kk": classKK, "ru": classRU, "en": classEN},
		"pickup":         route.Pickup,
		"destination":    route.Destination,
		"route_distance": distanceM,
		"route_duration": durationS,
		"fare":           o.Fare,
		"payment_method": payment,
	}
	if route.DestinationManual {
		delete(params, "route_distance")
		delete(params, "route_duration")
	}
	if via := route.ViaText("…"); via != "" {
		params["via"] = via
	}
	if locality != nil && *locality != "" {
		params["locality"] = *locality
	}
	if o.CommissionAmount > 0 {
		params["commission"] = o.CommissionAmount
	}
	if passengers != nil {
		params["passengers"] = *passengers
	}
	if cargoSize != nil {
		params["cargo_size"] = *cargoSize
	}
	if cargo != nil && *cargo != "" {
		params["cargo"] = *cargo
	}
	if comment != nil && *comment != "" {
		params["comment"] = *comment
	}
	return params, nil
}

// closeOffersOnAccept marks the winner's offer accepted (creating it when the
// driver accepted without one) and every other open offer lost. It returns
// the drivers who lost.
func closeOffersOnAccept(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) ([]offeredDriver, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_offers (order_id, driver_id, status, distance_to_pickup_m, closed_at)
		SELECT o.id, $2, 'accepted',
		       (SELECT round(ST_Distance(dl.location, o.pickup))::int FROM driver_locations dl WHERE dl.driver_id = $2),
		       now()
		FROM orders o WHERE o.id = $1
		ON CONFLICT (order_id, driver_id) DO UPDATE SET status = 'accepted', closed_at = now()`,
		orderID, driverID); err != nil {
		return nil, fmt.Errorf("dispatch: accept offer: %w", err)
	}
	rows, err := tx.Query(ctx, `
		UPDATE order_offers f SET status = 'lost', closed_at = now()
		FROM driver_profiles d
		WHERE f.order_id = $1 AND f.driver_id <> $2 AND f.status = 'sent' AND d.id = f.driver_id
		RETURNING f.driver_id, d.user_id`, orderID, driverID)
	if err != nil {
		return nil, fmt.Errorf("dispatch: close offers: %w", err)
	}
	lost, err := pgx.CollectRows(rows, pgx.RowToStructByPos[offeredDriver])
	if err != nil {
		return nil, fmt.Errorf("dispatch: close offers: %w", err)
	}
	return lost, nil
}
