package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/plate"
	"github.com/yereke99/jantaxi/admin/backend/internal/stops"
)

// DriverInfo is the assigned driver as needed by notifications.
type DriverInfo struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Code      string
	FirstName string
	LastName  string
	Make      string
	Model     string
	Color     string
	Plate     string // normalised plate (e.g. 123ABC04), raw input as a fallback
}

// Name is the driver's first name with the last-name initial ("Ерлан Б.").
func (d DriverInfo) Name() string {
	name := strings.TrimSpace(d.FirstName)
	if r := []rune(strings.TrimSpace(d.LastName)); len(r) > 0 {
		name = strings.TrimSpace(name + " " + string(r[0]) + ".")
	}
	if name == "" {
		return d.Code
	}
	return name
}

// Vehicle is "<make> <model>, <color>" as shown in messages.
func (d DriverInfo) Vehicle() string {
	v := strings.TrimSpace(strings.TrimSpace(d.Make) + " " + strings.TrimSpace(d.Model))
	if c := strings.TrimSpace(d.Color); c != "" {
		if v == "" {
			return c
		}
		v += ", " + c
	}
	if v == "" {
		return "—"
	}
	return v
}

// PlateLabel is the plate as shown in messages.
func (d DriverInfo) PlateLabel() string {
	if d.Plate == "" {
		return "—"
	}
	return d.Plate
}

// LoadDriverInfo reads a driver with the current (non-archived) vehicle.
func LoadDriverInfo(ctx context.Context, q db.Querier, driverID uuid.UUID) (DriverInfo, error) {
	var d DriverInfo
	var make_, model, color, plateNorm, plateRaw *string
	err := q.QueryRow(ctx, `
		SELECT d.id, d.user_id, d.code, d.first_name, d.last_name, v.make, v.model, v.color, v.plate_normalized, v.plate_raw
		FROM driver_profiles d
		LEFT JOIN LATERAL (
			SELECT make, model, color, plate_normalized, plate_raw FROM vehicles
			WHERE driver_id = d.id AND status <> 'archived'
			ORDER BY (status = 'claimed') DESC, created_at DESC LIMIT 1) v ON true
		WHERE d.id = $1`, driverID).
		Scan(&d.ID, &d.UserID, &d.Code, &d.FirstName, &d.LastName, &make_, &model, &color, &plateNorm, &plateRaw)
	if err != nil {
		return DriverInfo{}, fmt.Errorf("orders: load driver %s: %w", driverID, err)
	}
	d.Make, d.Model, d.Color = deref(make_), deref(model), deref(color)
	d.Plate = deref(plateNorm)
	if d.Plate == "" {
		d.Plate = strings.TrimSpace(deref(plateRaw))
	}
	return d, nil
}

// PlateDisplay formats a stored plate for display ("123 ABC 04"); a raw value
// is returned as is.
func PlateDisplay(normalized, raw *string) string {
	if n := deref(normalized); n != "" {
		return plate.Format(n)
	}
	return strings.TrimSpace(deref(raw))
}

// Route is the pickup and destination addresses of an order, as shown in messages.
type Route struct {
	Pickup      string
	Destination string
}

// LoadRoute reads the first and last stop addresses.
func LoadRoute(ctx context.Context, q db.Querier, orderID uuid.UUID) (Route, error) {
	list, err := stops.ForOrder(ctx, q, orderID)
	if err != nil {
		return Route{}, err
	}
	var r Route
	if p := stops.Pickup(list); p != nil {
		r.Pickup = stops.DisplayAddress(p.Address, p.Lat, p.Lon)
	}
	if d := stops.Destination(list); d != nil {
		r.Destination = stops.DisplayAddress(d.Address, d.Lat, d.Lon)
	}
	if r.Pickup == "" {
		r.Pickup = "—"
	}
	if r.Destination == "" {
		r.Destination = "—"
	}
	return r, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
