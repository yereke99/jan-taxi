package marketing

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// DriverOffer describes an active driver milestone; the server owns progress
// and payout, so the gift never invents a driver promotion in the client.
type DriverOffer struct {
	ID          uuid.UUID          `json:"id"`
	Title       settings.Localized `json:"title"`
	Description settings.Localized `json:"description"`
	Amount      int64              `json:"amount"`
	Rides       int                `json:"rides"`
	Completed   int                `json:"completed"`
	Categories  []string           `json:"categories"`
	EndsAt      *time.Time         `json:"ends_at"`
	Rewarded    bool               `json:"rewarded"`
}

func (s *Service) MountDriver(r chi.Router) {
	r.Get("/driver/promotions", httpx.H(s.driverPromotions))
	r.Post("/driver/promotions/presence", httpx.H(s.appPresence))
}

func (s *Service) driverPromotions(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	if d.Status != "approved" {
		return httpx.NewError(http.StatusForbidden, "driver_not_approved", "approved driver required")
	}
	offers := []DriverOffer{}
	ctx := r.Context()
	if !settings.Marketing(ctx, s.pool).Awards {
		return httpx.OK(w, map[string]any{"offers": offers})
	}
	live, err := liveCampaigns(ctx, s.pool, []string{TemplateDriverMilestone}, nil)
	if err != nil {
		return err
	}
	for _, c := range live {
		eligible, err := campaignForUser(ctx, s.pool, c, p.UserID, "driver")
		if err != nil {
			return err
		}
		if !eligible {
			continue
		}
		rules, ok := mustRules[MilestoneRules](c.Rules)
		if !ok || c.StartsAt == nil || c.EndsAt == nil {
			continue
		}
		o := DriverOffer{ID: c.ID, Title: c.Title, Description: c.Description, Amount: rules.Amount,
			Rides: rules.Rides, Categories: rules.Categories, EndsAt: c.EndsAt}
		if err := s.pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM orders WHERE driver_id = $1 AND status = 'completed'
			        AND category = ANY($2) AND completed_at >= $3 AND completed_at < $4
			        AND (COALESCE(cardinality($6::uuid[]),0)=0 OR jt_marketing_region(pickup_lat,pickup_lon)=ANY($6))),
			       EXISTS (SELECT 1 FROM marketing_grants WHERE campaign_id = $5 AND driver_id = $1 AND status = 'credited')`,
			d.ID, rules.Categories, *c.StartsAt, *c.EndsAt, c.ID, c.CityIDs).Scan(&o.Completed, &o.Rewarded); err != nil {
			return err
		}
		offers = append(offers, o)
	}
	return httpx.OK(w, map[string]any{"offers": offers})
}

func (s *Service) appPresence(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	var in struct {
		Active   *bool      `json:"active"`
		Position *geo.Point `json:"position"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Active == nil {
		return httpx.Validation(map[string]string{"active": "required"})
	}
	var cityID *uuid.UUID
	if in.Position != nil {
		if in.Position.Lat < -90 || in.Position.Lat > 90 || in.Position.Lon < -180 || in.Position.Lon > 180 {
			return httpx.Validation(map[string]string{"position": "invalid"})
		}
		err = s.pool.QueryRow(r.Context(), `SELECT jt_marketing_region($1,$2)`, in.Position.Lat, in.Position.Lon).Scan(&cityID)
		if err != nil && !db.IsNoRows(err) {
			return err
		}
	}
	_, err = s.pool.Exec(r.Context(), `
		INSERT INTO marketing_app_presence (user_id, app, active,city_id,city_seen_at)
		VALUES ($1, $2, $3,$4,CASE WHEN $5 THEN now() END)
		ON CONFLICT (user_id, app) DO UPDATE SET active = EXCLUDED.active, seen_at = now(),
		 city_id=CASE WHEN $5 THEN EXCLUDED.city_id ELSE marketing_app_presence.city_id END,
		 city_seen_at=CASE WHEN $5 THEN now() ELSE marketing_app_presence.city_seen_at END`, p.UserID, string(p.App), *in.Active, cityID, in.Position != nil)
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
