// Package lifecycle is the order state machine shared by the orders and
// dispatch packages (ARCHITECTURE §5): statuses, guarded transitions with a
// version bump, the status-event journal, the people involved in an order and
// the notification/realtime helpers every transition uses. Everything runs in
// the caller's transaction.
package lifecycle

import "slices"

// Order statuses (orders.status).
const (
	StatusDraft          = "draft"
	StatusSearching      = "searching"
	StatusAccepted       = "accepted"
	StatusDriverArriving = "driver_arriving"
	StatusInProgress     = "in_progress"
	StatusCompleted      = "completed"
	StatusCancelled      = "cancelled"
	StatusDisputed       = "disputed"
)

// Statuses lists every order status.
var Statuses = []string{StatusDraft, StatusSearching, StatusAccepted, StatusDriverArriving,
	StatusInProgress, StatusCompleted, StatusCancelled, StatusDisputed}

// ActiveStatuses are the statuses covered by the "one active order" indexes
// (a passenger's order; a driver's order excludes searching).
var ActiveStatuses = []string{StatusSearching, StatusAccepted, StatusDriverArriving, StatusInProgress}

// DriverActiveStatuses are the statuses in which an order occupies its driver
// (index orders_one_active_per_driver).
var DriverActiveStatuses = []string{StatusAccepted, StatusDriverArriving, StatusInProgress}

// HistoryStatuses are the finished statuses shown in history lists.
var HistoryStatuses = []string{StatusCompleted, StatusCancelled, StatusDisputed}

// ValidStatus reports whether s is a known status.
func ValidStatus(s string) bool { return slices.Contains(Statuses, s) }

// IsActive reports whether the order still waits for or is served by a driver.
func IsActive(status string) bool { return slices.Contains(ActiveStatuses, status) }

// OccupiesDriver reports whether the order is the driver's current trip.
func OccupiesDriver(status string) bool { return slices.Contains(DriverActiveStatuses, status) }

// Categories (orders.category) and the fields derived from them.
const (
	CategoryTaxi   = "taxi"
	CategoryParcel = "parcel"
	CategoryOther  = "other"

	ServicePassenger = "passenger"
	ServiceDelivery  = "delivery"

	CargoNone   = "none"
	CargoParcel = "parcel"
	CargoOther  = "other"
)

// ValidCategory reports whether c is a known category.
func ValidCategory(c string) bool {
	return c == CategoryTaxi || c == CategoryParcel || c == CategoryOther
}

// ServiceType returns the service type of a category.
func ServiceType(category string) string {
	if category == CategoryTaxi {
		return ServicePassenger
	}
	return ServiceDelivery
}

// CargoType returns the cargo type of a category.
func CargoType(category string) string {
	switch category {
	case CategoryParcel:
		return CargoParcel
	case CategoryOther:
		return CargoOther
	}
	return CargoNone
}

// Payment methods (orders.payment_method).
const (
	PaymentCash         = "cash"
	PaymentBankTransfer = "bank_transfer"
)

// ValidPaymentMethod reports whether m is accepted.
func ValidPaymentMethod(m string) bool { return m == PaymentCash || m == PaymentBankTransfer }

// Actor types (order_status_events.actor_type, orders.cancelled_by).
const (
	ActorPassenger = "passenger"
	ActorDriver    = "driver"
	ActorAdmin     = "admin"
	ActorSystem    = "system"
)

// Offer statuses (order_offers.status).
const (
	OfferSent      = "sent"
	OfferAccepted  = "accepted"
	OfferLost      = "lost"
	OfferExpired   = "expired"
	OfferCancelled = "cancelled"
)

// ReasonNoDriverFound is the cancel reason recorded by the search timeout.
const ReasonNoDriverFound = "no_driver_found"

// DisputeWindowHours is how long after completion a passenger may open a dispute.
const DisputeWindowHours = 72

// Passenger cancellation reasons (orders.cancel_reason_code, ARCHITECTURE
// §22.3). A comment is required with CancelOther.
const (
	CancelLongWait     = "long_wait"
	CancelDriverIssue  = "driver_issue"
	CancelVehicleIssue = "vehicle_issue"
	CancelChangedPlans = "changed_plans"
	CancelOther        = "other"
)

// CancelReasonCodes lists the passenger cancellation reasons in display order.
var CancelReasonCodes = []string{CancelLongWait, CancelDriverIssue, CancelVehicleIssue, CancelChangedPlans, CancelOther}

// ValidCancelReason reports whether code is a passenger cancellation reason.
func ValidCancelReason(code string) bool { return slices.Contains(CancelReasonCodes, code) }
