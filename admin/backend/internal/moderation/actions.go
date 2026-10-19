package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/users"
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicles"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// Text limits of the moderation requests (code points).
const (
	maxDecisionComment   = 500
	maxCorrectionComment = 200
	maxCorrectionItems   = 10
	maxRejectReason      = 500
	maxSuspendReason     = 300
)

// actionFunc performs one admin action inside the transaction on the locked
// driver profile.
type actionFunc func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal, p drivers.Profile) error

// run executes an admin action and responds with the updated driver detail.
func (h *Handler) run(w http.ResponseWriter, r *http.Request, fn actionFunc) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		p, err := drivers.LockProfile(ctx, tx, id)
		if err != nil {
			return err
		}
		return fn(ctx, tx, admin, p)
	})
	if err != nil {
		return err
	}
	d, err := LoadDetail(ctx, h.pool, id, admin)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

// openApplication locks the driver's newest application if its status is one of statuses.
func openApplication(ctx context.Context, tx pgx.Tx, driverID uuid.UUID, statuses ...string) (uuid.UUID, bool, error) {
	var (
		id     uuid.UUID
		status string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, status FROM driver_applications WHERE driver_id = $1 ORDER BY round DESC LIMIT 1 FOR UPDATE`, driverID).
		Scan(&id, &status)
	if db.IsNoRows(err) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("moderation: load application: %w", err)
	}
	for _, s := range statuses {
		if s == status {
			return id, true, nil
		}
	}
	return uuid.Nil, false, nil
}

func reviewApplication(ctx context.Context, tx pgx.Tx, appID uuid.UUID, status string, admin uuid.UUID, comment *string, corrections []drivers.CorrectionItem) error {
	raw := []byte("[]")
	if corrections != nil {
		var err error
		if raw, err = json.Marshal(corrections); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `
		UPDATE driver_applications
		SET status = $2, reviewed_at = now(), reviewed_by = $3, decision_comment = $4,
		    corrections = CASE WHEN $6 THEN $5::jsonb ELSE corrections END
		WHERE id = $1`, appID, status, admin, comment, raw, corrections != nil)
	if err != nil {
		return fmt.Errorf("moderation: review application: %w", err)
	}
	return nil
}

func logAudit(ctx context.Context, tx pgx.Tx, r *http.Request, action string, p drivers.Profile, after map[string]any) error {
	return audit.Log(ctx, tx, r, audit.Entry{
		Action: action, EntityType: "driver", EntityID: p.ID.String(),
		Before: map[string]any{"status": p.Status, "code": p.Code}, After: after,
	})
}

func notify(ctx context.Context, tx pgx.Tx, p drivers.Profile, template string, params map[string]any, dedupe string) error {
	return notifications.Enqueue(ctx, tx, notifications.Notification{
		Bot: notifications.DriverBot, UserID: p.UserID, Template: template, Params: params, DedupeKey: dedupe,
	})
}

func cleanOptional(s *string, max int, field string, fields map[string]string) *string {
	if s == nil {
		return nil
	}
	v, err := textutil.CleanText(*s, max)
	if err != nil {
		fields[field] = textutil.Reason(err)
		return nil
	}
	if v == "" {
		return nil
	}
	return &v
}

func cleanRequired(s *string, max int, field string, fields map[string]string) string {
	if s == nil {
		fields[field] = "required"
		return ""
	}
	v, err := textutil.CleanRequired(*s, max)
	if err != nil {
		fields[field] = textutil.Reason(err)
	}
	return v
}

// --- approve -------------------------------------------------------------------

type approveRequest struct {
	Comment *string `json:"comment"`
}

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) error {
	var req approveRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	comment := cleanOptional(req.Comment, maxDecisionComment, "comment", fields)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return h.run(w, r, func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal, p drivers.Profile) error {
		if p.Status != drivers.StatusPending {
			return invalidTransition(p.Status, "approve")
		}
		appID, ok, err := openApplication(ctx, tx, p.ID, drivers.StatusPending)
		if err != nil {
			return err
		}
		if !ok {
			return invalidTransition(p.Status, "approve")
		}
		// The plate and phone claims were taken on submission; re-assert the
		// plate claim in case the vehicle row was changed since.
		v, err := vehicles.CurrentForUpdate(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if v.Status != vehicles.StatusClaimed {
			if err := vehicles.SetStatus(ctx, tx, v.ID, vehicles.StatusClaimed); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE driver_profiles SET status = 'approved', approved_at = now() WHERE id = $1`, p.ID); err != nil {
			if db.IsUniqueViolation(err, drivers.PhoneUniqueIndex) {
				return drivers.ErrPhoneTaken
			}
			return fmt.Errorf("moderation: approve: %w", err)
		}
		if err := reviewApplication(ctx, tx, appID, "approved", admin.AdminID, comment, nil); err != nil {
			return err
		}
		if _, err := InsertEvent(ctx, tx, Event{
			DriverID: p.ID, ApplicationID: &appID, ActorType: "admin", AdminID: &admin.AdminID,
			Action: drivers.ActionApproved, FromStatus: p.Status, ToStatus: drivers.StatusApproved, Comment: comment,
		}); err != nil {
			return err
		}
		if _, err := wallets.Ensure(ctx, tx, p.ID); err != nil {
			return wallets.HTTPError(err)
		}
		if err := logAudit(ctx, tx, r, "driver.approve", p, map[string]any{
			"status": drivers.StatusApproved, "application_id": appID, "comment": comment,
		}); err != nil {
			return err
		}
		if err := notify(ctx, tx, p, notifications.TplModerationApproved, map[string]any{"driver_code": p.Code},
			"moderation_approved:"+appID.String()); err != nil {
			return err
		}
		if _, err := grantWelcomeBonus(ctx, tx, r, admin, p, v.PlateNormalized); err != nil {
			return err
		}
		return publish(ctx, tx, realtime.ModerationApproved, p, map[string]any{"status": drivers.StatusApproved})
	})
}

// --- request corrections -------------------------------------------------------------

type correctionItemRequest struct {
	Field   *string `json:"field"`
	Reason  *string `json:"reason"`
	Comment *string `json:"comment"`
}

type correctionsRequest struct {
	Items   []correctionItemRequest `json:"items"`
	Comment *string                 `json:"comment"`
}

func (req correctionsRequest) validate() ([]drivers.CorrectionItem, *string, error) {
	fields := map[string]string{}
	switch {
	case req.Items == nil:
		fields["items"] = "required"
	case len(req.Items) == 0:
		fields["items"] = "too_few"
	case len(req.Items) > maxCorrectionItems:
		fields["items"] = "too_many"
	}
	items := make([]drivers.CorrectionItem, 0, len(req.Items))
	seen := map[string]bool{}
	for i, it := range req.Items {
		prefix := "items." + strconv.Itoa(i) + "."
		var item drivers.CorrectionItem
		switch {
		case it.Field == nil || *it.Field == "":
			fields[prefix+"field"] = "required"
		case !drivers.ValidRegistrationField(*it.Field):
			fields[prefix+"field"] = "invalid"
		default:
			item.Field = *it.Field
		}
		switch {
		case it.Reason == nil || *it.Reason == "":
			fields[prefix+"reason"] = "required"
		case !drivers.ValidCorrectionReason(*it.Reason):
			fields[prefix+"reason"] = "invalid"
		default:
			item.Reason = *it.Reason
		}
		item.Comment = cleanOptional(it.Comment, maxCorrectionComment, prefix+"comment", fields)
		if item.Reason == "other" && item.Comment == nil && fields[prefix+"comment"] == "" {
			fields[prefix+"comment"] = "required"
		}
		key := item.Field + "/" + item.Reason
		if item.Field != "" && item.Reason != "" {
			if seen[key] {
				fields[prefix+"field"] = "duplicate"
			}
			seen[key] = true
		}
		items = append(items, item)
	}
	comment := cleanOptional(req.Comment, maxDecisionComment, "comment", fields)
	if len(fields) > 0 {
		return nil, nil, httpx.Validation(fields)
	}
	return items, comment, nil
}

func (h *Handler) requestCorrections(w http.ResponseWriter, r *http.Request) error {
	var req correctionsRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	items, comment, err := req.validate()
	if err != nil {
		return err
	}
	return h.run(w, r, func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal, p drivers.Profile) error {
		if p.Status != drivers.StatusPending {
			return invalidTransition(p.Status, "request_corrections")
		}
		appID, ok, err := openApplication(ctx, tx, p.ID, drivers.StatusPending)
		if err != nil {
			return err
		}
		if !ok {
			return invalidTransition(p.Status, "request_corrections")
		}
		// The vehicle stays claimed and the phone held while the driver fixes the data.
		if _, err := tx.Exec(ctx, `UPDATE driver_profiles SET status = 'changes_requested' WHERE id = $1`, p.ID); err != nil {
			return fmt.Errorf("moderation: request corrections: %w", err)
		}
		if err := reviewApplication(ctx, tx, appID, drivers.StatusChangesRequested, admin.AdminID, comment, items); err != nil {
			return err
		}
		if _, err := InsertEvent(ctx, tx, Event{
			DriverID: p.ID, ApplicationID: &appID, ActorType: "admin", AdminID: &admin.AdminID,
			Action: drivers.ActionChangesRequested, FromStatus: p.Status, ToStatus: drivers.StatusChangesRequested,
			Reasons: items, Comment: comment,
		}); err != nil {
			return err
		}
		fields := drivers.CorrectionFields(items)
		if err := logAudit(ctx, tx, r, "driver.request_corrections", p, map[string]any{
			"status": drivers.StatusChangesRequested, "application_id": appID, "items": items, "comment": comment,
		}); err != nil {
			return err
		}
		lang, err := users.Language(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		text := drivers.FieldLabels(fields, lang)
		if comment != nil {
			text += ". " + *comment
		}
		if err := notify(ctx, tx, p, notifications.TplModerationChangesRequested, map[string]any{"comment": text},
			"moderation_changes_requested:"+appID.String()); err != nil {
			return err
		}
		return publish(ctx, tx, realtime.ModerationChangesRequired, p, map[string]any{
			"status": drivers.StatusChangesRequested, "fields": fields,
		})
	})
}

// --- reject ---------------------------------------------------------------------

type rejectRequest struct {
	Reason  *string `json:"reason"`
	Comment *string `json:"comment"`
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) error {
	var req rejectRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	reason := cleanRequired(req.Reason, maxRejectReason, "reason", fields)
	internal := cleanOptional(req.Comment, maxDecisionComment, "comment", fields)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return h.run(w, r, func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal, p drivers.Profile) error {
		if p.Status != drivers.StatusPending && p.Status != drivers.StatusChangesRequested {
			return invalidTransition(p.Status, "reject")
		}
		var appRef *uuid.UUID
		if appID, ok, err := openApplication(ctx, tx, p.ID, drivers.StatusPending); err != nil {
			return err
		} else if ok {
			if err := reviewApplication(ctx, tx, appID, drivers.StatusRejected, admin.AdminID, &reason, nil); err != nil {
				return err
			}
			appRef = &appID
		} else if appID, ok, err := openApplication(ctx, tx, p.ID, drivers.StatusChangesRequested); err != nil {
			return err
		} else if ok {
			appRef = &appID // the earlier decision stays on record; the event carries the rejection
		}
		if _, err := tx.Exec(ctx, `
			UPDATE driver_profiles SET status = 'rejected', is_online = false, online_since = NULL WHERE id = $1`, p.ID); err != nil {
			return fmt.Errorf("moderation: reject: %w", err)
		}
		// Free the plate (and, through the status, the phone) for other drivers.
		v, err := vehicles.CurrentForUpdate(ctx, tx, p.ID)
		switch {
		case err == nil:
			if err := vehicles.SetStatus(ctx, tx, v.ID, vehicles.StatusReleased); err != nil {
				return err
			}
		case !errors.Is(err, vehicles.ErrNotFound):
			return err
		}
		eventID, err := InsertEvent(ctx, tx, Event{
			DriverID: p.ID, ApplicationID: appRef, ActorType: "admin", AdminID: &admin.AdminID,
			Action: drivers.ActionRejected, FromStatus: p.Status, ToStatus: drivers.StatusRejected, Comment: &reason,
		})
		if err != nil {
			return err
		}
		if err := logAudit(ctx, tx, r, "driver.reject", p, map[string]any{
			"status": drivers.StatusRejected, "reason": reason, "internal_comment": internal,
		}); err != nil {
			return err
		}
		if err := notify(ctx, tx, p, notifications.TplModerationRejected, map[string]any{"reason": reason},
			fmt.Sprintf("moderation_rejected:%s:%d", p.ID, eventID)); err != nil {
			return err
		}
		return publish(ctx, tx, realtime.ModerationRejected, p, map[string]any{"status": drivers.StatusRejected})
	})
}

// --- suspend / restore ----------------------------------------------------------

type suspendRequest struct {
	Reason *string `json:"reason"`
}

func activeOrderConflict(ctx context.Context, tx pgx.Tx, driverID uuid.UUID) error {
	order, active, err := drivers.FindActiveOrder(ctx, tx, driverID)
	if err != nil {
		return err
	}
	if active {
		return httpx.Conflict("conflict", "the driver has an active order").
			WithDetails(map[string]any{"reason": "active_order_exists", "active_order_id": order.ID})
	}
	return nil
}

func (h *Handler) suspend(w http.ResponseWriter, r *http.Request) error {
	var req suspendRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	reason := cleanRequired(req.Reason, maxSuspendReason, "reason", fields)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return h.run(w, r, func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal, p drivers.Profile) error {
		if p.Status != drivers.StatusApproved {
			return invalidTransition(p.Status, "suspend")
		}
		if err := activeOrderConflict(ctx, tx, p.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE driver_profiles
			SET status = 'suspended', suspended_at = now(), suspended_reason = $2, is_online = false, online_since = NULL
			WHERE id = $1`, p.ID, reason); err != nil {
			return fmt.Errorf("moderation: suspend: %w", err)
		}
		eventID, err := InsertEvent(ctx, tx, Event{
			DriverID: p.ID, ActorType: "admin", AdminID: &admin.AdminID, Action: drivers.ActionSuspended,
			FromStatus: p.Status, ToStatus: drivers.StatusSuspended, Comment: &reason,
		})
		if err != nil {
			return err
		}
		if err := logAudit(ctx, tx, r, "driver.suspend", p, map[string]any{"status": drivers.StatusSuspended, "reason": reason}); err != nil {
			return err
		}
		if err := notify(ctx, tx, p, notifications.TplDriverSuspended, map[string]any{"reason": reason},
			fmt.Sprintf("driver_suspended:%s:%d", p.ID, eventID)); err != nil {
			return err
		}
		return drivers.PublishStatus(ctx, tx, p.ID, p.UserID, drivers.StatusSuspended, false)
	})
}

func (h *Handler) restore(w http.ResponseWriter, r *http.Request) error {
	if err := decodeOptionalJSON(w, r, &struct{}{}); err != nil {
		return err
	}
	return h.run(w, r, func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal, p drivers.Profile) error {
		if p.Status != drivers.StatusSuspended {
			return invalidTransition(p.Status, "restore")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE driver_profiles SET status = 'approved', suspended_at = NULL, suspended_reason = NULL WHERE id = $1`, p.ID); err != nil {
			return fmt.Errorf("moderation: restore: %w", err)
		}
		eventID, err := InsertEvent(ctx, tx, Event{
			DriverID: p.ID, ActorType: "admin", AdminID: &admin.AdminID, Action: drivers.ActionRestored,
			FromStatus: p.Status, ToStatus: drivers.StatusApproved,
		})
		if err != nil {
			return err
		}
		if err := logAudit(ctx, tx, r, "driver.restore", p, map[string]any{"status": drivers.StatusApproved}); err != nil {
			return err
		}
		if err := notify(ctx, tx, p, notifications.TplDriverRestored, nil,
			fmt.Sprintf("driver_restored:%s:%d", p.ID, eventID)); err != nil {
			return err
		}
		return drivers.PublishStatus(ctx, tx, p.ID, p.UserID, drivers.StatusApproved, false)
	})
}

// --- city -----------------------------------------------------------------------

type cityRequest struct {
	CityID *string `json:"city_id"`
}

func (h *Handler) updateCity(w http.ResponseWriter, r *http.Request) error {
	var req cityRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.CityID == nil {
		return httpx.Validation(map[string]string{"city_id": "required"})
	}
	cityID, err := uuid.Parse(*req.CityID)
	if err != nil {
		return httpx.Validation(map[string]string{"city_id": "invalid"})
	}
	return h.run(w, r, func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal, p drivers.Profile) error {
		city, err := cities.Get(ctx, tx, cityID)
		if errors.Is(err, cities.ErrNotFound) {
			return httpx.Validation(map[string]string{"city_id": "invalid"})
		}
		if err != nil {
			return err
		}
		if !city.IsActive {
			return httpx.Validation(map[string]string{"city_id": "not_allowed"})
		}
		if city.ID == p.CityID {
			return nil
		}
		if err := activeOrderConflict(ctx, tx, p.ID); err != nil {
			return err
		}
		old, err := cities.Get(ctx, tx, p.CityID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE driver_profiles SET city_id = $2 WHERE id = $1`, p.ID, city.ID); err != nil {
			return fmt.Errorf("moderation: change city: %w", err)
		}
		note := old.Code + " → " + city.Code
		if _, err := InsertEvent(ctx, tx, Event{
			DriverID: p.ID, ActorType: "admin", AdminID: &admin.AdminID, Action: drivers.ActionCityChanged,
			FromStatus: p.Status, ToStatus: p.Status, Comment: &note,
		}); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, r, audit.Entry{
			Action: "driver.city_change", EntityType: "driver", EntityID: p.ID.String(),
			Before: map[string]any{"city_id": p.CityID, "city_code": old.Code},
			After:  map[string]any{"city_id": city.ID, "city_code": city.Code},
		}); err != nil {
			return err
		}
		return realtime.Publish(ctx, tx, realtime.Event{
			Type:   realtime.DriverStatusChanged,
			Scopes: []string{realtime.DriverScope(p.ID), realtime.UserScope(p.UserID), realtime.AdminScope},
			Data:   map[string]any{"driver_id": p.ID, "status": p.Status, "is_online": p.IsOnline, "city_id": city.ID},
		})
	})
}
