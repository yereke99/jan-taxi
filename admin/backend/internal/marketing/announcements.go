package marketing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// A campaign launch is approved by marketing.approve. Its one announcement
// uses that same approver and the normal broadcast queue, never the ride
// outbox. Resuming or editing a campaign does not notify everyone again.
func queueCampaignAnnouncement(ctx context.Context, tx pgx.Tx, c Campaign, adminID uuid.UUID) error {
	sw := settings.Marketing(ctx, tx)
	if !sw.Awards || !sw.Broadcasts {
		return nil
	}
	audience, target := AudiencePassengers, "coupons"
	var content settings.Localized
	switch c.Template {
	case TemplateReferral:
		rules, ok := mustRules[ReferralRules](c.Rules)
		if !ok {
			return nil
		}
		target = "invite"
		content = settings.Localized{
			KK: fmt.Sprintf("🎁 Досыңызды Jan Taxi-ге шақырып, %d ₸ бонус алыңыз. Бонус досыңыздың алғашқы жарамды сапарынан кейін бір рет беріледі.", rules.RewardAmount),
			RU: fmt.Sprintf("🎁 Пригласите друга в Jan Taxi и получите %d ₸ бонусами. Бонус даётся один раз после первой подходящей поездки друга.", rules.RewardAmount),
			EN: fmt.Sprintf("🎁 Invite a friend to Jan Taxi and earn %d ₸ in bonuses, once, after their first qualifying ride.", rules.RewardAmount)}
	case TemplateFirstRideCoupon:
		rules, ok := mustRules[CouponRules](c.Rules)
		if !ok {
			return nil
		}
		content = settings.Localized{KK: fmt.Sprintf("🎁 Алғашқы сапардан кейін келесі сапарға %d ₸ купон алыңыз. Шарттары қосымшада.", rules.Amount),
			RU: fmt.Sprintf("🎁 После первой поездки получите купон на %d ₸ для следующей. Условия — в приложении.", rules.Amount),
			EN: fmt.Sprintf("🎁 Earn a %d ₸ coupon for a later ride after your first trip. See the terms in the app.", rules.Amount)}
	case TemplateDriverMilestone:
		rules, ok := mustRules[MilestoneRules](c.Rules)
		if !ok {
			return nil
		}
		audience, target = AudienceDrivers, "promotions"
		content = settings.Localized{KK: fmt.Sprintf("🎁 Акция кезінде %d сапар орындап, %d ₸ бонус алыңыз. Прогресс пен шарттар қосымшада.", rules.Rides, rules.Amount),
			RU: fmt.Sprintf("🎁 Выполните %d поездок за время акции и получите %d ₸ бонуса. Прогресс и условия — в приложении.", rules.Rides, rules.Amount),
			EN: fmt.Sprintf("🎁 Complete %d trips during the promotion to earn %d ₸. Track your progress and terms in the app.", rules.Rides, rules.Amount)}
	default:
		return nil
	}
	approver := idPtr(adminID)
	if approver == nil {
		approver = c.ApprovedBy
	}
	if approver == nil {
		return nil
	}
	button := BroadcastButton{Label: settings.Localized{KK: "Сыйлықты ашу", RU: "Открыть подарок", EN: "Open gift"}, StartParam: target}
	raw, _ := json.Marshal(content)
	markup, _ := json.Marshal(button)
	var id uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO marketing_broadcasts (campaign_id,title,audience,content,button,status,scheduled_at,
 approved_by,approval_note,created_by,is_automatic)
 VALUES($1,$2,$3,$4,$5,'scheduled',GREATEST(now(),COALESCE($6,now())),$7,'Approved with campaign activation',$7,true)
 ON CONFLICT (campaign_id) WHERE is_automatic DO NOTHING RETURNING id`,
		c.ID, "🎁 "+c.Title.KK, audience, raw, markup, c.StartsAt, approver).Scan(&id)
	if db.IsNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return audit.Log(ctx, tx, nil, audit.Entry{AdminID: approver, Action: "marketing.broadcast.automatic", EntityType: "marketing_broadcast", EntityID: id.String(), After: map[string]any{"campaign_id": c.ID, "audience": audience}})
}

func automaticRecipientEligible(ctx context.Context, q db.Querier, b Broadcast, userID uuid.UUID, bot notifications.Bot) (bool, error) {
	if b.CampaignID == nil || !settings.Marketing(ctx, q).Awards {
		return false, nil
	}
	var active bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM marketing_app_presence WHERE user_id=$1 AND app=$2 AND active AND seen_at>now()-interval '90 seconds')`, userID, string(bot)).Scan(&active); err != nil {
		return false, err
	}
	if active {
		return false, nil
	}
	c, err := loadCampaign(ctx, q, *b.CampaignID, false)
	if err != nil {
		return false, err
	}
	eligible, err := campaignForUser(ctx, q, c, userID, string(bot))
	if err != nil || !eligible {
		return false, err
	}
	now := time.Now()
	if c.Status != StatusActive || (c.StartsAt != nil && now.Before(*c.StartsAt)) || (c.EndsAt != nil && !now.Before(*c.EndsAt)) {
		return false, nil
	}
	var already bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM marketing_grants g LEFT JOIN driver_profiles d ON d.id=g.driver_id
 WHERE g.campaign_id=$1 AND (g.user_id=$2 OR d.user_id=$2))`, c.ID, userID).Scan(&already); err != nil {
		return false, err
	}
	if already {
		return false, nil
	}
	switch c.Template {
	case TemplateReferral:
		rules, ok := mustRules[ReferralRules](c.Rules)
		if !ok {
			return false, nil
		}
		var disabled bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM referral_codes WHERE user_id=$1 AND disabled_at IS NOT NULL)`, userID).Scan(&disabled); err != nil {
			return false, err
		}
		if disabled {
			return false, nil
		}
		return referralUnlocked(ctx, q, userID, rules.Unlock)
	case TemplateFirstRideCoupon:
		var used bool
		err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE passenger_id=$1 AND completed_at IS NOT NULL)
 OR EXISTS(SELECT 1 FROM marketing_coupons WHERE user_id=$1 AND campaign_id=$2)`, userID, c.ID).Scan(&used)
		return !used, err
	case TemplateDriverMilestone:
		var approved bool
		err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM driver_profiles WHERE user_id=$1 AND status='approved')`, userID).Scan(&approved)
		return approved, err
	}
	return false, nil
}
