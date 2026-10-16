package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

type telegramAuthRequest struct {
	InitData string `json:"init_data"`
}

type telegramAuthResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	UserID    uuid.UUID `json:"user_id"`
}

// handleTelegramAuth exchanges Mini App initData for an app token.
func (s *Service) handleTelegramAuth(app App) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req telegramAuthRequest
		if err := httpx.DecodeJSON(w, r, &req); err != nil {
			return err
		}
		if req.InitData == "" {
			return httpx.Validation(map[string]string{"init_data": "required"})
		}
		now := s.now()
		data, err := s.ValidateInitData(req.InitData, app, now)
		if err != nil {
			httpx.Logger(r.Context()).Info("telegram auth rejected", slog.String("app", string(app)), slog.Any("code", httpx.AsError(err).Code))
			return err
		}
		userID, blocked, err := s.upsertTelegramUser(r.Context(), data.User, now)
		if err != nil {
			return httpx.Internal(err)
		}
		s.users.put(userID, userStatus{telegramID: data.User.ID, blocked: blocked, at: now})
		if blocked {
			return ErrUserBlocked
		}
		token, exp, err := s.IssueAppToken(userID, app, now)
		if err != nil {
			return err
		}
		return httpx.OK(w, telegramAuthResponse{Token: token, ExpiresAt: exp, UserID: userID})
	}
}

// upsertTelegramUser creates or refreshes the users row for a Telegram user and
// makes sure preferences exist. The interface language always starts as Kazakh;
// it is never derived from the Telegram client language.
func (s *Service) upsertTelegramUser(ctx context.Context, u TelegramUser, now time.Time) (uuid.UUID, bool, error) {
	var (
		id      uuid.UUID
		blocked bool
	)
	var username, lang *string
	if v := cleanName(u.Username, 64); v != "" {
		username = &v
	}
	if v := cleanName(u.LanguageCode, 16); v != "" {
		lang = &v
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (telegram_user_id, first_name, last_name, username, telegram_language_code, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (telegram_user_id) DO UPDATE SET
				first_name = EXCLUDED.first_name,
				last_name = EXCLUDED.last_name,
				username = EXCLUDED.username,
				telegram_language_code = EXCLUDED.telegram_language_code,
				last_seen_at = EXCLUDED.last_seen_at
			RETURNING id, is_blocked`,
			u.ID, cleanName(u.FirstName, 64), cleanName(u.LastName, 64), username, lang, now,
		).Scan(&id, &blocked); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO user_preferences (user_id, language) VALUES ($1, 'kk')
			ON CONFLICT (user_id) DO NOTHING`, id)
		return err
	})
	return id, blocked, err
}
