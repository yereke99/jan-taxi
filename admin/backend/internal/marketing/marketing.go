// Package marketing implements promotions (ARCHITECTURE §23): versioned
// campaigns, the first-ride coupon, the passenger bonus balance, referrals,
// promotions applied to orders with the driver's compensation, segments and
// broadcasts.
//
// Money rules live here and in the database, never in the apps: the server
// decides eligibility, computes every discount, reserves it in the order's
// creation transaction, releases it when the order is cancelled and settles it
// once when the order completes. Every money movement is an append-only ledger
// row under a unique idempotency key, so a replayed request, event or job run
// changes nothing.
//
// Lock order (to stay deadlock-free with the order paths): order row → coupon
// or bonus account → campaign row → driver wallet.
package marketing

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Campaign templates (marketing_campaigns.template).
const (
	TemplateFirstRideCoupon = "first_ride_coupon"
	TemplateReferral        = "referral_first_ride"
	TemplatePassengerCredit = "passenger_credit"
	TemplateDriverMilestone = "driver_milestone_bonus"
	TemplateManualGrant     = "manual_grant"
	TemplateBroadcastOnly   = "broadcast_only"
)

// Templates lists the implemented templates in display order.
var Templates = []string{TemplateFirstRideCoupon, TemplateReferral, TemplatePassengerCredit,
	TemplateDriverMilestone, TemplateManualGrant, TemplateBroadcastOnly}

// Campaign statuses.
const (
	StatusDraft     = "draft"
	StatusScheduled = "scheduled"
	StatusActive    = "active"
	StatusPaused    = "paused"
	StatusEnded     = "ended"
	StatusArchived  = "archived"
)

// Coupon statuses.
const (
	CouponIssued   = "issued"
	CouponReserved = "reserved"
	CouponRedeemed = "redeemed"
	CouponExpired  = "expired"
	CouponVoided   = "voided"
)

// Promotion kinds and application statuses (order_promotions).
const (
	KindCoupon = "coupon"
	KindBonus  = "bonus"

	AppReserved = "reserved"
	AppReleased = "released"
	AppSettled  = "settled"
	AppReversed = "reversed"
)

// Referral attribution statuses.
const (
	ReferralAttributed = "attributed"
	ReferralReview     = "review"
	ReferralRewarded   = "rewarded"
	ReferralCapped     = "capped"
	ReferralRejected   = "rejected"
)

// Referral unlock policies (ReferralRules.Unlock).
const (
	UnlockCouponRedeemed          = "coupon_redeemed"
	UnlockCouponRedeemedOrExpired = "coupon_redeemed_or_expired"
	UnlockFirstRideCompleted      = "first_ride_completed"
)

// Options configure the service.
type Options struct {
	// PassengerBotUsername builds referral links (t.me/<bot>?start=ref_<code>).
	PassengerBotUsername string
	Log                  *slog.Logger
}

// Service serves the marketing endpoints and jobs.
type Service struct {
	pool *pgxpool.Pool
	opt  Options
	log  *slog.Logger
}

// New builds the service.
func New(pool *pgxpool.Pool, opt Options) *Service {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	return &Service{pool: pool, opt: opt, log: opt.Log.With(slog.String("component", "marketing"))}
}

// Errors shared by the handlers.
var (
	errCampaignConflict = func(msg string) *httpx.Error {
		return httpx.Conflict("campaign_conflict", msg)
	}
	// errPromotionChanged tells the passenger app that the promotion it
	// showed is no longer what the server would apply; details carry the
	// reason and the discount the server computed (0 = none).
	errPromotionChanged = func(reason string, discount int64) *httpx.Error {
		return httpx.NewError(http.StatusConflict, "promotion_changed", "the promotion changed; review the price").
			WithDetails(map[string]any{"reason": reason, "discount": discount})
	}
)
