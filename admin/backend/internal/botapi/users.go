package botapi

import (
	"context"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/users"
)

type syncRequest struct {
	TelegramUserID int64  `json:"telegram_user_id"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Username       string `json:"username"`
	LanguageCode   string `json:"language_code"`
	Started        bool   `json:"started"`
	Blocked        *bool  `json:"blocked"`
}

func (s syncRequest) validate() error {
	fields := map[string]string{}
	if s.TelegramUserID < 1 {
		fields["telegram_user_id"] = "invalid"
	}
	for name, v := range map[string]struct {
		s   string
		max int
	}{"first_name": {s.FirstName, 128}, "last_name": {s.LastName, 128}, "username": {s.Username, 64}, "language_code": {s.LanguageCode, 16}} {
		if utf8.RuneCountInString(v.s) > v.max {
			fields[name] = "too_long"
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// syncDriver is the driver state returned to the driver bot with every sync,
// so it can pick the right keyboard without a second call.
type syncDriver struct {
	Registered    bool       `json:"registered"`
	DriverID      *uuid.UUID `json:"driver_id"`
	Code          *string    `json:"code"`
	Status        *string    `json:"status"`
	IsOnline      bool       `json:"is_online"`
	PhoneVerified bool       `json:"phone_verified"`
}

type syncResponse struct {
	UserID         uuid.UUID   `json:"user_id"`
	Language       string      `json:"language"`
	LanguageChosen bool        `json:"language_chosen"`
	IsNew          bool        `json:"is_new"`
	IsBlocked      bool        `json:"is_blocked"`
	Driver         *syncDriver `json:"driver,omitempty"`
}

// syncUser upserts the Telegram user the bot just saw and records /start or
// a block/unblock of this bot.
func (h *Handler) syncUser(w http.ResponseWriter, r *http.Request) error {
	var req syncRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := req.validate(); err != nil {
		return err
	}
	bot := chi.URLParam(r, "bot")
	var res syncResponse
	err := db.WithTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		u, isNew, err := users.SyncTelegramUser(r.Context(), tx, users.TelegramUser{
			ID: req.TelegramUserID, FirstName: req.FirstName, LastName: req.LastName,
			Username: req.Username, LanguageCode: req.LanguageCode,
		}, bot, req.Started)
		if err != nil {
			return err
		}
		if req.Blocked != nil && (*req.Blocked || !req.Started) {
			if err := users.MarkBotBlocked(r.Context(), tx, req.TelegramUserID, bot, *req.Blocked); err != nil {
				return err
			}
		}
		res = syncResponse{UserID: u.ID, Language: u.Language, IsNew: isNew, IsBlocked: u.IsBlocked}
		if res.LanguageChosen, err = languageChosen(r.Context(), tx, u.ID); err != nil {
			return err
		}
		if bot == BotDriver {
			d, err := loadSyncDriver(r.Context(), tx, u.ID)
			if err != nil {
				return err
			}
			res.Driver = &d
		}
		return nil
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}

func languageChosen(ctx context.Context, q db.Querier, userID uuid.UUID) (bool, error) {
	var chosen bool
	err := q.QueryRow(ctx, `SELECT language_chosen_at IS NOT NULL FROM user_preferences WHERE user_id = $1`, userID).Scan(&chosen)
	if db.IsNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load language choice: %w", err)
	}
	return chosen, nil
}

func loadSyncDriver(ctx context.Context, q db.Querier, userID uuid.UUID) (syncDriver, error) {
	var d syncDriver
	var id uuid.UUID
	var code, status string
	err := q.QueryRow(ctx, `
		SELECT dp.id, dp.code, dp.status, dp.is_online,
		       (u.phone_verified_at IS NOT NULL OR dp.phone_verified_at IS NOT NULL)
		FROM users u JOIN driver_profiles dp ON dp.user_id = u.id
		WHERE u.id = $1`, userID).Scan(&id, &code, &status, &d.IsOnline, &d.PhoneVerified)
	if db.IsNoRows(err) {
		err = q.QueryRow(ctx, `SELECT phone_verified_at IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&d.PhoneVerified)
		if err != nil {
			return d, fmt.Errorf("load phone state: %w", err)
		}
		return d, nil
	}
	if err != nil {
		return d, fmt.Errorf("load driver state: %w", err)
	}
	d.Registered, d.DriverID, d.Code, d.Status = true, &id, &code, &status
	return d, nil
}

type languageBody struct {
	Language string `json:"language"`
}

// setLanguage stores the user's language (same preference as PATCH /me/preferences).
func (h *Handler) setLanguage(w http.ResponseWriter, r *http.Request) error {
	tgID, err := tgIDParam(r)
	if err != nil {
		return err
	}
	var req languageBody
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if !users.ValidLanguage(req.Language) {
		return users.ErrInvalidLanguage
	}
	u, err := users.GetByTelegramID(r.Context(), h.pool, tgID)
	if err != nil {
		return err
	}
	if err := users.SetLanguage(r.Context(), h.pool, u.ID, req.Language); err != nil {
		return err
	}
	return httpx.OK(w, languageBody{Language: req.Language})
}
