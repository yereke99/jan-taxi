package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Core is the part of an order the state machine works with.
type Core struct {
	ID               uuid.UUID
	Number           int64
	PassengerID      uuid.UUID
	CityID           uuid.UUID
	Category         string
	ServiceType      string
	Status           string
	DriverID         *uuid.UUID
	VehicleID        *uuid.UUID
	Fare             int64
	CommissionAmount int64
	SearchExpiresAt  *time.Time
	AcceptedAt       *time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time
	CancelledAt      *time.Time
	Version          int
}

// HasDriver reports whether the order is assigned to driverID.
func (c Core) HasDriver(driverID uuid.UUID) bool {
	return c.DriverID != nil && *c.DriverID == driverID
}

// CoreColumns is the select list scanned by ScanCore (table alias o).
const CoreColumns = `o.id, o.number, o.passenger_id, o.city_id, o.category, o.service_type, o.status,
	o.driver_id, o.vehicle_id, o.fare, o.commission_amount, o.search_expires_at, o.accepted_at,
	o.started_at, o.completed_at, o.cancelled_at, o.version`

type scanner interface{ Scan(dest ...any) error }

// ScanCore scans a row selected with CoreColumns.
func ScanCore(row scanner) (Core, error) {
	var c Core
	var fare, commission int32
	err := row.Scan(&c.ID, &c.Number, &c.PassengerID, &c.CityID, &c.Category, &c.ServiceType, &c.Status,
		&c.DriverID, &c.VehicleID, &fare, &commission, &c.SearchExpiresAt, &c.AcceptedAt,
		&c.StartedAt, &c.CompletedAt, &c.CancelledAt, &c.Version)
	if err != nil {
		return Core{}, err
	}
	c.Fare, c.CommissionAmount = int64(fare), int64(commission)
	return c, nil
}

// LoadCore reads an order; with lock it takes the row lock (FOR UPDATE) so the
// caller's transaction owns the order until it ends. Unknown orders are 404.
func LoadCore(ctx context.Context, q db.Querier, orderID uuid.UUID, lock bool) (Core, error) {
	sql := `SELECT ` + CoreColumns + ` FROM orders o WHERE o.id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	c, err := ScanCore(q.QueryRow(ctx, sql, orderID))
	if db.IsNoRows(err) {
		return Core{}, httpx.NotFound()
	}
	if err != nil {
		return Core{}, fmt.Errorf("orders: load %s: %w", orderID, err)
	}
	return c, nil
}
