package settings

import (
	"context"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// ManualDestinationPolicy uses the same validated defaults shown to operators.
// Enablement is a reader-first rollout switch; money remains the active tariff's
// min/max/step. It never supplies coordinates or a distance-based recommendation.
type ManualDestinationPolicy struct {
	Enabled bool
	Pricing string
}

func ManualDestinations(ctx context.Context, q db.Querier) ManualDestinationPolicy {
	p := ManualDestinationPolicy{Pricing: "disabled"}
	_ = Effective(ctx, q, "orders.manual_destination_enabled", &p.Enabled)
	_ = Effective(ctx, q, "orders.manual_destination_pricing", &p.Pricing)
	return p
}
func (p ManualDestinationPolicy) Allowed() bool {
	return p.Enabled && p.Pricing == "passenger_tariff_bounds"
}
