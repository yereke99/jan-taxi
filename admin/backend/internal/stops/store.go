package stops

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// Input is one point of a new order, in route order.
type Input struct {
	Lat, Lon       float64
	Address        string
	AddressDetails *string
}

// Insert stores the route points of a new order (seq 0 pickup, last
// destination) and returns them as stored. Addresses are cleaned; an empty
// address is stored as formatted coordinates so every stop is displayable.
func Insert(ctx context.Context, q db.Querier, orderID uuid.UUID, in []Input) ([]Stop, error) {
	if len(in) < MinPoints || len(in) > MaxPoints {
		return nil, fmt.Errorf("stops: insert %d points", len(in))
	}
	out := make([]Stop, 0, len(in))
	for i, p := range in {
		// Points come from a server-side quote that was validated when it was
		// created, so over-long text is cut rather than rejected here.
		address := truncate(textutil.Clean(p.Address), MaxAddressLen)
		if address == "" {
			address = DisplayAddress("", p.Lat, p.Lon)
		}
		var details *string
		if p.AddressDetails != nil {
			if d := truncate(textutil.Clean(*p.AddressDetails), MaxDetailsLen); d != "" {
				details = &d
			}
		}
		s := Stop{ID: uuid.New(), Seq: i, Kind: KindFor(i, len(in)), Lat: p.Lat, Lon: p.Lon,
			Address: address, AddressDetails: details}
		if _, err := q.Exec(ctx, `
			INSERT INTO order_stops (id, order_id, seq, kind, lat, lon, address, address_details)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			s.ID, orderID, s.Seq, s.Kind, s.Lat, s.Lon, s.Address, s.AddressDetails); err != nil {
			return nil, fmt.Errorf("stops: insert: %w", err)
		}
		out = append(out, s)
	}
	return out, nil
}

// ForOrder returns the order's stops in seq order.
func ForOrder(ctx context.Context, q db.Querier, orderID uuid.UUID) ([]Stop, error) {
	m, err := ForOrders(ctx, q, []uuid.UUID{orderID})
	if err != nil {
		return nil, err
	}
	return m[orderID], nil
}

// ForOrders returns the stops of several orders, each list in seq order.
func ForOrders(ctx context.Context, q db.Querier, orderIDs []uuid.UUID) (map[uuid.UUID][]Stop, error) {
	out := make(map[uuid.UUID][]Stop, len(orderIDs))
	if len(orderIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT order_id, id, seq, kind, lat, lon, address, address_details, completed_at
		FROM order_stops WHERE order_id = ANY($1) ORDER BY order_id, seq`, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("stops: load: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var orderID uuid.UUID
		var s Stop
		if err := rows.Scan(&orderID, &s.ID, &s.Seq, &s.Kind, &s.Lat, &s.Lon, &s.Address, &s.AddressDetails, &s.CompletedAt); err != nil {
			return nil, fmt.Errorf("stops: scan: %w", err)
		}
		if s.CompletedAt != nil {
			*s.CompletedAt = s.CompletedAt.UTC()
		}
		out[orderID] = append(out[orderID], s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stops: load: %w", err)
	}
	return out, nil
}

// MarkCompleted sets completed_at on a stop that is not completed yet and
// reports whether it changed.
func MarkCompleted(ctx context.Context, q db.Querier, stopID uuid.UUID) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE order_stops SET completed_at = now() WHERE id = $1 AND completed_at IS NULL`, stopID)
	if err != nil {
		return false, fmt.Errorf("stops: complete: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkKindCompleted completes every not yet completed stop of the given kind
// on an order (pickup on start, destination on completion).
func MarkKindCompleted(ctx context.Context, q db.Querier, orderID uuid.UUID, kind string) error {
	if _, err := q.Exec(ctx, `
		UPDATE order_stops SET completed_at = now()
		WHERE order_id = $1 AND kind = $2 AND completed_at IS NULL`, orderID, kind); err != nil {
		return fmt.Errorf("stops: complete %s: %w", kind, err)
	}
	return nil
}

func truncate(s string, max int) string {
	c := []rune(s)
	if len(c) > max {
		c = c[:max]
	}
	return string(c)
}
