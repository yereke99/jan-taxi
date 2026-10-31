// Package moderation runs the driver application workflow: submission by the
// driver, which approves a registration that passes the automatic checks at
// once (owner decision 2026-10-31, no manual review), and the administrators'
// decisions (suspend, restore, change city, and approve, request corrections
// or reject for applications still pending from before). Every transition is
// a conditional state change in one transaction together with its moderation
// event, audit row, Telegram notification and realtime hint. No history is
// ever deleted.
package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
)

// Handler serves the submission and admin moderation endpoints.
type Handler struct {
	pool *pgxpool.Pool
}

// NewHandler builds the handler.
func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

// MountDriver mounts POST /driver/register on the driver app router.
func (h *Handler) MountDriver(r chi.Router) {
	r.Post("/driver/register", httpx.H(h.submit))
}

// MountAdmin mounts the admin driver routes (router under /api/v1/admin).
// Routes are registered flat so other packages can add /drivers/{id}/… routes.
func (h *Handler) MountAdmin(r chi.Router) {
	view := r.With(auth.Permit(auth.PermDriversView))
	view.Get("/drivers", httpx.H(h.list))
	view.Get("/drivers/{id}", httpx.H(h.detail))

	moderate := r.With(auth.Permit(auth.PermDriversModerate))
	moderate.Post("/drivers/{id}/approve", httpx.H(h.approveByAdmin))
	moderate.Post("/drivers/{id}/request-corrections", httpx.H(h.requestCorrections))
	moderate.Post("/drivers/{id}/reject", httpx.H(h.reject))

	manage := r.With(auth.Permit(auth.PermDriversManage))
	manage.Post("/drivers/{id}/suspend", httpx.H(h.suspend))
	manage.Post("/drivers/{id}/restore", httpx.H(h.restore))
	manage.Patch("/drivers/{id}", httpx.H(h.updateCity))
}

// Event is a moderation event to record.
type Event struct {
	DriverID      uuid.UUID
	ApplicationID *uuid.UUID
	ActorType     string // driver | admin | system
	AdminID       *uuid.UUID
	Action        string
	FromStatus    string
	ToStatus      string
	Reasons       []drivers.CorrectionItem
	Comment       *string
}

// InsertEvent appends a moderation event and returns its id.
func InsertEvent(ctx context.Context, q db.Querier, e Event) (int64, error) {
	reasons := e.Reasons
	if reasons == nil {
		reasons = []drivers.CorrectionItem{}
	}
	raw, err := json.Marshal(reasons)
	if err != nil {
		return 0, err
	}
	var id int64
	err = q.QueryRow(ctx, `
		INSERT INTO driver_moderation_events (driver_id, application_id, actor_type, admin_id, action, from_status, to_status, reasons, comment)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, $9)
		RETURNING id`, e.DriverID, e.ApplicationID, e.ActorType, e.AdminID, e.Action, e.FromStatus, e.ToStatus, raw, e.Comment).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("moderation: insert event: %w", err)
	}
	return id, nil
}

// publish sends a moderation hint to the driver (driver and user scopes) and
// the admin panel.
func publish(ctx context.Context, q db.Querier, typ string, p drivers.Profile, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	data["driver_id"] = p.ID
	return realtime.Publish(ctx, q, realtime.Event{
		Type:   typ,
		Scopes: []string{realtime.DriverScope(p.ID), realtime.UserScope(p.UserID), realtime.AdminScope},
		Data:   data,
	})
}

func invalidTransition(from, action string) error {
	return httpx.Conflict("invalid_transition", "transition not allowed").
		WithDetails(map[string]any{"from": from, "action": action})
}

// decodeOptionalJSON decodes a JSON body that may be absent or empty.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpx.MaxJSONBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
		}
		return httpx.BadRequest("could not read the request body").WithCause(err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return httpx.DecodeJSON(w, r, dst)
}
