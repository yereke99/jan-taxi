package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// lockedDriver is a driver profile read FOR SHARE, so moderation cannot change
// its status while the acceptance transaction runs.
type lockedDriver struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	CityID      uuid.UUID
	Status      string
	IsOnline    bool
	UserBlocked bool
}

func lockDriver(ctx context.Context, tx pgx.Tx, driverID uuid.UUID) (lockedDriver, error) {
	var d lockedDriver
	err := tx.QueryRow(ctx, `
		SELECT d.id, d.user_id, d.city_id, d.status, d.is_online, u.is_blocked
		FROM driver_profiles d JOIN users u ON u.id = d.user_id
		WHERE d.id = $1
		FOR SHARE OF d`, driverID).
		Scan(&d.ID, &d.UserID, &d.CityID, &d.Status, &d.IsOnline, &d.UserBlocked)
	if db.IsNoRows(err) {
		return lockedDriver{}, httpx.NotFound()
	}
	if err != nil {
		return lockedDriver{}, fmt.Errorf("dispatch: lock driver: %w", err)
	}
	return d, nil
}

func activeOrderOf(ctx context.Context, q db.Querier, driverID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT id FROM orders
		WHERE driver_id = $1 AND status IN ('accepted', 'driver_arriving', 'in_progress') LIMIT 1`, driverID).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("dispatch: active order: %w", err)
	}
	return id, nil
}

// currentVehicle returns the driver's current vehicle (claimed first), or nil.
func currentVehicle(ctx context.Context, q db.Querier, driverID uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT id FROM vehicles WHERE driver_id = $1 AND status <> 'archived'
		ORDER BY (status = 'claimed') DESC, created_at DESC LIMIT 1`, driverID).Scan(&id)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dispatch: vehicle: %w", err)
	}
	return &id, nil
}

// checkFunds locks the driver's wallet (FOR UPDATE, created when missing) and
// requires available >= commission.
func checkFunds(ctx context.Context, tx pgx.Tx, driverID uuid.UUID, commission int64) error {
	w, err := wallets.Lock(ctx, tx, driverID)
	if err != nil {
		return lifecycle.DBError(err)
	}
	if w.Available() < commission {
		return lifecycle.InsufficientBalance(commission, w.Available(), settings.TopUp(ctx, tx).Min)
	}
	return nil
}

// AcceptResult is the outcome of Accept.
type AcceptResult struct {
	Order lifecycle.Core
	// Replayed is true when the driver already holds this order (a repeated
	// request); nothing changed.
	Replayed bool
}

// Accept assigns a searching order to the driver, exactly-one-winner
// (ARCHITECTURE §5.1), in the caller's transaction:
//
//  1. the driver is approved, online, not blocked and in the order's city;
//  2. the wallet row is locked FOR UPDATE and must cover the commission
//     (402 insufficient_balance);
//  3. UPDATE … WHERE status='searching' AND driver_id IS NULL — 0 rows means
//     another driver won (409 order_already_taken);
//  4. the orders_one_active_per_driver index rejects a second active order
//     (409 driver_has_active_order);
//  5. the commission is reserved, the winner's offer accepted and the others
//     lost, the transition journaled, notifications and events queued.
func Accept(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) (AcceptResult, error) {
	drv, err := lockDriver(ctx, tx, driverID)
	if err != nil {
		return AcceptResult{}, err
	}
	switch {
	case drv.Status != "approved" || drv.UserBlocked:
		return AcceptResult{}, lifecycle.ErrDriverNotApproved()
	case !drv.IsOnline:
		return AcceptResult{}, lifecycle.ErrDriverOffline()
	}

	o, err := lifecycle.LoadCore(ctx, tx, orderID, false)
	if err != nil {
		return AcceptResult{}, err
	}
	// Orders of another city, and the driver's own passenger orders, are
	// invisible to this driver.
	if o.CityID != drv.CityID || o.PassengerID == drv.UserID {
		return AcceptResult{}, httpx.NotFound()
	}
	if o.HasDriver(driverID) && lifecycle.OccupiesDriver(o.Status) {
		return AcceptResult{Order: o, Replayed: true}, nil
	}
	if o.Status != lifecycle.StatusSearching {
		return AcceptResult{}, notAcceptable(o)
	}
	if active, err := activeOrderOf(ctx, tx, driverID); err != nil {
		return AcceptResult{}, err
	} else if active != uuid.Nil {
		return AcceptResult{}, lifecycle.ErrDriverHasActiveOrder(active)
	}
	if err := checkFunds(ctx, tx, driverID, o.CommissionAmount); err != nil {
		return AcceptResult{}, err
	}
	vehicleID, err := currentVehicle(ctx, tx, driverID)
	if err != nil {
		return AcceptResult{}, err
	}

	after, err := lifecycle.ScanCore(tx.QueryRow(ctx, `
		UPDATE orders o SET status = 'accepted', driver_id = $2, vehicle_id = $3, accepted_at = now(),
		       version = version + 1
		WHERE o.id = $1 AND o.status = 'searching' AND o.driver_id IS NULL
		RETURNING `+lifecycle.CoreColumns, orderID, driverID, vehicleID))
	switch {
	case db.IsNoRows(err):
		cur, lerr := lifecycle.LoadCore(ctx, tx, orderID, false)
		if lerr != nil {
			return AcceptResult{}, lerr
		}
		return AcceptResult{}, notAcceptable(cur)
	case db.IsUniqueViolation(err, lifecycle.IdxOneActivePerDriver):
		return AcceptResult{}, lifecycle.ErrDriverHasActiveOrder(uuid.Nil)
	case err != nil:
		return AcceptResult{}, lifecycle.DBError(fmt.Errorf("dispatch: accept: %w", err))
	}

	if err := assigned(ctx, tx, o, after, lifecycle.Driver(driverID), nil); err != nil {
		return AcceptResult{}, err
	}
	return AcceptResult{Order: after}, nil
}

// notAcceptable explains why an order that is no longer searching cannot be
// accepted: taken by a driver, or closed without one.
func notAcceptable(o lifecycle.Core) error {
	if o.DriverID != nil {
		return lifecycle.ErrOrderTaken()
	}
	return lifecycle.InvalidTransition(o.Status, "accept")
}

// assigned does everything after the order row points at its new driver:
// reserve the commission, settle offers, journal, notify and publish. prev is
// the driver who held the order before a re-assignment (nil otherwise).
func assigned(ctx context.Context, tx pgx.Tx, before, after lifecycle.Core, actor lifecycle.Actor, prev *uuid.UUID) error {
	driverID := *after.DriverID
	if _, err := wallets.Reserve(ctx, tx, driverID, after.ID, after.CommissionAmount); err != nil {
		if errors.Is(err, wallets.ErrInsufficientBalance) {
			w, _ := wallets.Get(ctx, tx, driverID)
			return lifecycle.InsufficientBalance(after.CommissionAmount, w.Available(), settings.TopUp(ctx, tx).Min)
		}
		return lifecycle.DBError(err)
	}
	lost, err := closeOffersOnAccept(ctx, tx, after.ID, driverID)
	if err != nil {
		return err
	}
	meta := map[string]any{"driver_id": driverID.String()}
	if prev != nil {
		meta["previous_driver_id"] = prev.String()
	}
	if err := lifecycle.InsertEvent(ctx, tx, lifecycle.Event{OrderID: after.ID, From: &before.Status,
		To: lifecycle.StatusAccepted, Actor: actor, Meta: meta}); err != nil {
		return err
	}

	info, err := lifecycle.LoadDriverInfo(ctx, tx, driverID)
	if err != nil {
		return err
	}
	route, err := lifecycle.LoadRoute(ctx, tx, after.ID)
	if err != nil {
		return err
	}
	suffix := after.ID.String() + ":" + driverID.String() + ":" + strconv.Itoa(after.Version)
	if err := lifecycle.Notify(ctx, tx, notifications.PassengerBot, after.PassengerID, notifications.TplOrderAcceptedPassenger,
		map[string]any{
			"order_id": after.ID.String(), "order_number": after.Number,
			"driver_name": info.Name(), "vehicle": info.Vehicle(), "plate": info.PlateLabel(),
		}, "order_accepted_passenger:"+suffix); err != nil {
		return err
	}
	if err := lifecycle.Notify(ctx, tx, notifications.DriverBot, info.UserID, notifications.TplOrderAcceptedDriver,
		map[string]any{
			"order_number": after.Number, "pickup": route.Pickup,
			"fare": after.Fare, "commission": after.CommissionAmount,
		}, "order_accepted_driver:"+suffix); err != nil {
		return err
	}

	if err := lifecycle.Publish(ctx, tx, realtime.OrderAccepted, lifecycle.EventData(after, nil),
		lifecycle.PartyScopes(after)...); err != nil {
		return err
	}
	if err := lifecycle.PublishWallet(ctx, tx, driverID, after.ID, "commission_reserve"); err != nil {
		return err
	}
	if len(lost) > 0 {
		ids := make([]uuid.UUID, len(lost))
		for i, l := range lost {
			ids[i] = l.DriverID
			if err := lifecycle.Notify(ctx, tx, notifications.DriverBot, l.UserID, notifications.TplOrderTakenDriver,
				map[string]any{"order_number": after.Number},
				"order_taken_driver:"+after.ID.String()+":"+l.DriverID.String()); err != nil {
				return err
			}
		}
		data := map[string]any{"order_id": after.ID.String(), "order_number": after.Number, "status": after.Status}
		if err := lifecycle.Publish(ctx, tx, realtime.OrderTaken, data, lifecycle.DriverScopes(ids)...); err != nil {
			return err
		}
	}
	return nil
}
