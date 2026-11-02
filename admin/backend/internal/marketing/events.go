package marketing

import (
	"context"
	"encoding/json"

	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/internal/wallets"
)

// The award worker (§23.3) consumes marketing_events: each completed ride is
// processed once, in its own transaction, against the campaigns live at the
// ride's completion. Awards are written with unique keys and constraints, so
// a retried or replayed event never awards twice; Telegram messages go
// through the outbox and never roll an award back.

const (
	eventBatch       = 20
	eventMaxAttempts = 10
	// eventMaxAge: rides queued longer ago (a stopped worker) earn nothing,
	// so a late restart cannot award a backlog unexpectedly.
	eventMaxAge = 7 * 24 * time.Hour
)

// ProcessEvents handles due events (job marketing_events) and returns how
// many it finished.
func (s *Service) ProcessEvents(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM marketing_events WHERE status = 'pending' AND next_attempt_at <= now()
		ORDER BY id LIMIT $1`, eventBatch)
	if err != nil {
		return 0, fmt.Errorf("marketing: due events: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return done, nil
		}
		if err := s.processEvent(ctx, id); err != nil {
			s.log.Error("marketing event failed", slog.Int64("event", id), slog.Any("err", err))
			s.retryEvent(ctx, id, err)
			continue
		}
		done++
	}
	return done, nil
}

func (s *Service) processEvent(ctx context.Context, id int64) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var orderID uuid.UUID
		var occurred time.Time
		var status string
		err := tx.QueryRow(ctx, `
			SELECT order_id, occurred_at, status FROM marketing_events WHERE id = $1 FOR UPDATE SKIP LOCKED`, id).
			Scan(&orderID, &occurred, &status)
		if db.IsNoRows(err) {
			return nil // another worker holds it
		}
		if err != nil {
			return err
		}
		if status != "pending" {
			return nil
		}
		result := map[string]any{}
		if time.Since(occurred) > eventMaxAge {
			result["skipped"] = "too_old"
		} else if err := s.rideCompleted(ctx, tx, orderID, result); err != nil {
			return err
		}
		raw, _ := json.Marshal(result)
		_, err = tx.Exec(ctx, `
			UPDATE marketing_events SET status = 'done', processed_at = now(), result = $2, attempts = attempts + 1,
			       last_error = NULL
			WHERE id = $1`, id, raw)
		return err
	})
}

func (s *Service) retryEvent(ctx context.Context, id int64, cause error) {
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE marketing_events
		SET attempts = attempts + 1, last_error = $2,
		    status = CASE WHEN attempts + 1 >= $3 THEN 'failed' ELSE 'pending' END,
		    next_attempt_at = now() + make_interval(secs => LEAST(600, 5 * power(2, attempts)))
		WHERE id = $1 AND status = 'pending'`, id, msg, eventMaxAttempts)
	if err != nil {
		s.log.Error("marketing event retry not recorded", slog.Int64("event", id), slog.Any("err", err))
	}
}

// ride is a completed order as the award rules see it.
type ride struct {
	id          uuid.UUID
	number      int64
	passengerID uuid.UUID
	driverID    *uuid.UUID
	category    string
	status      string
	completedAt *time.Time
	startedAt   *time.Time
}

func loadRide(ctx context.Context, q db.Querier, orderID uuid.UUID) (ride, error) {
	var r ride
	err := q.QueryRow(ctx, `
		SELECT id, number, passenger_id, driver_id, category, status, completed_at, started_at
		FROM orders WHERE id = $1`, orderID).Scan(&r.id, &r.number, &r.passengerID, &r.driverID, &r.category,
		&r.status, &r.completedAt, &r.startedAt)
	return r, err
}

// isFirstRide reports whether r is the passenger's first completed order of
// any kind (orders completed and later disputed still count as taken).
func isFirstRide(ctx context.Context, q db.Querier, r ride) (bool, error) {
	var earlier bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM orders WHERE passenger_id = $1 AND id <> $2 AND completed_at IS NOT NULL
		                AND (completed_at, id) < ($3, $2))`, r.passengerID, r.id, *r.completedAt).Scan(&earlier)
	return !earlier, err
}

// rideCompleted applies every live campaign to one completed ride.
func (s *Service) rideCompleted(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, result map[string]any) error {
	r, err := loadRide(ctx, tx, orderID)
	if err != nil {
		return err
	}
	if r.completedAt == nil || (r.status != "completed" && r.status != "disputed") {
		result["skipped"] = "not_completed"
		return nil
	}
	if !settings.Marketing(ctx, tx).Awards {
		result["skipped"] = "awards_disabled"
		return nil
	}
	live, err := liveCampaigns(ctx, tx, []string{TemplateFirstRideCoupon, TemplateReferral, TemplateDriverMilestone}, r.completedAt)
	if err != nil {
		return err
	}
	first, err := isFirstRide(ctx, tx, r)
	if err != nil {
		return err
	}
	for _, c := range live {
		// A ride completed before the current active period earns nothing.
		if c.ActiveSince == nil || r.completedAt.Before(*c.ActiveSince) {
			continue
		}
		var outcome string
		switch c.Template {
		case TemplateFirstRideCoupon:
			outcome, err = s.issueFirstRideCoupon(ctx, tx, c, r, first)
		case TemplateReferral:
			outcome, err = s.rewardReferral(ctx, tx, c, r, first)
		case TemplateDriverMilestone:
			outcome, err = s.driverMilestone(ctx, tx, c, r)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", c.Key, err)
		}
		if outcome != "" {
			result[c.Key] = outcome
		}
	}
	return nil
}

// issueFirstRideCoupon issues the first-ride coupon (§23.4): once per
// passenger and campaign, only for the passenger's first completed order and
// only when it is in the campaign's categories.
func (s *Service) issueFirstRideCoupon(ctx context.Context, tx pgx.Tx, c Campaign, r ride, first bool) (string, error) {
	rules, ok := mustRules[CouponRules](c.Rules)
	if !ok {
		return "bad_rules", nil
	}
	if !first {
		return "not_first_ride", nil
	}
	if !slices.Contains(rules.Categories, r.category) {
		return "category_not_eligible", nil
	}
	allowed, err := budgetAllows(ctx, tx, c.ID, rules.Amount)
	if err != nil {
		return "", err
	}
	if !allowed {
		s.log.Warn("coupon not issued: budget exhausted", slog.String("campaign", c.Key))
		return "budget_exhausted", nil
	}
	var couponID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO marketing_coupons (campaign_id, version_id, user_id, amount, min_order_fare, categories,
		                               source_order_id, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now() + make_interval(hours => $8))
		ON CONFLICT (campaign_id, user_id) DO NOTHING
		RETURNING id`, c.ID, c.VersionID, r.passengerID, rules.Amount, rules.MinOrderFare, rules.Categories,
		r.id, rules.ValidityHours).Scan(&couponID)
	if db.IsNoRows(err) {
		return "already_issued", nil
	}
	if err != nil {
		return "", fmt.Errorf("issue coupon: %w", err)
	}
	if err := notifications.Enqueue(ctx, tx, notifications.Notification{
		Bot: notifications.PassengerBot, UserID: r.passengerID, Template: notifications.TplPromoCouponIssued,
		Params:    map[string]any{"amount": rules.Amount, "validity": validityText(rules.ValidityHours)},
		DedupeKey: "promo_coupon_issued:" + couponID.String(),
	}); err != nil {
		return "", err
	}
	return "coupon_issued", publishPromotions(ctx, tx, r.passengerID)
}

// rewardReferral rewards the referrer when the invitee's first completed
// ride qualifies (§23.5): 300 ₸ once per referrer per campaign, whatever the
// number of friends; the invitee gets nothing from this campaign.
func (s *Service) rewardReferral(ctx context.Context, tx pgx.Tx, c Campaign, r ride, first bool) (string, error) {
	rules, ok := mustRules[ReferralRules](c.Rules)
	if !ok {
		return "bad_rules", nil
	}
	var attID, referrerID uuid.UUID
	var status string
	err := tx.QueryRow(ctx, `
		SELECT id, referrer_user_id, status FROM referral_attributions WHERE invitee_user_id = $1 FOR UPDATE`,
		r.passengerID).Scan(&attID, &referrerID, &status)
	if db.IsNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if status != ReferralAttributed {
		return "referral_" + status, nil
	}
	if !first {
		return "not_first_ride", nil
	}
	if !slices.Contains(rules.Categories, r.category) {
		return s.decideReferral(ctx, tx, attID, r, ReferralRejected, "category_not_eligible")
	}
	if reason, err := referralFraud(ctx, tx, referrerID, r, rules); err != nil {
		return "", err
	} else if reason != "" {
		decision := ReferralRejected
		if reason == "short_ride" {
			decision = ReferralReview
		}
		return s.decideReferral(ctx, tx, attID, r, decision, reason)
	}
	return s.payReferral(ctx, tx, c, attID, referrerID, r.id, rules.RewardAmount, uuid.Nil)
}

// referralFraud returns why a qualifying ride may not earn the reward
// automatically, or "".
func referralFraud(ctx context.Context, q db.Querier, referrerID uuid.UUID, r ride, rules ReferralRules) (string, error) {
	var blocked, samePhone, referrerDrove bool
	err := q.QueryRow(ctx, `
		SELECT ru.is_blocked,
		       ru.phone_e164 IS NOT NULL AND ru.phone_e164 = iu.phone_e164,
		       EXISTS (SELECT 1 FROM driver_profiles d WHERE d.id = $3 AND d.user_id = ru.id)
		FROM users ru, users iu WHERE ru.id = $1 AND iu.id = $2`, referrerID, r.passengerID, r.driverID).
		Scan(&blocked, &samePhone, &referrerDrove)
	if err != nil {
		return "", err
	}
	switch {
	case blocked:
		return "referrer_blocked", nil
	case samePhone:
		return "same_phone", nil
	case referrerDrove:
		return "referrer_drove", nil
	}
	if rules.MinRideSeconds > 0 && r.startedAt != nil &&
		r.completedAt.Sub(*r.startedAt) < time.Duration(rules.MinRideSeconds)*time.Second {
		return "short_ride", nil
	}
	return "", nil
}

func (s *Service) decideReferral(ctx context.Context, tx pgx.Tx, attID uuid.UUID, r ride, status, reason string) (string, error) {
	_, err := tx.Exec(ctx, `
		UPDATE referral_attributions SET status = $2, reason = $3, qualifying_order_id = $4, qualified_at = now()
		WHERE id = $1`, attID, status, reason, r.id)
	return "referral_" + status + ":" + reason, err
}

// payReferral credits the referral reward (also used when an operator
// approves a referral held for review). The grant is unique per referrer and
// campaign: a second qualifying friend ends as capped.
func (s *Service) payReferral(ctx context.Context, tx pgx.Tx, c Campaign, attID, referrerID, orderID uuid.UUID,
	amount int64, adminID uuid.UUID) (string, error) {
	var already bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM marketing_grants WHERE campaign_id = $1 AND user_id = $2 AND reason = 'referral_reward')`,
		c.ID, referrerID).Scan(&already); err != nil {
		return "", err
	}
	if already {
		_, err := tx.Exec(ctx, `
			UPDATE referral_attributions SET status = 'capped', reason = 'already_rewarded', qualifying_order_id = $2,
			       qualified_at = COALESCE(qualified_at, now())
			WHERE id = $1`, attID, orderID)
		return "referral_capped", err
	}
	allowed, err := budgetAllows(ctx, tx, c.ID, amount)
	if err != nil {
		return "", err
	}
	if !allowed {
		_, err := tx.Exec(ctx, `
			UPDATE referral_attributions SET status = 'review', reason = 'budget_exhausted', qualifying_order_id = $2,
			       qualified_at = COALESCE(qualified_at, now())
			WHERE id = $1`, attID, orderID)
		return "referral_review:budget_exhausted", err
	}
	var grantID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO marketing_grants (campaign_id, version_id, reason, user_id, amount, status, order_id, referral_id, admin_id)
		VALUES ($1, $2, 'referral_reward', $3, $4, 'pending', $5, $6, $7)
		ON CONFLICT DO NOTHING RETURNING id`, c.ID, c.VersionID, referrerID, amount, orderID, attID, idPtr(adminID)).
		Scan(&grantID)
	if db.IsNoRows(err) {
		_, err := tx.Exec(ctx, `
			UPDATE referral_attributions SET status = 'capped', reason = 'already_rewarded', qualifying_order_id = $2
			WHERE id = $1`, attID, orderID)
		return "referral_capped", err
	}
	if err != nil {
		return "", fmt.Errorf("referral grant: %w", err)
	}
	if err := creditPassengerGrant(ctx, tx, grantID, referrerID, c, amount, adminID, "referral reward"); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE referral_attributions SET status = 'rewarded', reason = NULL, qualifying_order_id = $2,
		       qualified_at = COALESCE(qualified_at, now()), decided_at = CASE WHEN $3::uuid IS NULL THEN decided_at ELSE now() END,
		       decided_by = COALESCE($3, decided_by)
		WHERE id = $1`, attID, orderID, idPtr(adminID)); err != nil {
		return "", err
	}
	if err := notifications.Enqueue(ctx, tx, notifications.Notification{
		Bot: notifications.PassengerBot, UserID: referrerID, Template: notifications.TplPromoReferralReward,
		Params: map[string]any{"amount": amount}, DedupeKey: "promo_referral_reward:" + grantID.String(),
	}); err != nil {
		return "", err
	}
	return "referral_rewarded", publishPromotions(ctx, tx, referrerID)
}

// creditPassengerGrant credits a pending passenger grant to the bonus balance
// (ledger grant, key "grant:<grant_id>") and marks it credited.
func creditPassengerGrant(ctx context.Context, tx pgx.Tx, grantID, userID uuid.UUID, c Campaign, amount int64,
	adminID uuid.UUID, description string) error {
	a, err := lockBonus(ctx, tx, userID)
	if err != nil {
		return err
	}
	key := "grant:" + grantID.String()
	if _, err := applyBonus(ctx, tx, &a, bonusEntry{
		typ: BonusGrant, balanceDelta: amount, campaignID: &c.ID, versionID: &c.VersionID,
		adminID: idPtr(adminID), key: key, description: description,
	}); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE marketing_grants SET status = 'credited', credited_at = now(), ledger_key = $2 WHERE id = $1`, grantID, key)
	return err
}

// driverMilestone credits a driver once when the ride makes them reach the
// campaign's number of completed rides inside its window.
func (s *Service) driverMilestone(ctx context.Context, tx pgx.Tx, c Campaign, r ride) (string, error) {
	rules, ok := mustRules[MilestoneRules](c.Rules)
	if !ok || r.driverID == nil || c.StartsAt == nil || c.EndsAt == nil {
		return "", nil
	}
	if !slices.Contains(rules.Categories, r.category) {
		return "", nil
	}
	var rides int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM orders
		WHERE driver_id = $1 AND status = 'completed' AND category = ANY($2) AND completed_at >= $3 AND completed_at < $4`,
		*r.driverID, rules.Categories, *c.StartsAt, *c.EndsAt).Scan(&rides); err != nil {
		return "", err
	}
	if rides < rules.Rides {
		return "", nil
	}
	allowed, err := budgetAllows(ctx, tx, c.ID, rules.Amount)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "milestone_budget_exhausted", nil
	}
	var grantID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO marketing_grants (campaign_id, version_id, reason, driver_id, amount, status, order_id)
		VALUES ($1, $2, 'driver_milestone', $3, $4, 'pending', $5)
		ON CONFLICT DO NOTHING RETURNING id`, c.ID, c.VersionID, *r.driverID, rules.Amount, r.id).Scan(&grantID)
	if db.IsNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	comment := c.Title.KK
	if err := wallets.CreditBonus(ctx, tx, *r.driverID, rules.Amount, uuid.Nil, grantID.String(), comment); err != nil {
		return "", fmt.Errorf("milestone bonus: %w", err)
	}
	key, _ := wallets.BonusKey(grantID.String())
	if _, err := tx.Exec(ctx, `
		UPDATE marketing_grants SET status = 'credited', credited_at = now(), ledger_key = $2 WHERE id = $1`,
		grantID, key); err != nil {
		return "", err
	}
	var userID uuid.UUID
	var balance int64
	if err := tx.QueryRow(ctx, `
		SELECT d.user_id, w.balance FROM driver_profiles d JOIN wallets w ON w.driver_id = d.id WHERE d.id = $1`,
		*r.driverID).Scan(&userID, &balance); err != nil {
		return "", err
	}
	if err := notifications.Enqueue(ctx, tx, notifications.Notification{
		Bot: notifications.DriverBot, UserID: userID, Template: notifications.TplDriverBonusGranted,
		Params:    map[string]any{"amount": rules.Amount, "balance": balance, "comment": comment},
		DedupeKey: "driver_bonus_granted:" + grantID.String(),
	}); err != nil {
		return "", err
	}
	return "milestone_credited", publishWallet(ctx, tx, *r.driverID)
}

// validityText renders a coupon validity for the notification: whole days
// when the hours divide evenly ("3 күн"), else hours.
func validityText(hours int) map[string]string {
	if hours%24 == 0 {
		d := hours / 24
		return map[string]string{"kk": strconv.Itoa(d) + " күн", "ru": strconv.Itoa(d) + " " + ruPlural(d, "день", "дня", "дней"),
			"en": strconv.Itoa(d) + " " + enPlural(d, "day", "days")}
	}
	return map[string]string{"kk": strconv.Itoa(hours) + " сағат", "ru": strconv.Itoa(hours) + " " + ruPlural(hours, "час", "часа", "часов"),
		"en": strconv.Itoa(hours) + " " + enPlural(hours, "hour", "hours")}
}

func ruPlural(n int, one, few, many string) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return few
	}
	return many
}

func enPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
