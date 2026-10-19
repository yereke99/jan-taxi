package botapi

import (
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/phone"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/users"
)

type passengerContactResponse struct {
	Phone         string `json:"phone"`
	PhoneVerified bool   `json:"phone_verified"`
}

// passengerContact handles POST /internal/v1/passenger/{tg_id}/contact: the
// passenger shared their phone with the passenger bot (§22.5, Mini App
// requestContact). Telegram vouches for the number only when
// contact.user_id equals the sender, so anything else is refused (403
// contact_mismatch). The number is stored on the user (users.phone_e164,
// phone_verified_at) and shown to the driver of the passenger's orders.
func (h *Handler) passengerContact(w http.ResponseWriter, r *http.Request) error {
	tgID, err := tgIDParam(r)
	if err != nil {
		return err
	}
	var req contactRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.ContactUserID != tgID {
		return errContactMismatch()
	}
	if len(req.Phone) > 32 {
		return httpx.Validation(map[string]string{"phone": "invalid"})
	}
	e164, err := phone.Normalize(req.Phone)
	if err != nil {
		return httpx.Validation(map[string]string{"phone": "invalid"})
	}
	ctx := r.Context()
	u, err := users.GetByTelegramID(ctx, h.pool, tgID)
	if err != nil {
		return err
	}
	if u.IsBlocked {
		return errUserBlocked()
	}
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET phone_e164 = $2, phone_verified_at = now() WHERE id = $1`,
			u.ID, e164); err != nil {
			return fmt.Errorf("botapi: store passenger phone: %w", err)
		}
		return realtime.Publish(ctx, tx, realtime.Event{Type: realtime.UserPhoneVerified,
			Scopes: []string{realtime.UserScope(u.ID)}, Data: map[string]any{"user_id": u.ID}})
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, passengerContactResponse{Phone: e164, PhoneVerified: true})
}
