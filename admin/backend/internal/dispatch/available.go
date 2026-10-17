package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
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

// orderFilter is the per-order part of §7 for driver @driver_id: a searching
// order that is not the driver's own passenger order, with a pickup inside
// the order's effective radius of the driver's fresh location. Nothing here
// depends on the driver's or the order's city. The affordability test is left
// to the caller so the same filter can count orders that only the balance
// excludes.
const orderFilter = `
	o.status = 'searching'
	AND o.passenger_id <> d.user_id
	AND ` + freshLocation + `
	AND ` + withinRadius

const orderFrom = `
	FROM orders o
	JOIN driver_profiles d ON d.id = @driver_id
	JOIN driver_locations dl ON dl.driver_id = d.id
	LEFT JOIN wallets w ON w.driver_id = d.id`

const affordable = `o.commission_amount <= COALESCE(w.balance - w.reserved, 0)`

// listCards runs the card query for driver driverID, nearest pickup first;
// orderID limits it to one order.
func listCards(ctx context.Context, q db.Querier, p settings.DispatchSettings, driverID uuid.UUID, orderID *uuid.UUID, limit int) ([]OrderCard, error) {
	rows, err := q.Query(ctx, `
		SELECT o.id, o.number, o.category, o.service_type, o.cargo_type, o.stop_count, o.comment,
		       o.cargo_description, o.fare, o.currency, o.commission_amount, o.payment_method,
		       o.distance_m, o.duration_s, o.created_at, o.search_expires_at, `+distanceToPickup+`
		`+orderFrom+`
		WHERE `+orderFilter+` AND `+affordable+`
		  AND (@order_id::uuid IS NULL OR o.id = @order_id)
		ORDER BY ST_Distance(dl.location, o.pickup) ASC, o.created_at DESC, o.id DESC
		LIMIT @limit`, policyArgs(p, pgx.NamedArgs{"driver_id": driverID, "order_id": orderID, "limit": limit}))
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

// countOrders counts the searching orders in the driver's reach whose
// commission the available balance covers (affordableOnly) or does not.
func countOrders(ctx context.Context, q db.Querier, p settings.DispatchSettings, driverID uuid.UUID, affordableOnly bool) (int, error) {
	cond := affordable
	if !affordableOnly {
		cond = `NOT (` + affordable + `)`
	}
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) `+orderFrom+` WHERE `+orderFilter+` AND `+cond,
		policyArgs(p, pgx.NamedArgs{"driver_id": driverID})).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("dispatch: count orders: %w", err)
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
// live, filtered by §7 (approved, online, no active order, fresh location
// within each order's effective radius, available balance ≥ commission) and
// sorted by distance to the pickup. A driver who is not eligible gets an
// empty list.
func AvailableOrders(ctx context.Context, q db.Querier, driverID uuid.UUID, limit int) ([]OrderCard, error) {
	res, err := ListAvailable(ctx, q, driverID, limit)
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

// ListAvailable returns the available orders together with the driver's
// eligibility. When the driver passes every driver-level rule but every
// searching order in reach is excluded only by the balance, eligibility is
// insufficient_balance.
func ListAvailable(ctx context.Context, q db.Querier, driverID uuid.UUID, limit int) (Available, error) {
	p := settings.Dispatch(ctx, q)
	s, err := loadDriverState(ctx, q, driverID, p)
	if err != nil {
		return Available{}, err
	}
	el := s.driverLevel()
	if !el.Eligible {
		return Available{Items: []OrderCard{}, Eligibility: el}, nil
	}
	items, err := listCards(ctx, q, p, driverID, nil, clampLimit(limit))
	if err != nil {
		return Available{}, err
	}
	if len(items) == 0 {
		n, err := countOrders(ctx, q, p, driverID, false)
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
	p := settings.Dispatch(ctx, q)
	s, err := loadDriverState(ctx, q, driverID, p)
	if err != nil {
		return 0, err
	}
	if !s.driverLevel().Eligible {
		return 0, nil
	}
	return countOrders(ctx, q, p, driverID, true)
}

// AvailableCard returns the card of one searching order if driverID is
// eligible for it right now (found=false otherwise).
func AvailableCard(ctx context.Context, q db.Querier, driverID, orderID uuid.UUID) (OrderCard, bool, error) {
	p := settings.Dispatch(ctx, q)
	s, err := loadDriverState(ctx, q, driverID, p)
	if err != nil {
		return OrderCard{}, false, err
	}
	if !s.driverLevel().Eligible {
		return OrderCard{}, false, nil
	}
	cards, err := listCards(ctx, q, p, driverID, &orderID, 1)
	if err != nil || len(cards) == 0 {
		return OrderCard{}, false, err
	}
	return cards[0], true, nil
}
