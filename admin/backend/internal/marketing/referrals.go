package marketing

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
)

// Referrals (§23.5). A passenger who may invite gets an opaque 8-character
// code; the link is t.me/<passenger bot>?start=ref_<code> (the bot) or
// ?startapp=ref_<code> (the Mini App). The code is bound to an invitee once,
// on the invitee's first arrival through a signed channel (the bot update or
// the Mini App's signed initData), never from a value the app sends.

// referral code alphabet: no 0/O, 1/I, so codes survive being typed.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var refPayloadRe = regexp.MustCompile(`^ref_([A-Za-z2-9]{8})$`)

// ReferralCodeFromPayload extracts the code of a "ref_<code>" start payload.
func ReferralCodeFromPayload(payload string) (string, bool) {
	m := refPayloadRe.FindStringSubmatch(strings.TrimSpace(payload))
	if m == nil {
		return "", false
	}
	return strings.ToUpper(m[1]), true
}

func newReferralCode() (string, error) {
	var b strings.Builder
	n := big.NewInt(int64(len(codeAlphabet)))
	for range 8 {
		i, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b.WriteByte(codeAlphabet[i.Int64()])
	}
	return b.String(), nil
}

// referralCode returns the passenger's code, creating it on first use.
func referralCode(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (string, error) {
	var code string
	err := tx.QueryRow(ctx, `SELECT code FROM referral_codes WHERE user_id = $1 AND disabled_at IS NULL`, userID).Scan(&code)
	if err == nil {
		return code, nil
	}
	if !db.IsNoRows(err) {
		return "", err
	}
	var disabled bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM referral_codes WHERE user_id = $1)`, userID).Scan(&disabled); err != nil {
		return "", err
	}
	if disabled {
		return "", nil // an operator disabled this passenger's invitations
	}
	for range 5 {
		c, err := newReferralCode()
		if err != nil {
			return "", err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO referral_codes (user_id, code) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, c)
		if err != nil {
			return "", fmt.Errorf("marketing: referral code: %w", err)
		}
		if tag.RowsAffected() == 1 {
			return c, nil
		}
		// A concurrent request created the passenger's code, or the random
		// code collided: read again.
		if err := tx.QueryRow(ctx, `SELECT code FROM referral_codes WHERE user_id = $1`, userID).Scan(&code); err == nil {
			return code, nil
		}
	}
	return "", fmt.Errorf("marketing: no free referral code")
}

// referralUnlocked reports whether the passenger may invite friends under
// policy. The caller selects the active campaign; the other policies retain
// their ride/coupon gates. The passenger view also checks the awards switch.
func referralUnlocked(ctx context.Context, q db.Querier, userID uuid.UUID, policy string) (bool, error) {
	var sql string
	switch policy {
	case UnlockImmediate:
		return true, nil
	case UnlockFirstRideCompleted:
		sql = `SELECT EXISTS (SELECT 1 FROM orders WHERE passenger_id = $1 AND status = 'completed')`
	case UnlockCouponRedeemedOrExpired:
		sql = `SELECT EXISTS (SELECT 1 FROM marketing_coupons c JOIN marketing_campaigns k ON k.id = c.campaign_id
			WHERE c.user_id = $1 AND k.template = 'first_ride_coupon'
			  AND (c.status IN ('redeemed', 'expired') OR (c.status = 'issued' AND c.expires_at <= now())))`
	default:
		sql = `SELECT EXISTS (SELECT 1 FROM marketing_coupons c JOIN marketing_campaigns k ON k.id = c.campaign_id
			WHERE c.user_id = $1 AND k.template = 'first_ride_coupon' AND c.status = 'redeemed')`
	}
	var ok bool
	err := q.QueryRow(ctx, sql, userID).Scan(&ok)
	return ok, err
}

// Capture outcomes (referral_events.outcome).
const (
	captureAttributed = "attributed"
	captureDuplicate  = "duplicate"
	captureRejected   = "rejected"
)

// CaptureResult reports a referral capture.
type CaptureResult struct {
	Attributed bool   `json:"attributed"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
}

// CaptureReferral binds the invitee to the code's owner when every rule
// holds (§23.5): an active referral campaign, a valid code of an unblocked
// passenger who may invite, no self-invitation, and a genuinely new invitee —
// account created within the campaign's attribution window, no orders, no
// driver profile and no earlier attribution (the first code wins; nothing
// rewrites it). Every attempt is kept in referral_events.
func (s *Service) CaptureReferral(ctx context.Context, inviteeID uuid.UUID, payload, source string) (CaptureResult, error) {
	code, ok := ReferralCodeFromPayload(payload)
	if !ok {
		return CaptureResult{Outcome: captureRejected, Reason: "invalid_code"}, nil
	}
	var res CaptureResult
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		res, err = captureReferral(ctx, tx, inviteeID, code, source)
		return err
	})
	if err != nil {
		return CaptureResult{}, err
	}
	s.log.Info("referral capture", slog.String("outcome", res.Outcome), slog.String("reason", res.Reason),
		slog.String("source", source))
	return res, nil
}

func captureReferral(ctx context.Context, tx pgx.Tx, inviteeID uuid.UUID, code, source string) (CaptureResult, error) {
	var referrerID *uuid.UUID
	record := func(outcome, reason string) (CaptureResult, error) {
		var r *string
		if reason != "" {
			r = &reason
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO referral_events (code, invitee_user_id, referrer_user_id, source, outcome, reason)
			VALUES ($1, $2, $3, $4, $5, $6)`, code, inviteeID, referrerID, source, outcome, r); err != nil {
			return CaptureResult{}, fmt.Errorf("marketing: referral event: %w", err)
		}
		return CaptureResult{Attributed: outcome == captureAttributed, Outcome: outcome, Reason: reason}, nil
	}
	// The invitee row is locked so two simultaneous arrivals (bot /start and
	// the Mini App) cannot both pass the checks.
	var created time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM users WHERE id = $1 FOR UPDATE`, inviteeID).Scan(&created); err != nil {
		return CaptureResult{}, fmt.Errorf("marketing: invitee: %w", err)
	}
	var existing uuid.UUID
	err := tx.QueryRow(ctx, `SELECT referrer_user_id FROM referral_attributions WHERE invitee_user_id = $1`, inviteeID).Scan(&existing)
	if err == nil {
		referrerID = &existing
		return record(captureDuplicate, "already_attributed")
	}
	if !db.IsNoRows(err) {
		return CaptureResult{}, err
	}
	live, err := liveCampaigns(ctx, tx, []string{TemplateReferral}, nil)
	if err != nil {
		return CaptureResult{}, err
	}
	if len(live) == 0 {
		return record(captureRejected, "campaign_inactive")
	}
	c := live[0]
	rules, ok := mustRules[ReferralRules](c.Rules)
	if !ok {
		return record(captureRejected, "campaign_inactive")
	}
	var owner uuid.UUID
	var ownerBlocked bool
	err = tx.QueryRow(ctx, `
		SELECT rc.user_id, u.is_blocked FROM referral_codes rc JOIN users u ON u.id = rc.user_id
		WHERE rc.code = $1 AND rc.disabled_at IS NULL`, code).Scan(&owner, &ownerBlocked)
	if db.IsNoRows(err) {
		return record(captureRejected, "unknown_code")
	}
	if err != nil {
		return CaptureResult{}, err
	}
	referrerID = &owner
	switch {
	case owner == inviteeID:
		return record(captureRejected, "self_referral")
	case ownerBlocked:
		return record(captureRejected, "referrer_blocked")
	}
	if unlocked, err := referralUnlocked(ctx, tx, owner, rules.Unlock); err != nil {
		return CaptureResult{}, err
	} else if !unlocked {
		return record(captureRejected, "referrer_locked")
	}
	var hasOrders, isDriver, fresh bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM orders WHERE passenger_id = $1),
		       EXISTS (SELECT 1 FROM driver_profiles WHERE user_id = $1),
		       $2::timestamptz > now() - make_interval(mins => $3)`,
		inviteeID, created, rules.AttributionWindowMinutes).Scan(&hasOrders, &isDriver, &fresh); err != nil {
		return CaptureResult{}, err
	}
	switch {
	case hasOrders:
		return record(captureRejected, "invitee_has_orders")
	case isDriver:
		return record(captureRejected, "invitee_is_driver")
	case !fresh:
		return record(captureRejected, "invitee_not_new")
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO referral_attributions (campaign_id, version_id, invitee_user_id, referrer_user_id, code, source)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (invitee_user_id) DO NOTHING`,
		c.ID, c.VersionID, inviteeID, owner, code, source)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("marketing: attribution: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return record(captureDuplicate, "already_attributed")
	}
	if err := publishPromotions(ctx, tx, owner); err != nil {
		return CaptureResult{}, err
	}
	return record(captureAttributed, "")
}

// HandleStartParam is the auth start-parameter hook: a passenger who opened
// the Mini App from a referral link (startapp=ref_<code>, inside the signed
// initData) is captured like a bot /start. Errors are logged, never returned:
// signing in must not depend on marketing.
func (s *Service) HandleStartParam(ctx context.Context, userID uuid.UUID, startParam string) {
	if !strings.HasPrefix(startParam, "ref_") {
		return
	}
	if _, err := s.CaptureReferral(ctx, userID, startParam, "miniapp"); err != nil {
		s.log.Warn("referral capture failed", slog.Any("err", err))
	}
}
