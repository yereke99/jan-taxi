package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/phone"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/pricing"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
)

// RouteProvider is recorded on every route snapshot.
const RouteProvider = "osrm"

// createRequest is the POST /orders body (openapi OrderCreateRequest).
type createRequest struct {
	QuoteID            *string `json:"quote_id"`
	Fare               *int64  `json:"fare"`
	PaymentMethod      *string `json:"payment_method"`
	Comment            *string `json:"comment"`
	CargoDescription   *string `json:"cargo_description"`
	RecipientPhone     *string `json:"recipient_phone"`
	ProhibitedItemsAck *bool   `json:"prohibited_items_ack"`
	IdempotencyKey     *string `json:"idempotency_key"`
}

// newOrder is a validated create request.
type newOrder struct {
	QuoteID          uuid.UUID
	IdempotencyKey   string
	Fare             int
	PaymentMethod    string
	Comment          *string
	CargoDescription *string
	RecipientPhone   *string
	Ack              bool
}

// parseCreate validates everything that does not depend on the quote. fields
// collects per-field reasons; the request can only proceed when quote_id and
// idempotency_key parsed.
func parseCreate(req createRequest, fields map[string]string) (newOrder, bool) {
	var n newOrder
	ok := true
	switch {
	case req.QuoteID == nil || strings.TrimSpace(*req.QuoteID) == "":
		fields["quote_id"], ok = "required", false
	default:
		id, err := uuid.Parse(strings.TrimSpace(*req.QuoteID))
		if err != nil {
			fields["quote_id"], ok = "invalid", false
		}
		n.QuoteID = id
	}
	switch {
	case req.IdempotencyKey == nil || strings.TrimSpace(*req.IdempotencyKey) == "":
		fields["idempotency_key"], ok = "required", false
	default:
		id, err := uuid.Parse(strings.TrimSpace(*req.IdempotencyKey))
		if err != nil {
			fields["idempotency_key"], ok = "invalid", false
		}
		n.IdempotencyKey = id.String()
	}
	switch {
	case req.Fare == nil:
		fields["fare"] = "required"
	case *req.Fare <= 0 || *req.Fare > math.MaxInt32:
		fields["fare"] = "invalid"
	default:
		n.Fare = int(*req.Fare)
	}
	switch {
	case req.PaymentMethod == nil || *req.PaymentMethod == "":
		fields["payment_method"] = "required"
	case !lifecycle.ValidPaymentMethod(*req.PaymentMethod):
		fields["payment_method"] = "invalid"
	default:
		n.PaymentMethod = *req.PaymentMethod
	}
	n.Comment = cleanField(fields, "comment", req.Comment, textutil.MaxFreeText)
	n.CargoDescription = cleanField(fields, "cargo_description", req.CargoDescription, textutil.MaxFreeText)
	if req.ProhibitedItemsAck != nil {
		n.Ack = *req.ProhibitedItemsAck
	}
	return n, ok
}

// checkCategory applies the per-category rules of §5 and normalises the
// recipient phone.
func checkCategory(category string, req createRequest, n *newOrder, fields map[string]string) {
	hasPhone := req.RecipientPhone != nil && strings.TrimSpace(*req.RecipientPhone) != ""
	switch category {
	case lifecycle.CategoryTaxi:
		if n.CargoDescription != nil {
			fields["cargo_description"] = "not_allowed"
		}
		if hasPhone {
			fields["recipient_phone"] = "not_allowed"
		}
		if n.Ack {
			fields["prohibited_items_ack"] = "not_allowed"
		}
		return
	case lifecycle.CategoryOther:
		if hasPhone {
			fields["recipient_phone"] = "not_allowed"
		}
	case lifecycle.CategoryParcel:
		if !hasPhone {
			fields["recipient_phone"] = "required"
		} else if p, err := phone.Normalize(*req.RecipientPhone); err != nil {
			fields["recipient_phone"] = "invalid"
		} else {
			n.RecipientPhone = &p
		}
	}
	if n.CargoDescription == nil && fields["cargo_description"] == "" {
		fields["cargo_description"] = "required"
	}
	if !n.Ack {
		fields["prohibited_items_ack"] = "must_be_true"
	}
}

// checkBlocked rejects delivery texts that mention prohibited items.
func checkBlocked(ctx context.Context, q pgx.Tx, category string, n newOrder, fields map[string]string) {
	if category == lifecycle.CategoryTaxi {
		return
	}
	keywords := settings.BlockedKeywords(ctx, q)
	if n.CargoDescription != nil && containsBlocked(*n.CargoDescription, keywords) {
		fields["cargo_description"] = "prohibited_content"
	}
	if n.Comment != nil && containsBlocked(*n.Comment, keywords) {
		fields["comment"] = "prohibited_content"
	}
}

type idemRow struct {
	ID            uuid.UUID
	QuoteID       *uuid.UUID
	Fare          int
	PaymentMethod string
}

func findByIdempotencyKey(ctx context.Context, q db.Querier, passengerID uuid.UUID, key string) (*idemRow, error) {
	var r idemRow
	err := q.QueryRow(ctx, `
		SELECT id, quote_id, fare, payment_method FROM orders
		WHERE passenger_id = $1 AND idempotency_key = $2`, passengerID, key).
		Scan(&r.ID, &r.QuoteID, &r.Fare, &r.PaymentMethod)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: idempotency lookup: %w", err)
	}
	return &r, nil
}

// replayOf checks that a request reusing an idempotency key repeats the
// original order; a different payload is 409 conflict.
func replayOf(existing *idemRow, n newOrder) (uuid.UUID, error) {
	if existing.QuoteID == nil || *existing.QuoteID != n.QuoteID || existing.Fare != n.Fare ||
		existing.PaymentMethod != n.PaymentMethod {
		return uuid.Nil, lifecycle.ConflictReason("idempotency_key_reused",
			"idempotency key already used for a different order")
	}
	return existing.ID, nil
}

func activePassengerOrder(ctx context.Context, q db.Querier, passengerID uuid.UUID, serviceType string) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT id FROM orders WHERE passenger_id = $1 AND service_type = $2 AND status = ANY($3) LIMIT 1`,
		passengerID, serviceType, lifecycle.ActiveStatuses).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("orders: active order lookup: %w", err)
	}
	return id, nil
}

// checkCancelsAfterAccept refuses a new order from a passenger who cancelled
// too many orders after a driver had accepted them within the last 24 hours
// (setting orders.passenger_cancels_after_accept_per_day, 0 = no limit): each
// such cancellation costs the driver the commission (§22.2), so creating and
// cancelling orders over and over would drain nearby drivers' wallets. The
// answer is 409 conflict {reason: too_many_cancellations, limit}.
func checkCancelsAfterAccept(ctx context.Context, q db.Querier, passengerID uuid.UUID) error {
	limit := settings.PassengerCancelsAfterAcceptPerDay(ctx, q)
	if limit <= 0 {
		return nil
	}
	var n int64
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM orders
		WHERE passenger_id = $1 AND status = 'cancelled' AND cancelled_by = 'passenger'
		  AND accepted_at IS NOT NULL AND cancelled_at > now() - interval '24 hours'`,
		passengerID).Scan(&n); err != nil {
		return fmt.Errorf("orders: cancellations after acceptance: %w", err)
	}
	if n >= limit {
		return lifecycle.ConflictReason("too_many_cancellations", "too many orders cancelled after a driver accepted them").
			WithDetails(map[string]any{"reason": "too_many_cancellations", "limit": limit})
	}
	return nil
}

// quoteError tells a consumed quote apart from an expired one: both are 422
// quote_expired, a consumed one with details {reason: quote_used}. The quote
// already belongs to the caller when pricing reports it expired.
func quoteError(ctx context.Context, q db.Querier, quoteID uuid.UUID, err error) error {
	var he *httpx.Error
	if !errors.As(err, &he) || he.Code != pricing.ErrQuoteExpired.Code {
		return err
	}
	var used bool
	if qerr := q.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM fare_quotes WHERE id = $1`, quoteID).
		Scan(&used); qerr != nil {
		return fmt.Errorf("orders: quote status: %w", qerr)
	}
	if used {
		return he.WithDetails(map[string]any{"reason": "quote_used"})
	}
	return err
}

var (
	errActiveRace = errors.New("orders: concurrent active order")
	errReplayRace = errors.New("orders: concurrent idempotent create")
)

// createOrder handles POST /orders.
func (s *Service) createOrder(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	var req createRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	n, ok := parseCreate(req, fields)
	if !ok {
		return httpx.Validation(fields)
	}

	ctx := r.Context()
	var orderID uuid.UUID
	replayed := false
	var serviceType string
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if existing, err := findByIdempotencyKey(ctx, tx, p.UserID, n.IdempotencyKey); err != nil {
			return err
		} else if existing != nil {
			orderID, err = replayOf(existing, n)
			replayed = true
			return err
		}
		quote, err := pricing.LoadQuoteForUpdate(ctx, tx, n.QuoteID, p.UserID)
		if err != nil {
			// A concurrent request with the same key may have used the quote
			// while this one waited for its lock.
			if existing, ferr := findByIdempotencyKey(ctx, tx, p.UserID, n.IdempotencyKey); ferr == nil && existing != nil {
				orderID, err = replayOf(existing, n)
				replayed = true
				return err
			}
			return quoteError(ctx, tx, n.QuoteID, err)
		}
		checkCategory(quote.Category, req, &n, fields)
		if len(fields) == 0 {
			checkBlocked(ctx, tx, quote.Category, n, fields)
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		if err := quote.ValidateFare(n.Fare); err != nil {
			return err
		}
		serviceType = lifecycle.ServiceType(quote.Category)
		if active, err := activePassengerOrder(ctx, tx, p.UserID, serviceType); err != nil {
			return err
		} else if active != uuid.Nil {
			return lifecycle.ActiveOrderExists(active)
		}
		if err := checkCancelsAfterAccept(ctx, tx, p.UserID); err != nil {
			return err
		}
		orderID, err = insertOrder(ctx, tx, p.UserID, quote, n)
		return err
	})
	switch {
	case errors.Is(err, errActiveRace):
		active, aerr := activePassengerOrder(ctx, s.pool, p.UserID, serviceType)
		if aerr != nil {
			return aerr
		}
		if active == uuid.Nil { // the competing order already finished; the client may retry
			return lifecycle.ConflictReason("active_order_exists", "passenger already has an active order of this service type")
		}
		return lifecycle.ActiveOrderExists(active)
	case errors.Is(err, errReplayRace):
		existing, ferr := findByIdempotencyKey(ctx, s.pool, p.UserID, n.IdempotencyKey)
		if ferr != nil || existing == nil {
			return httpx.Internal(errors.Join(err, ferr))
		}
		if orderID, err = replayOf(existing, n); err != nil {
			return err
		}
		replayed = true
	case err != nil:
		return lifecycle.DBError(err)
	}

	o, err := passengerOrder(ctx, s.pool, orderID, p.UserID)
	if err != nil {
		return err
	}
	if replayed {
		return httpx.OK(w, o)
	}
	return httpx.Created(w, o)
}

// insertOrder writes the order, its stops, route snapshot and first status
// event, consumes the quote, offers the order to eligible drivers and queues
// the passenger notification and realtime events.
func insertOrder(ctx context.Context, tx pgx.Tx, passengerID uuid.UUID, q pricing.StoredQuote, n newOrder) (uuid.UUID, error) {
	if len(q.Stops) < stops.MinPoints || len(q.Stops) > stops.MaxPoints {
		return uuid.Nil, httpx.Validation(map[string]string{"quote_id": "invalid"})
	}
	// The commission is snapshotted now and charged when a driver takes the
	// order (§22.2).
	policy := settings.Commission(ctx, tx)
	commission := pricing.OrderCommission(policy.Mode, policy.FixedAmount, n.Fare, q.CommissionBps)
	pickup := q.Stops[0]
	var id uuid.UUID
	var number int64
	err := tx.QueryRow(ctx, `
		INSERT INTO orders (passenger_id, city_id, category, service_type, cargo_type, cargo_description,
		                    recipient_phone, prohibited_items_ack, comment, status, fare, commission_bps,
		                    commission_amount, payment_method, tariff_id, quote_id, distance_m, duration_s,
		                    stop_count, pickup_lat, pickup_lon, idempotency_key, search_expires_at,
		                    vehicle_class, passenger_count, cargo_size, cargo_volume_l, commission_mode, locality)
		SELECT $1, c.id, $3, $4, $5, $6, $7, $8, $9, 'searching', $10, $11, $12, $13, $14, $15, $16, $17,
		       $18, $19, $20, $21, now() + make_interval(secs => c.search_timeout_s), $22, $23, $24, $25, $26, $27
		FROM cities c WHERE c.id = $2
		RETURNING id, number`,
		passengerID, q.CityID, q.Category, lifecycle.ServiceType(q.Category), lifecycle.CargoType(q.Category),
		n.CargoDescription, n.RecipientPhone, n.Ack && q.Category != lifecycle.CategoryTaxi, n.Comment, n.Fare,
		q.CommissionBps, commission, n.PaymentMethod, q.TariffID, q.ID, q.DistanceM, q.DurationS,
		len(q.Stops), pickup.Lat, pickup.Lon, n.IdempotencyKey,
		q.VehicleClass, q.PassengerCount, q.CargoSize, q.CargoVolumeL, policy.Mode, q.Locality).Scan(&id, &number)
	switch {
	case db.IsUniqueViolation(err, lifecycle.IdxOneActivePerPassenger):
		return uuid.Nil, errActiveRace
	case db.IsUniqueViolation(err, lifecycle.IdxPassengerIdempotency):
		return uuid.Nil, errReplayRace
	case db.IsNoRows(err):
		return uuid.Nil, httpx.Validation(map[string]string{"quote_id": "invalid"})
	case err != nil:
		return uuid.Nil, fmt.Errorf("orders: insert: %w", err)
	}

	in := make([]stops.Input, len(q.Stops))
	for i, st := range q.Stops {
		in[i] = stops.Input{Lat: st.Lat, Lon: st.Lon, Address: st.Address, AddressDetails: lifecycle.StrPtr(st.AddressDetails)}
	}
	stored, err := stops.Insert(ctx, tx, id, in)
	if err != nil {
		return uuid.Nil, err
	}
	if err := insertRouteSnapshot(ctx, tx, id, q, stored); err != nil {
		return uuid.Nil, err
	}
	if err := lifecycle.InsertEvent(ctx, tx, lifecycle.Event{OrderID: id, To: lifecycle.StatusSearching,
		Actor: lifecycle.Passenger(passengerID), Meta: map[string]any{"quote_id": q.ID.String()}}); err != nil {
		return uuid.Nil, err
	}
	if err := pricing.MarkQuoteUsed(ctx, tx, q.ID); err != nil {
		return uuid.Nil, err
	}
	if _, err := dispatch.OfferNewOrder(ctx, tx, id); err != nil {
		return uuid.Nil, err
	}
	if err := lifecycle.Notify(ctx, tx, notifications.PassengerBot, passengerID, notifications.TplOrderCreatedPassenger,
		map[string]any{"order_id": id.String(), "order_number": number, "fare": n.Fare},
		"order_created_passenger:"+id.String()); err != nil {
		return uuid.Nil, err
	}
	data := map[string]any{"order_id": id.String(), "order_number": number, "status": lifecycle.StatusSearching,
		"category": q.Category, "vehicle_class": q.VehicleClass}
	if err := lifecycle.Publish(ctx, tx, realtime.OrderCreated, data,
		realtime.UserScope(passengerID), realtime.AdminScope); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

type snapshotStop struct {
	Seq            int     `json:"seq"`
	Kind           string  `json:"kind"`
	Lat            float64 `json:"lat"`
	Lon            float64 `json:"lon"`
	Address        string  `json:"address"`
	AddressDetails *string `json:"address_details"`
}

func insertRouteSnapshot(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, q pricing.StoredQuote, list []stops.Stop) error {
	ss := make([]snapshotStop, len(list))
	for i, s := range list {
		ss[i] = snapshotStop{Seq: s.Seq, Kind: s.Kind, Lat: s.Lat, Lon: s.Lon, Address: s.Address, AddressDetails: s.AddressDetails}
	}
	legs := make([]RouteLeg, len(q.Legs))
	for i, l := range q.Legs {
		legs[i] = RouteLeg{DistanceM: l.DistanceM, DurationS: l.DurationS}
	}
	stopsJSON, err := json.Marshal(ss)
	if err != nil {
		return err
	}
	legsJSON, err := json.Marshal(legs)
	if err != nil {
		return err
	}
	bboxJSON, err := json.Marshal(q.BBox)
	if err != nil {
		return err
	}
	geometry := []byte(q.Geometry)
	if len(geometry) == 0 {
		geometry = []byte(`null`)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_route_snapshots (order_id, provider, stops, stops_hash, geometry, legs, bbox, distance_m, duration_s)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		orderID, RouteProvider, stopsJSON, q.StopsHash, geometry, legsJSON, bboxJSON, q.DistanceM, q.DurationS); err != nil {
		return fmt.Errorf("orders: insert route snapshot: %w", err)
	}
	return nil
}
