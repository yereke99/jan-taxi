package lifecycle

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// Error codes and constructors for the order API (openapi ErrorCode).

// InvalidTransition is 409 invalid_transition with details {from, action}.
func InvalidTransition(from, action string) *httpx.Error {
	return httpx.Conflict("invalid_transition", "transition not allowed").
		WithDetails(map[string]any{"from": from, "action": action})
}

// ErrOrderTaken is 409 order_already_taken.
func ErrOrderTaken() *httpx.Error {
	return httpx.Conflict("order_already_taken", "order already taken")
}

// ErrDriverHasActiveOrder is 409 driver_has_active_order.
func ErrDriverHasActiveOrder(activeOrderID uuid.UUID) *httpx.Error {
	e := httpx.Conflict("driver_has_active_order", "driver already has an active order")
	if activeOrderID != uuid.Nil {
		e = e.WithDetails(map[string]any{"active_order_id": activeOrderID.String()})
	}
	return e
}

// ErrDriverOffline is 409 driver_offline.
func ErrDriverOffline() *httpx.Error {
	return httpx.Conflict("driver_offline", "driver is offline")
}

// ErrDriverNotApproved is 403 driver_not_approved.
func ErrDriverNotApproved() *httpx.Error {
	return httpx.NewError(http.StatusForbidden, "driver_not_approved", "driver is not approved")
}

// InsufficientBalance is 402 insufficient_balance with the amounts and the
// minimum top-up as a hint.
func InsufficientBalance(required, available, topUpMin int64) *httpx.Error {
	return httpx.NewError(http.StatusPaymentRequired, "insufficient_balance", "insufficient balance").
		WithDetails(map[string]any{
			"required":         required,
			"available":        available,
			"currency":         wallets.Currency,
			"topup_min_amount": topUpMin,
		})
}

// ActiveOrderExists is 409 conflict for a passenger's second active order of
// the same service type.
func ActiveOrderExists(activeOrderID uuid.UUID) *httpx.Error {
	return httpx.Conflict("conflict", "passenger already has an active order of this service type").
		WithDetails(map[string]any{"reason": "active_order_exists", "active_order_id": activeOrderID.String()})
}

// ConflictReason is 409 conflict with details {reason}.
func ConflictReason(reason, message string) *httpx.Error {
	return httpx.Conflict("conflict", message).WithDetails(map[string]any{"reason": reason})
}

// OrderChanged is 409 conflict {reason: order_changed, status}: the order is
// no longer in the status the caller saw (Change.ExpectStatus).
func OrderChanged(status string) *httpx.Error {
	return httpx.Conflict("conflict", "the order changed meanwhile").
		WithDetails(map[string]any{"reason": "order_changed", "status": status})
}

// Unique indexes whose violations have a meaning for the API.
const (
	IdxOneActivePerDriver    = "orders_one_active_per_driver"
	IdxOneActivePerPassenger = "orders_one_active_per_passenger_service"
	IdxPassengerIdempotency  = "orders_passenger_id_idempotency_key_key"
	IdxOneOpenDispute        = "disputes_one_open_per_order"
	IdxRatingPK              = "order_ratings_pkey"
)

// DBError translates database errors that concurrent requests can produce
// into API errors; anything else is returned unchanged.
func DBError(err error) error {
	if err == nil {
		return nil
	}
	var he *httpx.Error
	if errors.As(err, &he) {
		return err
	}
	if pg := db.PgError(err); pg != nil {
		switch pg.Code {
		case "40P01", "40001": // deadlock_detected, serialization_failure
			return httpx.Conflict("conflict", "concurrent update, retry").WithCause(err)
		}
	}
	if db.IsUniqueViolation(err, IdxOneActivePerDriver) {
		return ErrDriverHasActiveOrder(uuid.Nil).WithCause(err)
	}
	return wallets.HTTPError(err)
}
