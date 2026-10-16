package orders

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
)

// Order is an order as seen by its passenger (openapi Order). It never carries
// the commission.
type Order struct {
	ID               uuid.UUID         `json:"id"`
	Number           int64             `json:"number"`
	Status           string            `json:"status"`
	Category         string            `json:"category"`
	ServiceType      string            `json:"service_type"`
	CargoType        string            `json:"cargo_type"`
	CargoDescription *string           `json:"cargo_description"`
	RecipientPhone   *string           `json:"recipient_phone"`
	Comment          *string           `json:"comment"`
	Fare             int64             `json:"fare"`
	Currency         string            `json:"currency"`
	PaymentMethod    string            `json:"payment_method"`
	DistanceM        int               `json:"distance_m"`
	DurationS        int               `json:"duration_s"`
	StopCount        int               `json:"stop_count"`
	City             CityRef           `json:"city"`
	Stops            []stops.Stop      `json:"stops"`
	Route            RouteSnapshot     `json:"route"`
	Driver           *OrderDriver      `json:"driver"`
	Vehicle          *Vehicle          `json:"vehicle"`
	DriverLocation   *DriverLocation   `json:"driver_location"`
	Events           []OrderEvent      `json:"events"`
	Rating           *OrderRating      `json:"rating"`
	Dispute          *OrderDisputeInfo `json:"dispute"`
	CancelReason     *string           `json:"cancel_reason"`
	CancelledBy      *string           `json:"cancelled_by"`
	CanCancel        bool              `json:"can_cancel"`
	CanRate          bool              `json:"can_rate"`
	CanDispute       bool              `json:"can_dispute"`
	Version          int               `json:"version"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	SearchExpiresAt  *time.Time        `json:"search_expires_at"`
	AcceptedAt       *time.Time        `json:"accepted_at"`
	ArrivedAt        *time.Time        `json:"arrived_at"`
	StartedAt        *time.Time        `json:"started_at"`
	CompletedAt      *time.Time        `json:"completed_at"`
	CancelledAt      *time.Time        `json:"cancelled_at"`
}

// OrderDriver is the assigned driver as shown to the passenger (openapi
// OrderDriver): last-name initial only, phone only while the order is active.
type OrderDriver struct {
	ID             uuid.UUID `json:"id"`
	Code           string    `json:"code"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name"`
	Phone          *string   `json:"phone"`
	Rating         *float64  `json:"rating"`
	RatingCount    int       `json:"rating_count"`
	CompletedTrips int       `json:"completed_trips"`
	PhotoURL       *string   `json:"photo_url"`
}

// averageRating is rating_sum/rating_count with one decimal, nil before the
// first rating.
func averageRating(sum, count int) *float64 {
	if count <= 0 {
		return nil
	}
	v := math.Round(float64(sum)/float64(count)*10) / 10
	return &v
}

func initial(lastName string) string {
	for _, r := range lastName {
		if r != ' ' {
			return string(r) + "."
		}
	}
	return ""
}

// canPassengerCancel: searching, accepted or driver_arriving.
func canPassengerCancel(status string) bool {
	return status == lifecycle.StatusSearching || status == lifecycle.StatusAccepted ||
		status == lifecycle.StatusDriverArriving
}

// canPassengerDispute: in progress, or completed within the dispute window,
// and the order was never disputed before.
func canPassengerDispute(r record, latest *Dispute, now time.Time) bool {
	if latest != nil {
		return false
	}
	switch r.Status {
	case lifecycle.StatusInProgress:
		return true
	case lifecycle.StatusCompleted:
		return r.CompletedAt != nil && now.Sub(*r.CompletedAt) <= lifecycle.DisputeWindowHours*time.Hour
	}
	return false
}

// passengerOrder builds the passenger view of order id owned by passengerID;
// any other order is 404 so ids cannot be probed.
func passengerOrder(ctx context.Context, q db.Querier, id, passengerID uuid.UUID) (Order, error) {
	r, err := loadRecord(ctx, q, id)
	if err != nil {
		return Order{}, err
	}
	if r.PassengerID != passengerID {
		return Order{}, httpx.NotFound()
	}
	d, err := loadDetails(ctx, q, id)
	if err != nil {
		return Order{}, err
	}
	latest, err := loadLatestDispute(ctx, q, id)
	if err != nil {
		return Order{}, err
	}
	o := Order{
		ID: r.ID, Number: r.Number, Status: r.Status, Category: r.Category, ServiceType: r.ServiceType,
		CargoType: r.CargoType, CargoDescription: r.CargoDescription, RecipientPhone: r.RecipientPhone,
		Comment: r.Comment, Fare: r.Fare, Currency: r.Currency, PaymentMethod: r.PaymentMethod,
		DistanceM: r.DistanceM, DurationS: r.DurationS, StopCount: r.StopCount, City: r.City,
		Stops: d.Stops, Route: d.Route, Vehicle: r.Vehicle, Events: publicEvents(d.Events), Rating: d.Rating,
		CancelReason: r.CancelReason, CancelledBy: r.CancelledBy,
		CanCancel:  canPassengerCancel(r.Status),
		CanRate:    r.Status == lifecycle.StatusCompleted && d.Rating == nil && r.DriverID != nil,
		CanDispute: canPassengerDispute(r, latest, time.Now()),
		Version:    r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		SearchExpiresAt: r.SearchExpiresAt, AcceptedAt: r.AcceptedAt, ArrivedAt: r.ArrivedAt,
		StartedAt: r.StartedAt, CompletedAt: r.CompletedAt, CancelledAt: r.CancelledAt,
	}
	if latest != nil {
		o.Dispute = latest.info()
	}
	if dr := r.Driver; dr != nil {
		od := &OrderDriver{ID: dr.ID, Code: dr.Code, FirstName: dr.FirstName, LastName: initial(dr.LastName),
			Rating: averageRating(dr.RatingSum, dr.RatingCount), RatingCount: dr.RatingCount,
			CompletedTrips: dr.CompletedTrips}
		if lifecycle.OccupiesDriver(r.Status) {
			od.Phone = dr.Phone
		}
		if dr.AvatarFileID != nil {
			u := "/api/v1/orders/" + r.ID.String() + "/driver-photo"
			od.PhotoURL = &u
		}
		o.Driver = od
		if lifecycle.OccupiesDriver(r.Status) {
			if o.DriverLocation, err = loadDriverLocation(ctx, q, dr.ID); err != nil {
				return Order{}, err
			}
		}
	}
	return o, nil
}

// OrderSummary is a passenger list row (openapi OrderSummary).
type OrderSummary struct {
	ID              uuid.UUID     `json:"id"`
	Number          int64         `json:"number"`
	Status          string        `json:"status"`
	Category        string        `json:"category"`
	ServiceType     string        `json:"service_type"`
	Fare            int64         `json:"fare"`
	Currency        string        `json:"currency"`
	PaymentMethod   string        `json:"payment_method"`
	Pickup          stops.Summary `json:"pickup"`
	Destination     stops.Summary `json:"destination"`
	StopCount       int           `json:"stop_count"`
	DistanceM       int           `json:"distance_m"`
	DriverFirstName *string       `json:"driver_first_name"`
	Rating          *int          `json:"rating"`
	CanRate         bool          `json:"can_rate"`
	CreatedAt       time.Time     `json:"created_at"`
	CompletedAt     *time.Time    `json:"completed_at"`
	CancelledAt     *time.Time    `json:"cancelled_at"`
}

// endpointColumns select the pickup (p) and destination (x) stops of order o.
const endpointColumns = `
	p.seq, p.kind, p.lat, p.lon, p.address,
	x.seq, x.kind, x.lat, x.lon, x.address`

const endpointJoins = `
	JOIN LATERAL (SELECT seq, kind, lat, lon, address FROM order_stops
	              WHERE order_id = o.id ORDER BY seq ASC LIMIT 1) p ON true
	JOIN LATERAL (SELECT seq, kind, lat, lon, address FROM order_stops
	              WHERE order_id = o.id ORDER BY seq DESC LIMIT 1) x ON true`

func endpointDest(p, x *stops.Summary) []any {
	return []any{&p.Seq, &p.Kind, &p.Lat, &p.Lon, &p.Address, &x.Seq, &x.Kind, &x.Lat, &x.Lon, &x.Address}
}

// listPassengerOrders returns one page of the passenger's orders with the
// given statuses, newest first.
func listPassengerOrders(ctx context.Context, q db.Querier, passengerID uuid.UUID, statuses []string, limit int, c *cursor) (httpx.CursorList[OrderSummary], error) {
	beforeAt, beforeID := cursorArgs(c)
	rows, err := q.Query(ctx, `
		SELECT o.id, o.number, o.status, o.category, o.service_type, o.fare, o.currency, o.payment_method,
		       o.stop_count, o.distance_m, d.first_name, r.rating, o.driver_id IS NOT NULL,
		       o.created_at, o.completed_at, o.cancelled_at, `+endpointColumns+`
		FROM orders o
		LEFT JOIN driver_profiles d ON d.id = o.driver_id
		LEFT JOIN order_ratings r ON r.order_id = o.id
		`+endpointJoins+`
		WHERE o.passenger_id = $1 AND o.status = ANY($2)
		  AND ($3::timestamptz IS NULL OR (o.created_at, o.id) < ($3::timestamptz, $4::uuid))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $5`, passengerID, statuses, beforeAt, beforeID, limit+1)
	if err != nil {
		return httpx.CursorList[OrderSummary]{}, fmt.Errorf("orders: list: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OrderSummary, error) {
		var s OrderSummary
		var rating *int16
		var hasDriver bool
		dest := []any{&s.ID, &s.Number, &s.Status, &s.Category, &s.ServiceType, &s.Fare, &s.Currency, &s.PaymentMethod,
			&s.StopCount, &s.DistanceM, &s.DriverFirstName, &rating, &hasDriver, &s.CreatedAt, &s.CompletedAt, &s.CancelledAt}
		if err := row.Scan(append(dest, endpointDest(&s.Pickup, &s.Destination)...)...); err != nil {
			return s, err
		}
		utc(&s.CreatedAt, s.CompletedAt, s.CancelledAt)
		if rating != nil {
			v := int(*rating)
			s.Rating = &v
		}
		s.CanRate = s.Status == lifecycle.StatusCompleted && rating == nil && hasDriver
		return s, nil
	})
	if err != nil {
		return httpx.CursorList[OrderSummary]{}, fmt.Errorf("orders: list: %w", err)
	}
	return page(items, limit, func(s OrderSummary) (time.Time, uuid.UUID) { return s.CreatedAt, s.ID }), nil
}

// page trims a limit+1 result to limit and sets the next cursor.
func page[T any](items []T, limit int, key func(T) (time.Time, uuid.UUID)) httpx.CursorList[T] {
	out := httpx.CursorList[T]{Items: items}
	if len(items) > limit {
		out.Items = items[:limit]
		t, id := key(out.Items[limit-1])
		out.NextCursor = encodeCursor(t, id)
	}
	if out.Items == nil {
		out.Items = []T{}
	}
	return out
}
