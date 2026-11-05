package marketing

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// MountPassenger mounts the passenger Mini App routes (§23.11):
// GET /promotions, GET /promotions/bonus/history, GET /promotions/referral,
// PUT /promotions/subscription.
func (s *Service) MountPassenger(r chi.Router) {
	r.Get("/promotions", httpx.H(s.passengerPromotions))
	r.Get("/promotions/bonus/history", httpx.H(s.passengerBonusHistory))
	r.Get("/promotions/referral", httpx.H(s.passengerReferral))
	r.Post("/promotions/presence", httpx.H(s.appPresence))
	r.Put("/promotions/subscription", httpx.H(s.passengerSubscription))
}

// PassengerCoupon is a coupon in the passenger app.
type PassengerCoupon struct {
	ID              uuid.UUID          `json:"id"`
	Title           settings.Localized `json:"title"`
	Amount          int64              `json:"amount"`
	MinOrderFare    int64              `json:"min_order_fare"`
	Categories      []string           `json:"categories"`
	Status          string             `json:"status"`
	IssuedAt        time.Time          `json:"issued_at"`
	ExpiresAt       time.Time          `json:"expires_at"`
	ReservedOrderID *uuid.UUID         `json:"reserved_order_id"`
	RedeemedAt      *time.Time         `json:"redeemed_at"`
}

// Offer is a promotion the passenger can still earn (e.g. the first-ride coupon).
type Offer struct {
	Kind          string             `json:"kind"`
	Title         settings.Localized `json:"title"`
	Description   settings.Localized `json:"description"`
	Amount        int64              `json:"amount"`
	ValidityHours int                `json:"validity_hours,omitempty"`
	Categories    []string           `json:"categories"`
}

// Banner is an informational campaign (broadcast_only).
type Banner struct {
	ID          uuid.UUID          `json:"id"`
	Title       settings.Localized `json:"title"`
	Description settings.Localized `json:"description"`
}

// ReferralSummary tells the app whether to show «Досыңды шақыр».
type ReferralSummary struct {
	// State: unavailable (no live campaign), locked (not yet allowed: the
	// first-ride coupon must be used first) or unlocked.
	State        string `json:"state"`
	LockReason   string `json:"lock_reason,omitempty"`
	RewardAmount int64  `json:"reward_amount"`
	Rewarded     bool   `json:"rewarded"`
}

// PassengerPromotions is GET /promotions (openapi PassengerPromotions).
type PassengerPromotions struct {
	ServerTime         time.Time         `json:"server_time"`
	RedemptionsEnabled bool              `json:"redemptions_enabled"`
	Coupons            []PassengerCoupon `json:"coupons"`
	Bonus              BonusAccount      `json:"bonus"`
	BonusCategories    []string          `json:"bonus_categories"`
	Offers             []Offer           `json:"offers"`
	Banners            []Banner          `json:"banners"`
	Referral           ReferralSummary   `json:"referral"`
	Subscribed         bool              `json:"marketing_subscribed"`
}

func (s *Service) passengerPromotions(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	v, err := s.promotionsFor(r.Context(), s.pool, p.UserID)
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}

func (s *Service) promotionsFor(ctx context.Context, q db.Querier, userID uuid.UUID) (PassengerPromotions, error) {
	sw := settings.Marketing(ctx, q)
	v := PassengerPromotions{RedemptionsEnabled: sw.Redemptions, Coupons: []PassengerCoupon{}, Offers: []Offer{},
		Banners: []Banner{}, BonusCategories: bonusCategories}
	if err := q.QueryRow(ctx, `SELECT now()`).Scan(&v.ServerTime); err != nil {
		return v, err
	}
	rows, err := q.Query(ctx, `
		SELECT c.id, ver.title, c.amount, c.min_order_fare, c.categories,
		       CASE WHEN c.status = 'issued' AND c.expires_at <= now() THEN 'expired' ELSE c.status END,
		       c.issued_at, c.expires_at, c.reserved_order_id, c.redeemed_at
		FROM marketing_coupons c JOIN marketing_campaign_versions ver ON ver.id = c.version_id
		WHERE c.user_id = $1 ORDER BY c.issued_at DESC LIMIT 10`, userID)
	if err != nil {
		return v, err
	}
	coupons, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (PassengerCoupon, error) {
		var c PassengerCoupon
		var title []byte
		err := row.Scan(&c.ID, &title, &c.Amount, &c.MinOrderFare, &c.Categories, &c.Status, &c.IssuedAt,
			&c.ExpiresAt, &c.ReservedOrderID, &c.RedeemedAt)
		c.Title = localized(title)
		return c, err
	})
	if err != nil {
		return v, err
	}
	v.Coupons = coupons
	if v.Bonus, err = GetBonus(ctx, q, userID); err != nil {
		return v, err
	}
	var completed bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM orders WHERE passenger_id = $1 AND completed_at IS NOT NULL)`, userID).
		Scan(&completed); err != nil {
		return v, err
	}
	live, err := liveCampaigns(ctx, q, []string{TemplateFirstRideCoupon, TemplateReferral, TemplateBroadcastOnly}, nil)
	if err != nil {
		return v, err
	}
	v.Referral = ReferralSummary{State: "unavailable"}
	for _, c := range live {
		eligible, err := campaignForUser(ctx, q, c, userID, "passenger")
		if err != nil {
			return v, err
		}
		if !eligible {
			continue
		}
		switch c.Template {
		case TemplateFirstRideCoupon:
			rules, ok := mustRules[CouponRules](c.Rules)
			if ok && sw.Awards && !completed && len(coupons) == 0 {
				v.Offers = append(v.Offers, Offer{Kind: TemplateFirstRideCoupon, Title: c.Title, Description: c.Description,
					Amount: rules.Amount, ValidityHours: rules.ValidityHours, Categories: rules.Categories})
			}
		case TemplateReferral:
			rules, ok := mustRules[ReferralRules](c.Rules)
			if !ok {
				continue
			}
			v.Referral.RewardAmount = rules.RewardAmount
			unlocked, err := referralUnlocked(ctx, q, userID, rules.Unlock)
			if err != nil {
				return v, err
			}
			if unlocked && sw.Awards {
				v.Referral.State = "unlocked"
			} else {
				v.Referral.State, v.Referral.LockReason = "locked", lockReason(rules.Unlock, sw.Awards)
			}
			if err := q.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM marketing_grants WHERE campaign_id = $1 AND user_id = $2
				                AND reason = 'referral_reward' AND status = 'credited')`, c.ID, userID).
				Scan(&v.Referral.Rewarded); err != nil {
				return v, err
			}
		case TemplateBroadcastOnly:
			v.Banners = append(v.Banners, Banner{ID: c.ID, Title: c.Title, Description: c.Description})
		}
	}
	var optOut *time.Time
	err = q.QueryRow(ctx, `SELECT marketing_opt_out_at FROM user_preferences WHERE user_id = $1`, userID).Scan(&optOut)
	if err != nil && !db.IsNoRows(err) {
		return v, err
	}
	v.Subscribed = optOut == nil
	return v, nil
}

func lockReason(policy string, awards bool) string {
	if !awards {
		return "paused"
	}
	switch policy {
	case UnlockFirstRideCompleted:
		return "first_ride_required"
	case UnlockCouponRedeemedOrExpired:
		return "coupon_not_used"
	}
	return "coupon_not_redeemed"
}

// PassengerBonusEntry is a bonus history row in the passenger app.
type PassengerBonusEntry struct {
	ID          int64     `json:"id"`
	Type        string    `json:"type"`
	Amount      int64     `json:"amount"`
	OrderNumber *int64    `json:"order_number"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Service) passengerBonusHistory(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	limit, err := httpx.QueryInt(r, "limit", 20, 1, 50)
	if err != nil {
		return err
	}
	var before *int64
	if v := r.URL.Query().Get("before"); v != "" {
		n, err := parseCursor(v)
		if err != nil {
			return httpx.Validation(map[string]string{"before": "invalid"})
		}
		before = &n
	}
	// Reservations and releases are internal steps; the passenger sees what
	// came in and what was spent.
	items, _, err := ListBonusLedger(r.Context(), s.pool, BonusFilter{UserID: &p.UserID, BeforeID: before,
		Limit: limit + 1, Types: []string{BonusGrant, BonusSpend, BonusAdjustmentCredit, BonusAdjustmentDebit}})
	if err != nil {
		return err
	}
	out := httpx.CursorList[PassengerBonusEntry]{Items: []PassengerBonusEntry{}}
	for i, e := range items {
		if i == limit {
			c := formatCursor(items[limit-1].ID)
			out.NextCursor = &c
			break
		}
		out.Items = append(out.Items, PassengerBonusEntry{ID: e.ID, Type: e.Type, Amount: e.BalanceDelta,
			OrderNumber: e.OrderNumber, CreatedAt: e.CreatedAt})
	}
	return httpx.OK(w, out)
}

// ReferralFriend is an invited friend as the inviter sees them: first name
// and last-name initial only.
type ReferralFriend struct {
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	UnderReview  bool      `json:"under_review,omitempty"`
	AttributedAt time.Time `json:"attributed_at"`
}

// ReferralView is GET /promotions/referral (openapi ReferralView).
type ReferralView struct {
	ReferralSummary
	Title       settings.Localized `json:"title"`
	Description settings.Localized `json:"description"`
	Code        string             `json:"code,omitempty"`
	Link        string             `json:"link,omitempty"`
	AppLink     string             `json:"app_link,omitempty"`
	Earned      int64              `json:"earned"`
	Friends     []ReferralFriend   `json:"friends"`
}

func (s *Service) passengerReferral(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var v ReferralView
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		promos, err := s.promotionsFor(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		v = ReferralView{ReferralSummary: promos.Referral, Friends: []ReferralFriend{}}
		if c, err := latestCampaign(ctx, tx, TemplateReferral); err != nil {
			return err
		} else if c != nil {
			v.Title, v.Description = c.Title, c.Description
		}
		if v.State == "unlocked" {
			code, err := referralCode(ctx, tx, p.UserID)
			if err != nil {
				return err
			}
			if code == "" {
				v.State, v.LockReason = "locked", "disabled"
			} else if bot := strings.TrimPrefix(s.opt.PassengerBotUsername, "@"); bot != "" {
				v.Code = code
				v.Link = "https://t.me/" + bot + "?start=ref_" + code
				v.AppLink = "https://t.me/" + bot + "?startapp=ref_" + code
			} else {
				v.Code = code
			}
		}
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(sum(amount), 0)::bigint FROM marketing_grants
			WHERE user_id = $1 AND reason = 'referral_reward' AND status = 'credited'`, p.UserID).Scan(&v.Earned); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT u.first_name, u.last_name, a.status, a.attributed_at
			FROM referral_attributions a JOIN users u ON u.id = a.invitee_user_id
			WHERE a.referrer_user_id = $1 ORDER BY a.attributed_at DESC LIMIT 50`, p.UserID)
		if err != nil {
			return err
		}
		v.Friends, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (ReferralFriend, error) {
			var f ReferralFriend
			var first, last string
			err := row.Scan(&first, &last, &f.Status, &f.AttributedAt)
			f.Name = friendName(first, last)
			f.UnderReview = f.Status == ReferralReview
			// Keep the status understood by older Mini Apps, which discard
			// friends with an unknown status. New clients show the review flag.
			if f.Status == ReferralRejected || f.UnderReview {
				f.Status = "not_counted"
			}
			return f, err
		})
		return err
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, v)
}

func friendName(first, last string) string {
	name := strings.TrimSpace(first)
	if name == "" {
		name = "Telegram"
	}
	if r, _ := utf8.DecodeRuneInString(strings.TrimSpace(last)); r != utf8.RuneError {
		name += " " + string(r) + "."
	}
	return name
}

type subscriptionRequest struct {
	Subscribed *bool `json:"subscribed"`
}

// passengerSubscription handles PUT /promotions/subscription: marketing
// messages on or off (order messages are not affected).
func (s *Service) passengerSubscription(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	var req subscriptionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.Subscribed == nil {
		return httpx.Validation(map[string]string{"subscribed": "required"})
	}
	if err := SetMarketingOptOut(r.Context(), s.pool, p.UserID, !*req.Subscribed); err != nil {
		return err
	}
	return httpx.OK(w, map[string]bool{"marketing_subscribed": *req.Subscribed})
}

// SetMarketingOptOut stores the person's marketing choice (both bots).
func SetMarketingOptOut(ctx context.Context, q db.Querier, userID uuid.UUID, optOut bool) error {
	_, err := q.Exec(ctx, `
		INSERT INTO user_preferences (user_id, language, marketing_opt_out_at)
		VALUES ($1, 'kk', CASE WHEN $2 THEN now() END)
		ON CONFLICT (user_id) DO UPDATE SET marketing_opt_out_at =
			CASE WHEN $2 THEN COALESCE(user_preferences.marketing_opt_out_at, now()) ELSE NULL END`, userID, optOut)
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}
