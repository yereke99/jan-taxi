package orders

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/dispatch"
	"github.com/yereke99/jantaxi/admin/backend/internal/orders/lifecycle"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/phone"
)

// Limits of operator-entered text.
const (
	maxAdminCancelReason = 200
	maxResolution        = 1000
	maxSearchLen         = 100
)

var driverCodeRe = regexp.MustCompile(`^[A-Z]{3}[0-9]{3,}$`)

func queryTime(r *http.Request, name string) (*time.Time, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, httpx.Validation(map[string]string{name: "invalid"})
	}
	return &t, nil
}

// parseAdminFilter reads the operator list filters.
func parseAdminFilter(r *http.Request) (adminFilter, error) {
	var f adminFilter
	var err error
	qs := r.URL.Query()
	if f.Status = strings.TrimSpace(qs.Get("status")); f.Status != "" && !lifecycle.ValidStatus(f.Status) {
		return f, httpx.Validation(map[string]string{"status": "invalid"})
	}
	if f.Category = strings.TrimSpace(qs.Get("category")); f.Category != "" && !lifecycle.ValidCategory(f.Category) {
		return f, httpx.Validation(map[string]string{"category": "invalid"})
	}
	if f.CityID, err = httpx.QueryUUID(r, "city_id"); err != nil {
		return f, err
	}
	if f.DriverID, err = httpx.QueryUUID(r, "driver_id"); err != nil {
		return f, err
	}
	if f.From, err = queryTime(r, "from"); err != nil {
		return f, err
	}
	if f.To, err = queryTime(r, "to"); err != nil {
		return f, err
	}
	q := strings.TrimSpace(qs.Get("q"))
	if len([]rune(q)) > maxSearchLen {
		return f, httpx.Validation(map[string]string{"q": "too_long"})
	}
	f.applySearch(q)
	return f, nil
}

// applySearch interprets q as an order number, an order id, a phone number
// (passenger, recipient or driver) or a driver code; whatever parses is matched.
func (f *adminFilter) applySearch(q string) {
	if q == "" {
		return
	}
	f.HasQuery = true
	plain := strings.TrimPrefix(q, "#")
	if n, err := strconv.ParseInt(plain, 10, 64); err == nil && n > 0 {
		f.Number = &n
	}
	if id, err := uuid.Parse(q); err == nil {
		f.OrderID = &id
	}
	if p, err := phone.Normalize(q); err == nil {
		f.Phone = p
	}
	if code := strings.ToUpper(q); driverCodeRe.MatchString(code) {
		f.Code = code
	}
}

// adminListOrders handles GET /admin/orders.
func (s *Service) adminListOrders(w http.ResponseWriter, r *http.Request) error {
	f, err := parseAdminFilter(r)
	if err != nil {
		return err
	}
	pg, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	items, total, err := listAdminOrders(r.Context(), s.pool, f, pg.PageSize, pg.Offset())
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[AdminOrderListItem]{Items: items, Page: pg.Page, PageSize: pg.PageSize, Total: total})
}

// adminListDriverOrders handles GET /admin/drivers/{id}/orders.
func (s *Service) adminListDriverOrders(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM driver_profiles WHERE id = $1)`, id).
		Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return httpx.NotFound()
	}
	f := adminFilter{DriverID: &id}
	if f.Status = strings.TrimSpace(r.URL.Query().Get("status")); f.Status != "" && !lifecycle.ValidStatus(f.Status) {
		return httpx.Validation(map[string]string{"status": "invalid"})
	}
	pg, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	items, total, err := listAdminOrders(r.Context(), s.pool, f, pg.PageSize, pg.Offset())
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[AdminOrderListItem]{Items: items, Page: pg.Page, PageSize: pg.PageSize, Total: total})
}

// adminGetOrder handles GET /admin/orders/{id}.
func (s *Service) adminGetOrder(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	o, err := adminOrder(r.Context(), s.pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, o)
}

// adminOrderAction runs fn for the calling admin in a transaction and returns
// the updated operator view.
func (s *Service) adminOrderAction(w http.ResponseWriter, r *http.Request, id uuid.UUID,
	fn func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal) ([]string, error)) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var warnings []string
	if err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		warnings, err = fn(ctx, tx, admin)
		return err
	}); err != nil {
		return err
	}
	o, err := adminOrder(ctx, s.pool, id)
	if err != nil {
		return err
	}
	o.Warnings = warnings
	return httpx.OK(w, o)
}

type adminReasonRequest struct {
	Reason *string `json:"reason"`
}

// adminCancelOrder handles POST /admin/orders/{id}/cancel: any active status,
// including in_progress.
func (s *Service) adminCancelOrder(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req adminReasonRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	reason := cleanRequiredField(fields, "reason", req.Reason, maxAdminCancelReason)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return s.adminOrderAction(w, r, id, func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal) ([]string, error) {
		res, err := lifecycle.Cancel(ctx, tx, lifecycle.CancelInput{Change: lifecycle.Change{
			OrderID: id, Actor: lifecycle.Admin(admin.AdminID), Reason: &reason, From: lifecycle.ActiveStatuses,
		}})
		if err != nil {
			return nil, err
		}
		if err := notifyPassengerCancelled(ctx, tx, res.After, &reason); err != nil {
			return nil, err
		}
		if res.After.DriverID != nil {
			if err := notifyDriverCancelled(ctx, tx, res.After, *res.After.DriverID, &reason); err != nil {
				return nil, err
			}
		}
		return nil, audit.Log(ctx, tx, r, audit.Entry{
			Action: "order.cancel", EntityType: "order", EntityID: id.String(),
			Before: map[string]any{"status": res.Before.Status, "driver_id": res.Before.DriverID},
			After: map[string]any{"status": res.After.Status, "reason": reason,
				"released_commission": res.Released},
		})
	})
}

type assignRequest struct {
	DriverID *string `json:"driver_id"`
}

// adminAssignOrder handles POST /admin/orders/{id}/assign.
func (s *Service) adminAssignOrder(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req assignRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.DriverID == nil || strings.TrimSpace(*req.DriverID) == "" {
		return httpx.Validation(map[string]string{"driver_id": "required"})
	}
	driverID, err := uuid.Parse(strings.TrimSpace(*req.DriverID))
	if err != nil {
		return httpx.Validation(map[string]string{"driver_id": "invalid"})
	}
	return s.adminOrderAction(w, r, id, func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal) ([]string, error) {
		res, err := dispatch.Assign(ctx, tx, id, driverID, admin.AdminID)
		if err != nil {
			return nil, err
		}
		var warnings []string
		if res.DriverOffline {
			warnings = append(warnings, dispatch.ReasonOffline)
		}
		if res.LocationWarning != "" {
			warnings = append(warnings, res.LocationWarning)
		}
		action := "order.assign"
		if res.PreviousDriverID != nil {
			action = "order.reassign"
		}
		return warnings, audit.Log(ctx, tx, r, audit.Entry{
			Action: action, EntityType: "order", EntityID: id.String(),
			Before: map[string]any{"status": res.Before.Status, "driver_id": res.Before.DriverID},
			After: map[string]any{"status": res.After.Status, "driver_id": res.After.DriverID,
				"driver_offline": res.DriverOffline, "location_warning": res.LocationWarning,
				"distance_to_pickup_m": res.DistanceToPickupM, "released_commission": res.Released},
		})
	})
}

// adminOpenDispute handles POST /admin/orders/{id}/dispute.
func (s *Service) adminOpenDispute(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req adminReasonRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	reason := cleanRequiredField(fields, "reason", req.Reason, maxDisputeReason)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return s.adminOrderAction(w, r, id, func(ctx context.Context, tx pgx.Tx, admin auth.AdminPrincipal) ([]string, error) {
		o, err := lifecycle.LoadCore(ctx, tx, id, true)
		if err != nil {
			return nil, err
		}
		if open, err := hasOpenDispute(ctx, tx, id); err != nil {
			return nil, err
		} else if open {
			return nil, lifecycle.ConflictReason("dispute_open", "order already has an open dispute")
		}
		if o.Status != lifecycle.StatusInProgress && o.Status != lifecycle.StatusCompleted {
			return nil, lifecycle.InvalidTransition(o.Status, "dispute")
		}
		disputeID, err := openDispute(ctx, tx, o, lifecycle.Admin(admin.AdminID), reason)
		if err != nil {
			return nil, err
		}
		return nil, audit.Log(ctx, tx, r, audit.Entry{
			Action: "order.dispute", EntityType: "order", EntityID: id.String(),
			Before: map[string]any{"status": o.Status},
			After:  map[string]any{"status": lifecycle.StatusDisputed, "dispute_id": disputeID, "reason": reason},
		})
	})
}

// adminListDisputes handles GET /admin/disputes.
func (s *Service) adminListDisputes(w http.ResponseWriter, r *http.Request) error {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != DisputeOpen && status != DisputeResolved {
		return httpx.Validation(map[string]string{"status": "invalid"})
	}
	pg, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	items, total, err := listDisputes(r.Context(), s.pool, status, pg)
	if err != nil {
		return err
	}
	if items == nil {
		items = []Dispute{}
	}
	return httpx.OK(w, httpx.Page[Dispute]{Items: items, Page: pg.Page, PageSize: pg.PageSize, Total: total})
}

type resolveRequest struct {
	Outcome          *string `json:"outcome"`
	Resolution       *string `json:"resolution"`
	RefundCommission *bool   `json:"refund_commission"`
}

// adminResolveDispute handles POST /admin/disputes/{id}/resolve.
func (s *Service) adminResolveDispute(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req resolveRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	fields := map[string]string{}
	outcome := ""
	switch {
	case req.Outcome == nil || *req.Outcome == "":
		fields["outcome"] = "required"
	case *req.Outcome != OutcomeCompleted && *req.Outcome != OutcomeCancelled:
		fields["outcome"] = "invalid"
	default:
		outcome = *req.Outcome
	}
	resolution := cleanRequiredField(fields, "resolution", req.Resolution, maxResolution)
	refund := req.RefundCommission != nil && *req.RefundCommission
	if refund && outcome == OutcomeCompleted {
		fields["refund_commission"] = "not_allowed"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	ctx := r.Context()
	var res resolveResult
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		res, err = resolveDispute(ctx, tx, resolveInput{DisputeID: id, AdminID: admin.AdminID, Outcome: outcome,
			Resolution: resolution, RefundCommission: refund})
		if err != nil {
			return err
		}
		return audit.Log(ctx, tx, r, audit.Entry{
			Action: "dispute.resolve", EntityType: "dispute", EntityID: id.String(),
			Before: map[string]any{"status": res.Before.Status, "order_status": res.OrderBefore.Status},
			After: map[string]any{"status": res.After.Status, "outcome": outcome, "order_id": res.Order.ID,
				"order_status": res.Order.Status, "charged": res.Charged, "released": res.Released,
				"refunded": res.Refunded},
		})
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, res.After)
}
