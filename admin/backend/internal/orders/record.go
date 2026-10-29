package orders

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
)

// record is one order with the rows it references, as read for the API views.
type record struct {
	ID                 uuid.UUID
	Number             int64
	PassengerID        uuid.UUID
	CityID             uuid.UUID
	Category           string
	ServiceType        string
	CargoType          string
	CargoDescription   *string
	RecipientPhone     *string
	ProhibitedItemsAck bool
	Comment            *string
	Status             string
	DriverID           *uuid.UUID
	VehicleID          *uuid.UUID
	Fare               int64
	Currency           string
	CommissionBps      int
	CommissionAmount   int64
	PaymentMethod      string
	TariffID           uuid.UUID
	QuoteID            *uuid.UUID
	DistanceM          int
	DurationS          int
	StopCount          int
	CancelReason       *string
	CancelReasonCode   *string
	CancelledBy        *string
	CommissionMode     string
	DriverETAS         *int
	DriverETAAt        *time.Time
	SearchExpiresAt    *time.Time
	AcceptedAt         *time.Time
	ArrivedAt          *time.Time
	StartedAt          *time.Time
	CompletedAt        *time.Time
	CancelledAt        *time.Time
	Version            int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Load               OrderLoad
	Place              OrderPlace

	City      CityRef
	Passenger passengerRow
	Driver    *driverRow
	Vehicle   *Vehicle
}

type passengerRow struct {
	TelegramID    int64
	FirstName     string
	LastName      string
	Username      *string
	Phone         *string
	PhoneVerified bool
	IsBlocked     bool
}

type driverRow struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Code           string
	FirstName      string
	LastName       string
	Phone          *string
	RatingSum      int
	RatingCount    int
	CompletedTrips int
	AvatarFileID   *uuid.UUID
}

// CityRef is a short city reference (openapi CityRef).
type CityRef struct {
	ID   uuid.UUID     `json:"id"`
	Code string        `json:"code"`
	Name LocalizedText `json:"name"`
}

// LocalizedText carries a name in every UI language.
type LocalizedText struct {
	KK string `json:"kk"`
	RU string `json:"ru"`
	EN string `json:"en"`
}

// Vehicle is the assigned vehicle (openapi Vehicle) with its own class, which
// may differ from the order's class when the class serves several (§21.1).
type Vehicle struct {
	Make             string         `json:"make"`
	Model            string         `json:"model"`
	Color            string         `json:"color"`
	Plate            string         `json:"plate"`
	VehicleClass     *string        `json:"vehicle_class"`
	VehicleClassName *LocalizedText `json:"vehicle_class_name"`
	Seats            *int           `json:"seats"`
	CargoVolumeL     *int           `json:"cargo_volume_l"`
}

const recordColumns = `
	o.id, o.number, o.passenger_id, o.city_id, o.category, o.service_type, o.cargo_type, o.cargo_description,
	o.recipient_phone, o.prohibited_items_ack, o.comment, o.status, o.driver_id, o.vehicle_id, o.fare, o.currency,
	o.commission_bps, o.commission_amount, o.payment_method, o.tariff_id, o.quote_id, COALESCE(o.distance_m,0), COALESCE(o.duration_s,0),
	o.stop_count, o.cancel_reason, o.cancelled_by, o.search_expires_at, o.accepted_at, o.arrived_at, o.started_at,
	o.completed_at, o.cancelled_at, o.version, o.created_at, o.updated_at,
	o.cancel_reason_code, o.commission_mode, o.driver_eta_s, o.driver_eta_at,
	c.code, c.name_kk, c.name_ru, c.name_en,
	pu.telegram_user_id, pu.first_name, pu.last_name, pu.username, pu.phone_e164, pu.phone_verified_at IS NOT NULL,
	pu.is_blocked,
	d.id, d.user_id, d.code, d.first_name, d.last_name, COALESCE(d.phone_e164, du.phone_e164),
	d.rating_sum, d.rating_count, d.completed_trips, d.avatar_file_id,
	v.make, v.model, v.color, v.plate_normalized, v.plate_raw,
	v.class_code, vvc.name_kk, vvc.name_ru, vvc.name_en, v.seats, v.cargo_volume_l,
	` + loadColumns + `, ` + placeColumns

const recordFrom = `
	FROM orders o
	JOIN cities c ON c.id = o.city_id
	JOIN users pu ON pu.id = o.passenger_id
	LEFT JOIN driver_profiles d ON d.id = o.driver_id
	LEFT JOIN users du ON du.id = d.user_id
	LEFT JOIN vehicles v ON v.id = o.vehicle_id
	LEFT JOIN vehicle_classes vvc ON vvc.code = v.class_code` + loadJoin + placeJoin

type scanner interface{ Scan(dest ...any) error }

func scanRecord(row scanner) (record, error) {
	var r record
	var (
		dID, dUser                         *uuid.UUID
		dCode, dFirst, dLast, dPhone       *string
		dRatingSum, dRatingCnt, dTrips     *int
		dAvatar                            *uuid.UUID
		vMake, vModel, vColor, vNorm, vRaw *string
		vClass, vKK, vRU, vEN              *string
		vSeats, vCargo                     *int
	)
	dest := []any{
		&r.ID, &r.Number, &r.PassengerID, &r.CityID, &r.Category, &r.ServiceType, &r.CargoType, &r.CargoDescription,
		&r.RecipientPhone, &r.ProhibitedItemsAck, &r.Comment, &r.Status, &r.DriverID, &r.VehicleID, &r.Fare, &r.Currency,
		&r.CommissionBps, &r.CommissionAmount, &r.PaymentMethod, &r.TariffID, &r.QuoteID, &r.DistanceM, &r.DurationS,
		&r.StopCount, &r.CancelReason, &r.CancelledBy, &r.SearchExpiresAt, &r.AcceptedAt, &r.ArrivedAt, &r.StartedAt,
		&r.CompletedAt, &r.CancelledAt, &r.Version, &r.CreatedAt, &r.UpdatedAt,
		&r.CancelReasonCode, &r.CommissionMode, &r.DriverETAS, &r.DriverETAAt,
		&r.City.Code, &r.City.Name.KK, &r.City.Name.RU, &r.City.Name.EN,
		&r.Passenger.TelegramID, &r.Passenger.FirstName, &r.Passenger.LastName, &r.Passenger.Username,
		&r.Passenger.Phone, &r.Passenger.PhoneVerified, &r.Passenger.IsBlocked,
		&dID, &dUser, &dCode, &dFirst, &dLast, &dPhone, &dRatingSum, &dRatingCnt, &dTrips, &dAvatar,
		&vMake, &vModel, &vColor, &vNorm, &vRaw, &vClass, &vKK, &vRU, &vEN, &vSeats, &vCargo}
	dest = append(dest, loadDest(&r.Load)...)
	err := row.Scan(append(dest, placeDest(&r.Place)...)...)
	if err != nil {
		return record{}, err
	}
	utc(r.SearchExpiresAt, r.AcceptedAt, r.ArrivedAt, r.StartedAt, r.CompletedAt, r.CancelledAt, &r.CreatedAt, &r.UpdatedAt,
		r.DriverETAAt)
	r.City.ID = r.CityID
	if dID != nil {
		r.Driver = &driverRow{ID: *dID, UserID: deref(dUser), Code: str(dCode), FirstName: str(dFirst),
			LastName: str(dLast), Phone: dPhone, RatingSum: num(dRatingSum), RatingCount: num(dRatingCnt),
			CompletedTrips: num(dTrips), AvatarFileID: dAvatar}
	}
	if r.VehicleID != nil {
		r.Vehicle = &Vehicle{Make: str(vMake), Model: str(vModel), Color: str(vColor), Plate: lifecycle.PlateDisplay(vNorm, vRaw),
			VehicleClass: vClass, Seats: vSeats, CargoVolumeL: vCargo}
		if vClass != nil {
			r.Vehicle.VehicleClassName = &LocalizedText{KK: str(vKK), RU: str(vRU), EN: str(vEN)}
		}
	}
	return r, nil
}

func loadRecord(ctx context.Context, q db.Querier, id uuid.UUID) (record, error) {
	r, err := scanRecord(q.QueryRow(ctx, `SELECT `+recordColumns+recordFrom+` WHERE o.id = $1`, id))
	if db.IsNoRows(err) {
		return record{}, httpx.NotFound()
	}
	if err != nil {
		return record{}, fmt.Errorf("orders: load %s: %w", id, err)
	}
	return r, nil
}

// RouteSnapshot is the immutable route stored with the order (openapi RouteSnapshot).
type RouteSnapshot struct {
	Unknown   bool            `json:"-"`
	Geometry  json.RawMessage `json:"geometry"`
	Legs      json.RawMessage `json:"legs"`
	BBox      json.RawMessage `json:"bbox"`
	DistanceM int             `json:"distance_m"`
	DurationS int             `json:"duration_s"`
}

// RouteLeg is one leg of a route (openapi RouteLeg).
type RouteLeg struct {
	DistanceM int `json:"distance_m"`
	DurationS int `json:"duration_s"`
}

func loadRoute(ctx context.Context, q db.Querier, orderID uuid.UUID) (RouteSnapshot, error) {
	var s RouteSnapshot
	var geometry, legs, bbox []byte
	err := q.QueryRow(ctx, `
		SELECT geometry, legs, bbox, COALESCE(distance_m,0), COALESCE(duration_s,0), distance_m IS NULL FROM order_route_snapshots WHERE order_id = $1`, orderID).
		Scan(&geometry, &legs, &bbox, &s.DistanceM, &s.DurationS, &s.Unknown)
	if err != nil && !db.IsNoRows(err) {
		return RouteSnapshot{}, fmt.Errorf("orders: load route: %w", err)
	}
	s.Geometry, s.Legs, s.BBox = rawOr(geometry, `null`), rawOr(legs, `[]`), rawOr(bbox, `null`)
	return s, nil
}

func rawOr(b []byte, def string) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(def)
	}
	return json.RawMessage(b)
}

// OrderEvent is one status change (openapi OrderEvent).
type OrderEvent struct {
	FromStatus *string   `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	ActorType  string    `json:"actor_type"`
	Reason     *string   `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

// AdminOrderEvent adds the journal id, actor id and metadata (openapi AdminOrderEvent).
type AdminOrderEvent struct {
	ID int64 `json:"id"`
	OrderEvent
	ActorID *uuid.UUID     `json:"actor_id"`
	Meta    map[string]any `json:"meta"`
}

func loadEvents(ctx context.Context, q db.Querier, orderID uuid.UUID) ([]AdminOrderEvent, error) {
	rows, err := q.Query(ctx, `
		SELECT id, from_status, to_status, actor_type, actor_id, reason, meta, created_at
		FROM order_status_events WHERE order_id = $1 ORDER BY id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: load events: %w", err)
	}
	defer rows.Close()
	out := []AdminOrderEvent{}
	for rows.Next() {
		var e AdminOrderEvent
		var meta []byte
		if err := rows.Scan(&e.ID, &e.FromStatus, &e.ToStatus, &e.ActorType, &e.ActorID, &e.Reason, &meta, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("orders: scan event: %w", err)
		}
		utc(&e.CreatedAt)
		e.Meta = map[string]any{}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &e.Meta)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func publicEvents(in []AdminOrderEvent) []OrderEvent {
	out := make([]OrderEvent, len(in))
	for i, e := range in {
		out[i] = e.OrderEvent
	}
	return out
}

// OrderRating is the passenger's rating of the trip (openapi OrderRating).
type OrderRating struct {
	Rating    int       `json:"rating"`
	Comment   *string   `json:"comment"`
	CreatedAt time.Time `json:"created_at"`
}

func loadRating(ctx context.Context, q db.Querier, orderID uuid.UUID) (*OrderRating, error) {
	var r OrderRating
	err := q.QueryRow(ctx, `SELECT rating, comment, created_at FROM order_ratings WHERE order_id = $1`, orderID).
		Scan(&r.Rating, &r.Comment, &r.CreatedAt)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: load rating: %w", err)
	}
	utc(&r.CreatedAt)
	return &r, nil
}

// DriverLocation is the assigned driver's last position (openapi DriverLocation).
type DriverLocation struct {
	Lat        float64   `json:"lat"`
	Lon        float64   `json:"lon"`
	AccuracyM  *float64  `json:"accuracy_m"`
	Heading    *float64  `json:"heading"`
	RecordedAt time.Time `json:"recorded_at"`
}

func loadDriverLocation(ctx context.Context, q db.Querier, driverID uuid.UUID) (*DriverLocation, error) {
	var l DriverLocation
	var acc, heading *float32
	err := q.QueryRow(ctx, `SELECT lat, lon, accuracy_m, heading, recorded_at FROM driver_locations WHERE driver_id = $1`, driverID).
		Scan(&l.Lat, &l.Lon, &acc, &heading, &l.RecordedAt)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: load driver location: %w", err)
	}
	l.AccuracyM, l.Heading = f64(acc), f64(heading)
	utc(&l.RecordedAt)
	return &l, nil
}

// distanceToPickup is the straight-line distance from the driver's last known
// location to the order's pickup, or nil without a location.
func distanceToPickup(ctx context.Context, q db.Querier, driverID, orderID uuid.UUID) (*int, error) {
	var d *int
	err := q.QueryRow(ctx, `
		SELECT round(ST_Distance(dl.location, o.pickup))::int
		FROM orders o JOIN driver_locations dl ON dl.driver_id = $1
		WHERE o.id = $2`, driverID, orderID).Scan(&d)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: distance to pickup: %w", err)
	}
	return d, nil
}

// details are the per-order rows every detail view needs.
type details struct {
	Stops  []stops.Stop
	Route  RouteSnapshot
	Events []AdminOrderEvent
	Rating *OrderRating
}

func loadDetails(ctx context.Context, q db.Querier, id uuid.UUID) (details, error) {
	var d details
	var err error
	if d.Stops, err = stops.ForOrder(ctx, q, id); err != nil {
		return details{}, err
	}
	if d.Stops == nil {
		d.Stops = []stops.Stop{}
	}
	if d.Route, err = loadRoute(ctx, q, id); err != nil {
		return details{}, err
	}
	if d.Events, err = loadEvents(ctx, q, id); err != nil {
		return details{}, err
	}
	if d.Rating, err = loadRating(ctx, q, id); err != nil {
		return details{}, err
	}
	return d, nil
}

// utc converts scanned timestamps to UTC in place (nil pointers are skipped),
// so every time in a response is an RFC 3339 UTC string whatever the
// process time zone.
func utc(ts ...*time.Time) {
	for _, t := range ts {
		if t != nil {
			*t = t.UTC()
		}
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func str(p *string) string { return deref(p) }
func num(p *int) int       { return deref(p) }

// f64 widens a real column, rounded to centimetres / hundredths of a degree
// so float32 noise does not leak into JSON.
func f64(p *float32) *float64 {
	if p == nil {
		return nil
	}
	v := math.Round(float64(*p)*100) / 100
	return &v
}
