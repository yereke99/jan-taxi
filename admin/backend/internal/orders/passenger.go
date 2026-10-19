package orders

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// listOrders handles GET /orders?scope=active|history.
func (s *Service) listOrders(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	statuses := lifecycle.HistoryStatuses
	switch strings.TrimSpace(r.URL.Query().Get("scope")) {
	case "", "history":
	case "active":
		statuses = lifecycle.ActiveStatuses
	default:
		return httpx.Validation(map[string]string{"scope": "invalid"})
	}
	limit, c, err := listParams(r)
	if err != nil {
		return err
	}
	list, err := listPassengerOrders(r.Context(), s.pool, p.UserID, statuses, limit, c)
	if err != nil {
		return err
	}
	return httpx.OK(w, list)
}

// getOrder handles GET /orders/{id}.
func (s *Service) getOrder(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	o, err := passengerOrder(r.Context(), s.pool, id, p.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

type passengerCancelRequest struct {
	ReasonCode *string `json:"reason_code"`
	Comment    *string `json:"comment"`
	// ExpectedStatus is the status the passenger saw when confirming. When
	// the order has moved on since (typically: a driver accepted a searching
	// order), the cancellation is refused with 409 conflict
	// {reason: order_changed, status} so the app shows the driver first.
	ExpectedStatus *string `json:"expected_status"`
}

// parse validates the cancellation reason (§22.3): reason_code is one of
// lifecycle.CancelReasonCodes; comment is optional free text (≤ 120 code
// points) and required for "other"; expected_status, when sent, is an order
// status.
func (req passengerCancelRequest) parse() (code string, comment *string, err error) {
	fields := map[string]string{}
	if req.ExpectedStatus != nil && !lifecycle.ValidStatus(*req.ExpectedStatus) {
		fields["expected_status"] = "invalid"
	}
	switch {
	case req.ReasonCode == nil || strings.TrimSpace(*req.ReasonCode) == "":
		fields["reason_code"] = "required"
	case !lifecycle.ValidCancelReason(*req.ReasonCode):
		fields["reason_code"] = "invalid"
	default:
		code = *req.ReasonCode
	}
	comment = cleanField(fields, "comment", req.Comment, textutil.MaxFreeText)
	if code == lifecycle.CancelOther && comment == nil && fields["comment"] == "" {
		fields["comment"] = "required"
	}
	if len(fields) > 0 {
		return "", nil, httpx.Validation(fields)
	}
	return code, comment, nil
}

// cancelOrder handles POST /orders/{id}/cancel: searching, accepted or
// driver_arriving → cancelled with the passenger's reason. Only passengers
// cancel (§22.3); the commission the driver paid is not returned (§22.2) and
// the driver is told. With expected_status the cancellation applies only to
// the order as the passenger saw it: a driver who accepted meanwhile is not
// charged for a cancellation meant for a search.
func (s *Service) cancelOrder(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req passengerCancelRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	code, comment, err := req.parse()
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = s.tx(ctx, func(tx pgx.Tx) error {
		res, err := lifecycle.Cancel(ctx, tx, lifecycle.CancelInput{Change: lifecycle.Change{
			OrderID: id, OnlyPassenger: &p.UserID, Actor: lifecycle.Passenger(p.UserID), Reason: comment,
			ReasonCode: &code, Meta: map[string]any{"reason_code": code}, ExpectStatus: req.ExpectedStatus,
			From: []string{lifecycle.StatusSearching, lifecycle.StatusAccepted, lifecycle.StatusDriverArriving},
		}})
		if err != nil {
			return err
		}
		if res.After.DriverID != nil {
			// The passenger's reason may be about the driver: it stays with
			// the operators.
			return notifyDriverCancelled(ctx, tx, res.After, *res.After.DriverID, nil, 0)
		}
		return nil
	})
	if err != nil {
		return err
	}
	o, err := passengerOrder(ctx, s.pool, id, p.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

func notifyDriverCancelled(ctx context.Context, tx pgx.Tx, o lifecycle.Core, driverID uuid.UUID, reason *string, refunded int64) error {
	info, err := lifecycle.LoadDriverInfo(ctx, tx, driverID)
	if err != nil {
		return err
	}
	params := map[string]any{"order_number": o.Number}
	if reason != nil {
		params["reason"] = *reason
	}
	if refunded > 0 {
		params["refunded"] = refunded
	}
	return lifecycle.Notify(ctx, tx, notifications.DriverBot, info.UserID, notifications.TplOrderCancelledDriver,
		params, "order_cancelled_driver:"+o.ID.String()+":"+driverID.String())
}

func notifyPassengerCancelled(ctx context.Context, tx pgx.Tx, o lifecycle.Core, reason *string) error {
	params := map[string]any{"order_number": o.Number}
	if reason != nil {
		params["reason"] = *reason
	}
	return lifecycle.Notify(ctx, tx, notifications.PassengerBot, o.PassengerID, notifications.TplOrderCancelledPassenger,
		params, "order_cancelled_passenger:"+o.ID.String())
}

type ratingRequest struct {
	Rating  *int    `json:"rating"`
	Comment *string `json:"comment"`
}

// rateOrder handles POST /orders/{id}/rating: completed orders only, once.
func (s *Service) rateOrder(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req ratingRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	switch {
	case req.Rating == nil:
		fields["rating"] = "required"
	case *req.Rating < 1:
		fields["rating"] = "too_small"
	case *req.Rating > 5:
		fields["rating"] = "too_large"
	}
	comment := cleanField(fields, "comment", req.Comment, textutil.MaxFreeText)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	ctx := r.Context()
	err = s.tx(ctx, func(tx pgx.Tx) error {
		o, err := lifecycle.LoadCore(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if o.PassengerID != p.UserID {
			return httpx.NotFound()
		}
		if o.Status != lifecycle.StatusCompleted || o.DriverID == nil {
			return lifecycle.InvalidTransition(o.Status, "rate")
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO order_ratings (order_id, passenger_id, driver_id, rating, comment)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT (order_id) DO NOTHING`,
			id, p.UserID, *o.DriverID, *req.Rating, comment)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return lifecycle.ConflictReason("already_rated", "order already rated")
		}
		_, err = tx.Exec(ctx, `
			UPDATE driver_profiles SET rating_sum = rating_sum + $2, rating_count = rating_count + 1
			WHERE id = $1`, *o.DriverID, *req.Rating)
		return err
	})
	if err != nil {
		return err
	}
	o, err := passengerOrder(ctx, s.pool, id, p.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

type disputeRequest struct {
	Reason *string `json:"reason"`
}

// maxDisputeReason is the dispute reason limit (disputes.reason).
const maxDisputeReason = 500

// disputeOrder handles POST /orders/{id}/dispute: while in progress or within
// 72 hours after completion, once per order.
func (s *Service) disputeOrder(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req disputeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	reason := cleanRequiredField(fields, "reason", req.Reason, maxDisputeReason)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	ctx := r.Context()
	err = s.tx(ctx, func(tx pgx.Tx) error {
		o, err := lifecycle.LoadCore(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if o.PassengerID != p.UserID {
			return httpx.NotFound()
		}
		var disputed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM disputes WHERE order_id = $1)`, id).Scan(&disputed); err != nil {
			return err
		}
		if disputed {
			return lifecycle.ConflictReason("dispute_exists", "order already disputed")
		}
		switch o.Status {
		case lifecycle.StatusInProgress:
		case lifecycle.StatusCompleted:
			var inWindow bool
			if err := tx.QueryRow(ctx, `SELECT completed_at > now() - make_interval(hours => $2)
				FROM orders WHERE id = $1`, id, lifecycle.DisputeWindowHours).Scan(&inWindow); err != nil {
				return err
			}
			if !inWindow {
				return lifecycle.InvalidTransition(o.Status, "dispute").WithDetails(map[string]any{
					"from": o.Status, "action": "dispute", "reason": "dispute_window_closed"})
			}
		default:
			return lifecycle.InvalidTransition(o.Status, "dispute")
		}
		_, err = openDispute(ctx, tx, o, lifecycle.Passenger(p.UserID), reason)
		return err
	})
	if err != nil {
		return err
	}
	o, err := passengerOrder(ctx, s.pool, id, p.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}
