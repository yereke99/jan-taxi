package marketing

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// MountInternal mounts the bot routes (internal listener, service secret):
// POST /passenger/{tg_id}/referral — a /start ref_<code> the passenger bot
// received; POST /bots/{bot}/users/{tg_id}/marketing — the unsubscribe button
// of a broadcast. The Telegram user id comes from the update the bot got.
func (s *Service) MountInternal(r chi.Router) {
	r.Post("/passenger/{tg_id}/referral", httpx.H(s.internalReferral))
	r.Post("/bots/{bot}/users/{tg_id}/marketing", httpx.H(s.internalMarketing))
}

func (s *Service) userByTelegram(r *http.Request) (uuid.UUID, error) {
	tg, err := strconv.ParseInt(chi.URLParam(r, "tg_id"), 10, 64)
	if err != nil || tg <= 0 {
		return uuid.Nil, httpx.Validation(map[string]string{"tg_id": "invalid"})
	}
	var id uuid.UUID
	err = s.pool.QueryRow(r.Context(), `SELECT id FROM users WHERE telegram_user_id = $1`, tg).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, httpx.NotFound()
	}
	return id, err
}

func (s *Service) internalReferral(w http.ResponseWriter, r *http.Request) error {
	if auth.BotFrom(r.Context()) != "passenger" {
		return httpx.Forbidden()
	}
	var req struct {
		Payload string `json:"payload"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	userID, err := s.userByTelegram(r)
	if err != nil {
		return err
	}
	res, err := s.CaptureReferral(r.Context(), userID, req.Payload, "bot_start")
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}

func (s *Service) internalMarketing(w http.ResponseWriter, r *http.Request) error {
	if bot := chi.URLParam(r, "bot"); bot != auth.BotFrom(r.Context()) {
		return httpx.Forbidden()
	}
	var req struct {
		OptOut *bool `json:"opt_out"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.OptOut == nil {
		return httpx.Validation(map[string]string{"opt_out": "required"})
	}
	userID, err := s.userByTelegram(r)
	if err != nil {
		return err
	}
	if err := SetMarketingOptOut(r.Context(), s.pool, userID, *req.OptOut); err != nil {
		return err
	}
	return httpx.OK(w, map[string]bool{"marketing_subscribed": !*req.OptOut})
}
