package cities

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// maxAliases bounds the alias list per place.
const maxAliases = 20

type placeRequest struct {
	// CityID: absent keeps the current value, null detaches the place from any city.
	CityID   json.RawMessage `json:"city_id"`
	Name     *Name           `json:"name"`
	Aliases  *[]string       `json:"aliases"`
	Lat      *float64        `json:"lat"`
	Lon      *float64        `json:"lon"`
	IsActive *bool           `json:"is_active"`
}

func (req placeRequest) apply(ctx context.Context, q db.Querier, p *Place, creating bool) (map[string]string, error) {
	fields := map[string]string{}
	if req.CityID != nil {
		if bytes.Equal(bytes.TrimSpace(req.CityID), []byte("null")) {
			p.CityID = nil
		} else {
			id, ok := parseUUIDJSON(req.CityID)
			if !ok {
				fields["city_id"] = reasonInvalid
			} else if _, err := Get(ctx, q, id); errors.Is(err, ErrNotFound) {
				fields["city_id"] = reasonNotFound
			} else if err != nil {
				return nil, httpx.Internal(err)
			} else {
				p.CityID = &id
			}
		}
	}
	if req.Name != nil || creating {
		p.Name = cleanNames(req.Name, "name", fields)
	}
	if req.Aliases != nil {
		p.Aliases = cleanAliases(*req.Aliases, fields)
	}
	pointChanged := false
	if req.Lat != nil || req.Lon != nil || creating {
		lat, lon := p.Point.Lat, p.Point.Lon
		if req.Lat != nil {
			lat = *req.Lat
		}
		if req.Lon != nil {
			lon = *req.Lon
		}
		pt := geo.Point{Lat: lat, Lon: lon}
		switch {
		case creating && (req.Lat == nil || req.Lon == nil):
			fields["location"] = reasonRequired
		case !validPoint(pt):
			fields["location"] = reasonInvalid
		default:
			p.Point, pointChanged = pt, true
		}
	}
	if req.IsActive != nil {
		p.IsActive = *req.IsActive
	}
	if p.Aliases == nil {
		p.Aliases = []string{} // the column is NOT NULL
	}
	if pointChanged {
		ok, err := geo.InServiceArea(ctx, q, p.Point)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		if !ok {
			fields["location"] = reasonOutside
		}
	}
	return fields, nil
}

func parseUUIDJSON(raw json.RawMessage) (uuid.UUID, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	return id, err == nil
}

func cleanAliases(in []string, fields map[string]string) []string {
	if len(in) > maxAliases {
		fields["aliases"] = reasonTooMany
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, a := range in {
		v, reason := cleanLabel(a)
		if reason != "" {
			fields["aliases"] = reason
			return nil
		}
		key := Fold(v)
		if seen[key] {
			fields["aliases"] = reasonDuplicate
			return nil
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

func (h *AdminHandler) listPlaces(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	cityID, err := httpx.QueryUUID(r, "city_id")
	if err != nil {
		return err
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) > maxNameLen {
		return httpx.Validation(map[string]string{"q": reasonTooLong})
	}
	items, total, err := ListPlaces(r.Context(), h.pool, PlaceFilter{CityID: cityID, Query: q, Page: page})
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, httpx.Page[Place]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

func (h *AdminHandler) createPlace(w http.ResponseWriter, r *http.Request) error {
	var req placeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	var created Place
	err := db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		p := Place{IsActive: true}
		fields, err := req.apply(ctx, tx, &p, true)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO places AS p (city_id, name_kk, name_ru, name_en, aliases, lat, lon, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING `+placeColumns,
			p.CityID, p.Name.KK, p.Name.RU, p.Name.EN, p.Aliases, p.Point.Lat, p.Point.Lon, p.IsActive)
		if created, err = scanPlace(row); err != nil {
			return httpx.Internal(err)
		}
		return logAudit(ctx, tx, r, "place.create", "place", created.ID.String(), nil, created)
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, created)
}

func (h *AdminHandler) updatePlace(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req placeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	var updated Place
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		before, err := scanPlace(tx.QueryRow(ctx, `SELECT `+placeColumns+` FROM places p WHERE p.id = $1 FOR UPDATE`, id))
		if db.IsNoRows(err) {
			return ErrPlaceNotFound
		}
		if err != nil {
			return httpx.Internal(err)
		}
		p := before
		fields, err := req.apply(ctx, tx, &p, false)
		if err != nil {
			return err
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
		row := tx.QueryRow(ctx, `
			UPDATE places AS p SET city_id = $2, name_kk = $3, name_ru = $4, name_en = $5, aliases = $6,
			       lat = $7, lon = $8, is_active = $9
			WHERE p.id = $1
			RETURNING `+placeColumns,
			id, p.CityID, p.Name.KK, p.Name.RU, p.Name.EN, p.Aliases, p.Point.Lat, p.Point.Lon, p.IsActive)
		if updated, err = scanPlace(row); err != nil {
			return httpx.Internal(err)
		}
		return logAudit(ctx, tx, r, "place.update", "place", id.String(), before, updated)
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, updated)
}

func (h *AdminHandler) deletePlace(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		before, err := scanPlace(tx.QueryRow(ctx, `DELETE FROM places p WHERE p.id = $1 RETURNING `+placeColumns, id))
		if db.IsNoRows(err) {
			return ErrPlaceNotFound
		}
		if err != nil {
			return httpx.Internal(err)
		}
		return logAudit(ctx, tx, r, "place.delete", "place", id.String(), before, nil)
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}
