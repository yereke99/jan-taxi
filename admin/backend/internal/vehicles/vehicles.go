// Package vehicles stores drivers' vehicles and their photos and guards plate
// uniqueness. A vehicle's status mirrors its driver's moderation state so that
// the partial unique index vehicles_plate_unique protects plates of every live
// driver (pending, approved, changes_requested, suspended): draft vehicles hold
// nothing, claimed vehicles hold their plate, released and archived ones free it.
package vehicles

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/plate"
)

// Vehicle statuses (vehicles.status).
const (
	StatusDraft    = "draft"
	StatusClaimed  = "claimed"
	StatusReleased = "released"
	StatusArchived = "archived"
)

// Field length limits (code points), matching the table checks.
const (
	MaxMake     = 40
	MaxModel    = 40
	MaxColor    = 30
	MaxPlateRaw = 20
)

// Errors returned to clients.
var (
	ErrNotFound   = httpx.NotFound()
	ErrPlateTaken = httpx.NewError(http.StatusConflict, "plate_taken", "plate is registered to another driver")
)

const plateUniqueIndex = "vehicles_plate_unique"

// Vehicle is a driver's vehicle. PlateNormalized is empty until a valid plate
// has been saved.
type Vehicle struct {
	ID              uuid.UUID
	DriverID        uuid.UUID
	Make            string
	Model           string
	Color           string
	PlateRaw        string
	PlateNormalized string
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PlateDisplay is the plate as shown to people: "123 ABC 04" for a valid
// plate, otherwise whatever the driver typed.
func (v Vehicle) PlateDisplay() string {
	if v.PlateNormalized != "" {
		return plate.Format(v.PlateNormalized)
	}
	return v.PlateRaw
}

// PlateKind reports the plate generation ("current" or "legacy"), or "" when
// no valid plate is stored. Moderators double-check legacy plates.
func (v Vehicle) PlateKind() string {
	if v.PlateNormalized == "" {
		return ""
	}
	p, err := plate.Parse(v.PlateNormalized)
	if err != nil {
		return ""
	}
	return string(p.Kind)
}

// Details are the editable text fields of a vehicle.
type Details struct {
	Make, Model, Color, PlateRaw, PlateNormalized string
}

const vehicleColumns = `id, driver_id, make, model, color, plate_raw, COALESCE(plate_normalized, ''), status, created_at, updated_at`

func scan(row interface{ Scan(...any) error }) (Vehicle, error) {
	var v Vehicle
	err := row.Scan(&v.ID, &v.DriverID, &v.Make, &v.Model, &v.Color, &v.PlateRaw, &v.PlateNormalized, &v.Status,
		&v.CreatedAt, &v.UpdatedAt)
	if db.IsNoRows(err) {
		return Vehicle{}, ErrNotFound
	}
	if err != nil {
		return Vehicle{}, fmt.Errorf("vehicles: scan: %w", err)
	}
	return v, nil
}

// Current returns the driver's current (non-archived) vehicle, or ErrNotFound.
func Current(ctx context.Context, q db.Querier, driverID uuid.UUID) (Vehicle, error) {
	return scan(q.QueryRow(ctx, `SELECT `+vehicleColumns+` FROM vehicles WHERE driver_id = $1 AND status <> 'archived'`, driverID))
}

// CurrentForUpdate is Current with the row locked (call inside a transaction).
func CurrentForUpdate(ctx context.Context, q db.Querier, driverID uuid.UUID) (Vehicle, error) {
	return scan(q.QueryRow(ctx, `SELECT `+vehicleColumns+` FROM vehicles WHERE driver_id = $1 AND status <> 'archived' FOR UPDATE`, driverID))
}

// CurrentByDriver returns the current vehicles of several drivers keyed by driver id.
func CurrentByDriver(ctx context.Context, q db.Querier, driverIDs []uuid.UUID) (map[uuid.UUID]Vehicle, error) {
	out := make(map[uuid.UUID]Vehicle, len(driverIDs))
	if len(driverIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+vehicleColumns+` FROM vehicles WHERE driver_id = ANY($1) AND status <> 'archived'`, driverIDs)
	if err != nil {
		return nil, fmt.Errorf("vehicles: list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out[v.DriverID] = v
	}
	return out, rows.Err()
}

// Create inserts a draft vehicle for the driver.
func Create(ctx context.Context, q db.Querier, driverID uuid.UUID, d Details) (Vehicle, error) {
	return scan(q.QueryRow(ctx, `
		INSERT INTO vehicles (driver_id, make, model, color, plate_raw, plate_normalized, status)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), 'draft')
		RETURNING `+vehicleColumns, driverID, d.Make, d.Model, d.Color, d.PlateRaw, d.PlateNormalized))
}

// UpdateDetails replaces the text fields. Changing the plate of a claimed
// vehicle to one claimed by another driver fails with ErrPlateTaken.
func UpdateDetails(ctx context.Context, q db.Querier, vehicleID uuid.UUID, d Details) error {
	tag, err := q.Exec(ctx, `
		UPDATE vehicles SET make = $2, model = $3, color = $4, plate_raw = $5, plate_normalized = NULLIF($6, '')
		WHERE id = $1`, vehicleID, d.Make, d.Model, d.Color, d.PlateRaw, d.PlateNormalized)
	if db.IsUniqueViolation(err, plateUniqueIndex) {
		return ErrPlateTaken
	}
	if err != nil {
		return fmt.Errorf("vehicles: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus moves a vehicle to status. Claiming a plate that another live
// driver holds fails with ErrPlateTaken; the unique index makes this safe
// under concurrency (exactly one of two simultaneous claims wins).
func SetStatus(ctx context.Context, q db.Querier, vehicleID uuid.UUID, status string) error {
	tag, err := q.Exec(ctx, `UPDATE vehicles SET status = $2 WHERE id = $1`, vehicleID, status)
	if db.IsUniqueViolation(err, plateUniqueIndex) {
		return ErrPlateTaken
	}
	if err != nil {
		return fmt.Errorf("vehicles: set status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PlateHeldByOther reports whether a claimed vehicle of another driver carries
// the normalised plate. driverID may be uuid.Nil (no driver profile yet).
func PlateHeldByOther(ctx context.Context, q db.Querier, plateNormalized string, driverID uuid.UUID) (bool, error) {
	var held bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM vehicles WHERE plate_normalized = $1 AND status = 'claimed' AND driver_id <> $2)`,
		plateNormalized, driverID).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("vehicles: plate check: %w", err)
	}
	return held, nil
}

// IsPlateTaken reports whether err is ErrPlateTaken.
func IsPlateTaken(err error) bool { return errors.Is(err, ErrPlateTaken) }
