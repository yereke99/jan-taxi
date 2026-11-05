package marketing

import (
	"context"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

func campaignInCity(c Campaign, cityID uuid.UUID) bool {
	return len(c.CityIDs) == 0 || slices.Contains(c.CityIDs, cityID)
}

func (s *Service) adminCampaignRegions(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.pool.Query(r.Context(), `SELECT id,name_kk,name_ru,name_en FROM cities WHERE is_active AND code<>'KAZ' ORDER BY name_ru`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type region struct {
		ID   uuid.UUID          `json:"id"`
		Name settings.Localized `json:"name"`
	}
	items := []region{}
	for rows.Next() {
		var x region
		if err := rows.Scan(&x.ID, &x.Name.KK, &x.Name.RU, &x.Name.EN); err != nil {
			return err
		}
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"items": items})
}

func validateCampaignCities(ctx context.Context, q db.Querier, ids []uuid.UUID, fields map[string]string) error {
	if len(ids) > 50 {
		fields["city_ids"] = "too_many"
		return nil
	}
	if len(ids) == 0 {
		return nil
	}
	var count int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM cities WHERE id=ANY($1::uuid[]) AND code<>'KAZ'`, ids).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		fields["city_ids"] = "invalid"
	}
	return nil
}

// Unknown location does not qualify for a restricted offer. Awards are also
// checked against the actual completed order's server-resolved city.
func campaignForUser(ctx context.Context, q db.Querier, c Campaign, userID uuid.UUID, app string) (bool, error) {
	if len(c.CityIDs) == 0 {
		return true, nil
	}
	var cityID *uuid.UUID
	if err := q.QueryRow(ctx, `SELECT jt_marketing_city($1,$2)`, userID, app).Scan(&cityID); err != nil {
		return false, err
	}
	return cityID != nil && campaignInCity(c, *cityID), nil
}
