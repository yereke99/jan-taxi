package cities

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

type zoneRequest struct {
	Name     *string         `json:"name"`
	IsActive *bool           `json:"is_active"`
	Geometry json.RawMessage `json:"geometry"`
}

func (h *AdminHandler) listZones(w http.ResponseWriter, r *http.Request) error {
	cityID, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := Get(ctx, h.pool, cityID); err != nil {
		return err
	}
	zones, err := ListZones(ctx, h.pool, cityID)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, map[string]any{"items": zones})
}

func (h *AdminHandler) createZone(w http.ResponseWriter, r *http.Request) error {
	cityID, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req zoneRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	fields := map[string]string{}
	var name string
	if req.Name == nil {
		fields["name"] = reasonRequired
	} else if v, reason := cleanLabel(*req.Name); reason != "" {
		fields["name"] = reason
	} else {
		name = v
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	geometry, err := ValidateZoneGeometry(ctx, h.pool, req.Geometry)
	if err != nil {
		return err
	}
	active := req.IsActive == nil || *req.IsActive
	var zone Zone
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		city, err := getCity(ctx, tx, "c.id = $1 FOR SHARE", cityID)
		if err != nil {
			return err
		}
		if city.IsDefaultRegion {
			return ErrDefaultRegionLocked
		}
		if zone, err = zoneInsert(ctx, tx, cityID, name, geometry, active); err != nil {
			return err
		}
		return logAudit(ctx, tx, r, "zone.create", "service_zone", zone.ID.String(), nil, zone)
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, zone)
}

func (h *AdminHandler) updateZone(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req zoneRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	fields := map[string]string{}
	var name *string
	if req.Name != nil {
		if v, reason := cleanLabel(*req.Name); reason != "" {
			fields["name"] = reason
		} else {
			name = &v
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	var geometry *string
	if req.Geometry != nil {
		g, err := ValidateZoneGeometry(ctx, h.pool, req.Geometry)
		if err != nil {
			return err
		}
		geometry = &g
	}
	var updated Zone
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM service_zones WHERE id = $1 FOR UPDATE`, id); err != nil {
			return httpx.Internal(err)
		}
		before, err := GetZone(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := checkZoneEditable(ctx, tx, before); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE service_zones SET
				name = COALESCE($2, name),
				is_active = COALESCE($3, is_active),
				geom = COALESCE(ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON($4::text), 4326)), geom)
			WHERE id = $1`, id, name, req.IsActive, geometry)
		if err != nil {
			return httpx.Internal(err)
		}
		if updated, err = GetZone(ctx, tx, id); err != nil {
			return err
		}
		return logAudit(ctx, tx, r, "zone.update", "service_zone", id.String(), before, updated)
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, updated)
}

func (h *AdminHandler) deleteZone(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		before, err := GetZone(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := checkZoneEditable(ctx, tx, before); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM service_zones WHERE id = $1`, id)
		if err != nil {
			return httpx.Internal(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrZoneNotFound
		}
		return logAudit(ctx, tx, r, "zone.delete", "service_zone", id.String(), before, nil)
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// checkZoneEditable refuses changes to the zone of the nationwide default
// region: it is the Kazakhstan boundary, and a smaller or missing one would
// change which orders the region prices against the city zones.
func checkZoneEditable(ctx context.Context, q db.Querier, z Zone) error {
	var locked bool
	if err := q.QueryRow(ctx, `SELECT is_default_region FROM cities WHERE id = $1`, z.CityID).Scan(&locked); err != nil {
		return httpx.Internal(err)
	}
	if locked {
		return ErrDefaultRegionLocked
	}
	return nil
}
