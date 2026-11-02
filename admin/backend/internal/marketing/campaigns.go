package marketing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// Campaign is a campaign with its current version (openapi MarketingCampaign).
type Campaign struct {
	ID                uuid.UUID          `json:"id"`
	Key               string             `json:"key"`
	Template          string             `json:"template"`
	Status            string             `json:"status"`
	IsSystem          bool               `json:"is_system"`
	Version           int                `json:"version"`
	VersionID         uuid.UUID          `json:"version_id"`
	Title             settings.Localized `json:"title"`
	Description       settings.Localized `json:"description"`
	Rules             json.RawMessage    `json:"rules"`
	StartsAt          *time.Time         `json:"starts_at"`
	EndsAt            *time.Time         `json:"ends_at"`
	BudgetLimit       *int64             `json:"budget_limit"`
	RedemptionsFrozen bool               `json:"redemptions_frozen"`
	Priority          int                `json:"priority"`
	ExclusivityGroup  *string            `json:"exclusivity_group"`
	ScheduledAt       *time.Time         `json:"scheduled_at"`
	ActiveSince       *time.Time         `json:"active_since"`
	ActivatedAt       *time.Time         `json:"activated_at"`
	PausedAt          *time.Time         `json:"paused_at"`
	EndedAt           *time.Time         `json:"ended_at"`
	ArchivedAt        *time.Time         `json:"archived_at"`
	CreatedBy         *uuid.UUID         `json:"created_by"`
	ApprovedBy        *uuid.UUID         `json:"approved_by"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	Usage             *Usage             `json:"usage,omitempty"`
}

// Version is one immutable rules/copy version (openapi MarketingCampaignVersion).
type Version struct {
	ID          uuid.UUID          `json:"id"`
	Version     int                `json:"version"`
	Title       settings.Localized `json:"title"`
	Description settings.Localized `json:"description"`
	Rules       json.RawMessage    `json:"rules"`
	StartsAt    *time.Time         `json:"starts_at"`
	EndsAt      *time.Time         `json:"ends_at"`
	ChangeNote  *string            `json:"change_note"`
	CreatedBy   *uuid.UUID         `json:"created_by"`
	CreatedAt   time.Time          `json:"created_at"`
}

// Usage is what a campaign has committed and spent (budget accounting,
// §23.8): committed = unexpired issued and reserved coupons plus pending
// grants; spent = redeemed coupons plus credited grants.
type Usage struct {
	Committed int64          `json:"committed"`
	Spent     int64          `json:"spent"`
	Remaining *int64         `json:"remaining"`
	Coupons   map[string]int `json:"coupons"`
	Grants    map[string]int `json:"grants"`
}

const campaignColumns = `c.id, c.key, c.template, c.status, c.is_system, c.current_version, v.id, v.title, v.description,
	v.rules, v.starts_at, v.ends_at, c.budget_limit, c.redemptions_frozen, c.priority, c.exclusivity_group,
	c.scheduled_at, c.active_since, c.activated_at, c.paused_at, c.ended_at, c.archived_at, c.created_by,
	c.approved_by, c.created_at, c.updated_at`

const campaignFrom = ` FROM marketing_campaigns c
	JOIN marketing_campaign_versions v ON v.campaign_id = c.id AND v.version = c.current_version`

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	var title, desc []byte
	err := row.Scan(&c.ID, &c.Key, &c.Template, &c.Status, &c.IsSystem, &c.Version, &c.VersionID, &title, &desc,
		&c.Rules, &c.StartsAt, &c.EndsAt, &c.BudgetLimit, &c.RedemptionsFrozen, &c.Priority, &c.ExclusivityGroup,
		&c.ScheduledAt, &c.ActiveSince, &c.ActivatedAt, &c.PausedAt, &c.EndedAt, &c.ArchivedAt, &c.CreatedBy,
		&c.ApprovedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Campaign{}, err
	}
	_ = json.Unmarshal(title, &c.Title)
	_ = json.Unmarshal(desc, &c.Description)
	return c, nil
}

// loadCampaign reads a campaign; lock takes its row lock (FOR UPDATE).
func loadCampaign(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (Campaign, error) {
	sql := `SELECT ` + campaignColumns + campaignFrom + ` WHERE c.id = $1`
	if lock {
		sql += ` FOR UPDATE OF c`
	}
	c, err := scanCampaign(q.QueryRow(ctx, sql, id))
	if db.IsNoRows(err) {
		return Campaign{}, httpx.NotFound()
	}
	if err != nil {
		return Campaign{}, fmt.Errorf("marketing: load campaign: %w", err)
	}
	return c, nil
}

// liveCampaigns returns the active campaigns of the given templates whose
// current version covers at (nil = now), highest priority first.
func liveCampaigns(ctx context.Context, q db.Querier, templates []string, at *time.Time) ([]Campaign, error) {
	rows, err := q.Query(ctx, `SELECT `+campaignColumns+campaignFrom+`
		WHERE c.status = 'active' AND c.template = ANY($1)
		  AND (v.starts_at IS NULL OR v.starts_at <= COALESCE($2, now()))
		  AND (v.ends_at IS NULL OR v.ends_at > COALESCE($2, now()))
		ORDER BY c.priority, c.created_at`, templates, at)
	if err != nil {
		return nil, fmt.Errorf("marketing: live campaigns: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Campaign, error) { return scanCampaign(row) })
}

// latestCampaign returns the most recent non-archived campaign of template
// (live ones first), or nil.
func latestCampaign(ctx context.Context, q db.Querier, template string) (*Campaign, error) {
	c, err := scanCampaign(q.QueryRow(ctx, `SELECT `+campaignColumns+campaignFrom+`
		WHERE c.template = $1 AND c.status <> 'archived'
		ORDER BY (c.status IN ('active', 'paused', 'scheduled')) DESC, c.created_at DESC LIMIT 1`, template))
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("marketing: latest campaign: %w", err)
	}
	return &c, nil
}

// usage computes a campaign's budget usage.
func usage(ctx context.Context, q db.Querier, c Campaign) (Usage, error) {
	u := Usage{Coupons: map[string]int{}, Grants: map[string]int{}}
	rows, err := q.Query(ctx, `
		SELECT CASE WHEN status = 'issued' AND expires_at <= now() THEN 'expired' ELSE status END, count(*),
		       COALESCE(sum(amount), 0)::bigint
		FROM marketing_coupons WHERE campaign_id = $1 GROUP BY 1`, c.ID)
	if err != nil {
		return Usage{}, fmt.Errorf("marketing: coupon usage: %w", err)
	}
	for rows.Next() {
		var status string
		var n int
		var sum int64
		if err := rows.Scan(&status, &n, &sum); err != nil {
			rows.Close()
			return Usage{}, err
		}
		u.Coupons[status] += n
		switch status {
		case CouponIssued, CouponReserved:
			u.Committed += sum
		case CouponRedeemed:
			u.Spent += sum
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Usage{}, err
	}
	rows, err = q.Query(ctx, `
		SELECT status, count(*), COALESCE(sum(amount), 0)::bigint FROM marketing_grants WHERE campaign_id = $1 GROUP BY 1`, c.ID)
	if err != nil {
		return Usage{}, fmt.Errorf("marketing: grant usage: %w", err)
	}
	for rows.Next() {
		var status string
		var n int
		var sum int64
		if err := rows.Scan(&status, &n, &sum); err != nil {
			rows.Close()
			return Usage{}, err
		}
		u.Grants[status] += n
		switch status {
		case "pending":
			u.Committed += sum
		case "credited":
			u.Spent += sum
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Usage{}, err
	}
	if c.BudgetLimit != nil {
		r := max(*c.BudgetLimit-u.Committed-u.Spent, 0)
		u.Remaining = &r
	}
	return u, nil
}

// budgetAllows locks the campaign row and reports whether amount more fits
// the budget. Callers write the coupon or grant in the same transaction, so
// concurrent awards of one campaign never overspend.
func budgetAllows(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID, amount int64) (bool, error) {
	c, err := loadCampaign(ctx, tx, campaignID, true)
	if err != nil {
		return false, err
	}
	if c.BudgetLimit == nil {
		return true, nil
	}
	u, err := usage(ctx, tx, c)
	if err != nil {
		return false, err
	}
	return u.Committed+u.Spent+amount <= *c.BudgetLimit, nil
}

// --- creation and versions ----------------------------------------------------

// campaignInput is the create / new-version body (openapi MarketingCampaignInput).
type campaignInput struct {
	Template         *string             `json:"template"`
	Key              *string             `json:"key"`
	Title            *settings.Localized `json:"title"`
	Description      *settings.Localized `json:"description"`
	Rules            json.RawMessage     `json:"rules"`
	StartsAt         *time.Time          `json:"starts_at"`
	EndsAt           *time.Time          `json:"ends_at"`
	BudgetLimit      *int64              `json:"budget_limit"`
	Priority         *int                `json:"priority"`
	ExclusivityGroup *string             `json:"exclusivity_group"`
	ChangeNote       *string             `json:"change_note"`
}

const (
	maxTitle       = 80
	maxDescription = 600
)

var keyChars = func(r rune) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' }

// cleanLocalized validates a localized text: every language required up to
// max code points (optional texts may be entirely empty).
func cleanLocalized(fields map[string]string, name string, in *settings.Localized, max int, required bool) settings.Localized {
	var out settings.Localized
	if in == nil {
		if required {
			fields[name] = "required"
		}
		return out
	}
	clean := func(lang, s string) string {
		v, err := textutil.CleanText(s, max)
		if err != nil {
			fields[name+"."+lang] = "too_long"
		}
		return v
	}
	out.KK, out.RU, out.EN = clean("kk", in.KK), clean("ru", in.RU), clean("en", in.EN)
	if out.IsZero() && !required {
		return out
	}
	for lang, v := range map[string]string{"kk": out.KK, "ru": out.RU, "en": out.EN} {
		if v == "" && fields[name+"."+lang] == "" {
			fields[name+"."+lang] = "required"
		}
	}
	return out
}

func randomSuffix() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// createCampaign stores a new draft campaign with version 1.
func createCampaign(ctx context.Context, tx pgx.Tx, in campaignInput, adminID uuid.UUID) (uuid.UUID, error) {
	fields := map[string]string{}
	template := ""
	if in.Template == nil || !slices.Contains(Templates, *in.Template) {
		fields["template"] = "invalid"
	} else {
		template = *in.Template
	}
	key := ""
	if in.Key != nil && strings.TrimSpace(*in.Key) != "" {
		key = strings.TrimSpace(*in.Key)
		if len(key) < 3 || len(key) > 40 || strings.IndexFunc(key, func(r rune) bool { return !keyChars(r) }) >= 0 {
			fields["key"] = "invalid"
		}
	} else if template != "" {
		key = template + "_" + randomSuffix()
		if len(key) > 40 {
			key = key[len(key)-40:]
		}
	}
	title := cleanLocalized(fields, "title", in.Title, maxTitle, true)
	desc := cleanLocalized(fields, "description", in.Description, maxDescription, false)
	if template != "" {
		if _, rf := ParseRules(template, in.Rules); len(rf) > 0 {
			for k, v := range rf {
				fields[k] = v
			}
		}
	}
	checkWindow(fields, template, in.StartsAt, in.EndsAt)
	if in.BudgetLimit != nil && (*in.BudgetLimit < 0 || *in.BudgetLimit > 1_000_000_000) {
		fields["budget_limit"] = "out_of_range"
	}
	priority := 100
	if in.Priority != nil {
		if *in.Priority < 0 || *in.Priority > 1000 {
			fields["priority"] = "out_of_range"
		}
		priority = *in.Priority
	}
	var group *string
	if in.ExclusivityGroup != nil && strings.TrimSpace(*in.ExclusivityGroup) != "" {
		g := strings.TrimSpace(*in.ExclusivityGroup)
		if len(g) > 40 || strings.IndexFunc(g, func(r rune) bool { return !keyChars(r) }) >= 0 {
			fields["exclusivity_group"] = "invalid"
		}
		group = &g
	}
	if template == TemplatePassengerCredit {
		if err := checkSegment(ctx, tx, in.Rules, fields); err != nil {
			return uuid.Nil, err
		}
	}
	if len(fields) > 0 {
		return uuid.Nil, httpx.Validation(fields)
	}
	rules, _ := ParseRules(template, in.Rules)
	rulesJSON, _ := json.Marshal(rules)
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO marketing_campaigns (key, template, budget_limit, priority, exclusivity_group, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		key, template, in.BudgetLimit, priority, group, idPtr(adminID)).Scan(&id)
	if db.IsUniqueViolation(err, "marketing_campaigns_key_key") {
		return uuid.Nil, httpx.Validation(map[string]string{"key": "taken"})
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("marketing: insert campaign: %w", err)
	}
	if err := insertVersion(ctx, tx, id, 1, title, desc, rulesJSON, in.StartsAt, in.EndsAt, in.ChangeNote, adminID); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// checkWindow validates a version's validity window.
func checkWindow(fields map[string]string, template string, starts, ends *time.Time) {
	if starts != nil && ends != nil && !ends.After(*starts) {
		fields["ends_at"] = "before_start"
	}
	if template == TemplateDriverMilestone && (starts == nil || ends == nil) {
		fields["ends_at"] = "window_required"
	}
}

// checkSegment requires the passenger_credit segment to exist, live and
// target passengers.
func checkSegment(ctx context.Context, q db.Querier, raw json.RawMessage, fields map[string]string) error {
	r, ok := mustRules[CreditRules](raw)
	if !ok || r.SegmentID == uuid.Nil {
		return nil
	}
	var audience string
	err := q.QueryRow(ctx, `SELECT audience FROM marketing_segments WHERE id = $1 AND archived_at IS NULL`, r.SegmentID).
		Scan(&audience)
	switch {
	case db.IsNoRows(err):
		fields["rules.segment_id"] = "not_found"
	case err != nil:
		return fmt.Errorf("marketing: segment: %w", err)
	case audience != AudiencePassengers:
		fields["rules.segment_id"] = "wrong_audience"
	}
	return nil
}

func insertVersion(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID, version int, title, desc settings.Localized,
	rules json.RawMessage, starts, ends *time.Time, note *string, adminID uuid.UUID) error {
	t, _ := json.Marshal(title)
	d, _ := json.Marshal(desc)
	var n *string
	if note != nil {
		if v, err := textutil.CleanText(*note, 300); err == nil && v != "" {
			n = &v
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO marketing_campaign_versions (campaign_id, version, title, description, rules, starts_at, ends_at,
		                                         change_note, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		campaignID, version, t, d, []byte(rules), starts, ends, n, idPtr(adminID)); err != nil {
		return fmt.Errorf("marketing: insert version: %w", err)
	}
	return nil
}

// newVersion stores the campaign's next version: the current one with the
// given changes. Coupons and grants already issued keep their own version.
func newVersion(ctx context.Context, tx pgx.Tx, c Campaign, in campaignInput, adminID uuid.UUID) error {
	fields := map[string]string{}
	title, desc := c.Title, c.Description
	if in.Title != nil {
		title = cleanLocalized(fields, "title", in.Title, maxTitle, true)
	}
	if in.Description != nil {
		desc = cleanLocalized(fields, "description", in.Description, maxDescription, false)
	}
	rules := c.Rules
	if len(in.Rules) > 0 {
		parsed, rf := ParseRules(c.Template, in.Rules)
		for k, v := range rf {
			fields[k] = v
		}
		if len(rf) == 0 {
			rules, _ = json.Marshal(parsed)
		}
		if c.Template == TemplatePassengerCredit {
			if c.Status != StatusDraft {
				fields["rules"] = "locked_after_activation"
			}
			if err := checkSegment(ctx, tx, in.Rules, fields); err != nil {
				return err
			}
		}
	}
	starts, ends := c.StartsAt, c.EndsAt
	if in.StartsAt != nil {
		starts = in.StartsAt
	}
	if in.EndsAt != nil {
		ends = in.EndsAt
	}
	checkWindow(fields, c.Template, starts, ends)
	if in.Template != nil && *in.Template != c.Template {
		fields["template"] = "immutable"
	}
	if in.Key != nil && *in.Key != c.Key {
		fields["key"] = "immutable"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	next := c.Version + 1
	if err := insertVersion(ctx, tx, c.ID, next, title, desc, rules, starts, ends, in.ChangeNote, adminID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE marketing_campaigns SET current_version = $2 WHERE id = $1`, c.ID, next); err != nil {
		return fmt.Errorf("marketing: bump version: %w", err)
	}
	return nil
}

// listVersions returns every version of a campaign, newest first.
func listVersions(ctx context.Context, q db.Querier, campaignID uuid.UUID) ([]Version, error) {
	rows, err := q.Query(ctx, `
		SELECT id, version, title, description, rules, starts_at, ends_at, change_note, created_by, created_at
		FROM marketing_campaign_versions WHERE campaign_id = $1 ORDER BY version DESC`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("marketing: versions: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Version, error) {
		var v Version
		var t, d []byte
		err := row.Scan(&v.ID, &v.Version, &t, &d, &v.Rules, &v.StartsAt, &v.EndsAt, &v.ChangeNote, &v.CreatedBy, &v.CreatedAt)
		_ = json.Unmarshal(t, &v.Title)
		_ = json.Unmarshal(d, &v.Description)
		return v, err
	})
}

// --- status transitions -------------------------------------------------------

// Campaign actions and the statuses they apply to (§23.2).
const (
	ActionSchedule   = "schedule"
	ActionUnschedule = "unschedule"
	ActionActivate   = "activate"
	ActionPause      = "pause"
	ActionResume     = "resume"
	ActionEnd        = "end"
	ActionArchive    = "archive"
	ActionFreeze     = "freeze_redemptions"
	ActionUnfreeze   = "unfreeze_redemptions"
)

type transition struct {
	from    []string
	to      string // "" keeps the status (freeze/unfreeze)
	approve bool   // needs marketing.approve (money starts or resumes flowing)
}

var transitions = map[string]transition{
	ActionSchedule:   {from: []string{StatusDraft}, to: StatusScheduled, approve: true},
	ActionUnschedule: {from: []string{StatusScheduled}, to: StatusDraft},
	ActionActivate:   {from: []string{StatusDraft, StatusScheduled}, to: StatusActive, approve: true},
	ActionPause:      {from: []string{StatusActive}, to: StatusPaused},
	ActionResume:     {from: []string{StatusPaused}, to: StatusActive, approve: true},
	ActionEnd:        {from: []string{StatusActive, StatusPaused, StatusScheduled}, to: StatusEnded, approve: true},
	ActionArchive:    {from: []string{StatusDraft, StatusEnded}, to: StatusArchived},
	ActionFreeze: {from: []string{StatusDraft, StatusScheduled, StatusActive, StatusPaused, StatusEnded},
		approve: true},
	ActionUnfreeze: {from: []string{StatusDraft, StatusScheduled, StatusActive, StatusPaused, StatusEnded},
		approve: true},
}

// applyTransition moves a locked campaign through action. scheduledAt is
// required for schedule. Activating a passenger_credit campaign snapshots its
// recipients as pending grants (credited by the grants job).
func applyTransition(ctx context.Context, tx pgx.Tx, c Campaign, action string, scheduledAt *time.Time,
	adminID uuid.UUID) error {
	t, ok := transitions[action]
	if !ok {
		return httpx.Validation(map[string]string{"action": "invalid"})
	}
	if !slices.Contains(t.from, c.Status) {
		return httpx.NewError(409, "invalid_transition", "campaign cannot "+action+" from "+c.Status).
			WithDetails(map[string]any{"from": c.Status, "action": action})
	}
	if c.Template == TemplateManualGrant && t.to != "" && t.to != StatusArchived {
		// The manual bucket has no lifecycle: operators' credits do not depend on it.
		return errCampaignConflict("manual adjustments have no schedule")
	}
	var err error
	switch action {
	case ActionSchedule:
		if scheduledAt == nil || !scheduledAt.After(time.Now()) {
			return httpx.Validation(map[string]string{"scheduled_at": "must_be_future"})
		}
		_, err = tx.Exec(ctx, `UPDATE marketing_campaigns SET status = 'scheduled', scheduled_at = $2, approved_by = $3 WHERE id = $1`,
			c.ID, *scheduledAt, idPtr(adminID))
	case ActionUnschedule:
		_, err = tx.Exec(ctx, `UPDATE marketing_campaigns SET status = 'draft', scheduled_at = NULL WHERE id = $1`, c.ID)
	case ActionActivate, ActionResume:
		if err := beforeActivation(ctx, tx, c); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE marketing_campaigns SET status = 'active', active_since = now(), activated_at = COALESCE(activated_at, now()),
			       paused_at = NULL, approved_by = COALESCE($2, approved_by)
			WHERE id = $1`, c.ID, idPtr(adminID))
		if db.IsUniqueViolation(err, "marketing_campaigns_one_live_launch") {
			return errCampaignConflict("another campaign of this kind is already live")
		}
		if err == nil && action == ActionActivate && c.Template == TemplatePassengerCredit {
			err = snapshotCredits(ctx, tx, c)
		}
	case ActionPause:
		_, err = tx.Exec(ctx, `UPDATE marketing_campaigns SET status = 'paused', paused_at = now() WHERE id = $1`, c.ID)
	case ActionEnd:
		_, err = tx.Exec(ctx, `UPDATE marketing_campaigns SET status = 'ended', ended_at = now() WHERE id = $1`, c.ID)
		if err == nil {
			// Credits not yet paid out are not paid after the end.
			_, err = tx.Exec(ctx, `
				UPDATE marketing_grants SET status = 'skipped', skip_reason = 'campaign_ended'
				WHERE campaign_id = $1 AND status = 'pending'`, c.ID)
		}
	case ActionArchive:
		_, err = tx.Exec(ctx, `UPDATE marketing_campaigns SET status = 'archived', archived_at = now() WHERE id = $1`, c.ID)
	case ActionFreeze, ActionUnfreeze:
		_, err = tx.Exec(ctx, `UPDATE marketing_campaigns SET redemptions_frozen = $2 WHERE id = $1`, c.ID, action == ActionFreeze)
	}
	if err != nil {
		if he := httpx.AsError(err); he != nil && he.Status != 500 {
			return err
		}
		return fmt.Errorf("marketing: %s campaign: %w", action, err)
	}
	return nil
}

// beforeActivation refuses an activation that could not work as configured.
func beforeActivation(ctx context.Context, tx pgx.Tx, c Campaign) error {
	if c.EndsAt != nil && !c.EndsAt.After(time.Now()) {
		return httpx.Validation(map[string]string{"ends_at": "in_past"})
	}
	if c.Template == TemplatePassengerCredit {
		fields := map[string]string{}
		if err := checkSegment(ctx, tx, c.Rules, fields); err != nil {
			return err
		}
		if len(fields) > 0 {
			return httpx.Validation(fields)
		}
	}
	return nil
}

// ActivateDue starts scheduled campaigns whose time has come and ends active
// ones whose version window closed (job marketing_campaign_schedule).
func (s *Service) ActivateDue(ctx context.Context) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT c.id FROM marketing_campaigns c WHERE c.status = 'scheduled' AND c.scheduled_at <= now()
			FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			c, err := loadCampaign(ctx, tx, id, true)
			if err != nil {
				return err
			}
			if err := applyTransition(ctx, tx, c, ActionActivate, nil, uuid.Nil); err != nil {
				s.log.Warn("scheduled campaign not activated", "campaign", c.Key, "err", err)
				if _, err := tx.Exec(ctx, `UPDATE marketing_campaigns SET status = 'draft', scheduled_at = NULL WHERE id = $1`, id); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec(ctx, `
			UPDATE marketing_campaigns c SET status = 'ended', ended_at = now()
			FROM marketing_campaign_versions v
			WHERE v.campaign_id = c.id AND v.version = c.current_version AND c.status IN ('active', 'paused')
			  AND v.ends_at IS NOT NULL AND v.ends_at <= now()`)
		return err
	})
}

// listCampaigns lists campaigns for the admin panel.
func listCampaigns(ctx context.Context, q db.Querier, status, template, search string, page httpx.PageParams) ([]Campaign, int, error) {
	cond := ` WHERE ($1 = '' OR c.status = $1) AND ($2 = '' OR c.template = $2)
		AND ($3 = '' OR c.key ILIKE '%' || $3 || '%' OR v.title::text ILIKE '%' || $3 || '%')`
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*)`+campaignFrom+cond, status, template, search).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("marketing: count campaigns: %w", err)
	}
	rows, err := q.Query(ctx, `SELECT `+campaignColumns+campaignFrom+cond+`
		ORDER BY (c.status = 'archived'), c.priority, c.created_at DESC
		LIMIT $4 OFFSET $5`, status, template, search, page.PageSize, page.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("marketing: list campaigns: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Campaign, error) { return scanCampaign(row) })
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		u, err := usage(ctx, q, items[i])
		if err != nil {
			return nil, 0, err
		}
		items[i].Usage = &u
	}
	return items, total, nil
}

// itoa is strconv.Itoa for audit ids.
func itoa(n int) string { return strconv.Itoa(n) }
