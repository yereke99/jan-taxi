package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// OrderCard is an available order as listed to an eligible driver
// (openapi DriverOrderCard): no passenger contact data, no address details.
type OrderCard struct {
	ID                uuid.UUID       `json:"id"`
	Number            int64           `json:"number"`
	Category          string          `json:"category"`
	ServiceType       string          `json:"service_type"`
	CargoType         string          `json:"cargo_type"`
	Stops             []stops.Summary `json:"stops"`
	StopCount         int             `json:"stop_count"`
	Comment           *string         `json:"comment"`
	CargoDescription  *string         `json:"cargo_description"`
	Fare              int64           `json:"fare"`
	Currency          string          `json:"currency"`
	CommissionAmount  int64           `json:"commission_amount"`
	PaymentMethod     string          `json:"payment_method"`
	DistanceM         int             `json:"distance_m"`
	DurationS         int             `json:"duration_s"`
	DistanceToPickupM *int            `json:"distance_to_pickup_m"`
	CreatedAt         time.Time       `json:"created_at"`
	SearchExpiresAt   *time.Time      `json:"search_expires_at"`
}

// Available is the GET /driver/orders response (openapi AvailableOrders).
type Available struct {
	Items       []OrderCard `json:"items"`
	Eligibility Eligibility `json:"eligibility"`
}

// Limits for available-order lists.
const (
	DefaultLimit = 50
	MaxLimit     = 100
)

// orderFilter is the per-order part of §7 for driver $1: same city, not the
// driver's own passenger order, commission covered by the available balance
// and, when the city requires it, a fresh location within the dispatch radius.
// The affordability test is left to the caller so the same filter can count
// orders that only the balance excludes.
const orderFilter = `
	o.status = 'searching'
	AND o.city_id = d.city_id
	AND o.passenger_id <> d.user_id
	AND (NOT c.require_driver_location OR (
		dl.recorded_at > now() - make_interval(secs => c.driver_location_max_age_s)
		AND ST_DWithin(dl.location, o.pickup, c.dispatch_radius_m)))`

const orderFrom = `
	FROM orders o
	JOIN driver_profiles d ON d.id = $1
	JOIN cities c ON c.id = d.city_id
	LEFT JOIN wallets w ON w.driver_id = d.id
	LEFT JOIN driver_locations dl ON dl.driver_id = d.id`

const affordable = `o.commission_amount <= COALESCE(w.balance - w.reserved, 0)`

// listCards runs the card query for driver driverID; orderID limits it to one order.
func listCards(ctx context.Context, q db.Querier, driverID uuid.UUID, orderID *uuid.UUID, limit int) ([]OrderCard, error) {
	rows, err := q.Query(ctx, `
		SELECT o.id, o.number, o.category, o.service_type, o.cargo_type, o.stop_count, o.comment,
		       o.cargo_description, o.fare, o.currency, o.commission_amount, o.payment_method,
		       o.distance_m, o.duration_s, o.created_at, o.search_expires_at,
		       CASE WHEN dl.location IS NULL THEN NULL ELSE round(ST_Distance(dl.location, o.pickup))::int END
		`+orderFrom+`
		WHERE `+orderFilter+` AND `+affordable+`
		  AND ($2::uuid IS NULL OR o.id = $2)
		ORDER BY CASE WHEN dl.recorded_at > now() - make_interval(secs => c.driver_location_max_age_s)
		              THEN ST_Distance(dl.location, o.pickup) END ASC NULLS LAST,
		         o.created_at DESC, o.id DESC
		LIMIT $3`, driverID, orderID, limit)
	if err != nil {
		return nil, fmt.Errorf("dispatch: available orders: %w", err)
	}
	cards, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OrderCard, error) {
		var c OrderCard
		err := row.Scan(&c.ID, &c.Number, &c.Category, &c.ServiceType, &c.CargoType, &c.StopCount, &c.Comment,
			&c.CargoDescription, &c.Fare, &c.Currency, &c.CommissionAmount, &c.PaymentMethod,
			&c.DistanceM, &c.DurationS, &c.CreatedAt, &c.SearchExpiresAt, &c.DistanceToPickupM)
		c.CreatedAt = c.CreatedAt.UTC()
		if c.SearchExpiresAt != nil {
			*c.SearchExpiresAt = c.SearchExpiresAt.UTC()
		}
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("dispatch: available orders: %w", err)
	}
	if len(cards) == 0 {
		return []OrderCard{}, nil
	}
	ids := make([]uuid.UUID, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	byOrder, err := stops.ForOrders(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	for i := range cards {
		list := byOrder[cards[i].ID]
		cards[i].Stops = make([]stops.Summary, len(list))
		for j, s := range list {
			cards[i].Stops[j] = s.Summary()
		}
		if cards[i].Currency == "" {
			cards[i].Currency = wallets.Currency
		}
	}
	return cards, nil
}

// countUnaffordable counts searching orders the driver would see if the
// balance covered their commission.
func countUnaffordable(ctx context.Context, q db.Querier, driverID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) `+orderFrom+`
		WHERE `+orderFilter+` AND NOT (`+affordable+`)`, driverID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("dispatch: count unaffordable: %w", err)
	}
	return n, nil
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	return min(limit, MaxLimit)
}

// AvailableOrders returns the searching orders driverID may accept right now:
// live, in the driver's city, filtered by §7 (approved, online, no active
// order, available balance ≥ commission, city location policy). Orders are
// sorted by distance to the pickup when the driver has a fresh location,
// otherwise newest first. A driver who is not eligible gets an empty list.
func AvailableOrders(ctx context.Context, q db.Querier, driverID uuid.UUID, limit int) ([]OrderCard, error) {
	res, err := ListAvailable(ctx, q, driverID, limit)
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

// ListAvailable returns the available orders together with the driver's
// eligibility. When the driver passes every driver-level rule but every
// searching order is excluded only by the balance, eligibility is
// insufficient_balance.
func ListAvailable(ctx context.Context, q db.Querier, driverID uuid.UUID, limit int) (Available, error) {
	s, err := loadDriverState(ctx, q, driverID)
	if err != nil {
		return Available{}, err
	}
	el := s.driverLevel()
	if !el.Eligible {
		return Available{Items: []OrderCard{}, Eligibility: el}, nil
	}
	items, err := listCards(ctx, q, driverID, nil, clampLimit(limit))
	if err != nil {
		return Available{}, err
	}
	if len(items) == 0 {
		n, err := countUnaffordable(ctx, q, driverID)
		if err != nil {
			return Available{}, err
		}
		if n > 0 {
			el = notEligible(ReasonInsufficientBalance)
		}
	}
	return Available{Items: items, Eligibility: el}, nil
}

// CountAvailable returns how many orders the driver could accept now.
func CountAvailable(ctx context.Context, q db.Querier, driverID uuid.UUID) (int, error) {
	s, err := loadDriverState(ctx, q, driverID)
	if err != nil {
		return 0, err
	}
	if !s.driverLevel().Eligible {
		return 0, nil
	}
	var n int
	err = q.QueryRow(ctx, `SELECT count(*) `+orderFrom+` WHERE `+orderFilter+` AND `+affordable, driverID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("dispatch: count available: %w", err)
	}
	return n, nil
}

// AvailableCard returns the card of one searching order if driverID is
// eligible for it right now (found=false otherwise).
func AvailableCard(ctx context.Context, q db.Querier, driverID, orderID uuid.UUID) (OrderCard, bool, error) {
	s, err := loadDriverState(ctx, q, driverID)
	if err != nil {
		return OrderCard{}, false, err
	}
	if !s.driverLevel().Eligible {
		return OrderCard{}, false, nil
	}
	cards, err := listCards(ctx, q, driverID, &orderID, 1)
	if err != nil || len(cards) == 0 {
		return OrderCard{}, false, err
	}
	return cards[0], true, nil
}
