package marketing

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// snapshotCredits freezes a passenger_credit campaign's recipients when it is
// activated: one pending grant per reachable passenger of the segment. The
// whole snapshot must fit the budget, otherwise activation is refused.
func snapshotCredits(ctx context.Context, tx pgx.Tx, c Campaign) error {
	rules, ok := mustRules[CreditRules](c.Rules)
	if !ok {
		return httpx.Validation(map[string]string{"rules": "invalid"})
	}
	seg, err := loadSegment(ctx, tx, rules.SegmentID)
	if err != nil {
		return err
	}
	fields := map[string]string{}
	f := parseFilters(seg.Audience, seg.Filters, fields)
	if len(fields) > 0 {
		return httpx.Validation(map[string]string{"rules.segment_id": "invalid"})
	}
	var args []any
	from, suppressed := audienceQuery(AudiencePassengers, f, &args)
	var reachable int64
	if err := tx.QueryRow(ctx, `SELECT count(*)`+from+` AND NOT `+suppressed, args...).Scan(&reachable); err != nil {
		return fmt.Errorf("marketing: credit audience: %w", err)
	}
	if c.BudgetLimit != nil {
		u, err := usage(ctx, tx, c)
		if err != nil {
			return err
		}
		if need := reachable * rules.Amount; u.Committed+u.Spent+need > *c.BudgetLimit {
			return httpx.NewError(409, "budget_insufficient", "the segment does not fit the budget").
				WithDetails(map[string]any{"needed": need, "remaining": max(*c.BudgetLimit-u.Committed-u.Spent, 0),
					"recipients": reachable})
		}
	}
	args = append(args, c.ID, c.VersionID, rules.Amount)
	n := len(args)
	_, err = tx.Exec(ctx, `
		INSERT INTO marketing_grants (campaign_id, version_id, reason, user_id, amount, status)
		SELECT $`+itoa(n-2)+`, $`+itoa(n-1)+`, 'passenger_credit', u.id, $`+itoa(n)+`, 'pending'`+
		from+` AND NOT `+suppressed+` ON CONFLICT DO NOTHING`, args...)
	if err != nil {
		return fmt.Errorf("marketing: snapshot credits: %w", err)
	}
	return nil
}

// CreditPending pays pending passenger_credit grants in small batches (job
// marketing_grants). A grant of a campaign that is no longer active waits
// (paused) or was skipped (ended); with awards off nothing is paid.
func (s *Service) CreditPending(ctx context.Context) (int, error) {
	if !settings.Marketing(ctx, s.pool).Awards {
		return 0, nil
	}
	paid := 0
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT g.id, g.user_id, g.amount, g.campaign_id FROM marketing_grants g
			JOIN marketing_campaigns c ON c.id = g.campaign_id
			WHERE g.status = 'pending' AND g.reason = 'passenger_credit' AND c.status = 'active'
			ORDER BY g.created_at LIMIT 100 FOR UPDATE OF g SKIP LOCKED`)
		if err != nil {
			return err
		}
		type pending struct {
			id, userID, campaignID uuid.UUID
			amount                 int64
		}
		list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pending, error) {
			var p pending
			err := row.Scan(&p.id, &p.userID, &p.amount, &p.campaignID)
			return p, err
		})
		if err != nil {
			return err
		}
		campaigns := map[uuid.UUID]Campaign{}
		for _, p := range list {
			c, ok := campaigns[p.campaignID]
			if !ok {
				if c, err = loadCampaign(ctx, tx, p.campaignID, false); err != nil {
					return err
				}
				campaigns[p.campaignID] = c
			}
			if err := creditPassengerGrant(ctx, tx, p.id, p.userID, c, p.amount, uuid.Nil, "passenger credit"); err != nil {
				return err
			}
			if err := notifications.Enqueue(ctx, tx, notifications.Notification{
				Bot: notifications.PassengerBot, UserID: p.userID, Template: notifications.TplPromoBonusGranted,
				Params:    map[string]any{"amount": p.amount, "comment": c.Title.KK},
				DedupeKey: "promo_bonus_granted:" + p.id.String(),
			}); err != nil {
				return err
			}
			if err := publishPromotions(ctx, tx, p.userID); err != nil {
				return err
			}
			paid++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if paid > 0 {
		s.log.Info("passenger credits paid", slog.Int("count", paid))
	}
	return paid, nil
}

// adjustBonus applies an operator's manual correction of a passenger's bonus
// balance (finance.manage, §23.8): credits go through the manual_adjustments
// campaign (its budget and max_amount apply), debits take only available
// money. key is the client's idempotency key (a UUID).
func adjustBonus(ctx context.Context, tx pgx.Tx, userID uuid.UUID, amount int64, adminID uuid.UUID, key, reason string) error {
	id, err := uuid.Parse(strings.TrimPrefix(strings.TrimSpace(key), "bonus_adjust:"))
	if err != nil || id == uuid.Nil {
		return httpx.Validation(map[string]string{"idempotency_key": "invalid"})
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 300 {
		return httpx.Validation(map[string]string{"reason": "required"})
	}
	if amount == 0 || amount > 1_000_000 || amount < -1_000_000 {
		return httpx.Validation(map[string]string{"amount": "out_of_range"})
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return httpx.NotFound()
	}
	ledgerKey := "bonus_adjust:" + id.String()
	a, err := lockBonus(ctx, tx, userID)
	if err != nil {
		return err
	}
	if amount < 0 {
		_, err := applyBonus(ctx, tx, &a, bonusEntry{typ: BonusAdjustmentDebit, balanceDelta: amount,
			adminID: idPtr(adminID), key: ledgerKey, description: reason})
		return bonusError(err)
	}
	c, err := latestCampaign(ctx, tx, TemplateManualGrant)
	if err != nil {
		return err
	}
	if c == nil {
		return errCampaignConflict("the manual adjustments campaign is missing")
	}
	if rules, ok := mustRules[ManualRules](c.Rules); ok && amount > rules.MaxAmount {
		return httpx.Validation(map[string]string{"amount": "above_max"}).
			WithDetails(map[string]any{"max_amount": rules.MaxAmount})
	}
	// A replay (same key and amount) is answered before the budget check.
	var replay bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM passenger_bonus_ledger WHERE idempotency_key = $1)`,
		ledgerKey).Scan(&replay); err != nil {
		return err
	}
	if !replay {
		allowed, err := budgetAllows(ctx, tx, c.ID, amount)
		if err != nil {
			return err
		}
		if !allowed {
			return httpx.NewError(409, "budget_insufficient", "the manual adjustments budget is used up")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO marketing_grants (campaign_id, version_id, reason, user_id, amount, status, ledger_key,
			                              admin_id, credited_at)
			VALUES ($1, $2, 'manual', $3, $4, 'credited', $5, $6, now())`,
			c.ID, c.VersionID, userID, amount, ledgerKey, idPtr(adminID)); err != nil {
			return fmt.Errorf("marketing: manual grant: %w", err)
		}
	}
	_, err = applyBonus(ctx, tx, &a, bonusEntry{typ: BonusAdjustmentCredit, balanceDelta: amount,
		campaignID: &c.ID, versionID: &c.VersionID, adminID: idPtr(adminID), key: ledgerKey, description: reason})
	return bonusError(err)
}

func bonusError(err error) error {
	switch err {
	case nil:
		return nil
	case errBonusInsufficient:
		return httpx.NewError(409, "insufficient_balance", "the bonus balance does not cover the debit")
	case errBonusKeyConflict:
		return httpx.Conflict("idempotency_key_reused", "idempotency key already used for another adjustment")
	}
	return err
}
