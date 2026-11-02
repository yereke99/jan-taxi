package marketing

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// Promotions on orders (§23.6). The order's fare, its commission and the
// commission's basis never change: a promotion only lowers what the
// passenger pays the driver in cash (fare - discount), and the driver gets
// the discount back on the wallet when the order completes.

// bonusCategories are the order categories a bonus balance pays for: the
// customer's assignment says "the next taxi order".
var bonusCategories = []string{"taxi"}

// PromotionRequest is the promotion a passenger asked for in POST /orders.
type PromotionRequest struct {
	Kind     string // KindCoupon | KindBonus | "" (none)
	Discount *int64 // the discount the app showed; the order is refused when it differs
}

// ParseRequest validates the optional promotion fields of POST /orders.
func ParseRequest(kind *string, discount *int64, fields map[string]string) PromotionRequest {
	var r PromotionRequest
	if kind != nil {
		switch *kind {
		case "", "none":
		case KindCoupon, KindBonus:
			r.Kind = *kind
		default:
			fields["promotion"] = "invalid"
		}
	}
	if discount != nil {
		if *discount < 0 || *discount > 10_000_000 {
			fields["promotion_discount"] = "invalid"
		}
		r.Discount = discount
	}
	return r
}

// Plan is a validated promotion for an order being created; what it uses is
// locked until the creating transaction ends.
type Plan struct {
	Kind       string
	Discount   int64
	CouponID   uuid.UUID
	CampaignID uuid.UUID
	VersionID  uuid.UUID
	UserID     uuid.UUID
	Fare       int64
}

// None reports whether the order carries no promotion.
func (p Plan) None() bool { return p.Discount <= 0 }

// PlanOrder validates the requested promotion for a new order inside its
// creation transaction and locks the coupon row or the bonus account it would
// use. The server computes the discount; when it is not what the app showed,
// the order is refused with 409 promotion_changed, so a passenger never pays
// more than the confirmed price. An order without a promotion costs nothing.
func PlanOrder(ctx context.Context, tx pgx.Tx, userID uuid.UUID, category string, fare int64, req PromotionRequest) (Plan, error) {
	plan := Plan{UserID: userID, Fare: fare}
	if req.Kind == "" {
		if req.Discount != nil && *req.Discount > 0 {
			return plan, errPromotionChanged("no_promotion", 0)
		}
		return plan, nil
	}
	if !settings.Marketing(ctx, tx).Redemptions {
		return plan, errPromotionChanged("redemptions_disabled", 0)
	}
	switch req.Kind {
	case KindCoupon:
		c, reason, err := lockUsableCoupon(ctx, tx, userID, category, fare)
		if err != nil {
			return plan, err
		}
		if c == nil {
			return plan, errPromotionChanged(reason, 0)
		}
		plan.Kind, plan.Discount, plan.CouponID, plan.CampaignID, plan.VersionID =
			KindCoupon, c.amount, c.id, c.campaignID, c.versionID
	case KindBonus:
		if !slices.Contains(bonusCategories, category) {
			return plan, errPromotionChanged("category_not_eligible", 0)
		}
		a, err := lockBonus(ctx, tx, userID)
		if err != nil {
			return plan, err
		}
		available := a.balance - a.reserved
		if available <= 0 {
			return plan, errPromotionChanged("no_bonus", 0)
		}
		// The bonus pays at most the fare; what is left stays for later.
		plan.Kind, plan.Discount = KindBonus, min(available, fare)
	}
	if req.Discount != nil && *req.Discount != plan.Discount {
		return plan, errPromotionChanged("discount_changed", plan.Discount)
	}
	return plan, nil
}

type usableCoupon struct {
	id, campaignID, versionID uuid.UUID
	amount                    int64
}

// lockUsableCoupon locks the passenger's coupon that fits the order, or
// returns nil and why none does.
func lockUsableCoupon(ctx context.Context, tx pgx.Tx, userID uuid.UUID, category string, fare int64) (*usableCoupon, string, error) {
	rows, err := tx.Query(ctx, `
		SELECT c.id, c.campaign_id, c.version_id, c.amount, c.min_order_fare, c.categories, c.expires_at > now(),
		       k.redemptions_frozen OR k.status = 'archived'
		FROM marketing_coupons c JOIN marketing_campaigns k ON k.id = c.campaign_id
		WHERE c.user_id = $1 AND c.status = 'issued'
		ORDER BY c.expires_at
		FOR UPDATE OF c`, userID)
	if err != nil {
		return nil, "", fmt.Errorf("marketing: lock coupon: %w", err)
	}
	defer rows.Close()
	reason := "no_coupon"
	for rows.Next() {
		var c usableCoupon
		var minFare int64
		var cats []string
		var valid, frozen bool
		if err := rows.Scan(&c.id, &c.campaignID, &c.versionID, &c.amount, &minFare, &cats, &valid, &frozen); err != nil {
			return nil, "", fmt.Errorf("marketing: lock coupon: %w", err)
		}
		switch {
		case !valid:
			reason = "coupon_expired"
		case frozen:
			reason = "coupon_frozen"
		case !slices.Contains(cats, category):
			reason = "category_not_eligible"
		case fare < minFare:
			reason = "fare_below_minimum"
		default:
			return &c, "", nil
		}
	}
	return nil, reason, rows.Err()
}

// Reserve records plan for the order created in the same transaction: the
// coupon becomes reserved for it, or the bonus amount is reserved on the
// passenger's account. The unique indexes make a second reservation of one
// coupon or a second promotion on one order impossible.
func Reserve(ctx context.Context, tx pgx.Tx, plan Plan, orderID uuid.UUID) error {
	if plan.None() {
		return nil
	}
	var appID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO order_promotions (order_id, user_id, kind, coupon_id, campaign_id, version_id, gross_fare, discount)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		orderID, plan.UserID, plan.Kind, idPtr(plan.CouponID), idPtr(plan.CampaignID), idPtr(plan.VersionID),
		plan.Fare, plan.Discount).Scan(&appID)
	if err != nil {
		return fmt.Errorf("marketing: reserve promotion: %w", err)
	}
	switch plan.Kind {
	case KindCoupon:
		tag, err := tx.Exec(ctx, `
			UPDATE marketing_coupons SET status = 'reserved', reserved_order_id = $2, reserved_at = now()
			WHERE id = $1 AND status = 'issued'`, plan.CouponID, orderID)
		if err != nil {
			return fmt.Errorf("marketing: reserve coupon: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return errPromotionChanged("coupon_unavailable", 0)
		}
	case KindBonus:
		a, err := lockBonus(ctx, tx, plan.UserID)
		if err != nil {
			return err
		}
		if _, err := applyBonus(ctx, tx, &a, bonusEntry{
			typ: BonusReserve, reservedDelta: plan.Discount, orderID: &orderID, applicationID: &appID,
			key: "bonus_reserve:" + appID.String(), description: "order reservation",
		}); err != nil {
			if err == errBonusInsufficient {
				return errPromotionChanged("no_bonus", 0)
			}
			return err
		}
	}
	return publishPromotions(ctx, tx, plan.UserID)
}

// ReleaseOrder returns the promotion reserved by a cancelled order: the
// coupon is usable again until it expires (an expired one closes), a bonus
// reservation goes back to the available balance. Nothing is paid to the
// driver. A promotion already settled (the order was completed and then
// cancelled by a dispute) is flagged for finance review instead; nothing
// moves automatically. A first-ride coupon earned by the cancelled ride is
// voided while unused. It returns the released discount.
func ReleaseOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, reason string) (int64, error) {
	var (
		appID, userID uuid.UUID
		kind          string
		couponID      *uuid.UUID
		discount      int64
		status        string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, user_id, kind, coupon_id, discount, status FROM order_promotions WHERE order_id = $1 FOR UPDATE`,
		orderID).Scan(&appID, &userID, &kind, &couponID, &discount, &status)
	switch {
	case db.IsNoRows(err):
		return 0, voidEarnedCoupon(ctx, tx, orderID)
	case err != nil:
		return 0, fmt.Errorf("marketing: release promotion: %w", err)
	}
	if status == AppSettled {
		if _, err := tx.Exec(ctx, `
			UPDATE order_promotions SET review_reason = 'order_cancelled_after_settlement' WHERE id = $1`, appID); err != nil {
			return 0, fmt.Errorf("marketing: flag promotion: %w", err)
		}
		return 0, voidEarnedCoupon(ctx, tx, orderID)
	}
	if status != AppReserved {
		return 0, voidEarnedCoupon(ctx, tx, orderID)
	}
	if len(reason) > 200 {
		reason = reason[:200]
	}
	if _, err := tx.Exec(ctx, `
		UPDATE order_promotions SET status = 'released', released_at = now(), release_reason = $2 WHERE id = $1`,
		appID, reason); err != nil {
		return 0, fmt.Errorf("marketing: release promotion: %w", err)
	}
	switch kind {
	case KindCoupon:
		// A coupon whose own qualifying ride was cancelled meanwhile (a
		// dispute) is voided rather than returned. The source order is read
		// FOR SHARE so a concurrent dispute resolution is waited for.
		var sourceCancelled bool
		if err := tx.QueryRow(ctx, `
			SELECT so.status = 'cancelled' FROM marketing_coupons c JOIN orders so ON so.id = c.source_order_id
			WHERE c.id = $1 FOR SHARE OF so`, couponID).Scan(&sourceCancelled); err != nil && !db.IsNoRows(err) {
			return 0, fmt.Errorf("marketing: release coupon: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE marketing_coupons
			SET status = CASE WHEN $3 THEN 'voided' WHEN expires_at > now() THEN 'issued' ELSE 'expired' END,
			    closed_at = CASE WHEN NOT $3 AND expires_at > now() THEN NULL ELSE now() END,
			    void_reason = CASE WHEN $3 THEN 'qualifying_ride_cancelled' ELSE void_reason END,
			    reserved_order_id = NULL, reserved_at = NULL
			WHERE id = $1 AND status = 'reserved' AND reserved_order_id = $2`, couponID, orderID, sourceCancelled); err != nil {
			return 0, fmt.Errorf("marketing: release coupon: %w", err)
		}
	case KindBonus:
		a, err := lockBonus(ctx, tx, userID)
		if err != nil {
			return 0, err
		}
		if _, err := applyBonus(ctx, tx, &a, bonusEntry{
			typ: BonusRelease, reservedDelta: -discount, orderID: &orderID, applicationID: &appID,
			key: "bonus_release:" + appID.String(), description: "order cancelled",
		}); err != nil {
			return 0, err
		}
	}
	if err := voidEarnedCoupon(ctx, tx, orderID); err != nil {
		return 0, err
	}
	return discount, publishPromotions(ctx, tx, userID)
}

// voidEarnedCoupon handles the awards of a ride cancelled after completion
// (a dispute): "a ride that is stopped or not completed earns nothing". Its
// unused first-ride coupon is voided (a reserved one is voided when its order
// ends, see ReleaseOrder and SettleOrder). A referral the ride qualified is
// rejected while under review and flagged for finance when already paid; no
// money moves automatically.
func voidEarnedCoupon(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE marketing_coupons SET status = 'voided', closed_at = now(), void_reason = 'qualifying_ride_cancelled'
		WHERE source_order_id = $1 AND status = 'issued'`, orderID); err != nil {
		return fmt.Errorf("marketing: void earned coupon: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE referral_attributions a
		SET status = CASE WHEN a.status = 'review' THEN 'rejected' ELSE a.status END,
		    reason = 'qualifying_ride_cancelled'
		FROM orders o
		WHERE o.id = $1 AND a.invitee_user_id = o.passenger_id AND a.qualifying_order_id = o.id
		  AND a.status IN ('review', 'rewarded')`, orderID); err != nil {
		return fmt.Errorf("marketing: flag earned referral: %w", err)
	}
	return nil
}

// SettleOrder finishes the promotion of a completed order exactly once, in
// the completion transaction: the coupon is redeemed or the bonus spent, and
// the discount is credited to the completing driver's wallet
// (promo_compensation, key "promo:<application_id>"). It returns the
// compensation (0 without a promotion or on a replay).
func SettleOrder(ctx context.Context, tx pgx.Tx, orderID, driverID uuid.UUID) (int64, error) {
	var (
		appID, userID uuid.UUID
		kind          string
		couponID      *uuid.UUID
		discount      int64
	)
	err := tx.QueryRow(ctx, `
		SELECT id, user_id, kind, coupon_id, discount FROM order_promotions
		WHERE order_id = $1 AND status = 'reserved' FOR UPDATE`, orderID).Scan(&appID, &userID, &kind, &couponID, &discount)
	if db.IsNoRows(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("marketing: settle promotion: %w", err)
	}
	var review *string
	switch kind {
	case KindCoupon:
		tag, err := tx.Exec(ctx, `
			UPDATE marketing_coupons SET status = 'redeemed', redeemed_order_id = $2, redeemed_at = now(),
			       reserved_order_id = NULL
			WHERE id = $1 AND status = 'reserved' AND reserved_order_id = $2`, couponID, orderID)
		if err != nil {
			return 0, fmt.Errorf("marketing: redeem coupon: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return 0, fmt.Errorf("marketing: coupon of order %s is not reserved for it", orderID)
		}
		// The ride was done at the discounted price, so the driver is paid;
		// finance reviews a coupon whose qualifying ride was cancelled since.
		var sourceCancelled bool
		if err := tx.QueryRow(ctx, `
			SELECT so.status = 'cancelled' FROM marketing_coupons c JOIN orders so ON so.id = c.source_order_id
			WHERE c.id = $1`, couponID).Scan(&sourceCancelled); err != nil && !db.IsNoRows(err) {
			return 0, fmt.Errorf("marketing: redeem coupon: %w", err)
		}
		if sourceCancelled {
			r := "coupon_source_cancelled"
			review = &r
		}
	case KindBonus:
		a, err := lockBonus(ctx, tx, userID)
		if err != nil {
			return 0, err
		}
		if _, err := applyBonus(ctx, tx, &a, bonusEntry{
			typ: BonusSpend, balanceDelta: -discount, reservedDelta: -discount, orderID: &orderID,
			applicationID: &appID, key: "bonus_spend:" + appID.String(), description: "paid part of the fare",
		}); err != nil {
			return 0, err
		}
	}
	key := "promo:" + appID.String()
	if err := wallets.CreditPromoCompensation(ctx, tx, driverID, orderID, discount, key); err != nil {
		return 0, fmt.Errorf("marketing: driver compensation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE order_promotions SET status = 'settled', settled_at = now(), driver_id = $2, compensation = discount,
		       compensation_key = $3, review_reason = COALESCE($4, review_reason)
		WHERE id = $1`, appID, driverID, key, review); err != nil {
		return 0, fmt.Errorf("marketing: settle promotion: %w", err)
	}
	return discount, publishPromotions(ctx, tx, userID)
}

// RecordRideCompleted queues the ride for the award worker (first-ride
// coupon, referral reward, driver milestones) in the completion transaction.
// Nothing is queued while awards are switched off or no such campaign is
// active, so switching a campaign on never awards rides completed before.
// One statement: it runs on every completion.
func RecordRideCompleted(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, completedAt time.Time) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO marketing_events (kind, order_id, occurred_at)
		SELECT 'ride_completed', $1, $2
		WHERE COALESCE((SELECT value FROM app_settings WHERE key = 'marketing.awards_enabled'), 'true'::jsonb) <> 'false'::jsonb
		  AND EXISTS (SELECT 1 FROM marketing_campaigns WHERE status = 'active'
		              AND template IN ('first_ride_coupon', 'referral_first_ride', 'driver_milestone_bonus'))
		ON CONFLICT (kind, order_id) DO NOTHING`, orderID, completedAt); err != nil {
		return fmt.Errorf("marketing: record ride: %w", err)
	}
	return nil
}

// OrderPromotion is the promotion on an order as passengers, drivers and
// admins see it (openapi OrderPromotion). CashDue is what the passenger pays
// the driver; Compensation is set once the driver was credited.
type OrderPromotion struct {
	Kind         string              `json:"kind"`
	Title        *settings.Localized `json:"title"`
	Discount     int64               `json:"discount"`
	GrossFare    int64               `json:"gross_fare"`
	CashDue      int64               `json:"cash_due"`
	Status       string              `json:"status"`
	Compensation *int64              `json:"compensation"`
	SettledAt    *time.Time          `json:"settled_at"`
}

// LoadOrderPromotion returns the order's promotion, or nil.
func LoadOrderPromotion(ctx context.Context, q db.Querier, orderID uuid.UUID) (*OrderPromotion, error) {
	var p OrderPromotion
	var title []byte
	err := q.QueryRow(ctx, `
		SELECT op.kind, v.title, op.discount, op.gross_fare, op.status, op.compensation, op.settled_at
		FROM order_promotions op LEFT JOIN marketing_campaign_versions v ON v.id = op.version_id
		WHERE op.order_id = $1`, orderID).Scan(&p.Kind, &title, &p.Discount, &p.GrossFare, &p.Status,
		&p.Compensation, &p.SettledAt)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("marketing: order promotion: %w", err)
	}
	if len(title) > 0 {
		var t settings.Localized
		if json.Unmarshal(title, &t) == nil {
			p.Title = &t
		}
	}
	p.CashDue = p.GrossFare - p.Discount
	return &p, nil
}

// publishPromotions tells the passenger's apps that their promotions changed.
func publishPromotions(ctx context.Context, q db.Querier, userID uuid.UUID) error {
	return realtime.Publish(ctx, q, realtime.Event{Type: EventPromotionsUpdated,
		Scopes: []string{realtime.UserScope(userID)}, Data: map[string]any{"user_id": userID.String()}})
}

// EventPromotionsUpdated is the realtime hint that a passenger's coupons,
// bonus or referral state changed; the app re-reads GET /promotions.
const EventPromotionsUpdated = "promotions.updated"
