package drivers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/iin"
)

// IINHeldByOther reports whether another live driver (pending, approved,
// changes_requested or suspended) holds the IIN. The partial unique index
// driver_profiles_iin_unique has the final word under concurrency.
func IINHeldByOther(ctx context.Context, q db.Querier, value string, driverID uuid.UUID) (bool, error) {
	var held bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM driver_profiles WHERE iin = $1 AND id <> $2 AND status = ANY($3))`,
		value, driverID, liveStatuses).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("drivers: iin check: %w", err)
	}
	return held, nil
}

// errIINAlreadySet refuses to replace a stored IIN: drivers enter it once,
// corrections go through support.
var errIINAlreadySet = httpx.Conflict("conflict", "the IIN is already set").
	WithDetails(map[string]any{"reason": "iin_already_set"})

type profileIINRequest struct {
	IIN *string `json:"iin"`
}

// putProfileIIN stores the IIN of a driver who has none yet, typically one
// approved before the IIN was asked for. Working never depends on it. The
// IIN is set once: the same value again is a no-op, another one is refused.
func (h *Handler) putProfileIIN(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	var req profileIINRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.IIN == nil || strings.TrimSpace(*req.IIN) == "" {
		return httpx.Validation(map[string]string{"iin": "required"})
	}
	value, err := iin.Normalize(*req.IIN)
	if err != nil {
		return httpx.Validation(map[string]string{"iin": iinReason(err)})
	}
	ctx := r.Context()
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		prof, err := LockProfile(ctx, tx, d.ID)
		if err != nil {
			return err
		}
		switch prof.IINValue() {
		case value:
			return nil
		case "":
		default:
			return errIINAlreadySet
		}
		held, err := IINHeldByOther(ctx, tx, value, prof.ID)
		if err != nil {
			return err
		}
		if held {
			return ErrIINTaken
		}
		_, err = tx.Exec(ctx, `UPDATE driver_profiles SET iin = $2 WHERE id = $1`, prof.ID, value)
		if db.IsUniqueViolation(err, IINUniqueIndex) {
			return ErrIINTaken
		}
		if err != nil {
			return fmt.Errorf("drivers: store iin: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	reg, err := LoadRegistration(ctx, h.pool, d.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, NewProfileView(reg))
}
