package orders

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
)

// AdminPassengerRef is the passenger as shown to operators (openapi AdminPassengerRef).
type AdminPassengerRef struct {
	ID             uuid.UUID `json:"id"`
	TelegramUserID int64     `json:"telegram_user_id"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name"`
	Username       *string   `json:"username"`
	Phone          *string   `json:"phone"`
	IsBlocked      bool      `json:"is_blocked"`
}

// DriverRef is a short driver reference (openapi DriverRef).
type DriverRef struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Phone     *string   `json:"phone"`
}

// OrderOffer is one dispatch offer (openapi OrderOffer).
type OrderOffer struct {
	Driver            DriverRef  `json:"driver"`
	Status            string     `json:"status"`
	DistanceToPickupM *int       `json:"distance_to_pickup_m"`
	SentAt            time.Time  `json:"sent_at"`
	ClosedAt          *time.Time `json:"closed_at"`
}

// WalletReservation is a commission reservation (openapi WalletReservation).
type WalletReservation struct {
	ID        uuid.UUID  `json:"id"`
	Amount    int64      `json:"amount"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at"`
}

// LedgerRow is a wallet journal row linked to the order (openapi LedgerEntry
// plus the driver it belongs to).
type LedgerRow struct {
	ID             int64      `json:"id"`
	DriverID       uuid.UUID  `json:"driver_id"`
	EntryType      string     `json:"entry_type"`
	BalanceDelta   int64      `json:"balance_delta"`
	ReservedDelta  int64      `json:"reserved_delta"`
	BalanceAfter   int64      `json:"balance_after"`
	ReservedAfter  int64      `json:"reserved_after"`
	Currency       string     `json:"currency"`
	OrderID        *uuid.UUID `json:"order_id"`
	OrderNumber    *int64     `json:"order_number"`
	TopUpID        *uuid.UUID `json:"topup_id"`
	TopUpReference *string    `json:"topup_reference"`
	Description    *string    `json:"description"`
	CreatedAt      time.Time  `json:"created_at"`
}

// AdminOrder is the complete order for operators (openapi AdminOrder) plus the
// ledger rows of its commission and, after an assignment, warnings.
type AdminOrder struct {
	ID                 uuid.UUID          `json:"id"`
	Number             int64              `json:"number"`
	Status             string             `json:"status"`
	Category           string             `json:"category"`
	ServiceType        string             `json:"service_type"`
	CargoType          string             `json:"cargo_type"`
	CargoDescription   *string            `json:"cargo_description"`
	RecipientPhone     *string            `json:"recipient_phone"`
	ProhibitedItemsAck bool               `json:"prohibited_items_ack"`
	Comment            *string            `json:"comment"`
	Fare               int64              `json:"fare"`
	Currency           string             `json:"currency"`
	CommissionBps      int                `json:"commission_bps"`
	CommissionAmount   int64              `json:"commission_amount"`
	PaymentMethod      string             `json:"payment_method"`
	TariffID           uuid.UUID          `json:"tariff_id"`
	QuoteID            *uuid.UUID         `json:"quote_id"`
	DistanceM          int                `json:"distance_m"`
	DurationS          int                `json:"duration_s"`
	StopCount          int                `json:"stop_count"`
	City               CityRef            `json:"city"`
	Stops              []stops.Stop       `json:"stops"`
	Route              RouteSnapshot      `json:"route"`
	Passenger          AdminPassengerRef  `json:"passenger"`
	Driver             *DriverRef         `json:"driver"`
	Vehicle            *Vehicle           `json:"vehicle"`
	DriverLocation     *DriverLocation    `json:"driver_location"`
	Offers             []OrderOffer       `json:"offers"`
	Events             []AdminOrderEvent  `json:"events"`
	Reservation        *WalletReservation `json:"reservation"`
	Ledger             []LedgerRow        `json:"ledger"`
	Dispute            *Dispute           `json:"dispute"`
	Rating             *OrderRating       `json:"rating"`
	CancelReason       *string            `json:"cancel_reason"`
	CancelledBy        *string            `json:"cancelled_by"`
	Version            int                `json:"version"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
	SearchExpiresAt    *time.Time         `json:"search_expires_at"`
	AcceptedAt         *time.Time         `json:"accepted_at"`
	ArrivedAt          *time.Time         `json:"arrived_at"`
	StartedAt          *time.Time         `json:"started_at"`
	CompletedAt        *time.Time         `json:"completed_at"`
	CancelledAt        *time.Time         `json:"cancelled_at"`
	Warnings           []string           `json:"warnings,omitempty"`
}

func adminPassenger(r record) AdminPassengerRef {
	return AdminPassengerRef{ID: r.PassengerID, TelegramUserID: r.Passenger.TelegramID,
		FirstName: r.Passenger.FirstName, LastName: r.Passenger.LastName, Username: r.Passenger.Username,
		Phone: r.Passenger.Phone, IsBlocked: r.Passenger.IsBlocked}
}

func driverRef(d *driverRow) *DriverRef {
	if d == nil {
		return nil
	}
	return &DriverRef{ID: d.ID, Code: d.Code, FirstName: d.FirstName, LastName: d.LastName, Phone: d.Phone}
}

// adminOrder builds the operator view of an order.
func adminOrder(ctx context.Context, q db.Querier, id uuid.UUID) (AdminOrder, error) {
	r, err := loadRecord(ctx, q, id)
	if err != nil {
		return AdminOrder{}, err
	}
	d, err := loadDetails(ctx, q, id)
	if err != nil {
		return AdminOrder{}, err
	}
	o := AdminOrder{
		ID: r.ID, Number: r.Number, Status: r.Status, Category: r.Category, ServiceType: r.ServiceType,
		CargoType: r.CargoType, CargoDescription: r.CargoDescription, RecipientPhone: r.RecipientPhone,
		ProhibitedItemsAck: r.ProhibitedItemsAck, Comment: r.Comment, Fare: r.Fare, Currency: r.Currency,
		CommissionBps: r.CommissionBps, CommissionAmount: r.CommissionAmount, PaymentMethod: r.PaymentMethod,
		TariffID: r.TariffID, QuoteID: r.QuoteID, DistanceM: r.DistanceM, DurationS: r.DurationS,
		StopCount: r.StopCount, City: r.City, Stops: d.Stops, Route: d.Route, Passenger: adminPassenger(r),
		Driver: driverRef(r.Driver), Vehicle: r.Vehicle, Events: d.Events, Rating: d.Rating,
		CancelReason: r.CancelReason, CancelledBy: r.CancelledBy, Version: r.Version, CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt, SearchExpiresAt: r.SearchExpiresAt, AcceptedAt: r.AcceptedAt,
		ArrivedAt: r.ArrivedAt, StartedAt: r.StartedAt, CompletedAt: r.CompletedAt, CancelledAt: r.CancelledAt,
	}
	if r.Driver != nil && lifecycle.OccupiesDriver(r.Status) {
		if o.DriverLocation, err = loadDriverLocation(ctx, q, r.Driver.ID); err != nil {
			return AdminOrder{}, err
		}
	}
	if o.Offers, err = loadOffers(ctx, q, id); err != nil {
		return AdminOrder{}, err
	}
	if o.Reservation, err = loadReservation(ctx, q, id); err != nil {
		return AdminOrder{}, err
	}
	if o.Ledger, err = loadOrderLedger(ctx, q, id); err != nil {
		return AdminOrder{}, err
	}
	if o.Dispute, err = loadLatestDispute(ctx, q, id); err != nil {
		return AdminOrder{}, err
	}
	return o, nil
}

func loadOffers(ctx context.Context, q db.Querier, orderID uuid.UUID) ([]OrderOffer, error) {
	rows, err := q.Query(ctx, `
		SELECT d.id, d.code, d.first_name, d.last_name, COALESCE(d.phone_e164, u.phone_e164),
		       f.status, f.distance_to_pickup_m, f.sent_at, f.closed_at
		FROM order_offers f
		JOIN driver_profiles d ON d.id = f.driver_id
		JOIN users u ON u.id = d.user_id
		WHERE f.order_id = $1 ORDER BY f.sent_at, f.id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: load offers: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OrderOffer, error) {
		var f OrderOffer
		err := row.Scan(&f.Driver.ID, &f.Driver.Code, &f.Driver.FirstName, &f.Driver.LastName, &f.Driver.Phone,
			&f.Status, &f.DistanceToPickupM, &f.SentAt, &f.ClosedAt)
		utc(&f.SentAt, f.ClosedAt)
		return f, err
	})
	if err != nil {
		return nil, fmt.Errorf("orders: load offers: %w", err)
	}
	if out == nil {
		out = []OrderOffer{}
	}
	return out, nil
}

func loadReservation(ctx context.Context, q db.Querier, orderID uuid.UUID) (*WalletReservation, error) {
	var w WalletReservation
	err := q.QueryRow(ctx, `
		SELECT id, amount, status, created_at, closed_at FROM wallet_reservations
		WHERE order_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, orderID).
		Scan(&w.ID, &w.Amount, &w.Status, &w.CreatedAt, &w.ClosedAt)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: load reservation: %w", err)
	}
	utc(&w.CreatedAt, w.ClosedAt)
	return &w, nil
}

func loadOrderLedger(ctx context.Context, q db.Querier, orderID uuid.UUID) ([]LedgerRow, error) {
	rows, err := q.Query(ctx, `
		SELECT l.id, w.driver_id, l.entry_type, l.balance_delta, l.reserved_delta, l.balance_after, l.reserved_after,
		       w.currency, l.order_id, o.number, l.topup_id, t.reference, l.description, l.created_at
		FROM wallet_ledger l
		JOIN wallets w ON w.id = l.wallet_id
		LEFT JOIN orders o ON o.id = l.order_id
		LEFT JOIN top_up_requests t ON t.id = l.topup_id
		WHERE l.order_id = $1 ORDER BY l.id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: load ledger: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (LedgerRow, error) {
		var l LedgerRow
		err := row.Scan(&l.ID, &l.DriverID, &l.EntryType, &l.BalanceDelta, &l.ReservedDelta, &l.BalanceAfter,
			&l.ReservedAfter, &l.Currency, &l.OrderID, &l.OrderNumber, &l.TopUpID, &l.TopUpReference,
			&l.Description, &l.CreatedAt)
		utc(&l.CreatedAt)
		return l, err
	})
	if err != nil {
		return nil, fmt.Errorf("orders: load ledger: %w", err)
	}
	if out == nil {
		out = []LedgerRow{}
	}
	return out, nil
}

// AdminOrderListItem is an operator list row (openapi AdminOrderListItem).
type AdminOrderListItem struct {
	ID               uuid.UUID         `json:"id"`
	Number           int64             `json:"number"`
	Status           string            `json:"status"`
	Category         string            `json:"category"`
	ServiceType      string            `json:"service_type"`
	City             CityRef           `json:"city"`
	Fare             int64             `json:"fare"`
	Currency         string            `json:"currency"`
	CommissionAmount int64             `json:"commission_amount"`
	PaymentMethod    string            `json:"payment_method"`
	Passenger        AdminPassengerRef `json:"passenger"`
	Driver           *DriverRef        `json:"driver"`
	Pickup           stops.Summary     `json:"pickup"`
	Destination      stops.Summary     `json:"destination"`
	StopCount        int               `json:"stop_count"`
	DistanceM        int               `json:"distance_m"`
	HasOpenDispute   bool              `json:"has_open_dispute"`
	CreatedAt        time.Time         `json:"created_at"`
	AcceptedAt       *time.Time        `json:"accepted_at"`
	CompletedAt      *time.Time        `json:"completed_at"`
	CancelledAt      *time.Time        `json:"cancelled_at"`
	CancelledBy      *string           `json:"cancelled_by"`
}

// adminFilter selects orders for the operator list.
type adminFilter struct {
	Status   string
	CityID   *uuid.UUID
	Category string
	DriverID *uuid.UUID
	From, To *time.Time
	// Search terms derived from q; empty fields do not match.
	Number   *int64
	OrderID  *uuid.UUID
	Phone    string
	Code     string
	HasQuery bool
}

const adminWhere = `
	($1 = '' OR o.status = $1)
	AND ($2::uuid IS NULL OR o.city_id = $2)
	AND ($3 = '' OR o.category = $3)
	AND ($4::uuid IS NULL OR o.driver_id = $4)
	AND ($5::timestamptz IS NULL OR o.created_at >= $5)
	AND ($6::timestamptz IS NULL OR o.created_at < $6)
	AND (NOT $7 OR o.number = $8 OR o.id = $9
	     OR ($10 <> '' AND (pu.phone_e164 = $10 OR o.recipient_phone = $10
	                        OR COALESCE(d.phone_e164, du.phone_e164) = $10))
	     OR ($11 <> '' AND d.code = $11))`

func (f adminFilter) args() []any {
	return []any{f.Status, f.CityID, f.Category, f.DriverID, f.From, f.To, f.HasQuery, f.Number, f.OrderID, f.Phone, f.Code}
}

// listAdminOrders returns one page of orders matching f, newest first.
func listAdminOrders(ctx context.Context, q db.Querier, f adminFilter, limit, offset int) ([]AdminOrderListItem, int, error) {
	from := `
		FROM orders o
		JOIN cities c ON c.id = o.city_id
		JOIN users pu ON pu.id = o.passenger_id
		LEFT JOIN driver_profiles d ON d.id = o.driver_id
		LEFT JOIN users du ON du.id = d.user_id`
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) `+from+` WHERE `+adminWhere, f.args()...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("orders: count admin orders: %w", err)
	}
	args := append(f.args(), limit, offset)
	rows, err := q.Query(ctx, `
		SELECT o.id, o.number, o.status, o.category, o.service_type, o.city_id, c.code, c.name_kk, c.name_ru, c.name_en,
		       o.fare, o.currency, o.commission_amount, o.payment_method,
		       o.passenger_id, pu.telegram_user_id, pu.first_name, pu.last_name, pu.username, pu.phone_e164, pu.is_blocked,
		       d.id, d.code, d.first_name, d.last_name, COALESCE(d.phone_e164, du.phone_e164),
		       o.stop_count, o.distance_m,
		       EXISTS (SELECT 1 FROM disputes ds WHERE ds.order_id = o.id AND ds.status = 'open'),
		       o.created_at, o.accepted_at, o.completed_at, o.cancelled_at, o.cancelled_by, `+endpointColumns+from+endpointJoins+`
		WHERE `+adminWhere+`
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $12 OFFSET $13`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("orders: list admin orders: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanAdminListItem)
	if err != nil {
		return nil, 0, fmt.Errorf("orders: list admin orders: %w", err)
	}
	if items == nil {
		items = []AdminOrderListItem{}
	}
	return items, total, nil
}

func scanAdminListItem(row pgx.CollectableRow) (AdminOrderListItem, error) {
	var it AdminOrderListItem
	var dID *uuid.UUID
	var dCode, dFirst, dLast, dPhone *string
	dest := []any{&it.ID, &it.Number, &it.Status, &it.Category, &it.ServiceType, &it.City.ID, &it.City.Code,
		&it.City.Name.KK, &it.City.Name.RU, &it.City.Name.EN, &it.Fare, &it.Currency, &it.CommissionAmount,
		&it.PaymentMethod, &it.Passenger.ID, &it.Passenger.TelegramUserID, &it.Passenger.FirstName,
		&it.Passenger.LastName, &it.Passenger.Username, &it.Passenger.Phone, &it.Passenger.IsBlocked,
		&dID, &dCode, &dFirst, &dLast, &dPhone, &it.StopCount, &it.DistanceM, &it.HasOpenDispute,
		&it.CreatedAt, &it.AcceptedAt, &it.CompletedAt, &it.CancelledAt, &it.CancelledBy}
	if err := row.Scan(append(dest, endpointDest(&it.Pickup, &it.Destination)...)...); err != nil {
		return it, err
	}
	utc(&it.CreatedAt, it.AcceptedAt, it.CompletedAt, it.CancelledAt)
	if dID != nil {
		it.Driver = &DriverRef{ID: *dID, Code: str(dCode), FirstName: str(dFirst), LastName: str(dLast), Phone: dPhone}
	}
	return it, nil
}
