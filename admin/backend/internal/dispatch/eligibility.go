// Package dispatch decides which drivers may take which orders (ARCHITECTURE
// §7): free drivers whose fresh location is within the order's effective
// radius of the pickup, wherever in Kazakhstan the order is. It creates offers
// when an order is placed and as its search radius grows, performs the atomic
// acceptance (§5.1) and admin assignment, and runs the search-expiry job.
package dispatch

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// Eligibility reasons (AvailableOrders.eligibility.reason). The location
// reasons also appear in accept errors and admin assignment warnings.
const (
	ReasonNotApproved         = "not_approved"
	ReasonOffline             = "offline"
	ReasonInsufficientBalance = "insufficient_balance"
	ReasonHasActiveOrder      = "has_active_order"
	ReasonLocationRequired    = "location_required" // no location fix at all
	ReasonLocationStale       = "location_stale"    // last fix older than dispatch.location_max_age_s
	// ReasonOutOfRadius is never an eligibility reason (orders outside the
	// radius are simply not listed); it warns about an admin assignment of a
	// driver who is farther from the pickup than the order's radius.
	ReasonOutOfRadius = "out_of_radius"
)

// Eligibility says whether a driver can take orders right now and, if not,
// the first blocking reason.
type Eligibility struct {
	Eligible bool    `json:"eligible"`
	Reason   *string `json:"reason"`
}

func notEligible(reason string) Eligibility { return Eligibility{Reason: &reason} }

// driverState is everything §7 looks at for one driver.
type driverState struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Status        string
	IsOnline      bool
	UserBlocked   bool
	Available     int64
	HasLocation   bool
	LocationFresh bool
	ActiveOrderID *uuid.UUID
}

func loadDriverState(ctx context.Context, q db.Querier, driverID uuid.UUID, p settings.DispatchSettings) (driverState, error) {
	var s driverState
	err := q.QueryRow(ctx, `
		SELECT d.id, d.user_id, d.status, d.is_online, u.is_blocked,
		       COALESCE(w.balance - w.reserved, 0),
		       dl.driver_id IS NOT NULL,
		       COALESCE(`+freshLocation+`, false),
		       (SELECT a.id FROM orders a
		         WHERE a.driver_id = d.id AND a.status IN ('accepted', 'driver_arriving', 'in_progress') LIMIT 1)
		FROM driver_profiles d
		JOIN users u ON u.id = d.user_id
		LEFT JOIN wallets w ON w.driver_id = d.id
		LEFT JOIN driver_locations dl ON dl.driver_id = d.id
		WHERE d.id = @driver_id`, policyArgs(p, pgx.NamedArgs{"driver_id": driverID})).
		Scan(&s.ID, &s.UserID, &s.Status, &s.IsOnline, &s.UserBlocked, &s.Available,
			&s.HasLocation, &s.LocationFresh, &s.ActiveOrderID)
	if db.IsNoRows(err) {
		return driverState{}, httpx.NotFound()
	}
	if err != nil {
		return driverState{}, fmt.Errorf("dispatch: load driver %s: %w", driverID, err)
	}
	return s, nil
}

// driverLevel applies the per-driver rules of §7 (everything except the
// per-order radius and balance checks): approved and not blocked, online, no
// active order, and a location fix that is fresh.
func (s driverState) driverLevel() Eligibility {
	switch {
	case s.Status != "approved" || s.UserBlocked:
		return notEligible(ReasonNotApproved)
	case !s.IsOnline:
		return notEligible(ReasonOffline)
	case s.ActiveOrderID != nil:
		return notEligible(ReasonHasActiveOrder)
	case !s.HasLocation:
		return notEligible(ReasonLocationRequired)
	case !s.LocationFresh:
		return notEligible(ReasonLocationStale)
	}
	return Eligibility{Eligible: true}
}

// CheckEligibility returns the driver-level eligibility (approved, online, no
// active order, fresh location). Radius and balance are checked per order;
// see ListAvailable.
func CheckEligibility(ctx context.Context, q db.Querier, driverID uuid.UUID) (Eligibility, error) {
	s, err := loadDriverState(ctx, q, driverID, settings.Dispatch(ctx, q))
	if err != nil {
		return Eligibility{}, err
	}
	return s.driverLevel(), nil
}
