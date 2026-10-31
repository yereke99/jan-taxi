package dispatch

import (
	"context"
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

// AssignResult is the outcome of an admin assignment.
type AssignResult struct {
	Before, After lifecycle.Core
	// DriverOffline warns that the driver was offline when assigned.
	DriverOffline bool
	// LocationWarning warns that the driver could not have taken the order
	// themselves: location_required (no location fix), location_stale (fix
	// older than dispatch.location_max_age_s) or out_of_radius (farther from
	// the pickup than the order's effective radius). Empty otherwise.
	LocationWarning string
	// DistanceToPickupM is the driver's distance from the pickup when a
	// location fix exists.
	DistanceToPickupM *int
	// PreviousDriverID is set when the order was taken from another driver.
	PreviousDriverID *uuid.UUID
	// Released is a reservation (earlier commission model) returned to the
	// previous driver.
	Released int64
	// Refunded is the commission refunded to the previous driver on the
	// operator's request.
	Refunded int64
	// Charged is the commission charged to the new driver (§22.2).
	Charged int64
}

// AssignOptions are the operator's choices for an assignment.
type AssignOptions struct {
	// RefundPrevious returns the commission the previous driver paid when the
	// order is taken from them.
	RefundPrevious bool
}

func invalidDriver(reason string) *httpx.Error {
	return httpx.Validation(map[string]string{"driver_id": "not_allowed"}).
		WithDetails(map[string]any{"reason": reason})
}

// Assign gives an order to a driver on an operator's behalf. A searching order
// goes through the same checks as Accept (approval, active order, balance)
// except that the driver need not be online, nor have a fresh location within
// the order's radius: DriverOffline and LocationWarning report those, so the
// operator sees that the driver may be far away. The new driver pays the
// commission as if they had taken the order (§22.2). An accepted or
// driver_arriving order is re-assigned: the previous driver keeps having paid
// unless opts.RefundPrevious (a reservation of the earlier model is
// released), and the order restarts at accepted. Locks are taken in the same
// order as Accept (order, then driver profile, then wallet) so the two never
// deadlock; the unlocked first read keeps what the operator saw.
func Assign(ctx context.Context, tx pgx.Tx, orderID, driverID, adminID uuid.UUID, opts AssignOptions) (AssignResult, error) {
	peek, err := lifecycle.LoadCore(ctx, tx, orderID, false)
	if err != nil {
		return AssignResult{}, err
	}
	before, err := lifecycle.LoadCore(ctx, tx, orderID, true)
	if err != nil {
		return AssignResult{}, err
	}
	drv, err := lockDriver(ctx, tx, driverID)
	if err != nil {
		if httpx.AsError(err).Status == 404 {
			return AssignResult{}, httpx.Validation(map[string]string{"driver_id": "invalid"})
		}
		return AssignResult{}, err
	}
	if drv.Status != "approved" || drv.UserBlocked {
		return AssignResult{}, lifecycle.ErrDriverNotApproved()
	}
	switch {
	case peek.PassengerID == drv.UserID:
		return AssignResult{}, invalidDriver("own_order")
	case peek.HasDriver(driverID) && lifecycle.OccupiesDriver(peek.Status):
		return AssignResult{}, lifecycle.ConflictReason("already_assigned", "order is already assigned to this driver")
	}
	if active, err := activeOrderOf(ctx, tx, driverID); err != nil {
		return AssignResult{}, err
	} else if active != uuid.Nil {
		return AssignResult{}, lifecycle.ErrDriverHasActiveOrder(active)
	}
	// Seats and cargo space are physical limits: unlike location, an operator
	// cannot override them (§21.4).
	if ok, err := fits(ctx, tx, orderID, driverID); err != nil {
		return AssignResult{}, lifecycle.DBError(err)
	} else if !ok {
		return AssignResult{}, invalidDriver(ReasonVehicleMismatch)
	}
	if err := checkFunds(ctx, tx, driverID, peek.CommissionAmount); err != nil {
		return AssignResult{}, err
	}
	// A driver accepted the searching order while this request waited for
	// the lock: the operator chose to fill an open order, not to take it from
	// that driver.
	if peek.Status == lifecycle.StatusSearching && before.DriverID != nil {
		return AssignResult{}, lifecycle.ErrOrderTaken()
	}
	rc, err := loadReach(ctx, tx, orderID, driverID, settings.Dispatch(ctx, tx))
	if err != nil {
		return AssignResult{}, lifecycle.DBError(err)
	}
	res := AssignResult{Before: before, DriverOffline: !drv.IsOnline, LocationWarning: rc.problem(),
		DistanceToPickupM: rc.DistanceM}
	switch before.Status {
	case lifecycle.StatusSearching:
	case lifecycle.StatusAccepted, lifecycle.StatusDriverArriving:
		if before.HasDriver(driverID) {
			return AssignResult{}, lifecycle.ConflictReason("already_assigned", "order is already assigned to this driver")
		}
		res.PreviousDriverID = before.DriverID
		if res.Released, err = wallets.ReleaseForOrder(ctx, tx, orderID, "order re-assigned by operator"); err != nil {
			return AssignResult{}, lifecycle.DBError(err)
		}
		if opts.RefundPrevious {
			if res.Refunded, err = wallets.RefundCommission(ctx, tx, orderID, *before.DriverID, adminID); err != nil {
				return AssignResult{}, lifecycle.DBError(err)
			}
		}
	default:
		return AssignResult{}, lifecycle.InvalidTransition(before.Status, "assign")
	}
	vehicleID, err := currentVehicle(ctx, tx, driverID)
	if err != nil {
		return AssignResult{}, err
	}
	after, err := lifecycle.ScanCore(tx.QueryRow(ctx, `
		UPDATE orders o SET status = 'accepted', driver_id = $3, vehicle_id = $4, accepted_at = now(),
		       arrived_at = NULL, version = version + 1
		WHERE o.id = $1 AND o.status = $2
		RETURNING `+lifecycle.CoreColumns, orderID, before.Status, driverID, vehicleID))
	switch {
	case db.IsUniqueViolation(err, lifecycle.IdxOneActivePerDriver):
		return AssignResult{}, lifecycle.ErrDriverHasActiveOrder(uuid.Nil)
	case err != nil:
		return AssignResult{}, lifecycle.DBError(fmt.Errorf("dispatch: assign: %w", err))
	}
	res.After = after

	if res.PreviousDriverID != nil {
		if err := releasedFromDriver(ctx, tx, after, *res.PreviousDriverID, res.Released, res.Refunded); err != nil {
			return AssignResult{}, err
		}
	}
	if res.Charged, err = assigned(ctx, tx, before, after, lifecycle.Admin(adminID), res.PreviousDriverID, AcceptOptions{}); err != nil {
		return AssignResult{}, err
	}
	return res, nil
}

// releasedFromDriver closes the previous driver's part of a re-assigned order:
// their offer is cancelled and they are told the order is no longer theirs.
func releasedFromDriver(ctx context.Context, tx pgx.Tx, after lifecycle.Core, prevID uuid.UUID, released, refunded int64) error {
	if _, err := tx.Exec(ctx, `
		UPDATE order_offers SET status = 'cancelled', closed_at = now()
		WHERE order_id = $1 AND driver_id = $2`, after.ID, prevID); err != nil {
		return fmt.Errorf("dispatch: cancel previous offer: %w", err)
	}
	prev, err := lifecycle.LoadDriverInfo(ctx, tx, prevID)
	if err != nil {
		return err
	}
	params := map[string]any{"order_number": after.Number}
	if refunded > 0 {
		params["refunded"] = refunded
	}
	if err := lifecycle.Notify(ctx, tx, notifications.DriverBot, prev.UserID, notifications.TplOrderCancelledDriver,
		params, "order_cancelled_driver:"+after.ID.String()+":"+prevID.String()+":"+strconv.Itoa(after.Version)); err != nil {
		return err
	}
	if err := lifecycle.CloseOfferMessage(ctx, tx, after.ID, prevID, lifecycle.OfferMessageCancelled); err != nil {
		return err
	}
	walletChanged := released > 0 || refunded > 0
	data := map[string]any{"order_id": after.ID.String(), "order_number": after.Number,
		"status": after.Status, "reassigned": true}
	if err := lifecycle.Publish(ctx, tx, realtime.OrderCancelled, data, realtime.DriverScope(prevID)); err != nil {
		return err
	}
	switch {
	case refunded > 0:
		return lifecycle.PublishWallet(ctx, tx, prevID, after.ID, wallets.EntryCommissionRefund)
	case walletChanged:
		return lifecycle.PublishWallet(ctx, tx, prevID, after.ID, wallets.EntryCommissionRelease)
	}
	return nil
}
