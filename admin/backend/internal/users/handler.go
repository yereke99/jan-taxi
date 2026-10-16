package users

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Handler serves the Mini App profile endpoints.
type Handler struct {
	pool *pgxpool.Pool
}

// NewHandler builds the profile handler.
func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

// MountApp registers GET /me and PATCH /me/preferences on a router that carries
// the app-token middleware (passenger or driver).
func (h *Handler) MountApp(r chi.Router) {
	r.Get("/me", httpx.H(h.me))
	r.Patch("/me/preferences", httpx.H(h.updatePreferences))
}

// Name is a name in the three supported languages.
type Name struct {
	KK string `json:"kk"`
	RU string `json:"ru"`
	EN string `json:"en"`
}

// CityRef identifies a city in profile payloads.
type CityRef struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name Name      `json:"name"`
}

// DriverSummary is the driver part of /me for the driver app.
type DriverSummary struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	Status   string    `json:"status"`
	IsOnline bool      `json:"is_online"`
	City     CityRef   `json:"city"`
}

// Me is the response of GET /me.
type Me struct {
	ID             uuid.UUID      `json:"id"`
	TelegramUserID int64          `json:"telegram_user_id"`
	FirstName      string         `json:"first_name"`
	LastName       string         `json:"last_name"`
	Username       *string        `json:"username"`
	Language       string         `json:"language"`
	PhoneMasked    *string        `json:"phone_masked"`
	PhoneVerified  bool           `json:"phone_verified"`
	App            auth.App       `json:"app"`
	Driver         *DriverSummary `json:"driver"`
}

type driverRow struct {
	summary         DriverSummary
	phone           string
	phoneVerifiedAt *time.Time
}

func loadDriver(ctx context.Context, q db.Querier, userID uuid.UUID) (*driverRow, error) {
	var d driverRow
	s := &d.summary
	err := q.QueryRow(ctx, `
		SELECT d.id, d.code, d.status, d.is_online, COALESCE(d.phone_e164, ''), d.phone_verified_at,
		       c.id, c.code, c.name_kk, c.name_ru, c.name_en
		FROM driver_profiles d
		JOIN cities c ON c.id = d.city_id
		WHERE d.user_id = $1`, userID).Scan(
		&s.ID, &s.Code, &s.Status, &s.IsOnline, &d.phone, &d.phoneVerifiedAt,
		&s.City.ID, &s.City.Code, &s.City.Name.KK, &s.City.Name.RU, &s.City.Name.EN)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load driver summary: %w", err)
	}
	return &d, nil
}

// LoadMe builds the /me payload for a principal.
func LoadMe(ctx context.Context, q db.Querier, p auth.Principal) (Me, error) {
	u, err := GetByID(ctx, q, p.UserID)
	if err != nil {
		return Me{}, err
	}
	me := Me{
		ID: u.ID, TelegramUserID: u.TelegramUserID, FirstName: u.FirstName, LastName: u.LastName,
		Language: u.Language, App: p.App,
	}
	if u.Username != "" {
		me.Username = &u.Username
	}
	phone, verified := u.PhoneE164, u.PhoneVerifiedAt != nil
	if p.App == auth.AppDriver {
		d, err := loadDriver(ctx, q, u.ID)
		if err != nil {
			return Me{}, err
		}
		if d != nil {
			me.Driver = &d.summary
			// The driver bot's contact flow may record the number on the profile only.
			if phone == "" && d.phone != "" {
				phone, verified = d.phone, d.phoneVerifiedAt != nil
			}
		}
	}
	if phone != "" {
		m := MaskPhone(phone)
		me.PhoneMasked = &m
		me.PhoneVerified = verified
	}
	return me, nil
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	me, err := LoadMe(r.Context(), h.pool, p)
	if errors.Is(err, ErrNotFound) {
		// A valid token for a user that no longer exists: make the app sign in again.
		return httpx.Unauthorized()
	}
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.OK(w, me)
}

type preferencesRequest struct {
	Language *string `json:"language"`
}

func (h *Handler) updatePreferences(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	var req preferencesRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.Language == nil {
		return httpx.Validation(map[string]string{"language": "required"})
	}
	if err := SetLanguage(r.Context(), h.pool, p.UserID, *req.Language); err != nil {
		if errors.Is(err, ErrNotFound) {
			return httpx.Unauthorized()
		}
		return err
	}
	return h.me(w, r)
}
