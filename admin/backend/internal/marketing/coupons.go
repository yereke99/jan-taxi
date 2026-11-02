package marketing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Coupon is a coupon as passengers and admins see it (openapi MarketingCoupon).
// Validity is decided by expires_at against the database clock: a coupon is
// unusable from that instant even before the expiry job marks it.
type Coupon struct {
	ID              uuid.UUID  `json:"id"`
	CampaignID      uuid.UUID  `json:"campaign_id"`
	CampaignKey     string     `json:"campaign_key"`
	Version         int        `json:"version"`
	UserID          uuid.UUID  `json:"user_id,omitempty"`
	Amount          int64      `json:"amount"`
	MinOrderFare    int64      `json:"min_order_fare"`
	Categories      []string   `json:"categories"`
	Status          string     `json:"status"`
	IssuedAt        time.Time  `json:"issued_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	SourceOrder     *int64     `json:"source_order_number"`
	ReservedOrderID *uuid.UUID `json:"reserved_order_id"`
	RedeemedOrder   *int64     `json:"redeemed_order_number"`
	RedeemedAt      *time.Time `json:"redeemed_at"`
	ClosedAt        *time.Time `json:"closed_at"`
	VoidReason      *string    `json:"void_reason"`
}

const couponColumns = `c.id, c.campaign_id, k.key, v.version, c.user_id, c.amount, c.min_order_fare, c.categories,
	CASE WHEN c.status = 'issued' AND c.expires_at <= now() THEN 'expired' ELSE c.status END,
	c.issued_at, c.expires_at, so.number, c.reserved_order_id, ro.number, c.redeemed_at, c.closed_at, c.void_reason`

const couponFrom = ` FROM marketing_coupons c
	JOIN marketing_campaigns k ON k.id = c.campaign_id
	JOIN marketing_campaign_versions v ON v.id = c.version_id
	LEFT JOIN orders so ON so.id = c.source_order_id
	LEFT JOIN orders ro ON ro.id = c.redeemed_order_id`

func scanCoupon(row pgx.Row) (Coupon, error) {
	var c Coupon
	err := row.Scan(&c.ID, &c.CampaignID, &c.CampaignKey, &c.Version, &c.UserID, &c.Amount, &c.MinOrderFare,
		&c.Categories, &c.Status, &c.IssuedAt, &c.ExpiresAt, &c.SourceOrder, &c.ReservedOrderID, &c.RedeemedOrder,
		&c.RedeemedAt, &c.ClosedAt, &c.VoidReason)
	return c, err
}

// userCoupons returns a passenger's coupons, newest first.
func userCoupons(ctx context.Context, q db.Querier, userID uuid.UUID) ([]Coupon, error) {
	rows, err := q.Query(ctx, `SELECT `+couponColumns+couponFrom+` WHERE c.user_id = $1 ORDER BY c.issued_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, fmt.Errorf("marketing: coupons: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Coupon, error) { return scanCoupon(row) })
}

// listCoupons lists coupons for the admin panel.
func listCoupons(ctx context.Context, q db.Querier, campaignID, userID *uuid.UUID, status string, page httpx.PageParams) ([]Coupon, int, error) {
	cond := ` WHERE ($1::uuid IS NULL OR c.campaign_id = $1) AND ($2::uuid IS NULL OR c.user_id = $2)
		AND ($3 = '' OR (CASE WHEN c.status = 'issued' AND c.expires_at <= now() THEN 'expired' ELSE c.status END) = $3)`
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM marketing_coupons c`+cond, campaignID, userID, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("marketing: count coupons: %w", err)
	}
	rows, err := q.Query(ctx, `SELECT `+couponColumns+couponFrom+cond+`
		ORDER BY c.issued_at DESC, c.id LIMIT $4 OFFSET $5`, campaignID, userID, status, page.PageSize, page.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("marketing: list coupons: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Coupon, error) { return scanCoupon(row) })
	return items, total, err
}

// ExpireCoupons closes issued coupons past their expiry (job
// marketing_coupon_expiry); it only records what expires_at already decides.
func (s *Service) ExpireCoupons(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE marketing_coupons SET status = 'expired', closed_at = now()
		WHERE id IN (SELECT id FROM marketing_coupons WHERE status = 'issued' AND expires_at <= now()
		             ORDER BY expires_at LIMIT 1000 FOR UPDATE SKIP LOCKED)`)
	if err != nil {
		return 0, fmt.Errorf("marketing: expire coupons: %w", err)
	}
	return tag.RowsAffected(), nil
}

// voidCoupon voids an unused coupon on an operator's decision (fraud).
func voidCoupon(ctx context.Context, tx pgx.Tx, id, adminID uuid.UUID, reason string) (Coupon, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE marketing_coupons SET status = 'voided', closed_at = now(), void_reason = $3, voided_by = $2
		WHERE id = $1 AND status = 'issued'`, id, idPtr(adminID), reason)
	if err != nil {
		return Coupon{}, fmt.Errorf("marketing: void coupon: %w", err)
	}
	c, err := scanCoupon(tx.QueryRow(ctx, `SELECT `+couponColumns+couponFrom+` WHERE c.id = $1`, id))
	if db.IsNoRows(err) {
		return Coupon{}, httpx.NotFound()
	}
	if err != nil {
		return Coupon{}, err
	}
	if tag.RowsAffected() == 0 {
		return Coupon{}, httpx.NewError(409, "invalid_transition", "only an unused coupon can be voided").
			WithDetails(map[string]any{"from": c.Status, "action": "void"})
	}
	return c, publishPromotions(ctx, tx, c.UserID)
}
