// Package dispatch decides which drivers may take which orders (ARCHITECTURE
// §7), creates offers when an order is placed, performs the atomic acceptance
// (§5.1) and admin assignment, and runs the search-expiry job.
package dispatch

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Eligibility reasons (AvailableOrders.eligibility.reason).
const (
	ReasonNotApproved         = "not_approved"
	ReasonOffline             = "offline"
	ReasonInsufficientBalance = "insufficient_balance"
	ReasonHasActiveOrder      = "has_active_order"
	ReasonLocationRequired    = "location_required"
	ReasonLocationStale       = "location_stale"
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
	ID              uuid.UUID
	UserID          uuid.UUID
	CityID          uuid.UUID
	Status          string
	IsOnline        bool
	UserBlocked     bool
	Available       int64
	HasLocation     bool
	LocationFresh   bool
	RequireLocation bool
	ActiveOrderID   *uuid.UUID
}

func loadDriverState(ctx context.Context, q db.Querier, driverID uuid.UUID) (driverState, error) {
	var s driverState
	err := q.QueryRow(ctx, `
		SELECT d.id, d.user_id, d.city_id, d.status, d.is_online, u.is_blocked,
		       COALESCE(w.balance - w.reserved, 0),
		       dl.driver_id IS NOT NULL,
		       COALESCE(dl.recorded_at > now() - make_interval(secs => c.driver_location_max_age_s), false),
		       c.require_driver_location,
		       (SELECT a.id FROM orders a
		         WHERE a.driver_id = d.id AND a.status IN ('accepted', 'driver_arriving', 'in_progress') LIMIT 1)
		FROM driver_profiles d
		JOIN users u ON u.id = d.user_id
		JOIN cities c ON c.id = d.city_id
		LEFT JOIN wallets w ON w.driver_id = d.id
		LEFT JOIN driver_locations dl ON dl.driver_id = d.id
		WHERE d.id = $1`, driverID).
		Scan(&s.ID, &s.UserID, &s.CityID, &s.Status, &s.IsOnline, &s.UserBlocked, &s.Available,
			&s.HasLocation, &s.LocationFresh, &s.RequireLocation, &s.ActiveOrderID)
	if db.IsNoRows(err) {
		return driverState{}, httpx.NotFound()
	}
	if err != nil {
		return driverState{}, fmt.Errorf("dispatch: load driver %s: %w", driverID, err)
	}
	return s, nil
}

// driverLevel applies the per-driver rules of §7 (everything except the
// per-order balance and distance checks).
func (s driverState) driverLevel() Eligibility {
	switch {
	case s.Status != "approved" || s.UserBlocked:
		return notEligible(ReasonNotApproved)
	case !s.IsOnline:
		return notEligible(ReasonOffline)
	case s.ActiveOrderID != nil:
		return notEligible(ReasonHasActiveOrder)
	case s.RequireLocation && !s.HasLocation:
		return notEligible(ReasonLocationRequired)
	case s.RequireLocation && !s.LocationFresh:
		return notEligible(ReasonLocationStale)
	}
	return Eligibility{Eligible: true}
}

// CheckEligibility returns the driver-level eligibility (approved, online, no
// active order, location policy). Balance is checked per order; see ListAvailable.
func CheckEligibility(ctx context.Context, q db.Querier, driverID uuid.UUID) (Eligibility, error) {
	s, err := loadDriverState(ctx, q, driverID)
	if err != nil {
		return Eligibility{}, err
	}
	return s.driverLevel(), nil
}
