package orders

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// listAvailable handles GET /driver/orders.
func (s *Service) listAvailable(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	res, err := dispatch.ListAvailable(r.Context(), s.pool, d.ID, dispatch.DefaultLimit)
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}

// getActiveDriverOrder handles GET /driver/orders/active (204 when none).
func (s *Service) getActiveDriverOrder(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	id, err := activeDriverOrderID(r.Context(), s.pool, d.ID)
	if err != nil {
		return err
	}
	if id == uuid.Nil {
		return httpx.NoContent(w)
	}
	return s.writeDriverOrder(w, r, id, d.ID)
}

// listDriverHistory handles GET /driver/orders/history.
func (s *Service) listDriverHistory(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	limit, c, err := listParams(r)
	if err != nil {
		return err
	}
	list, err := listDriverHistory(r.Context(), s.pool, d.ID, limit, c)
	if err != nil {
		return err
	}
	return httpx.OK(w, list)
}

// getDriverOrder handles GET /driver/orders/{id}.
func (s *Service) getDriverOrder(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	return s.writeDriverOrder(w, r, id, d.ID)
}

func (s *Service) writeDriverOrder(w http.ResponseWriter, r *http.Request, orderID, driverID uuid.UUID) error {
	v, err := driverOrder(r.Context(), s.pool, orderID, driverID)
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}

// acceptOrder handles POST /driver/orders/{id}/accept (ARCHITECTURE §5.1).
func (s *Service) acceptOrder(w http.ResponseWriter, r *http.Request) error {
	d, err := approvedDriver(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	res, err := dispatch.AcceptOrder(ctx, s.pool, id, d.ID, dispatch.AcceptOptions{})
	if err != nil {
		return err
	}
	if !res.Replayed {
		// The road estimate, after the commit (§22.6).
		s.eta.Go(id)
	}
	return s.writeDriverOrder(w, r, id, d.ID)
}

// driverAction runs a guarded transition of the caller's own order and
// returns the updated driver view.
func (s *Service) driverAction(w http.ResponseWriter, r *http.Request,
	fn func(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) error) error {
	d, err := approvedDriver(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.tx(ctx, func(tx pgx.Tx) error { return fn(ctx, tx, id, d.ID) }); err != nil {
		return err
	}
	return s.writeDriverOrder(w, r, id, d.ID)
}

// markArriving handles POST /driver/orders/{id}/arriving: the driver is at the
// pickup and waiting (accepted → driver_arriving).
func (s *Service) markArriving(w http.ResponseWriter, r *http.Request) error {
	return s.driverAction(w, r, arrive)
}

func arrive(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) error {
	_, after, err := lifecycle.Apply(ctx, tx, lifecycle.Change{
		OrderID: orderID, OnlyDriver: &driverID, From: []string{lifecycle.StatusAccepted},
		To: lifecycle.StatusDriverArriving, Action: "arrive", Actor: lifecycle.Driver(driverID),
	})
	if err != nil {
		return err
	}
	info, err := lifecycle.LoadDriverInfo(ctx, tx, driverID)
	if err != nil {
		return err
	}
	if err := lifecycle.Notify(ctx, tx, notifications.PassengerBot, after.PassengerID, notifications.TplOrderDriverArriving,
		map[string]any{"order_id": after.ID.String(), "vehicle": info.Vehicle(), "plate": info.PlateLabel()},
		"order_driver_arriving:"+after.ID.String()+":"+strconv.Itoa(after.Version)); err != nil {
		return err
	}
	return lifecycle.Publish(ctx, tx, realtime.OrderDriverArriving, lifecycle.EventData(after, nil),
		lifecycle.PartyScopes(after)...)
}

// startTrip handles POST /driver/orders/{id}/start (accepted | driver_arriving
// → in_progress); the pickup stop is completed.
func (s *Service) startTrip(w http.ResponseWriter, r *http.Request) error {
	return s.driverAction(w, r, start)
}

func start(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) error {
	_, after, err := lifecycle.Apply(ctx, tx, lifecycle.Change{
		OrderID: orderID, OnlyDriver: &driverID,
		From: []string{lifecycle.StatusAccepted, lifecycle.StatusDriverArriving},
		To:   lifecycle.StatusInProgress, Action: "start", Actor: lifecycle.Driver(driverID),
	})
	if err != nil {
		return err
	}
	if err := stops.MarkKindCompleted(ctx, tx, orderID, stops.KindPickup); err != nil {
		return err
	}
	if err := lifecycle.Notify(ctx, tx, notifications.PassengerBot, after.PassengerID, notifications.TplOrderStarted,
		map[string]any{"order_id": after.ID.String(), "order_number": after.Number},
		"order_started:"+after.ID.String()); err != nil {
		return err
	}
	return lifecycle.Publish(ctx, tx, realtime.OrderStarted, lifecycle.EventData(after, nil),
		lifecycle.PartyScopes(after)...)
}

// completeStop handles POST /driver/orders/{id}/stops/{stopId}/complete: only
// intermediate stops, strictly in seq order, while in progress; repeating it
// for a completed stop is a no-op.
func (s *Service) completeStop(w http.ResponseWriter, r *http.Request) error {
	stopID, err := httpx.UUIDParam(r, "stopId")
	if err != nil {
		return err
	}
	return s.driverAction(w, r, func(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) error {
		return completeIntermediate(ctx, tx, orderID, driverID, stopID)
	})
}

func completeIntermediate(ctx context.Context, tx pgx.Tx, orderID, driverID, stopID uuid.UUID) error {
	o, err := lifecycle.LoadCore(ctx, tx, orderID, true)
	if err != nil {
		return err
	}
	if !o.HasDriver(driverID) {
		return httpx.NotFound()
	}
	list, err := stops.ForOrder(ctx, tx, orderID)
	if err != nil {
		return err
	}
	st := stops.Find(list, stopID)
	if st == nil {
		return httpx.NotFound()
	}
	if o.Status != lifecycle.StatusInProgress || st.Kind != stops.KindIntermediate {
		return lifecycle.InvalidTransition(o.Status, "complete_stop")
	}
	if st.CompletedAt != nil {
		return nil
	}
	if next := stops.NextIntermediate(list); next == nil || next.ID != st.ID {
		e := lifecycle.InvalidTransition(o.Status, "complete_stop")
		details := map[string]any{"from": o.Status, "action": "complete_stop"}
		if next != nil {
			details["next_stop_id"] = next.ID.String()
		}
		return e.WithDetails(details)
	}
	if _, err := stops.MarkCompleted(ctx, tx, st.ID); err != nil {
		return err
	}
	if err := lifecycle.Notify(ctx, tx, notifications.PassengerBot, o.PassengerID, notifications.TplOrderStopCompleted,
		map[string]any{"order_id": o.ID.String(), "order_number": o.Number,
			"stop_address": stops.DisplayAddress(st.Address, st.Lat, st.Lon)},
		"order_stop_completed:"+o.ID.String()+":"+st.ID.String()); err != nil {
		return err
	}
	return lifecycle.Publish(ctx, tx, realtime.OrderStopCompleted,
		lifecycle.EventData(o, map[string]any{"stop_id": st.ID.String(), "seq": st.Seq}),
		lifecycle.PartyScopes(o)...)
}

// completeOrder handles POST /driver/orders/{id}/complete: in_progress →
// completed once every intermediate stop is done; settles the commission
// exactly once.
func (s *Service) completeOrder(w http.ResponseWriter, r *http.Request) error {
	return s.driverAction(w, r, complete)
}

func complete(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) error {
	o, err := lifecycle.LoadCore(ctx, tx, orderID, true)
	if err != nil {
		return err
	}
	if !o.HasDriver(driverID) {
		return httpx.NotFound()
	}
	if o.Status != lifecycle.StatusInProgress {
		return lifecycle.InvalidTransition(o.Status, "complete")
	}
	list, err := stops.ForOrder(ctx, tx, orderID)
	if err != nil {
		return err
	}
	if pending := stops.PendingIntermediate(list); len(pending) > 0 {
		ids := make([]string, len(pending))
		for i, p := range pending {
			ids[i] = p.ID.String()
		}
		return lifecycle.InvalidTransition(o.Status, "complete").WithDetails(map[string]any{
			"from": o.Status, "action": "complete", "pending_stop_ids": ids})
	}
	_, after, err := lifecycle.Apply(ctx, tx, lifecycle.Change{
		OrderID: orderID, OnlyDriver: &driverID, From: []string{lifecycle.StatusInProgress},
		To: lifecycle.StatusCompleted, Action: "complete", Actor: lifecycle.Driver(driverID),
	})
	if err != nil {
		return err
	}
	_, err = finishCompleted(ctx, tx, after, true)
	return err
}

// finishCompleted closes the remaining stops, counts the trip, settles what
// is left of the commission (driver profile before wallet, the lock order
// every path uses) and notifies both sides; it returns the commission the
// driver paid for the order. The commission is charged when the driver takes
// the order (§22.2); only a reservation left by an acceptance under the
// earlier model is charged here, once. notify=false is used by dispute
// resolution, which sends its own messages.
func finishCompleted(ctx context.Context, tx pgx.Tx, after lifecycle.Core, notify bool) (int64, error) {
	driverID := *after.DriverID
	for _, kind := range []string{stops.KindIntermediate, stops.KindDestination} {
		if err := stops.MarkKindCompleted(ctx, tx, after.ID, kind); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE driver_profiles SET completed_trips = completed_trips + 1 WHERE id = $1`,
		driverID); err != nil {
		return 0, err
	}
	charged, err := wallets.SettleOnComplete(ctx, tx, after.ID, driverID)
	if err != nil {
		return 0, lifecycle.DBError(err)
	}
	wal, err := wallets.Get(ctx, tx, driverID)
	if err != nil {
		return 0, httpx.Internal(err)
	}
	if notify {
		info, err := lifecycle.LoadDriverInfo(ctx, tx, driverID)
		if err != nil {
			return 0, err
		}
		if err := lifecycle.Notify(ctx, tx, notifications.PassengerBot, after.PassengerID, notifications.TplOrderCompletedPassenger,
			map[string]any{"order_id": after.ID.String(), "order_number": after.Number, "fare": after.Fare},
			"order_completed_passenger:"+after.ID.String()); err != nil {
			return 0, err
		}
		if err := lifecycle.Notify(ctx, tx, notifications.DriverBot, info.UserID, notifications.TplOrderCompletedDriver,
			map[string]any{"order_number": after.Number, "fare": after.Fare, "balance": wal.Balance},
			"order_completed_driver:"+after.ID.String()); err != nil {
			return 0, err
		}
	}
	if err := lifecycle.Publish(ctx, tx, realtime.OrderCompleted, lifecycle.EventData(after, nil),
		lifecycle.PartyScopes(after)...); err != nil {
		return 0, err
	}
	return charged, lifecycle.PublishWallet(ctx, tx, driverID, after.ID, wallets.EntryCommissionCharge)
}
