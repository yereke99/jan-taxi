package orders

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
)

// DriverOrderDetail is an order as seen by a driver (openapi DriverOrderDetail).
// For an available order (is_mine=false) passenger contact, recipient phone,
// address details and history are withheld.
type DriverOrderDetail struct {
	ID                uuid.UUID       `json:"id"`
	Number            int64           `json:"number"`
	Status            string          `json:"status"`
	IsMine            bool            `json:"is_mine"`
	Category          string          `json:"category"`
	ServiceType       string          `json:"service_type"`
	CargoType         string          `json:"cargo_type"`
	CargoDescription  *string         `json:"cargo_description"`
	RecipientPhone    *string         `json:"recipient_phone"`
	Comment           *string         `json:"comment"`
	Fare              int64           `json:"fare"`
	Currency          string          `json:"currency"`
	CommissionBps     int             `json:"commission_bps"`
	CommissionAmount  int64           `json:"commission_amount"`
	PaymentMethod     string          `json:"payment_method"`
	DistanceM         int             `json:"distance_m"`
	DurationS         int             `json:"duration_s"`
	DistanceToPickupM *int            `json:"distance_to_pickup_m"`
	StopCount         int             `json:"stop_count"`
	Stops             []stops.Stop    `json:"stops"`
	Route             RouteSnapshot   `json:"route"`
	Passenger         *OrderPassenger `json:"passenger"`
	NextStopID        *uuid.UUID      `json:"next_stop_id"`
	Events            []OrderEvent    `json:"events"`
	Rating            *OrderRating    `json:"rating"`
	CancelReason      *string         `json:"cancel_reason"`
	CancelledBy       *string         `json:"cancelled_by"`
	CanAccept         bool            `json:"can_accept"`
	CanArrive         bool            `json:"can_arrive"`
	CanStart          bool            `json:"can_start"`
	CanComplete       bool            `json:"can_complete"`
	CanCancel         bool            `json:"can_cancel"`
	Version           int             `json:"version"`
	CreatedAt         time.Time       `json:"created_at"`
	SearchExpiresAt   *time.Time      `json:"search_expires_at"`
	AcceptedAt        *time.Time      `json:"accepted_at"`
	ArrivedAt         *time.Time      `json:"arrived_at"`
	StartedAt         *time.Time      `json:"started_at"`
	CompletedAt       *time.Time      `json:"completed_at"`
	CancelledAt       *time.Time      `json:"cancelled_at"`
}

// OrderPassenger is the passenger as shown to the assigned driver while the
// order is active (openapi OrderPassenger).
type OrderPassenger struct {
	FirstName string  `json:"first_name"`
	Phone     *string `json:"phone"`
}

// nextStopID is the stop to drive to: the pickup until the trip starts, then
// the first stop not completed yet.
func nextStopID(status string, list []stops.Stop) *uuid.UUID {
	switch status {
	case lifecycle.StatusSearching, lifecycle.StatusAccepted, lifecycle.StatusDriverArriving:
		if p := stops.Pickup(list); p != nil {
			return &p.ID
		}
	case lifecycle.StatusInProgress:
		for i := range list {
			if list[i].Kind != stops.KindPickup && list[i].CompletedAt == nil {
				return &list[i].ID
			}
		}
	}
	return nil
}

// driverOrder builds the driver view of an order: the driver's own order (any
// status), or a searching order the driver is eligible for right now. Any
// other order is 404.
func driverOrder(ctx context.Context, q db.Querier, id, driverID uuid.UUID) (DriverOrderDetail, error) {
	r, err := loadRecord(ctx, q, id)
	if err != nil {
		return DriverOrderDetail{}, err
	}
	mine := r.DriverID != nil && *r.DriverID == driverID
	var dist *int
	if !mine {
		if r.Status != lifecycle.StatusSearching {
			return DriverOrderDetail{}, httpx.NotFound()
		}
		card, ok, err := dispatch.AvailableCard(ctx, q, driverID, id)
		if err != nil {
			return DriverOrderDetail{}, err
		}
		if !ok {
			return DriverOrderDetail{}, httpx.NotFound()
		}
		dist = card.DistanceToPickupM
	} else if dist, err = distanceToPickup(ctx, q, driverID, id); err != nil {
		return DriverOrderDetail{}, err
	}
	d, err := loadDetails(ctx, q, id)
	if err != nil {
		return DriverOrderDetail{}, err
	}
	v := DriverOrderDetail{
		ID: r.ID, Number: r.Number, Status: r.Status, IsMine: mine, Category: r.Category,
		ServiceType: r.ServiceType, CargoType: r.CargoType, CargoDescription: r.CargoDescription,
		Comment: r.Comment, Fare: r.Fare, Currency: r.Currency, CommissionBps: r.CommissionBps,
		CommissionAmount: r.CommissionAmount, PaymentMethod: r.PaymentMethod, DistanceM: r.DistanceM,
		DurationS: r.DurationS, DistanceToPickupM: dist, StopCount: r.StopCount, Stops: d.Stops,
		Route: d.Route, NextStopID: nextStopID(r.Status, d.Stops), Events: []OrderEvent{},
		CancelReason: r.CancelReason, CancelledBy: r.CancelledBy, Version: r.Version, CreatedAt: r.CreatedAt,
		SearchExpiresAt: r.SearchExpiresAt, AcceptedAt: r.AcceptedAt, ArrivedAt: r.ArrivedAt,
		StartedAt: r.StartedAt, CompletedAt: r.CompletedAt, CancelledAt: r.CancelledAt,
	}
	if !mine {
		withheld := make([]stops.Stop, len(d.Stops))
		for i, s := range d.Stops {
			s.AddressDetails = nil
			withheld[i] = s
		}
		v.Stops = withheld
		v.CanAccept = true
		return v, nil
	}
	v.Events = publicEvents(d.Events)
	v.Rating = d.Rating
	if lifecycle.OccupiesDriver(r.Status) {
		v.RecipientPhone = r.RecipientPhone
		v.Passenger = &OrderPassenger{FirstName: r.Passenger.FirstName, Phone: r.Passenger.Phone}
	}
	v.CanArrive = r.Status == lifecycle.StatusAccepted
	v.CanStart = r.Status == lifecycle.StatusAccepted || r.Status == lifecycle.StatusDriverArriving
	v.CanCancel = v.CanStart
	v.CanComplete = r.Status == lifecycle.StatusInProgress && len(stops.PendingIntermediate(d.Stops)) == 0
	return v, nil
}

// activeDriverOrderID returns the driver's current order id, or uuid.Nil.
func activeDriverOrderID(ctx context.Context, q db.Querier, driverID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT id FROM orders WHERE driver_id = $1 AND status = ANY($2)
		ORDER BY accepted_at DESC NULLS LAST LIMIT 1`, driverID, lifecycle.DriverActiveStatuses).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("orders: active driver order: %w", err)
	}
	return id, nil
}

// DriverOrderSummary is a driver history row (openapi DriverOrderSummary).
type DriverOrderSummary struct {
	ID               uuid.UUID     `json:"id"`
	Number           int64         `json:"number"`
	Status           string        `json:"status"`
	Category         string        `json:"category"`
	Fare             int64         `json:"fare"`
	Currency         string        `json:"currency"`
	CommissionAmount int64         `json:"commission_amount"`
	PaymentMethod    string        `json:"payment_method"`
	Pickup           stops.Summary `json:"pickup"`
	Destination      stops.Summary `json:"destination"`
	StopCount        int           `json:"stop_count"`
	DistanceM        int           `json:"distance_m"`
	CreatedAt        time.Time     `json:"created_at"`
	CompletedAt      *time.Time    `json:"completed_at"`
	CancelledAt      *time.Time    `json:"cancelled_at"`
	CancelledBy      *string       `json:"cancelled_by"`
}

// listDriverHistory returns one page of the driver's finished orders.
func listDriverHistory(ctx context.Context, q db.Querier, driverID uuid.UUID, limit int, c *cursor) (httpx.CursorList[DriverOrderSummary], error) {
	beforeAt, beforeID := cursorArgs(c)
	rows, err := q.Query(ctx, `
		SELECT o.id, o.number, o.status, o.category, o.fare, o.currency, o.commission_amount, o.payment_method,
		       o.stop_count, o.distance_m, o.created_at, o.completed_at, o.cancelled_at, o.cancelled_by, `+endpointColumns+`
		FROM orders o `+endpointJoins+`
		WHERE o.driver_id = $1 AND o.status = ANY($2)
		  AND ($3::timestamptz IS NULL OR (o.created_at, o.id) < ($3::timestamptz, $4::uuid))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $5`, driverID, lifecycle.HistoryStatuses, beforeAt, beforeID, limit+1)
	if err != nil {
		return httpx.CursorList[DriverOrderSummary]{}, fmt.Errorf("orders: driver history: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DriverOrderSummary, error) {
		var s DriverOrderSummary
		dest := []any{&s.ID, &s.Number, &s.Status, &s.Category, &s.Fare, &s.Currency, &s.CommissionAmount,
			&s.PaymentMethod, &s.StopCount, &s.DistanceM, &s.CreatedAt, &s.CompletedAt, &s.CancelledAt, &s.CancelledBy}
		err := row.Scan(append(dest, endpointDest(&s.Pickup, &s.Destination)...)...)
		utc(&s.CreatedAt, s.CompletedAt, s.CancelledAt)
		return s, err
	})
	if err != nil {
		return httpx.CursorList[DriverOrderSummary]{}, fmt.Errorf("orders: driver history: %w", err)
	}
	return page(items, limit, func(s DriverOrderSummary) (time.Time, uuid.UUID) { return s.CreatedAt, s.ID }), nil
}
