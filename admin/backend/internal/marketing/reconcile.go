package marketing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reconciliation (§23.8): every settled promotion has exactly one driver
// compensation of the same amount; every passenger bonus account equals the
// sum of its ledger and its reservations equal its reserved promotions;
// coupons and promotions agree with their orders; credited grants have their
// ledger rows. One consistent snapshot (REPEATABLE READ).

// Totals are the money figures of the promotions.
type Totals struct {
	DiscountsSettled      int64 `json:"discounts_settled"`
	CompensationCredited  int64 `json:"compensation_credited"`
	CompensationReversed  int64 `json:"compensation_reversed"`
	BonusOutstanding      int64 `json:"bonus_outstanding"`
	BonusReserved         int64 `json:"bonus_reserved"`
	CouponLiability       int64 `json:"coupon_liability"`
	PromotionsReserved    int64 `json:"promotions_reserved"`
	PromotionsUnderReview int   `json:"promotions_under_review"`
	EventsFailed          int   `json:"events_failed"`
	EventsPending         int   `json:"events_pending"`
}

// Issue is one reconciliation finding (ids only, no personal data).
type Issue struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Note string `json:"note,omitempty"`
}

// Reconciliation is GET /admin/marketing/reconciliation.
type Reconciliation struct {
	OK        bool      `json:"ok"`
	CheckedAt time.Time `json:"checked_at"`
	Totals    Totals    `json:"totals"`
	Issues    []Issue   `json:"issues"`
}

var reconcileChecks = []struct {
	kind string
	sql  string
}{
	{"compensation_mismatch", `
		SELECT op.id::text, 'discount ' || op.discount || ' vs ledger ' || COALESCE(l.balance_delta::text, 'none')
		FROM order_promotions op
		LEFT JOIN wallet_ledger l ON l.idempotency_key = op.compensation_key AND l.entry_type = 'promo_compensation'
		WHERE op.status IN ('settled', 'reversed') AND (l.id IS NULL OR l.balance_delta <> op.discount
		      OR l.order_id <> op.order_id)`},
	{"orphan_compensation", `
		SELECT l.id::text, 'no settled promotion for ' || l.idempotency_key FROM wallet_ledger l
		WHERE l.entry_type = 'promo_compensation'
		  AND NOT EXISTS (SELECT 1 FROM order_promotions op WHERE op.compensation_key = l.idempotency_key)`},
	{"reversal_without_status", `
		SELECT l.id::text, l.idempotency_key FROM wallet_ledger l
		WHERE l.entry_type = 'promo_compensation_reversal'
		  AND NOT EXISTS (SELECT 1 FROM order_promotions op
		                  WHERE op.compensation_key || ':reversal' = l.idempotency_key AND op.status = 'reversed')`},
	{"bonus_account_mismatch", `
		SELECT a.user_id::text, 'balance ' || a.balance || '/' || COALESCE(s.b, 0) || ' reserved ' || a.reserved || '/' || COALESCE(s.r, 0)
		FROM passenger_bonus_accounts a
		LEFT JOIN (SELECT user_id, sum(balance_delta) AS b, sum(reserved_delta) AS r FROM passenger_bonus_ledger GROUP BY user_id) s
		       ON s.user_id = a.user_id
		WHERE a.balance <> COALESCE(s.b, 0) OR a.reserved <> COALESCE(s.r, 0)`},
	{"bonus_reservation_mismatch", `
		SELECT a.user_id::text, 'reserved ' || a.reserved || ' vs promotions ' || COALESCE(p.d, 0)
		FROM passenger_bonus_accounts a
		LEFT JOIN (SELECT user_id, sum(discount) AS d FROM order_promotions WHERE kind = 'bonus' AND status = 'reserved'
		           GROUP BY user_id) p ON p.user_id = a.user_id
		WHERE a.reserved <> COALESCE(p.d, 0)`},
	{"stuck_reservation", `
		SELECT op.id::text, 'order ' || o.status FROM order_promotions op JOIN orders o ON o.id = op.order_id
		WHERE op.status = 'reserved' AND o.status NOT IN ('searching', 'accepted', 'driver_arriving', 'in_progress', 'disputed')`},
	{"coupon_promotion_mismatch", `
		SELECT c.id::text, 'coupon ' || c.status FROM marketing_coupons c
		WHERE (c.status = 'reserved' AND NOT EXISTS (SELECT 1 FROM order_promotions op
		        WHERE op.coupon_id = c.id AND op.status = 'reserved' AND op.order_id = c.reserved_order_id))
		   OR (c.status = 'redeemed' AND NOT EXISTS (SELECT 1 FROM order_promotions op
		        WHERE op.coupon_id = c.id AND op.status IN ('settled', 'reversed') AND op.order_id = c.redeemed_order_id))`},
	{"grant_without_ledger", `
		SELECT g.id::text, g.reason FROM marketing_grants g
		WHERE g.status = 'credited' AND g.user_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM passenger_bonus_ledger l WHERE l.idempotency_key = g.ledger_key AND l.balance_delta = g.amount)`},
	{"driver_grant_without_ledger", `
		SELECT g.id::text, g.reason FROM marketing_grants g
		WHERE g.status = 'credited' AND g.driver_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM wallet_ledger l WHERE l.idempotency_key = g.ledger_key AND l.balance_delta = g.amount)`},
	{"referral_cap_exceeded", `
		SELECT user_id::text, count(*)::text || ' rewards' FROM marketing_grants
		WHERE reason = 'referral_reward' GROUP BY campaign_id, user_id HAVING count(*) > 1`},
	{"order_discount_mismatch", `
		SELECT o.id::text, 'order ' || o.promo_discount || ' vs promotion ' || COALESCE(op.discount, 0)
		FROM orders o LEFT JOIN order_promotions op ON op.order_id = o.id
		WHERE o.promo_discount <> COALESCE(op.discount, 0)
		  AND (o.promo_discount > 0 OR op.id IS NOT NULL)`},
}

// Reconcile runs every check and computes the totals.
func Reconcile(ctx context.Context, pool *pgxpool.Pool) (Reconciliation, error) {
	rep := Reconciliation{Issues: []Issue{}}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return rep, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&rep.CheckedAt); err != nil {
		return rep, err
	}
	for _, c := range reconcileChecks {
		rows, err := tx.Query(ctx, c.sql+` LIMIT 100`)
		if err != nil {
			return rep, fmt.Errorf("marketing: reconcile %s: %w", c.kind, err)
		}
		for rows.Next() {
			var is Issue
			if err := rows.Scan(&is.Ref, &is.Note); err != nil {
				rows.Close()
				return rep, err
			}
			is.Kind = c.kind
			rep.Issues = append(rep.Issues, is)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return rep, err
		}
	}
	t := &rep.Totals
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(discount) FILTER (WHERE status IN ('settled', 'reversed')), 0)::bigint,
		       COALESCE(sum(discount) FILTER (WHERE status = 'reserved'), 0)::bigint,
		       count(*) FILTER (WHERE review_reason IS NOT NULL AND status = 'settled')
		FROM order_promotions`).Scan(&t.DiscountsSettled, &t.PromotionsReserved, &t.PromotionsUnderReview); err != nil {
		return rep, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(balance_delta) FILTER (WHERE entry_type = 'promo_compensation'), 0)::bigint,
		       COALESCE(-sum(balance_delta) FILTER (WHERE entry_type = 'promo_compensation_reversal'), 0)::bigint
		FROM wallet_ledger WHERE entry_type IN ('promo_compensation', 'promo_compensation_reversal')`).
		Scan(&t.CompensationCredited, &t.CompensationReversed); err != nil {
		return rep, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(balance), 0)::bigint, COALESCE(sum(reserved), 0)::bigint FROM passenger_bonus_accounts`).
		Scan(&t.BonusOutstanding, &t.BonusReserved); err != nil {
		return rep, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(amount), 0)::bigint FROM marketing_coupons
		WHERE status = 'reserved' OR (status = 'issued' AND expires_at > now())`).Scan(&t.CouponLiability); err != nil {
		return rep, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'failed'), count(*) FILTER (WHERE status = 'pending')
		FROM marketing_events`).Scan(&t.EventsFailed, &t.EventsPending); err != nil {
		return rep, err
	}
	if t.DiscountsSettled != t.CompensationCredited {
		rep.Issues = append(rep.Issues, Issue{Kind: "totals_mismatch",
			Note: fmt.Sprintf("discounts %d vs compensation %d", t.DiscountsSettled, t.CompensationCredited)})
	}
	if t.EventsFailed > 0 {
		rep.Issues = append(rep.Issues, Issue{Kind: "failed_events", Note: fmt.Sprintf("%d", t.EventsFailed)})
	}
	rep.OK = len(rep.Issues) == 0
	return rep, nil
}

// ReconcileJob logs the reconciliation (job marketing_reconcile); a mismatch
// is an error for the logs and the admin overview.
func (s *Service) ReconcileJob(ctx context.Context) error {
	rep, err := Reconcile(ctx, s.pool)
	if err != nil {
		return err
	}
	if !rep.OK {
		s.log.Error("marketing reconciliation found issues", slog.Int("issues", len(rep.Issues)),
			slog.String("first", rep.Issues[0].Kind))
	}
	return nil
}
