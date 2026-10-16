package botapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// orderSummary is the OpenAPI BotOrderSummary.
type orderSummary struct {
	ID                 uuid.UUID `json:"id"`
	Number             int64     `json:"number"`
	Status             string    `json:"status"`
	Category           string    `json:"category"`
	Fare               int64     `json:"fare"`
	Currency           string    `json:"currency"`
	PickupAddress      string    `json:"pickup_address"`
	DestinationAddress string    `json:"destination_address"`
	CreatedAt          time.Time `json:"created_at"`
}

type orderList struct {
	Items []orderSummary `json:"items"`
}

// stopLabel renders a stop address, or its coordinates when the address is empty.
const stopLabel = `COALESCE(NULLIF(btrim(%[1]s.address), ''),
	to_char(%[1]s.lat, 'FM990.00000') || ', ' || to_char(%[1]s.lon, 'FM990.00000'))`

var orderSummarySelect = fmt.Sprintf(`
	SELECT o.id, o.number, o.status, o.category, o.fare, o.currency,
	       COALESCE(%s, ''), COALESCE(%s, ''), o.created_at
	FROM orders o
	LEFT JOIN order_stops p ON p.order_id = o.id AND p.seq = 0
	LEFT JOIN LATERAL (
		SELECT s.address, s.lat, s.lon FROM order_stops s
		WHERE s.order_id = o.id ORDER BY s.seq DESC LIMIT 1
	) d ON true`, fmt.Sprintf(stopLabel, "p"), fmt.Sprintf(stopLabel, "d"))

func queryOrderSummaries(ctx context.Context, q db.Querier, where string, args ...any) ([]orderSummary, error) {
	rows, err := q.Query(ctx, orderSummarySelect+" "+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list order summaries: %w", err)
	}
	defer rows.Close()
	out := []orderSummary{}
	for rows.Next() {
		var o orderSummary
		if err := rows.Scan(&o.ID, &o.Number, &o.Status, &o.Category, &o.Fare, &o.Currency,
			&o.PickupAddress, &o.DestinationAddress, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan order summary: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list order summaries: %w", err)
	}
	return out, nil
}

// passengerOrders lists a passenger's most recent orders (empty for unknown users).
func (h *Handler) passengerOrders(w http.ResponseWriter, r *http.Request) error {
	tgID, err := tgIDParam(r)
	if err != nil {
		return err
	}
	limit, err := httpx.QueryInt(r, "limit", 5, 1, 20)
	if err != nil {
		return err
	}
	items, err := queryOrderSummaries(r.Context(), h.pool, `
		JOIN users u ON u.id = o.passenger_id
		WHERE u.telegram_user_id = $1 AND o.status <> 'draft'
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $2`, tgID, limit)
	if err != nil {
		return err
	}
	return httpx.OK(w, orderList{Items: items})
}

// activeDriverOrder returns the driver's current order, if any.
func activeDriverOrder(ctx context.Context, q db.Querier, driverID uuid.UUID) (*orderSummary, error) {
	items, err := queryOrderSummaries(ctx, q, `
		WHERE o.driver_id = $1 AND o.status IN ('accepted', 'driver_arriving', 'in_progress')
		ORDER BY o.accepted_at DESC NULLS LAST
		LIMIT 1`, driverID)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}
