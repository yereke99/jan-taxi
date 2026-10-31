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
	ID          uuid.UUID `json:"id"`
	Number      int64     `json:"number"`
	Status      string    `json:"status"`
	IsMine      bool      `json:"is_mine"`
	Category    string    `json:"category"`
	ServiceType string    `json:"service_type"`
	CargoType   string    `json:"cargo_type"`
	OrderLoad
	OrderPlace
	CargoDescription *string `json:"cargo_description"`
	RecipientPhone   *string `json:"recipient_phone"`
	Comment          *string `json:"comment"`
	Fare             int64   `json:"fare"`
	Currency         string  `json:"currency"`
	CommissionBps    int     `json:"commission_bps"`
	CommissionAmount int64   `json:"commission_amount"`
	// CommissionCharged is what the driver's wallet actually paid for this
	// order (charges minus refunds); 0 when the order is not theirs. It stays
	// when the passenger cancels (§22.2).
	CommissionCharged int64           `json:"commission_charged"`
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
	// CanCancel is always false: drivers cannot cancel (§22.3).
	CanCancel       bool       `json:"can_cancel"`
	Version         int        `json:"version"`
	CreatedAt       time.Time  `json:"created_at"`
	SearchExpiresAt *time.Time `json:"search_expires_at"`
	AcceptedAt      *time.Time `json:"accepted_at"`
	ArrivedAt       *time.Time `json:"arrived_at"`
	StartedAt       *time.Time `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	CancelledAt     *time.Time `json:"cancelled_at"`
}

// OrderPassenger is the passenger as shown to the assigned driver while the
// order is active (openapi OrderPassenger): the first name, the phone when
// the passenger shared it through Telegram (§22.5) and how to write to them
// in Telegram (https://t.me/<username>, or tg://user?id=<id> without a
// username).
type OrderPassenger struct {
	FirstName        string  `json:"first_name"`
	Phone            *string `json:"phone"`
	PhoneVerified    bool    `json:"phone_verified"`
	TelegramUsername *string `json:"telegram_username"`
	TelegramURL      string  `json:"telegram_url"`
}

func orderPassenger(p passengerRow) *OrderPassenger {
	op := &OrderPassenger{FirstName: p.FirstName, PhoneVerified: p.PhoneVerified,
		TelegramURL: lifecycle.TelegramLink(p.Username, p.TelegramID)}
	if p.PhoneVerified && p.Phone != nil && *p.Phone != "" {
		op.Phone = p.Phone
	}
	if p.Username != nil && *p.Username != "" {
		op.TelegramUsername = p.Username
	}
	return op
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

// DriverView is the driver view of orderID for driverID (GET
// /driver/orders/{id}); the driver bot gets it after «Алу».
func DriverView(ctx context.Context, q db.Querier, orderID, driverID uuid.UUID) (DriverOrderDetail, error) {
	return driverOrder(ctx, q, orderID, driverID)
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
		switch {
		case ok:
			dist = card.DistanceToPickupM
		default:
			// An offer the driver still holds (opened from the bot's offer
			// message) shows even while they cannot take it right now —
			// offline, location too old, balance too low: the accept button
			// then says what to fix. Anything else stays invisible.
			held, err := dispatch.HoldsOpenOffer(ctx, q, id, driverID)
			if err != nil {
				return DriverOrderDetail{}, err
			}
			if !held {
				return DriverOrderDetail{}, httpx.NotFound()
			}
			if dist, err = distanceToPickup(ctx, q, driverID, id); err != nil {
				return DriverOrderDetail{}, err
			}
		}
	} else if dist, err = distanceToPickup(ctx, q, driverID, id); err != nil {
		return DriverOrderDetail{}, err
	}
	var charged int64
	if mine {
		if charged, err = commissionCharged(ctx, q, driverID, id); err != nil {
			return DriverOrderDetail{}, err
		}
	}
	d, err := loadDetails(ctx, q, id)
	if err != nil {
		return DriverOrderDetail{}, err
	}
	// §22.3: a passenger's cancellation reason is for operators only; the
	// driver learns that the passenger cancelled, never why.
	cancelReason := r.CancelReason
	if r.CancelledBy != nil && *r.CancelledBy == lifecycle.ActorPassenger {
		cancelReason = nil
	}
	v := DriverOrderDetail{
		ID: r.ID, Number: r.Number, Status: r.Status, IsMine: mine, Category: r.Category,
		ServiceType: r.ServiceType, CargoType: r.CargoType, OrderLoad: r.Load, OrderPlace: r.Place,
		CargoDescription: r.CargoDescription,
		Comment:          r.Comment, Fare: r.Fare, Currency: r.Currency, CommissionBps: r.CommissionBps,
		CommissionAmount: r.CommissionAmount, CommissionCharged: charged, PaymentMethod: r.PaymentMethod, DistanceM: r.DistanceM,
		DurationS: r.DurationS, DistanceToPickupM: dist, StopCount: r.StopCount, Stops: d.Stops,
		Route: d.Route, NextStopID: nextStopID(r.Status, d.Stops), Events: []OrderEvent{},
		CancelReason: cancelReason, CancelledBy: r.CancelledBy, Version: r.Version, CreatedAt: r.CreatedAt,
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
		v.Passenger = orderPassenger(r.Passenger)
	}
	v.CanArrive = r.Status == lifecycle.StatusAccepted
	v.CanStart = r.Status == lifecycle.StatusAccepted || r.Status == lifecycle.StatusDriverArriving
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
	ID       uuid.UUID `json:"id"`
	Number   int64     `json:"number"`
	Status   string    `json:"status"`
	Category string    `json:"category"`
	OrderLoad
	OrderPlace
	Fare             int64  `json:"fare"`
	Currency         string `json:"currency"`
	CommissionAmount int64  `json:"commission_amount"`
	// CommissionCharged is what the driver's wallet paid for the order
	// (charges minus refunds), e.g. 50 on an order the passenger cancelled.
	CommissionCharged int64         `json:"commission_charged"`
	PaymentMethod     string        `json:"payment_method"`
	Pickup            stops.Summary `json:"pickup"`
	Destination       stops.Summary `json:"destination"`
	StopCount         int           `json:"stop_count"`
	Unknown           bool          `json:"-"`
	DistanceM         int           `json:"distance_m"`
	CreatedAt         time.Time     `json:"created_at"`
	CompletedAt       *time.Time    `json:"completed_at"`
	CancelledAt       *time.Time    `json:"cancelled_at"`
	CancelledBy       *string       `json:"cancelled_by"`
}

// listDriverHistory returns one page of the driver's finished orders.
func listDriverHistory(ctx context.Context, q db.Querier, driverID uuid.UUID, limit int, c *cursor) (httpx.CursorList[DriverOrderSummary], error) {
	beforeAt, beforeID := cursorArgs(c)
	rows, err := q.Query(ctx, `
		SELECT o.id, o.number, o.status, o.category, o.fare, o.currency, o.commission_amount, `+commissionChargedColumn+`,
		       o.payment_method, o.stop_count, COALESCE(o.distance_m,0), o.distance_m IS NULL, o.created_at, o.completed_at, o.cancelled_at, o.cancelled_by,
		       `+endpointColumns+`,
		       `+loadColumns+`, `+placeColumns+`
		FROM orders o `+endpointJoins+loadJoin+placeJoin+`
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
			&s.CommissionCharged, &s.PaymentMethod, &s.StopCount, &s.DistanceM, &s.Unknown, &s.CreatedAt, &s.CompletedAt, &s.CancelledAt, &s.CancelledBy}
		dest = append(dest, endpointDest(&s.Pickup, &s.Destination)...)
		dest = append(dest, loadDest(&s.OrderLoad)...)
		err := row.Scan(append(dest, placeDest(&s.OrderPlace)...)...)
		utc(&s.CreatedAt, s.CompletedAt, s.CancelledAt)
		return s, err
	})
	if err != nil {
		return httpx.CursorList[DriverOrderSummary]{}, fmt.Errorf("orders: driver history: %w", err)
	}
	return page(items, limit, func(s DriverOrderSummary) (time.Time, uuid.UUID) { return s.CreatedAt, s.ID }), nil
}

// commissionChargedColumn is the commission the wallet of driver $1 paid for
// order o: commission charges minus refunds (a legacy reservation moves no
// balance, so it does not count until it is charged).
const commissionChargedColumn = `COALESCE((
		SELECT -sum(l.balance_delta) FROM wallet_ledger l JOIN wallets w ON w.id = l.wallet_id
		WHERE l.order_id = o.id AND w.driver_id = $1 AND l.entry_type IN ('commission_charge', 'commission_refund')), 0)::bigint`

// commissionCharged is commissionChargedColumn for one order.
func commissionCharged(ctx context.Context, q db.Querier, driverID, orderID uuid.UUID) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT `+commissionChargedColumn+` FROM orders o WHERE o.id = $2`, driverID, orderID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("orders: commission charged: %w", err)
	}
	return n, nil
}
