package marketing

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/google/uuid"
)

// Rules are typed per template and interpreted only by the server; there is
// no expression language. Validation reasons go to validation_failed fields
// named "rules.<field>".

// CouponRules: first_ride_coupon. A coupon of Amount is issued once, after
// the passenger's first completed ride in Categories, valid ValidityHours,
// for one later order in Categories whose fare is at least MinOrderFare.
type CouponRules struct {
	Amount        int64    `json:"amount"`
	ValidityHours int      `json:"validity_hours"`
	MinOrderFare  int64    `json:"min_order_fare"`
	Categories    []string `json:"categories"`
}

// ReferralRules: referral_first_ride. An invitee bound to a referrer on first
// arrival (within AttributionWindowMinutes of the account's creation) earns
// the referrer RewardAmount to the bonus balance when the invitee's first
// completed ride is in Categories; at most once per referrer per campaign.
// Unlock decides when a passenger may invite; a qualifying ride shorter than
// MinRideSeconds goes to manual review.
type ReferralRules struct {
	RewardAmount             int64    `json:"reward_amount"`
	Categories               []string `json:"categories"`
	AttributionWindowMinutes int      `json:"attribution_window_minutes"`
	Unlock                   string   `json:"unlock"`
	MinRideSeconds           int      `json:"min_ride_seconds"`
}

// CreditRules: passenger_credit. Every passenger of the segment (snapshotted
// at activation) gets Amount on the bonus balance once.
type CreditRules struct {
	Amount    int64     `json:"amount"`
	SegmentID uuid.UUID `json:"segment_id"`
}

// MilestoneRules: driver_milestone_bonus. A driver who completes Rides
// orders in Categories inside the version's [starts_at, ends_at) window gets
// Amount on the wallet once.
type MilestoneRules struct {
	Amount     int64    `json:"amount"`
	Rides      int      `json:"rides"`
	Categories []string `json:"categories"`
}

// ManualRules: manual_grant. One manual bonus credit may not exceed MaxAmount.
type ManualRules struct {
	MaxAmount int64 `json:"max_amount"`
}

// BroadcastOnlyRules: broadcast_only carries copy only, no money.
type BroadcastOnlyRules struct{}

var categories = []string{"taxi", "parcel", "other"}

var unlockPolicies = []string{UnlockImmediate, UnlockCouponRedeemed, UnlockCouponRedeemedOrExpired, UnlockFirstRideCompleted}

// ParseRules decodes and validates raw rules for template. Unknown fields are
// refused, so a rule the server does not implement can never be stored.
func ParseRules(template string, raw json.RawMessage) (any, map[string]string) {
	fields := map[string]string{}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decode := func(dst any) bool {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(dst); err != nil {
			fields["rules"] = "invalid"
			return false
		}
		return true
	}
	money := func(name string, v, lo, hi int64) {
		if v < lo || v > hi {
			fields["rules."+name] = "out_of_range"
		}
	}
	number := func(name string, v, lo, hi int) {
		if v < lo || v > hi {
			fields["rules."+name] = "out_of_range"
		}
	}
	cats := func(list []string) {
		if len(list) == 0 || len(list) > len(categories) {
			fields["rules.categories"] = "invalid"
			return
		}
		seen := map[string]bool{}
		for _, c := range list {
			if !slices.Contains(categories, c) || seen[c] {
				fields["rules.categories"] = "invalid"
				return
			}
			seen[c] = true
		}
	}
	switch template {
	case TemplateFirstRideCoupon:
		var r CouponRules
		if !decode(&r) {
			return nil, fields
		}
		money("amount", r.Amount, 1, 100_000)
		number("validity_hours", r.ValidityHours, 1, 720)
		// The cash a passenger pays never goes below zero: the coupon applies
		// only to fares of at least its value (no partial redemption).
		if r.MinOrderFare < r.Amount || r.MinOrderFare > 10_000_000 {
			fields["rules.min_order_fare"] = "below_amount"
		}
		cats(r.Categories)
		return r, fields
	case TemplateReferral:
		var r ReferralRules
		if !decode(&r) {
			return nil, fields
		}
		money("reward_amount", r.RewardAmount, 1, 100_000)
		cats(r.Categories)
		number("attribution_window_minutes", r.AttributionWindowMinutes, 5, 7*24*60)
		if !slices.Contains(unlockPolicies, r.Unlock) {
			fields["rules.unlock"] = "invalid"
		}
		number("min_ride_seconds", r.MinRideSeconds, 0, 3600)
		return r, fields
	case TemplatePassengerCredit:
		var r CreditRules
		if !decode(&r) {
			return nil, fields
		}
		money("amount", r.Amount, 1, 100_000)
		if r.SegmentID == uuid.Nil {
			fields["rules.segment_id"] = "required"
		}
		return r, fields
	case TemplateDriverMilestone:
		var r MilestoneRules
		if !decode(&r) {
			return nil, fields
		}
		money("amount", r.Amount, 1, 100_000)
		number("rides", r.Rides, 1, 1000)
		cats(r.Categories)
		return r, fields
	case TemplateManualGrant:
		var r ManualRules
		if !decode(&r) {
			return nil, fields
		}
		money("max_amount", r.MaxAmount, 1, 1_000_000)
		return r, fields
	case TemplateBroadcastOnly:
		var r BroadcastOnlyRules
		if !decode(&r) {
			return nil, fields
		}
		return r, fields
	}
	fields["template"] = "invalid"
	return nil, fields
}

// mustRules decodes stored rules (validated when they were written).
func mustRules[T any](raw json.RawMessage) (T, bool) {
	var r T
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, false
	}
	return r, true
}
