package marketing

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// MountAdmin mounts /admin/marketing/* (§23.10). Reads need marketing.view;
// drafts and segments marketing.edit; broadcasts marketing.broadcast;
// activations, budgets, switches, voids and referral decisions
// marketing.approve; money corrections finance.manage.
func (s *Service) MountAdmin(r chi.Router) {
	view := r.With(auth.Permit(auth.PermMarketingView))
	view.Get("/marketing/overview", httpx.H(s.adminOverview))
	view.Get("/marketing/campaigns", httpx.H(s.adminListCampaigns))
	view.Get("/marketing/campaigns/{id}", httpx.H(s.adminGetCampaign))
	view.Get("/marketing/campaigns/{id}/preview", httpx.H(s.adminPreviewCampaign))
	view.Get("/marketing/coupons", httpx.H(s.adminListCoupons))
	view.Get("/marketing/bonus-ledger", httpx.H(s.adminBonusLedger))
	view.Get("/marketing/users", httpx.H(s.adminFindUsers))
	view.Get("/marketing/users/{id}", httpx.H(s.adminUser))
	view.Get("/marketing/referrals", httpx.H(s.adminListReferrals))
	view.Get("/marketing/applications", httpx.H(s.adminListApplications))
	view.Get("/marketing/segments", httpx.H(s.adminListSegments))
	view.Post("/marketing/segments/preview", httpx.H(s.adminPreviewSegment))
	view.Get("/marketing/broadcasts", httpx.H(s.adminListBroadcasts))
	view.Get("/marketing/broadcasts/{id}", httpx.H(s.adminGetBroadcast))
	view.Get("/marketing/broadcasts/{id}/recipients", httpx.H(s.adminListRecipients))
	view.Get("/marketing/reconciliation", httpx.H(s.adminReconciliation))
	view.Get("/marketing/reports", httpx.H(s.adminReports))
	view.Get("/marketing/settings", httpx.H(s.adminSettings))

	edit := r.With(auth.Permit(auth.PermMarketingEdit))
	edit.Post("/marketing/campaigns", httpx.H(s.adminCreateCampaign))
	edit.Post("/marketing/campaigns/{id}/duplicate", httpx.H(s.adminDuplicateCampaign))
	edit.Post("/marketing/segments", httpx.H(s.adminCreateSegment))
	edit.Patch("/marketing/segments/{id}", httpx.H(s.adminUpdateSegment))

	// Either permission reaches these; each action checks its own.
	editOrApprove := r.With(permitAny(auth.PermMarketingEdit, auth.PermMarketingApprove))
	editOrApprove.Post("/marketing/campaigns/{id}/versions", httpx.H(s.adminNewVersion))
	editOrApprove.Post("/marketing/campaigns/{id}/transition", httpx.H(s.adminTransition))

	approve := r.With(auth.Permit(auth.PermMarketingApprove))
	approve.Patch("/marketing/campaigns/{id}/budget", httpx.H(s.adminBudget))
	approve.Patch("/marketing/settings", httpx.H(s.adminUpdateSettings))
	approve.Post("/marketing/coupons/{id}/void", httpx.H(s.adminVoidCoupon))
	approve.Post("/marketing/referrals/{id}/review", httpx.H(s.adminReviewReferral))

	broadcast := r.With(auth.Permit(auth.PermMarketingBroadcast))
	broadcast.Post("/marketing/broadcasts", httpx.H(s.adminCreateBroadcast))
	broadcast.Patch("/marketing/broadcasts/{id}", httpx.H(s.adminUpdateBroadcast))
	broadcast.Post("/marketing/broadcasts/{id}/test", httpx.H(s.adminTestBroadcast))
	broadcast.Post("/marketing/broadcasts/{id}/transition", httpx.H(s.adminTransitionBroadcast))

	finance := r.With(auth.Permit(auth.PermFinanceManage))
	finance.Post("/marketing/users/{id}/bonus-adjustments", httpx.H(s.adminAdjustBonus))
	finance.Post("/marketing/applications/{id}/reverse", httpx.H(s.adminReverseApplication))

	r.With(auth.Permit(auth.PermMarketingView, auth.PermReportsExport)).
		Get("/marketing/reports.csv", httpx.H(s.adminReportsCSV))
}

// permitAny lets the request through when the admin holds any of perms.
func permitAny(perms ...auth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := auth.AdminFrom(r.Context())
			if !ok {
				httpx.WriteError(w, r, httpx.Unauthorized())
				return
			}
			if !slices.ContainsFunc(perms, a.Can) {
				httpx.WriteError(w, r, httpx.Forbidden())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// reasonText validates the operator's reason of a high-risk action.
func reasonText(raw *string, required bool) (string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		if required {
			return "", httpx.Validation(map[string]string{"reason": "required"})
		}
		return "", nil
	}
	v, err := textutil.CleanText(*raw, 300)
	if err != nil {
		return "", httpx.Validation(map[string]string{"reason": "too_long"})
	}
	if required && len([]rune(v)) < 3 {
		return "", httpx.Validation(map[string]string{"reason": "too_short"})
	}
	return v, nil
}

func requireConfirm(confirm bool) error {
	if !confirm {
		return httpx.Validation(map[string]string{"confirm": "required"})
	}
	return nil
}

func (s *Service) tx(r *http.Request, fn func(tx pgx.Tx) error) error {
	return db.WithTx(r.Context(), s.pool, fn)
}

// --- campaigns ---------------------------------------------------------------

func (s *Service) adminListCampaigns(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	items, total, err := listCampaigns(r.Context(), s.pool, q.Get("status"), q.Get("template"),
		strings.TrimSpace(q.Get("q")), page)
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[Campaign]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

// CampaignDetail is GET /admin/marketing/campaigns/{id}.
type CampaignDetail struct {
	Campaign
	Versions []Version `json:"versions"`
}

func (s *Service) campaignDetail(r *http.Request, id uuid.UUID) (CampaignDetail, error) {
	ctx := r.Context()
	c, err := loadCampaign(ctx, s.pool, id, false)
	if err != nil {
		return CampaignDetail{}, err
	}
	u, err := usage(ctx, s.pool, c)
	if err != nil {
		return CampaignDetail{}, err
	}
	c.Usage = &u
	versions, err := listVersions(ctx, s.pool, id)
	if err != nil {
		return CampaignDetail{}, err
	}
	return CampaignDetail{Campaign: c, Versions: versions}, nil
}

func (s *Service) adminGetCampaign(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	d, err := s.campaignDetail(r, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

func (s *Service) adminCreateCampaign(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	var in campaignInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Template != nil && *in.Template == TemplateManualGrant {
		return httpx.Validation(map[string]string{"template": "system_only"})
	}
	if in.BudgetLimit != nil && !admin.Can(auth.PermMarketingApprove) {
		// A draft may carry a budget, but setting one is an approver's call.
		in.BudgetLimit = nil
	}
	var id uuid.UUID
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if id, err = createCampaign(r.Context(), tx, in, admin.AdminID); err != nil {
			return err
		}
		c, err := loadCampaign(r.Context(), tx, id, false)
		if err != nil {
			return err
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: "marketing.campaign.create",
			EntityType: "marketing_campaign", EntityID: id.String(), After: c})
	})
	if err != nil {
		return err
	}
	d, err := s.campaignDetail(r, id)
	if err != nil {
		return err
	}
	return httpx.Created(w, d)
}

func (s *Service) adminDuplicateCampaign(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var newID uuid.UUID
	err = s.tx(r, func(tx pgx.Tx) error {
		c, err := loadCampaign(r.Context(), tx, id, false)
		if err != nil {
			return err
		}
		if c.Template == TemplateManualGrant {
			return httpx.Validation(map[string]string{"template": "system_only"})
		}
		tpl := c.Template
		in := campaignInput{Template: &tpl, Title: &c.Title, Description: &c.Description, Rules: c.Rules,
			StartsAt: c.StartsAt, EndsAt: c.EndsAt, Priority: &c.Priority, ExclusivityGroup: c.ExclusivityGroup}
		if newID, err = createCampaign(r.Context(), tx, in, admin.AdminID); err != nil {
			return err
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: "marketing.campaign.duplicate",
			EntityType: "marketing_campaign", EntityID: newID.String(), After: map[string]any{"from": id.String()}})
	})
	if err != nil {
		return err
	}
	d, err := s.campaignDetail(r, newID)
	if err != nil {
		return err
	}
	return httpx.Created(w, d)
}

func (s *Service) adminNewVersion(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var in campaignInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		c, err := loadCampaign(r.Context(), tx, id, true)
		if err != nil {
			return err
		}
		if c.Status == StatusArchived {
			return httpx.NewError(409, "invalid_transition", "an archived campaign cannot change")
		}
		// Changing a campaign that is (or will be) live is an approver's call;
		// drafts are edited freely.
		// System campaigns count as live: the launch promotions' rules and the
		// manual bucket's per-credit cap are approvers' decisions.
		live := c.Status != StatusDraft && c.Status != StatusEnded || c.IsSystem
		if live && !admin.Can(auth.PermMarketingApprove) || !live && !admin.Can(auth.PermMarketingEdit) {
			return httpx.Forbidden()
		}
		if in.BudgetLimit != nil || in.Priority != nil || in.ExclusivityGroup != nil {
			return httpx.Validation(map[string]string{"budget_limit": "use_budget_endpoint"})
		}
		if err := newVersion(r.Context(), tx, c, in, admin.AdminID); err != nil {
			return err
		}
		after, err := loadCampaign(r.Context(), tx, id, false)
		if err != nil {
			return err
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: "marketing.campaign.version",
			EntityType: "marketing_campaign", EntityID: id.String(), Before: c, After: after})
	})
	if err != nil {
		return err
	}
	d, err := s.campaignDetail(r, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

type transitionRequest struct {
	Action      string     `json:"action"`
	Reason      *string    `json:"reason"`
	Confirm     bool       `json:"confirm"`
	ScheduledAt *time.Time `json:"scheduled_at"`
	// ExpectedVersion is the version the approver reviewed; a campaign
	// changed since is refused (409 version_conflict).
	ExpectedVersion *int `json:"expected_version"`
}

func (s *Service) adminTransition(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req transitionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	t, ok := transitions[req.Action]
	if !ok {
		return httpx.Validation(map[string]string{"action": "invalid"})
	}
	if t.approve && !admin.Can(auth.PermMarketingApprove) || !t.approve && !admin.Can(auth.PermMarketingEdit) &&
		!admin.Can(auth.PermMarketingApprove) {
		return httpx.Forbidden()
	}
	reason, err := reasonText(req.Reason, t.approve)
	if err != nil {
		return err
	}
	if t.approve {
		if err := requireConfirm(req.Confirm); err != nil {
			return err
		}
		if req.ExpectedVersion == nil {
			return httpx.Validation(map[string]string{"expected_version": "required"})
		}
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		c, err := loadCampaign(r.Context(), tx, id, true)
		if err != nil {
			return err
		}
		// The system campaigns (the customer's launch promotions and the
		// manual bucket) are switched, paused and archived by approvers only.
		if c.IsSystem && !admin.Can(auth.PermMarketingApprove) {
			return httpx.Forbidden()
		}
		if t.approve && *req.ExpectedVersion != c.Version {
			return httpx.NewError(409, "version_conflict", "the campaign changed since it was reviewed").
				WithDetails(map[string]any{"current_version": c.Version})
		}
		if err := applyTransition(r.Context(), tx, c, req.Action, req.ScheduledAt, admin.AdminID); err != nil {
			return err
		}
		after, err := loadCampaign(r.Context(), tx, id, false)
		if err != nil {
			return err
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: "marketing.campaign." + req.Action,
			EntityType: "marketing_campaign", EntityID: id.String(),
			Before: map[string]any{"status": c.Status, "redemptions_frozen": c.RedemptionsFrozen},
			After:  map[string]any{"status": after.Status, "redemptions_frozen": after.RedemptionsFrozen, "reason": reason}})
	})
	if err != nil {
		return err
	}
	d, err := s.campaignDetail(r, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

type budgetRequest struct {
	BudgetLimit *int64  `json:"budget_limit"`
	Unlimited   bool    `json:"unlimited"`
	Reason      *string `json:"reason"`
	Confirm     bool    `json:"confirm"`
}

func (s *Service) adminBudget(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req budgetRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	reason, err := reasonText(req.Reason, true)
	if err != nil {
		return err
	}
	if err := requireConfirm(req.Confirm); err != nil {
		return err
	}
	if !req.Unlimited && (req.BudgetLimit == nil || *req.BudgetLimit < 0 || *req.BudgetLimit > 1_000_000_000) {
		return httpx.Validation(map[string]string{"budget_limit": "out_of_range"})
	}
	limit := req.BudgetLimit
	if req.Unlimited {
		limit = nil
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		c, err := loadCampaign(r.Context(), tx, id, true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE marketing_campaigns SET budget_limit = $2 WHERE id = $1`, id, limit); err != nil {
			return err
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: "marketing.campaign.budget",
			EntityType: "marketing_campaign", EntityID: id.String(),
			Before: map[string]any{"budget_limit": c.BudgetLimit},
			After:  map[string]any{"budget_limit": limit, "reason": reason}})
	})
	if err != nil {
		return err
	}
	d, err := s.campaignDetail(r, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

// CampaignPreview estimates who a campaign reaches and what it may cost
// before it is switched on.
type CampaignPreview struct {
	Audience     int64    `json:"audience"`
	PerPerson    int64    `json:"per_person"`
	MaxLiability *int64   `json:"max_liability"`
	BudgetLimit  *int64   `json:"budget_limit"`
	Usage        Usage    `json:"usage"`
	Warnings     []string `json:"warnings"`
}

func (s *Service) adminPreviewCampaign(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	c, err := loadCampaign(ctx, s.pool, id, false)
	if err != nil {
		return err
	}
	p := CampaignPreview{BudgetLimit: c.BudgetLimit, Warnings: []string{}}
	if p.Usage, err = usage(ctx, s.pool, c); err != nil {
		return err
	}
	sw := settings.Marketing(ctx, s.pool)
	if !sw.Awards {
		p.Warnings = append(p.Warnings, "awards_switch_off")
	}
	if !sw.Redemptions {
		p.Warnings = append(p.Warnings, "redemptions_switch_off")
	}
	q := s.pool
	switch c.Template {
	case TemplateFirstRideCoupon:
		rules, _ := mustRules[CouponRules](c.Rules)
		p.PerPerson = rules.Amount
		err = q.QueryRow(ctx, `
			SELECT count(*) FROM users u
			WHERE NOT u.is_blocked AND (u.passenger_bot_started_at IS NOT NULL OR EXISTS (SELECT 1 FROM orders o WHERE o.passenger_id = u.id))
			  AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.passenger_id = u.id AND o.completed_at IS NOT NULL)`).Scan(&p.Audience)
		var lowTariffs int
		if err == nil {
			err = q.QueryRow(ctx, `SELECT count(*) FROM tariffs WHERE is_active AND category = ANY($1) AND min_fare < $2`,
				rules.Categories, rules.MinOrderFare).Scan(&lowTariffs)
		}
		if lowTariffs > 0 {
			p.Warnings = append(p.Warnings, "fares_below_coupon_minimum")
		}
	case TemplateReferral:
		rules, _ := mustRules[ReferralRules](c.Rules)
		p.PerPerson = rules.RewardAmount
		err = q.QueryRow(ctx, `SELECT count(*) FROM users u WHERE NOT u.is_blocked AND u.passenger_bot_started_at IS NOT NULL`).
			Scan(&p.Audience)
		if rules.Unlock == UnlockCouponRedeemed || rules.Unlock == UnlockCouponRedeemedOrExpired {
			p.Warnings = append(p.Warnings, "legacy_passengers_locked")
			if cc, cerr := latestCampaign(ctx, q, TemplateFirstRideCoupon); cerr != nil {
				err = cerr
			} else if cc == nil || cc.Status != StatusActive {
				p.Warnings = append(p.Warnings, "coupon_campaign_not_active")
			}
		}
	case TemplatePassengerCredit:
		rules, _ := mustRules[CreditRules](c.Rules)
		p.PerPerson = rules.Amount
		if seg, serr := loadSegment(ctx, q, rules.SegmentID); serr == nil {
			fields := map[string]string{}
			if cnt, cerr := countAudience(ctx, q, AudiencePassengers, parseFilters(seg.Audience, seg.Filters, fields)); cerr == nil {
				p.Audience = cnt.Reachable
			}
		}
	case TemplateDriverMilestone:
		rules, _ := mustRules[MilestoneRules](c.Rules)
		p.PerPerson = rules.Amount
		err = q.QueryRow(ctx, `SELECT count(*) FROM driver_profiles WHERE status = 'approved'`).Scan(&p.Audience)
	}
	if err != nil {
		return err
	}
	if p.PerPerson > 0 {
		liability := p.Audience * p.PerPerson
		if c.BudgetLimit != nil && *c.BudgetLimit < liability {
			liability = *c.BudgetLimit
		}
		p.MaxLiability = &liability
		if c.BudgetLimit == nil {
			p.Warnings = append(p.Warnings, "no_budget_limit")
		}
	}
	return httpx.OK(w, p)
}

// --- coupons, bonus, users -----------------------------------------------------

func (s *Service) adminListCoupons(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	campaignID, err := httpx.QueryUUID(r, "campaign_id")
	if err != nil {
		return err
	}
	userID, err := httpx.QueryUUID(r, "user_id")
	if err != nil {
		return err
	}
	items, total, err := listCoupons(r.Context(), s.pool, campaignID, userID, r.URL.Query().Get("status"), page)
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[Coupon]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

func (s *Service) adminBonusLedger(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	userID, err := httpx.QueryUUID(r, "user_id")
	if err != nil {
		return err
	}
	var types []string
	if t := r.URL.Query().Get("type"); t != "" {
		if !slices.Contains(BonusEntryTypes, t) {
			return httpx.Validation(map[string]string{"type": "invalid"})
		}
		types = []string{t}
	}
	items, total, err := ListBonusLedger(r.Context(), s.pool, BonusFilter{UserID: userID, Types: types,
		Limit: page.PageSize, Offset: page.Offset()})
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[BonusEntry]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

func (s *Service) adminVoidCoupon(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Reason *string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	reason, err := reasonText(req.Reason, true)
	if err != nil {
		return err
	}
	var c Coupon
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if c, err = voidCoupon(r.Context(), tx, id, admin.AdminID, reason); err != nil {
			return err
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: "marketing.coupon.void", EntityType: "marketing_coupon",
			EntityID: id.String(), After: map[string]any{"status": c.Status, "reason": reason}})
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, c)
}

// UserRef is a person in marketing views: masked, never the full phone.
type UserRef struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	PhoneMasked *string   `json:"phone_masked"`
	TelegramID  int64     `json:"telegram_user_id"`
	CreatedAt   time.Time `json:"created_at"`
}

func maskPhone(p *string) *string {
	if p == nil || len(*p) < 4 {
		return nil
	}
	v := strings.Repeat("•", len(*p)-4) + (*p)[len(*p)-4:]
	return &v
}

func (s *Service) adminFindUsers(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 3 {
		return httpx.OK(w, map[string]any{"items": []UserRef{}})
	}
	digits := strings.Map(func(c rune) rune {
		if c >= '0' && c <= '9' {
			return c
		}
		return -1
	}, q)
	rows, err := s.pool.Query(r.Context(), `
		SELECT u.id, u.first_name, u.last_name, u.phone_e164, u.telegram_user_id, u.created_at
		FROM users u LEFT JOIN referral_codes rc ON rc.user_id = u.id
		WHERE ($1 <> '' AND (u.telegram_user_id::text = $1 OR right(u.phone_e164, 10) = right($1, 10)))
		   OR rc.code = upper($2)
		ORDER BY u.created_at DESC LIMIT 20`, digits, q)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (UserRef, error) {
		var u UserRef
		var first, last string
		var phone *string
		err := row.Scan(&u.ID, &first, &last, &phone, &u.TelegramID, &u.CreatedAt)
		u.Name, u.PhoneMasked = friendName(first, last), maskPhone(phone)
		return u, err
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]any{"items": items})
}

// UserMarketing is GET /admin/marketing/users/{id}.
type UserMarketing struct {
	User       UserRef          `json:"user"`
	Bonus      BonusAccount     `json:"bonus"`
	Coupons    []Coupon         `json:"coupons"`
	Ledger     []BonusEntry     `json:"ledger"`
	Code       *string          `json:"referral_code"`
	InvitedBy  *ReferralRow     `json:"invited_by"`
	Invited    []ReferralRow    `json:"invited"`
	Promotions []ApplicationRow `json:"promotions"`
}

func (s *Service) adminUser(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	var v UserMarketing
	var first, last string
	var phone *string
	err = s.pool.QueryRow(ctx, `SELECT id, first_name, last_name, phone_e164, telegram_user_id, created_at FROM users WHERE id = $1`, id).
		Scan(&v.User.ID, &first, &last, &phone, &v.User.TelegramID, &v.User.CreatedAt)
	if db.IsNoRows(err) {
		return httpx.NotFound()
	}
	if err != nil {
		return err
	}
	v.User.Name, v.User.PhoneMasked = friendName(first, last), maskPhone(phone)
	if v.Bonus, err = GetBonus(ctx, s.pool, id); err != nil {
		return err
	}
	if v.Coupons, err = userCoupons(ctx, s.pool, id); err != nil {
		return err
	}
	if v.Ledger, _, err = ListBonusLedger(ctx, s.pool, BonusFilter{UserID: &id, Limit: 50}); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `SELECT code FROM referral_codes WHERE user_id = $1`, id).Scan(&v.Code); err != nil && !db.IsNoRows(err) {
		return err
	}
	by, _, err := listReferrals(ctx, s.pool, referralFilter{InviteeID: &id}, httpx.PageParams{Page: 1, PageSize: 1})
	if err != nil {
		return err
	}
	if len(by) > 0 {
		v.InvitedBy = &by[0]
	}
	if v.Invited, _, err = listReferrals(ctx, s.pool, referralFilter{ReferrerID: &id}, httpx.PageParams{Page: 1, PageSize: 100}); err != nil {
		return err
	}
	if v.Promotions, _, err = listApplications(ctx, s.pool, applicationFilter{UserID: &id}, httpx.PageParams{Page: 1, PageSize: 50}); err != nil {
		return err
	}
	return httpx.OK(w, v)
}

type adjustRequest struct {
	Amount         int64   `json:"amount"`
	Reason         *string `json:"reason"`
	IdempotencyKey string  `json:"idempotency_key"`
	Confirm        bool    `json:"confirm"`
}

func (s *Service) adminAdjustBonus(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req adjustRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	reason, err := reasonText(req.Reason, true)
	if err != nil {
		return err
	}
	if err := requireConfirm(req.Confirm); err != nil {
		return err
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		before, err := GetBonus(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if err := adjustBonus(r.Context(), tx, id, req.Amount, admin.AdminID, req.IdempotencyKey, reason); err != nil {
			return err
		}
		after, err := GetBonus(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if req.Amount > 0 && after.Balance != before.Balance {
			// The passenger hears about a credit (not about corrections down).
			if err := notifications.Enqueue(r.Context(), tx, notifications.Notification{
				Bot: notifications.PassengerBot, UserID: id, Template: notifications.TplPromoBonusGranted,
				Params: map[string]any{"amount": req.Amount}, DedupeKey: "promo_bonus_granted:" + req.IdempotencyKey,
			}); err != nil {
				return err
			}
		}
		if err := publishPromotions(r.Context(), tx, id); err != nil {
			return err
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: "marketing.bonus.adjust", EntityType: "passenger_bonus",
			EntityID: id.String(), Before: before, After: map[string]any{"account": after, "amount": req.Amount, "reason": reason}})
	})
	if err != nil {
		return err
	}
	b, err := GetBonus(r.Context(), s.pool, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, b)
}

// --- referrals and applications -------------------------------------------------

// ReferralRow is an attribution in admin lists (masked people).
type ReferralRow struct {
	ID              uuid.UUID  `json:"id"`
	Campaign        string     `json:"campaign_key"`
	Invitee         UserRef    `json:"invitee"`
	Referrer        UserRef    `json:"referrer"`
	Code            string     `json:"code"`
	Source          string     `json:"source"`
	Status          string     `json:"status"`
	Reason          *string    `json:"reason"`
	QualifyingOrder *int64     `json:"qualifying_order_number"`
	AttributedAt    time.Time  `json:"attributed_at"`
	QualifiedAt     *time.Time `json:"qualified_at"`
	DecidedAt       *time.Time `json:"decided_at"`
}

type referralFilter struct {
	Status     string
	InviteeID  *uuid.UUID
	ReferrerID *uuid.UUID
}

func listReferrals(ctx context.Context, q db.Querier, f referralFilter, page httpx.PageParams) ([]ReferralRow, int, error) {
	cond := ` WHERE ($1 = '' OR a.status = $1) AND ($2::uuid IS NULL OR a.invitee_user_id = $2)
		AND ($3::uuid IS NULL OR a.referrer_user_id = $3)`
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM referral_attributions a`+cond, f.Status, f.InviteeID, f.ReferrerID).
		Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.Query(ctx, `
		SELECT a.id, k.key, iu.id, iu.first_name, iu.last_name, iu.phone_e164, iu.telegram_user_id, iu.created_at,
		       ru.id, ru.first_name, ru.last_name, ru.phone_e164, ru.telegram_user_id, ru.created_at,
		       a.code, a.source, a.status, a.reason, o.number, a.attributed_at, a.qualified_at, a.decided_at
		FROM referral_attributions a
		JOIN marketing_campaigns k ON k.id = a.campaign_id
		JOIN users iu ON iu.id = a.invitee_user_id
		JOIN users ru ON ru.id = a.referrer_user_id
		LEFT JOIN orders o ON o.id = a.qualifying_order_id`+cond+`
		ORDER BY a.attributed_at DESC LIMIT $4 OFFSET $5`, f.Status, f.InviteeID, f.ReferrerID, page.PageSize, page.Offset())
	if err != nil {
		return nil, 0, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ReferralRow, error) {
		var a ReferralRow
		var if1, il, rf, rl string
		var ip, rp *string
		err := row.Scan(&a.ID, &a.Campaign, &a.Invitee.ID, &if1, &il, &ip, &a.Invitee.TelegramID, &a.Invitee.CreatedAt,
			&a.Referrer.ID, &rf, &rl, &rp, &a.Referrer.TelegramID, &a.Referrer.CreatedAt,
			&a.Code, &a.Source, &a.Status, &a.Reason, &a.QualifyingOrder, &a.AttributedAt, &a.QualifiedAt, &a.DecidedAt)
		a.Invitee.Name, a.Invitee.PhoneMasked = friendName(if1, il), maskPhone(ip)
		a.Referrer.Name, a.Referrer.PhoneMasked = friendName(rf, rl), maskPhone(rp)
		return a, err
	})
	return items, total, err
}

func (s *Service) adminListReferrals(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	referrer, err := httpx.QueryUUID(r, "referrer_id")
	if err != nil {
		return err
	}
	items, total, err := listReferrals(r.Context(), s.pool, referralFilter{Status: r.URL.Query().Get("status"),
		ReferrerID: referrer}, page)
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[ReferralRow]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

type reviewRequest struct {
	Decision string  `json:"decision"`
	Reason   *string `json:"reason"`
}

// adminReviewReferral decides a referral held for review: approve pays the
// reward (still once per referrer, within the budget), reject closes it.
func (s *Service) adminReviewReferral(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req reviewRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	reason, err := reasonText(req.Reason, true)
	if err != nil {
		return err
	}
	if req.Decision != "approve" && req.Decision != "reject" {
		return httpx.Validation(map[string]string{"decision": "invalid"})
	}
	var outcome string
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var campaignID, referrerID uuid.UUID
		var orderID *uuid.UUID
		var status string
		err := tx.QueryRow(ctx, `
			SELECT campaign_id, referrer_user_id, qualifying_order_id, status FROM referral_attributions WHERE id = $1 FOR UPDATE`,
			id).Scan(&campaignID, &referrerID, &orderID, &status)
		if db.IsNoRows(err) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		if status != ReferralReview {
			return httpx.NewError(409, "invalid_transition", "only a referral under review can be decided").
				WithDetails(map[string]any{"from": status, "action": req.Decision})
		}
		if req.Decision == "reject" {
			_, err := tx.Exec(ctx, `
				UPDATE referral_attributions SET status = 'rejected', decided_at = now(), decided_by = $2, decision_note = $3
				WHERE id = $1`, id, admin.AdminID, reason)
			outcome = "rejected"
			if err != nil {
				return err
			}
		} else {
			c, err := loadCampaign(ctx, tx, campaignID, false)
			if err != nil {
				return err
			}
			rules, _ := mustRules[ReferralRules](c.Rules)
			if orderID == nil {
				return httpx.NewError(409, "invalid_transition", "the referral has no qualifying ride")
			}
			if outcome, err = s.payReferral(ctx, tx, c, id, referrerID, *orderID, rules.RewardAmount, admin.AdminID); err != nil {
				return err
			}
			if outcome == "referral_review:budget_exhausted" {
				return httpx.NewError(409, "budget_insufficient", "the referral campaign budget is used up")
			}
			if _, err := tx.Exec(ctx, `UPDATE referral_attributions SET decision_note = $2 WHERE id = $1`, id, reason); err != nil {
				return err
			}
		}
		return audit.Log(ctx, tx, r, audit.Entry{Action: "marketing.referral." + req.Decision,
			EntityType: "referral_attribution", EntityID: id.String(),
			Before: map[string]any{"status": status}, After: map[string]any{"outcome": outcome, "reason": reason}})
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]string{"outcome": outcome})
}

// ApplicationRow is a promotion applied to an order (admin view).
type ApplicationRow struct {
	ID           uuid.UUID  `json:"id"`
	OrderID      uuid.UUID  `json:"order_id"`
	OrderNumber  int64      `json:"order_number"`
	OrderStatus  string     `json:"order_status"`
	UserID       uuid.UUID  `json:"user_id"`
	Kind         string     `json:"kind"`
	CampaignKey  *string    `json:"campaign_key"`
	GrossFare    int64      `json:"gross_fare"`
	Discount     int64      `json:"discount"`
	Status       string     `json:"status"`
	DriverCode   *string    `json:"driver_code"`
	Compensation *int64     `json:"compensation"`
	ReviewReason *string    `json:"review_reason"`
	ReservedAt   time.Time  `json:"reserved_at"`
	SettledAt    *time.Time `json:"settled_at"`
	ReleasedAt   *time.Time `json:"released_at"`
	ReversedAt   *time.Time `json:"reversed_at"`
}

type applicationFilter struct {
	Status string
	Review bool
	UserID *uuid.UUID
}

func listApplications(ctx context.Context, q db.Querier, f applicationFilter, page httpx.PageParams) ([]ApplicationRow, int, error) {
	cond := ` WHERE ($1 = '' OR op.status = $1) AND (NOT $2 OR op.review_reason IS NOT NULL) AND ($3::uuid IS NULL OR op.user_id = $3)`
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM order_promotions op`+cond, f.Status, f.Review, f.UserID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.Query(ctx, `
		SELECT op.id, op.order_id, o.number, o.status, op.user_id, op.kind, k.key, op.gross_fare, op.discount, op.status,
		       d.code, op.compensation, op.review_reason, op.reserved_at, op.settled_at, op.released_at, op.reversed_at
		FROM order_promotions op JOIN orders o ON o.id = op.order_id
		LEFT JOIN marketing_campaigns k ON k.id = op.campaign_id
		LEFT JOIN driver_profiles d ON d.id = op.driver_id`+cond+`
		ORDER BY op.reserved_at DESC LIMIT $4 OFFSET $5`, f.Status, f.Review, f.UserID, page.PageSize, page.Offset())
	if err != nil {
		return nil, 0, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ApplicationRow, error) {
		var a ApplicationRow
		err := row.Scan(&a.ID, &a.OrderID, &a.OrderNumber, &a.OrderStatus, &a.UserID, &a.Kind, &a.CampaignKey,
			&a.GrossFare, &a.Discount, &a.Status, &a.DriverCode, &a.Compensation, &a.ReviewReason, &a.ReservedAt,
			&a.SettledAt, &a.ReleasedAt, &a.ReversedAt)
		return a, err
	})
	return items, total, err
}

func (s *Service) adminListApplications(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	items, total, err := listApplications(r.Context(), s.pool, applicationFilter{Status: q.Get("status"),
		Review: q.Get("review") == "true"}, page)
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[ApplicationRow]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

// adminReverseApplication reverses a settled promotion on finance's decision
// (e.g. a ride cancelled after completion by a dispute): the driver's
// compensation is debited (only available money; else 409) and a spent bonus
// returns to the passenger. A redeemed coupon stays redeemed.
func (s *Service) adminReverseApplication(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Reason  *string `json:"reason"`
		Confirm bool    `json:"confirm"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	reason, err := reasonText(req.Reason, true)
	if err != nil {
		return err
	}
	if err := requireConfirm(req.Confirm); err != nil {
		return err
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var orderID, userID uuid.UUID
		var driverID *uuid.UUID
		var kind, status string
		var discount int64
		var key *string
		err := tx.QueryRow(ctx, `
			SELECT order_id, user_id, driver_id, kind, status, discount, compensation_key FROM order_promotions
			WHERE id = $1 FOR UPDATE`, id).Scan(&orderID, &userID, &driverID, &kind, &status, &discount, &key)
		if db.IsNoRows(err) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		if status != AppSettled || driverID == nil || key == nil {
			return httpx.NewError(409, "invalid_transition", "only a settled promotion can be reversed").
				WithDetails(map[string]any{"from": status, "action": "reverse"})
		}
		// Lock order as in SettleOrder: promotion → bonus account → wallet.
		var a bonusAccount
		if kind == KindBonus {
			if a, err = lockBonus(ctx, tx, userID); err != nil {
				return err
			}
		}
		if err := reverseCompensation(ctx, tx, *driverID, orderID, discount, *key, admin.AdminID, reason); err != nil {
			return err
		}
		if kind == KindBonus {
			if _, err := applyBonus(ctx, tx, &a, bonusEntry{typ: BonusAdjustmentCredit, balanceDelta: discount,
				orderID: &orderID, applicationID: &id, adminID: idPtr(admin.AdminID),
				key: "bonus_refund:" + id.String(), description: reason}); err != nil {
				return bonusError(err)
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE order_promotions SET status = 'reversed', reversed_at = now(), reversed_by = $2, reverse_reason = $3
			WHERE id = $1`, id, admin.AdminID, reason); err != nil {
			return err
		}
		if err := publishWallet(ctx, tx, *driverID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, r, audit.Entry{Action: "marketing.promotion.reverse", EntityType: "order_promotion",
			EntityID: id.String(), Before: map[string]any{"status": status},
			After: map[string]any{"status": AppReversed, "amount": discount, "reason": reason}})
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, map[string]string{"status": AppReversed})
}

// --- segments -------------------------------------------------------------------

func (s *Service) adminListSegments(w http.ResponseWriter, r *http.Request) error {
	items, err := listSegments(r.Context(), s.pool)
	if err != nil {
		return err
	}
	for i := range items {
		fields := map[string]string{}
		f := parseFilters(items[i].Audience, items[i].Filters, fields)
		if len(fields) == 0 && items[i].ArchivedAt == nil {
			c, err := countAudience(r.Context(), s.pool, items[i].Audience, f)
			if err != nil {
				return err
			}
			items[i].Count = &c
		}
	}
	return httpx.OK(w, map[string]any{"items": items})
}

func (s *Service) adminPreviewSegment(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Audience string          `json:"audience"`
		Filters  json.RawMessage `json:"filters"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	fields := map[string]string{}
	f := parseFilters(in.Audience, in.Filters, fields)
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	c, err := countAudience(r.Context(), s.pool, in.Audience, f)
	if err != nil {
		return err
	}
	return httpx.OK(w, c)
}

func (s *Service) saveSegmentHandler(w http.ResponseWriter, r *http.Request, id *uuid.UUID) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	var in segmentInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	var saved uuid.UUID
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if saved, err = saveSegment(r.Context(), tx, id, in, admin.AdminID); err != nil {
			return err
		}
		seg, err := loadSegment(r.Context(), tx, saved)
		if err != nil {
			return err
		}
		action := "marketing.segment.update"
		if id == nil {
			action = "marketing.segment.create"
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: action, EntityType: "marketing_segment",
			EntityID: saved.String(), After: seg})
	})
	if err != nil {
		return err
	}
	seg, err := loadSegment(r.Context(), s.pool, saved)
	if err != nil {
		return err
	}
	if id == nil {
		return httpx.Created(w, seg)
	}
	return httpx.OK(w, seg)
}

func (s *Service) adminCreateSegment(w http.ResponseWriter, r *http.Request) error {
	return s.saveSegmentHandler(w, r, nil)
}

func (s *Service) adminUpdateSegment(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	return s.saveSegmentHandler(w, r, &id)
}

// --- broadcasts -----------------------------------------------------------------

func (s *Service) adminListBroadcasts(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	items, total, err := listBroadcasts(r.Context(), s.pool, r.URL.Query().Get("status"), page)
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[Broadcast]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

// BroadcastDetail is GET /admin/marketing/broadcasts/{id}: the broadcast,
// its delivery counts and, before sending, the audience estimate.
type BroadcastDetail struct {
	Broadcast
	Estimate          *AudienceCount `json:"estimate"`
	ApprovalThreshold int64          `json:"approval_threshold"`
}

func (s *Service) broadcastDetail(r *http.Request, id uuid.UUID) (BroadcastDetail, error) {
	ctx := r.Context()
	b, err := loadBroadcast(ctx, s.pool, id, false)
	if err != nil {
		return BroadcastDetail{}, err
	}
	if b.Stats, err = broadcastStats(ctx, s.pool, id); err != nil {
		return BroadcastDetail{}, err
	}
	d := BroadcastDetail{Broadcast: b, ApprovalThreshold: settings.BroadcastApprovalThreshold(ctx, s.pool)}
	if b.Status == BroadcastDraft || b.Status == BroadcastScheduled {
		if f, err := broadcastFilters(ctx, s.pool, b); err == nil {
			if c, err := countAudience(ctx, s.pool, b.Audience, f); err == nil {
				d.Estimate = &c
			}
		}
	}
	return d, nil
}

func (s *Service) adminGetBroadcast(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	d, err := s.broadcastDetail(r, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

func (s *Service) saveBroadcastHandler(w http.ResponseWriter, r *http.Request, id *uuid.UUID) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	var in broadcastInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	var saved uuid.UUID
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if saved, err = saveBroadcast(r.Context(), tx, id, in, admin.AdminID); err != nil {
			return err
		}
		b, err := loadBroadcast(r.Context(), tx, saved, false)
		if err != nil {
			return err
		}
		action := "marketing.broadcast.update"
		if id == nil {
			action = "marketing.broadcast.create"
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: action, EntityType: "marketing_broadcast",
			EntityID: saved.String(), After: b})
	})
	if err != nil {
		return err
	}
	d, err := s.broadcastDetail(r, saved)
	if err != nil {
		return err
	}
	if id == nil {
		return httpx.Created(w, d)
	}
	return httpx.OK(w, d)
}

func (s *Service) adminCreateBroadcast(w http.ResponseWriter, r *http.Request) error {
	return s.saveBroadcastHandler(w, r, nil)
}

func (s *Service) adminUpdateBroadcast(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	return s.saveBroadcastHandler(w, r, &id)
}

func (s *Service) adminTestBroadcast(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		TelegramUserID int64 `json:"telegram_user_id"`
	}
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.TelegramUserID <= 0 {
		return httpx.Validation(map[string]string{"telegram_user_id": "required"})
	}
	var deliveryID int64
	err = s.tx(r, func(tx pgx.Tx) error {
		b, err := loadBroadcast(r.Context(), tx, id, true)
		if err != nil {
			return err
		}
		if deliveryID, err = queueTest(r.Context(), tx, b, req.TelegramUserID); err != nil {
			return err
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: "marketing.broadcast.test",
			EntityType: "marketing_broadcast", EntityID: id.String(), After: map[string]any{"delivery_id": deliveryID}})
	})
	if err != nil {
		return err
	}
	return httpx.Created(w, map[string]int64{"delivery_id": deliveryID})
}

type broadcastTransitionRequest struct {
	Action      string     `json:"action"`
	ScheduledAt *time.Time `json:"scheduled_at"`
	Reason      *string    `json:"reason"`
	Confirm     bool       `json:"confirm"`
}

func (s *Service) adminTransitionBroadcast(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req broadcastTransitionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	starts := req.Action == BroadcastActionSchedule || req.Action == BroadcastActionSendNow
	reason, err := reasonText(req.Reason, false)
	if err != nil {
		return err
	}
	if starts {
		if err := requireConfirm(req.Confirm); err != nil {
			return err
		}
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		b, err := loadBroadcast(r.Context(), tx, id, true)
		if err != nil {
			return err
		}
		if err := transitionBroadcast(r.Context(), tx, b, req.Action, req.ScheduledAt, admin.AdminID,
			admin.Can(auth.PermMarketingApprove), reason); err != nil {
			return err
		}
		after, err := loadBroadcast(r.Context(), tx, id, false)
		if err != nil {
			return err
		}
		return audit.Log(r.Context(), tx, r, audit.Entry{Action: "marketing.broadcast." + req.Action,
			EntityType: "marketing_broadcast", EntityID: id.String(), Before: map[string]any{"status": b.Status},
			After: map[string]any{"status": after.Status, "recipients": after.RecipientCount, "reason": reason}})
	})
	if err != nil {
		return err
	}
	d, err := s.broadcastDetail(r, id)
	if err != nil {
		return err
	}
	return httpx.OK(w, d)
}

func (s *Service) adminListRecipients(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	items, total, err := listRecipients(r.Context(), s.pool, id, r.URL.Query().Get("status"), page)
	if err != nil {
		return err
	}
	return httpx.OK(w, httpx.Page[BroadcastRecipient]{Items: items, Page: page.Page, PageSize: page.PageSize, Total: total})
}

// --- reconciliation, settings ------------------------------------------------------

func (s *Service) adminReconciliation(w http.ResponseWriter, r *http.Request) error {
	rep, err := Reconcile(r.Context(), s.pool)
	if err != nil {
		return err
	}
	return httpx.OK(w, rep)
}

// marketingSettingKeys are the app_settings keys of Marketing → Safety.
var marketingSettingKeys = []string{"marketing.awards_enabled", "marketing.redemptions_enabled",
	"marketing.broadcasts_enabled", "marketing.broadcast_rate_per_s", "marketing.broadcast_approval_threshold",
	"marketing.test_recipients"}

func (s *Service) adminSettings(w http.ResponseWriter, r *http.Request) error {
	all, err := settings.List(r.Context(), s.pool)
	if err != nil {
		return err
	}
	items := make([]settings.Item, 0, len(marketingSettingKeys))
	for _, it := range all {
		if slices.Contains(marketingSettingKeys, it.Key) {
			items = append(items, it)
		}
	}
	return httpx.OK(w, map[string]any{"items": items})
}

func (s *Service) adminUpdateSettings(w http.ResponseWriter, r *http.Request) error {
	admin, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	var body map[string]json.RawMessage
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		return err
	}
	var reasonRaw *string
	if raw, ok := body["reason"]; ok {
		var v string
		if json.Unmarshal(raw, &v) == nil {
			reasonRaw = &v
		}
		delete(body, "reason")
	}
	reason, err := reasonText(reasonRaw, true)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return httpx.BadRequest("no settings to update")
	}
	values := map[string]any{}
	fields := map[string]string{}
	for k, raw := range body {
		if !slices.Contains(marketingSettingKeys, k) {
			fields[k] = settings.ReasonUnknown
			continue
		}
		d, _ := settings.Lookup(k)
		norm, reason := d.Normalize(raw)
		if reason != "" {
			fields[k] = reason
			continue
		}
		var v any
		_ = json.Unmarshal(norm, &v)
		values[k] = v
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		for k, v := range values {
			var before any
			if err := tx.QueryRow(r.Context(), `SELECT value FROM app_settings WHERE key = $1`, k).Scan(&before); err != nil && !db.IsNoRows(err) {
				return err
			}
			if err := settings.Set(r.Context(), tx, k, v, admin.AdminID); err != nil {
				return err
			}
			if err := audit.Log(r.Context(), tx, r, audit.Entry{Action: "settings.update", EntityType: "app_setting",
				EntityID: k, Before: map[string]any{"value": before}, After: map[string]any{"value": v, "reason": reason}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for k := range values {
		settings.Invalidate(k)
	}
	return s.adminSettings(w, r)
}
