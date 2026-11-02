package marketing

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// reportZone is the business time zone of the daily marketing figures (the
// dashboard's zone).
const reportZone = "Asia/Aqtobe"

// Overview is GET /admin/marketing/overview: aggregates only.
type Overview struct {
	Switches   settings.MarketingSwitches `json:"switches"`
	Campaigns  []Campaign                 `json:"campaigns"`
	Coupons    map[string]int             `json:"coupons"`
	Referrals  map[string]int             `json:"referrals"`
	Broadcasts map[string]int             `json:"broadcasts"`
	Bonus      struct {
		Outstanding int64 `json:"outstanding"`
		Reserved    int64 `json:"reserved"`
		Accounts    int   `json:"accounts"`
	} `json:"bonus"`
	Compensation struct {
		Credited int64 `json:"credited"`
		Pending  int64 `json:"pending"`
	} `json:"compensation"`
	Health struct {
		EventsPending     int  `json:"events_pending"`
		EventsFailed      int  `json:"events_failed"`
		OldestPendingS    *int `json:"oldest_pending_s"`
		StuckReservations int  `json:"stuck_reservations"`
		ReviewPromotions  int  `json:"review_promotions"`
		ReviewReferrals   int  `json:"review_referrals"`
		BroadcastQueue    int  `json:"broadcast_queue"`
		OverdueScheduled  int  `json:"overdue_scheduled"`
	} `json:"health"`
}

func countBy(ctx context.Context, q db.Querier, sql string) (map[string]int, error) {
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

func (s *Service) adminOverview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var o Overview
	o.Switches = settings.Marketing(ctx, s.pool)
	var err error
	if o.Campaigns, _, err = listCampaigns(ctx, s.pool, "", "", "", httpx.PageParams{Page: 1, PageSize: 100}); err != nil {
		return err
	}
	if o.Coupons, err = countBy(ctx, s.pool, `
		SELECT CASE WHEN status = 'issued' AND expires_at <= now() THEN 'expired' ELSE status END, count(*)
		FROM marketing_coupons GROUP BY 1`); err != nil {
		return err
	}
	if o.Referrals, err = countBy(ctx, s.pool, `SELECT status, count(*) FROM referral_attributions GROUP BY 1`); err != nil {
		return err
	}
	if o.Broadcasts, err = countBy(ctx, s.pool, `SELECT status, count(*) FROM marketing_broadcasts GROUP BY 1`); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(balance), 0)::bigint, COALESCE(sum(reserved), 0)::bigint, count(*) FILTER (WHERE balance > 0)
		FROM passenger_bonus_accounts`).Scan(&o.Bonus.Outstanding, &o.Bonus.Reserved, &o.Bonus.Accounts); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(discount) FILTER (WHERE status IN ('settled', 'reversed')), 0)::bigint,
		       COALESCE(sum(discount) FILTER (WHERE status = 'reserved'), 0)::bigint,
		       count(*) FILTER (WHERE review_reason IS NOT NULL AND status = 'settled')
		FROM order_promotions`).Scan(&o.Compensation.Credited, &o.Compensation.Pending, &o.Health.ReviewPromotions); err != nil {
		return err
	}
	h := &o.Health
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'pending'), count(*) FILTER (WHERE status = 'failed'),
		       EXTRACT(EPOCH FROM now() - min(created_at) FILTER (WHERE status = 'pending'))::int
		FROM marketing_events`).Scan(&h.EventsPending, &h.EventsFailed, &h.OldestPendingS); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM order_promotions op JOIN orders o ON o.id = op.order_id
		   WHERE op.status = 'reserved' AND o.status IN ('completed', 'cancelled')),
		  (SELECT count(*) FROM referral_attributions WHERE status = 'review'),
		  (SELECT count(*) FROM marketing_broadcast_recipients WHERE status IN ('queued', 'sending')),
		  (SELECT count(*) FROM marketing_campaigns WHERE status = 'scheduled' AND scheduled_at < now() - interval '5 minutes')
		  + (SELECT count(*) FROM marketing_broadcasts WHERE status = 'scheduled' AND scheduled_at < now() - interval '5 minutes')`).
		Scan(&h.StuckReservations, &h.ReviewReferrals, &h.BroadcastQueue, &h.OverdueScheduled); err != nil {
		return err
	}
	return httpx.OK(w, o)
}

// ReportDay is one day of marketing figures.
type ReportDay struct {
	Date             string `json:"date"`
	CouponsIssued    int    `json:"coupons_issued"`
	CouponsRedeemed  int    `json:"coupons_redeemed"`
	CouponsExpired   int    `json:"coupons_expired"`
	Referrals        int    `json:"referrals"`
	ReferralRewards  int    `json:"referral_rewards"`
	BonusGranted     int64  `json:"bonus_granted"`
	DiscountsSettled int64  `json:"discounts_settled"`
	MessagesSent     int    `json:"messages_sent"`
	MessagesFailed   int    `json:"messages_failed"`
}

// Report is GET /admin/marketing/reports?from=YYYY-MM-DD&to=YYYY-MM-DD.
type Report struct {
	From     string      `json:"from"`
	To       string      `json:"to"`
	Timezone string      `json:"timezone"`
	Days     []ReportDay `json:"days"`
	Funnel   struct {
		Attributed int `json:"attributed"`
		Qualified  int `json:"qualified"`
		Rewarded   int `json:"rewarded"`
		Capped     int `json:"capped"`
		Issued     int `json:"coupons_issued"`
		Redeemed   int `json:"coupons_redeemed"`
	} `json:"funnel"`
}

func reportPeriod(r *http.Request) (from, to time.Time, loc *time.Location, err error) {
	loc, lerr := time.LoadLocation(reportZone)
	if lerr != nil {
		loc = time.FixedZone("UTC+5", 5*3600)
	}
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from, to = today.AddDate(0, 0, -29), today.AddDate(0, 0, 1)
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		if from, err = time.ParseInLocation("2006-01-02", v, loc); err != nil {
			return from, to, loc, httpx.Validation(map[string]string{"from": "invalid"})
		}
	}
	if v := q.Get("to"); v != "" {
		t, perr := time.ParseInLocation("2006-01-02", v, loc)
		if perr != nil {
			return from, to, loc, httpx.Validation(map[string]string{"to": "invalid"})
		}
		to = t.AddDate(0, 0, 1)
	}
	if !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		return from, to, loc, httpx.Validation(map[string]string{"to": "out_of_range"})
	}
	return from, to, loc, nil
}

func (s *Service) buildReport(ctx context.Context, from, to time.Time, loc *time.Location) (Report, error) {
	rep := Report{From: from.Format("2006-01-02"), To: to.AddDate(0, 0, -1).Format("2006-01-02"), Timezone: loc.String()}
	rows, err := s.pool.Query(ctx, `
		WITH days AS (
			SELECT d::date AS day FROM generate_series($1::timestamptz AT TIME ZONE $3,
			       ($2::timestamptz AT TIME ZONE $3) - interval '1 day', interval '1 day') d
		)
		SELECT to_char(day, 'YYYY-MM-DD'),
		  (SELECT count(*) FROM marketing_coupons WHERE (issued_at AT TIME ZONE $3)::date = day),
		  (SELECT count(*) FROM marketing_coupons WHERE (redeemed_at AT TIME ZONE $3)::date = day),
		  (SELECT count(*) FROM marketing_coupons WHERE status = 'expired' AND (closed_at AT TIME ZONE $3)::date = day),
		  (SELECT count(*) FROM referral_attributions WHERE (attributed_at AT TIME ZONE $3)::date = day),
		  (SELECT count(*) FROM marketing_grants WHERE reason = 'referral_reward' AND status = 'credited'
		     AND (credited_at AT TIME ZONE $3)::date = day),
		  (SELECT COALESCE(sum(amount), 0)::bigint FROM marketing_grants WHERE status = 'credited' AND user_id IS NOT NULL
		     AND (credited_at AT TIME ZONE $3)::date = day),
		  (SELECT COALESCE(sum(discount), 0)::bigint FROM order_promotions WHERE status IN ('settled', 'reversed')
		     AND (settled_at AT TIME ZONE $3)::date = day),
		  (SELECT count(*) FROM marketing_broadcast_recipients WHERE status = 'sent' AND NOT is_test
		     AND (sent_at AT TIME ZONE $3)::date = day),
		  (SELECT count(*) FROM marketing_broadcast_recipients WHERE status IN ('failed', 'blocked') AND NOT is_test
		     AND (updated_at AT TIME ZONE $3)::date = day)
		FROM days ORDER BY day`, from, to, loc.String())
	if err != nil {
		return rep, err
	}
	rep.Days, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (ReportDay, error) {
		var d ReportDay
		err := row.Scan(&d.Date, &d.CouponsIssued, &d.CouponsRedeemed, &d.CouponsExpired, &d.Referrals,
			&d.ReferralRewards, &d.BonusGranted, &d.DiscountsSettled, &d.MessagesSent, &d.MessagesFailed)
		return d, err
	})
	if err != nil {
		return rep, err
	}
	f := &rep.Funnel
	err = s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM referral_attributions WHERE attributed_at >= $1 AND attributed_at < $2),
		  (SELECT count(*) FROM referral_attributions WHERE attributed_at >= $1 AND attributed_at < $2 AND qualified_at IS NOT NULL),
		  (SELECT count(*) FROM referral_attributions WHERE attributed_at >= $1 AND attributed_at < $2 AND status = 'rewarded'),
		  (SELECT count(*) FROM referral_attributions WHERE attributed_at >= $1 AND attributed_at < $2 AND status = 'capped'),
		  (SELECT count(*) FROM marketing_coupons WHERE issued_at >= $1 AND issued_at < $2),
		  (SELECT count(*) FROM marketing_coupons WHERE issued_at >= $1 AND issued_at < $2 AND status = 'redeemed')`,
		from, to).Scan(&f.Attributed, &f.Qualified, &f.Rewarded, &f.Capped, &f.Issued, &f.Redeemed)
	return rep, err
}

func (s *Service) adminReports(w http.ResponseWriter, r *http.Request) error {
	from, to, loc, err := reportPeriod(r)
	if err != nil {
		return err
	}
	rep, err := s.buildReport(r.Context(), from, to, loc)
	if err != nil {
		return err
	}
	return httpx.OK(w, rep)
}

// adminReportsCSV exports the daily aggregates (no personal data).
func (s *Service) adminReportsCSV(w http.ResponseWriter, r *http.Request) error {
	from, to, loc, err := reportPeriod(r)
	if err != nil {
		return err
	}
	rep, err := s.buildReport(r.Context(), from, to, loc)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="marketing.csv"`)
	w.Header().Set("Cache-Control", "private, no-store")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"date", "coupons_issued", "coupons_redeemed", "coupons_expired", "referrals",
		"referral_rewards", "bonus_granted", "discounts_settled", "messages_sent", "messages_failed"})
	for _, d := range rep.Days {
		_ = cw.Write([]string{d.Date, strconv.Itoa(d.CouponsIssued), strconv.Itoa(d.CouponsRedeemed),
			strconv.Itoa(d.CouponsExpired), strconv.Itoa(d.Referrals), strconv.Itoa(d.ReferralRewards),
			strconv.FormatInt(d.BonusGranted, 10), strconv.FormatInt(d.DiscountsSettled, 10),
			strconv.Itoa(d.MessagesSent), strconv.Itoa(d.MessagesFailed)})
	}
	cw.Flush()
	return cw.Error()
}

// reverseCompensation debits a promotion compensation, mapping wallet errors
// to API errors.
func reverseCompensation(ctx context.Context, tx pgx.Tx, driverID, orderID uuid.UUID, amount int64, key string,
	adminID uuid.UUID, reason string) error {
	err := wallets.ReversePromoCompensation(ctx, tx, driverID, orderID, amount, key, adminID, reason)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, wallets.ErrInsufficientBalance):
		return httpx.NewError(409, "insufficient_balance", "the driver's available balance does not cover the reversal")
	case errors.Is(err, wallets.ErrNotFound):
		return httpx.NewError(409, "conflict", "no compensation to reverse")
	}
	return err
}
