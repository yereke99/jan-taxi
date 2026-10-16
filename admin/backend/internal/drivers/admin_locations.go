package drivers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// LastLocation is a driver's last known position (OpenAPI DriverLastLocation)
// plus its age in seconds at the time of the response.
type LastLocation struct {
	Lat        float64   `json:"lat"`
	Lon        float64   `json:"lon"`
	AccuracyM  *float64  `json:"accuracy_m"`
	Heading    *float64  `json:"heading"`
	SpeedMps   *float64  `json:"speed_mps"`
	Source     string    `json:"source"`
	RecordedAt time.Time `json:"recorded_at"`
	ReceivedAt time.Time `json:"received_at"`
	AgeS       int64     `json:"age_s"`
}

func (l *LastLocation) setAge(now time.Time) {
	l.RecordedAt, l.ReceivedAt = l.RecordedAt.UTC(), l.ReceivedAt.UTC()
	l.AgeS = int64(math.Max(0, now.Sub(l.RecordedAt).Seconds()))
}

// GetLastLocation returns the driver's last known location, or nil.
func GetLastLocation(ctx context.Context, q db.Querier, driverID uuid.UUID) (*LastLocation, error) {
	var (
		l            LastLocation
		acc, hd, spd *float32
	)
	err := q.QueryRow(ctx, `
		SELECT lat, lon, accuracy_m, heading, speed_mps, source, recorded_at, received_at
		FROM driver_locations WHERE driver_id = $1`, driverID).
		Scan(&l.Lat, &l.Lon, &acc, &hd, &spd, &l.Source, &l.RecordedAt, &l.ReceivedAt)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("drivers: last location: %w", err)
	}
	l.AccuracyM, l.Heading, l.SpeedMps = f64(acc), f64(hd), f64(spd)
	l.setAge(time.Now())
	return &l, nil
}

func f64(v *float32) *float64 {
	if v == nil {
		return nil
	}
	x := math.Round(float64(*v)*100) / 100
	return &x
}

// DriverRef identifies a driver on the admin map (OpenAPI DriverRef).
type DriverRef struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Phone     *string   `json:"phone"`
}

// OrderRef is a short order reference (OpenAPI OrderRef).
type OrderRef struct {
	ID     uuid.UUID `json:"id"`
	Number int64     `json:"number"`
	Status string    `json:"status"`
}

// LocationItem is one driver on the live map (OpenAPI DriverLocationItem).
type LocationItem struct {
	Driver      DriverRef    `json:"driver"`
	Status      string       `json:"status"`
	IsOnline    bool         `json:"is_online"`
	ActiveOrder *OrderRef    `json:"active_order"`
	Location    LastLocation `json:"location"`
}

// LiveLocations lists drivers that are online or on an active order together
// with their last known location, optionally in one city.
func LiveLocations(ctx context.Context, q db.Querier, cityID *uuid.UUID) ([]LocationItem, error) {
	rows, err := q.Query(ctx, `
		SELECT dp.id, dp.code, dp.first_name, dp.last_name, dp.phone_e164, dp.status, dp.is_online,
		       o.id, o.number, o.status,
		       l.lat, l.lon, l.accuracy_m, l.heading, l.speed_mps, l.source, l.recorded_at, l.received_at
		FROM driver_locations l
		JOIN driver_profiles dp ON dp.id = l.driver_id
		LEFT JOIN LATERAL (
			SELECT id, number, status FROM orders
			WHERE driver_id = dp.id AND status = ANY($2)
			ORDER BY accepted_at DESC NULLS LAST LIMIT 1
		) o ON true
		WHERE (dp.is_online OR o.id IS NOT NULL) AND ($1::uuid IS NULL OR dp.city_id = $1)
		ORDER BY dp.code`, cityID, ActiveOrderStatuses)
	if err != nil {
		return nil, fmt.Errorf("drivers: live locations: %w", err)
	}
	defer rows.Close()
	now := time.Now()
	out := []LocationItem{}
	for rows.Next() {
		var (
			it           LocationItem
			orderID      *uuid.UUID
			orderNumber  *int64
			orderStatus  *string
			acc, hd, spd *float32
		)
		if err := rows.Scan(&it.Driver.ID, &it.Driver.Code, &it.Driver.FirstName, &it.Driver.LastName, &it.Driver.Phone,
			&it.Status, &it.IsOnline, &orderID, &orderNumber, &orderStatus,
			&it.Location.Lat, &it.Location.Lon, &acc, &hd, &spd, &it.Location.Source, &it.Location.RecordedAt,
			&it.Location.ReceivedAt); err != nil {
			return nil, fmt.Errorf("drivers: live locations: %w", err)
		}
		it.Location.AccuracyM, it.Location.Heading, it.Location.SpeedMps = f64(acc), f64(hd), f64(spd)
		it.Location.setAge(now)
		if orderID != nil {
			it.ActiveOrder = &OrderRef{ID: *orderID, Number: *orderNumber, Status: *orderStatus}
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (h *Handler) adminLocations(w http.ResponseWriter, r *http.Request) error {
	cityID, err := httpx.QueryUUID(r, "city_id")
	if err != nil {
		return err
	}
	items, err := LiveLocations(r.Context(), h.pool, cityID)
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"items": items})
}
