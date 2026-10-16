package botapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/phone"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/users"
)

type contactRequest struct {
	Phone         string `json:"phone"`
	ContactUserID int64  `json:"contact_user_id"`
}

type contactResponse struct {
	Phone         string     `json:"phone"`
	PhoneVerified bool       `json:"phone_verified"`
	DriverID      *uuid.UUID `json:"driver_id"`
}

// errContactMismatch is returned for a contact that is not the sender's own
// (403 forbidden, details.reason contact_mismatch).
func errContactMismatch() *httpx.Error {
	return httpx.Forbidden().WithDetails(map[string]any{"reason": "contact_mismatch"})
}

// driverContact stores the phone number of a contact the user shared with the
// driver bot. Telegram guarantees that the number belongs to the account only
// when contact.user_id equals the sender, so anything else is refused before
// anything is stored.
func (h *Handler) driverContact(w http.ResponseWriter, r *http.Request) error {
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
	res := contactResponse{Phone: e164, PhoneVerified: true}
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		if err := drivers.ApplyVerifiedPhone(ctx, tx, u.ID, e164); err != nil {
			return err
		}
		scopes := []string{realtime.UserScope(u.ID)}
		data := map[string]any{"user_id": u.ID}
		id, err := drivers.IDForUser(ctx, tx, u.ID)
		switch {
		case err == nil:
			res.DriverID = &id
			scopes = append(scopes, realtime.DriverScope(id))
			data["driver_id"] = id
		case !isNotFound(err):
			return err
		}
		if err := notifications.Enqueue(ctx, tx, notifications.Notification{
			Bot: notifications.DriverBot, UserID: u.ID, Template: notifications.TplPhoneVerified,
			Params:    map[string]any{"phone": phone.Format(e164)},
			DedupeKey: notifications.TplPhoneVerified + ":" + u.ID.String() + ":" + e164,
		}); err != nil {
			return err
		}
		return realtime.Publish(ctx, tx, realtime.Event{Type: realtime.DriverPhoneVerified, Scopes: scopes, Data: data})
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}
