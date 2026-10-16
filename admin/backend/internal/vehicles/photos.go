package vehicles

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Photo kinds (vehicle_photos.kind). Every kind except extra keeps a single
// current photo; up to MaxExtraPhotos extra photos are kept.
const (
	PhotoMain  = "main"
	PhotoBack  = "back"
	PhotoSide  = "side"
	PhotoPlate = "plate"
	PhotoExtra = "extra"
)

// MaxExtraPhotos is the number of additional vehicle photos a driver may keep.
const MaxExtraPhotos = 4

// PhotoKinds lists the kinds in display order.
var PhotoKinds = []string{PhotoMain, PhotoBack, PhotoSide, PhotoPlate, PhotoExtra}

// ErrTooManyPhotos is returned when the extra-photo limit is reached.
var ErrTooManyPhotos = httpx.Validation(map[string]string{"kind": "too_many"}).
	WithDetails(map[string]any{"max_extra_photos": MaxExtraPhotos})

// Photo is a current vehicle photo with its image metadata.
type Photo struct {
	FileID    uuid.UUID
	Kind      string
	Width     int
	Height    int
	CreatedAt time.Time
}

// Photos returns the current photos of a vehicle in display order.
func Photos(ctx context.Context, q db.Querier, vehicleID uuid.UUID) ([]Photo, error) {
	rows, err := q.Query(ctx, `
		SELECT vp.file_id, vp.kind, f.width, f.height, f.created_at
		FROM vehicle_photos vp
		JOIN files f ON f.id = vp.file_id AND f.deleted_at IS NULL
		WHERE vp.vehicle_id = $1 AND vp.is_current
		ORDER BY array_position(ARRAY['main', 'back', 'side', 'plate', 'extra'], vp.kind), vp.created_at, vp.id`, vehicleID)
	if err != nil {
		return nil, fmt.Errorf("vehicles: photos: %w", err)
	}
	defer rows.Close()
	out := []Photo{}
	for rows.Next() {
		var p Photo
		if err := rows.Scan(&p.FileID, &p.Kind, &p.Width, &p.Height, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("vehicles: photos: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddPhoto records fileID as a current photo of the vehicle. For single kinds
// the previous current photo is kept as history (is_current = false). Callers
// serialise concurrent uploads by locking the driver profile row first.
func AddPhoto(ctx context.Context, q db.Querier, vehicleID uuid.UUID, kind string, fileID uuid.UUID) error {
	if kind == PhotoExtra {
		var n int
		if err := q.QueryRow(ctx, `
			SELECT count(*) FROM vehicle_photos WHERE vehicle_id = $1 AND kind = 'extra' AND is_current`, vehicleID).Scan(&n); err != nil {
			return fmt.Errorf("vehicles: count photos: %w", err)
		}
		if n >= MaxExtraPhotos {
			return ErrTooManyPhotos
		}
	} else if _, err := q.Exec(ctx, `
		UPDATE vehicle_photos SET is_current = false WHERE vehicle_id = $1 AND kind = $2 AND is_current`, vehicleID, kind); err != nil {
		return fmt.Errorf("vehicles: replace photo: %w", err)
	}
	if _, err := q.Exec(ctx, `INSERT INTO vehicle_photos (vehicle_id, kind, file_id) VALUES ($1, $2, $3)`, vehicleID, kind, fileID); err != nil {
		return fmt.Errorf("vehicles: add photo: %w", err)
	}
	return nil
}

// PhotoKindOf returns the kind of a current photo of the vehicle showing
// fileID, or ErrNotFound.
func PhotoKindOf(ctx context.Context, q db.Querier, vehicleID, fileID uuid.UUID) (string, error) {
	var kind string
	err := q.QueryRow(ctx, `
		SELECT kind FROM vehicle_photos WHERE vehicle_id = $1 AND file_id = $2 AND is_current`, vehicleID, fileID).Scan(&kind)
	if db.IsNoRows(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("vehicles: photo kind: %w", err)
	}
	return kind, nil
}

// RemoveExtraPhoto takes an extra photo out of the current set (the row stays
// as history).
func RemoveExtraPhoto(ctx context.Context, q db.Querier, vehicleID, fileID uuid.UUID) error {
	tag, err := q.Exec(ctx, `
		UPDATE vehicle_photos SET is_current = false
		WHERE vehicle_id = $1 AND file_id = $2 AND kind = 'extra' AND is_current`, vehicleID, fileID)
	if err != nil {
		return fmt.Errorf("vehicles: remove photo: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
