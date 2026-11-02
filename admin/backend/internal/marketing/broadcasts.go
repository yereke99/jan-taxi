package marketing

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// Broadcasts (§23.7): a marketing message to one bot's audience, sent by its
// own worker at a low rate, never through the operational outbox, so order
// messages keep their capacity.

// Broadcast statuses.
const (
	BroadcastDraft     = "draft"
	BroadcastScheduled = "scheduled"
	BroadcastSending   = "sending"
	BroadcastPaused    = "paused"
	BroadcastCompleted = "completed"
	BroadcastCancelled = "cancelled"
	BroadcastFailed    = "failed"
)

// Recipient statuses.
const (
	RecipientQueued     = "queued"
	RecipientSending    = "sending"
	RecipientSent       = "sent"
	RecipientFailed     = "failed"
	RecipientBlocked    = "blocked"
	RecipientSuppressed = "suppressed"
	RecipientCancelled  = "cancelled"
)

// maxMessage keeps a broadcast well inside Telegram's 4096-character limit
// after HTML escaping.
const maxMessage = 3000

// BroadcastButton opens the audience's Mini App at a start parameter (§18).
type BroadcastButton struct {
	Label      settings.Localized `json:"label"`
	StartParam string             `json:"start_param"`
}

// Broadcast is a marketing message (openapi MarketingBroadcast).
type Broadcast struct {
	ID             uuid.UUID          `json:"id"`
	CampaignID     *uuid.UUID         `json:"campaign_id"`
	Title          string             `json:"title"`
	Audience       string             `json:"audience"`
	SegmentID      *uuid.UUID         `json:"segment_id"`
	Filters        json.RawMessage    `json:"filters"`
	Content        settings.Localized `json:"content"`
	Button         *BroadcastButton   `json:"button"`
	Status         string             `json:"status"`
	ScheduledAt    *time.Time         `json:"scheduled_at"`
	StartedAt      *time.Time         `json:"started_at"`
	CompletedAt    *time.Time         `json:"completed_at"`
	CancelledAt    *time.Time         `json:"cancelled_at"`
	RecipientCount *int               `json:"recipient_count"`
	CreatedBy      *uuid.UUID         `json:"created_by"`
	ApprovedBy     *uuid.UUID         `json:"approved_by"`
	ApprovalNote   *string            `json:"approval_note"`
	Version        int                `json:"version"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	Stats          map[string]int     `json:"stats"`
}

const broadcastColumns = `b.id, b.campaign_id, b.title, b.audience, b.segment_id, b.filters, b.content, b.button,
	b.status, b.scheduled_at, b.started_at, b.completed_at, b.cancelled_at, b.recipient_count, b.created_by,
	b.approved_by, b.approval_note, b.version, b.created_at, b.updated_at`

func scanBroadcast(row pgx.Row) (Broadcast, error) {
	var b Broadcast
	var content, button []byte
	err := row.Scan(&b.ID, &b.CampaignID, &b.Title, &b.Audience, &b.SegmentID, &b.Filters, &content, &button,
		&b.Status, &b.ScheduledAt, &b.StartedAt, &b.CompletedAt, &b.CancelledAt, &b.RecipientCount, &b.CreatedBy,
		&b.ApprovedBy, &b.ApprovalNote, &b.Version, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return Broadcast{}, err
	}
	b.Content = localized(content)
	if len(button) > 0 && string(button) != "null" {
		var bt BroadcastButton
		if json.Unmarshal(button, &bt) == nil {
			b.Button = &bt
		}
	}
	return b, nil
}

func loadBroadcast(ctx context.Context, q db.Querier, id uuid.UUID, lock bool) (Broadcast, error) {
	sql := `SELECT ` + broadcastColumns + ` FROM marketing_broadcasts b WHERE b.id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	b, err := scanBroadcast(q.QueryRow(ctx, sql, id))
	if db.IsNoRows(err) {
		return Broadcast{}, httpx.NotFound()
	}
	if err != nil {
		return Broadcast{}, fmt.Errorf("marketing: broadcast: %w", err)
	}
	return b, nil
}

// broadcastStats counts recipients by status (test sends apart).
func broadcastStats(ctx context.Context, q db.Querier, id uuid.UUID) (map[string]int, error) {
	rows, err := q.Query(ctx, `
		SELECT CASE WHEN is_test THEN 'test_' || status ELSE status END, count(*)
		FROM marketing_broadcast_recipients WHERE broadcast_id = $1 GROUP BY 1`, id)
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

func listBroadcasts(ctx context.Context, q db.Querier, status string, page httpx.PageParams) ([]Broadcast, int, error) {
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM marketing_broadcasts b WHERE ($1 = '' OR b.status = $1)`, status).
		Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.Query(ctx, `SELECT `+broadcastColumns+` FROM marketing_broadcasts b WHERE ($1 = '' OR b.status = $1)
		ORDER BY b.created_at DESC LIMIT $2 OFFSET $3`, status, page.PageSize, page.Offset())
	if err != nil {
		return nil, 0, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Broadcast, error) { return scanBroadcast(row) })
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		if items[i].Stats, err = broadcastStats(ctx, q, items[i].ID); err != nil {
			return nil, 0, err
		}
	}
	return items, total, nil
}

type broadcastInput struct {
	Title      *string             `json:"title"`
	Audience   *string             `json:"audience"`
	SegmentID  *uuid.UUID          `json:"segment_id"`
	Filters    json.RawMessage     `json:"filters"`
	Content    *settings.Localized `json:"content"`
	Button     *BroadcastButton    `json:"button"`
	NoButton   bool                `json:"no_button"`
	CampaignID *uuid.UUID          `json:"campaign_id"`
}

var startParamRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// cleanMessage normalises a broadcast text: NFC, CRLF to LF, control and
// bidi-override characters removed (line breaks kept), at most two blank
// lines in a row, trimmed. The text is HTML-escaped when sent.
func cleanMessage(s string) (string, bool) {
	s = norm.NFC.String(strings.ReplaceAll(s, "\r\n", "\n"))
	var b strings.Builder
	newlines := 0
	for _, r := range s {
		switch {
		case r == '\n':
			newlines++
			if newlines <= 2 {
				b.WriteRune(r)
			}
			continue
		case r == '\t':
			r = ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) && r != '‍':
			continue
		}
		newlines = 0
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	return out, utf8.RuneCountInString(out) <= maxMessage
}

// saveBroadcast creates (id nil) or edits a draft broadcast.
func saveBroadcast(ctx context.Context, tx pgx.Tx, id *uuid.UUID, in broadcastInput, adminID uuid.UUID) (uuid.UUID, error) {
	fields := map[string]string{}
	cur := Broadcast{Audience: AudiencePassengers, Filters: json.RawMessage(`{}`)}
	if id != nil {
		var err error
		if cur, err = loadBroadcast(ctx, tx, *id, true); err != nil {
			return uuid.Nil, err
		}
		if cur.Status != BroadcastDraft {
			return uuid.Nil, httpx.NewError(409, "invalid_transition", "only a draft broadcast can be edited").
				WithDetails(map[string]any{"from": cur.Status, "action": "edit"})
		}
	}
	title := cur.Title
	if in.Title != nil {
		v, err := textutil.CleanText(*in.Title, 120)
		if err != nil {
			fields["title"] = "too_long"
		}
		title = v
	}
	if title == "" {
		fields["title"] = "required"
	}
	audience := cur.Audience
	if in.Audience != nil {
		audience = *in.Audience
	}
	if audience != AudiencePassengers && audience != AudienceDrivers {
		fields["audience"] = "invalid"
	}
	segmentID := cur.SegmentID
	if in.SegmentID != nil {
		segmentID = in.SegmentID
		if *in.SegmentID == uuid.Nil {
			segmentID = nil
		}
	}
	filters := cur.Filters
	if in.Filters != nil {
		filters = in.Filters
	}
	if segmentID != nil {
		seg, err := loadSegment(ctx, tx, *segmentID)
		switch {
		case err != nil && httpx.AsError(err).Status == 404:
			fields["segment_id"] = "not_found"
		case err != nil:
			return uuid.Nil, err
		case seg.Audience != audience:
			fields["segment_id"] = "wrong_audience"
		case seg.ArchivedAt != nil:
			fields["segment_id"] = "archived"
		}
		filters = json.RawMessage(`{}`)
	}
	f := parseFilters(audience, filters, fields)
	content := cur.Content
	if in.Content != nil {
		content = *in.Content
	}
	for lang, v := range map[string]*string{"kk": &content.KK, "ru": &content.RU, "en": &content.EN} {
		clean, ok := cleanMessage(*v)
		if !ok {
			fields["content."+lang] = "too_long"
		} else if clean == "" {
			fields["content."+lang] = "required"
		}
		*v = clean
	}
	button := cur.Button
	if in.Button != nil {
		button = in.Button
	}
	if in.NoButton {
		button = nil
	}
	if button != nil {
		for lang, v := range map[string]*string{"kk": &button.Label.KK, "ru": &button.Label.RU, "en": &button.Label.EN} {
			clean, err := textutil.CleanText(*v, 40)
			if err != nil || clean == "" {
				fields["button.label."+lang] = "invalid"
			}
			*v = clean
		}
		if !startParamRe.MatchString(button.StartParam) {
			fields["button.start_param"] = "invalid"
		}
	}
	campaignID := cur.CampaignID
	if in.CampaignID != nil {
		campaignID = in.CampaignID
		if *in.CampaignID == uuid.Nil {
			campaignID = nil
		}
	}
	if len(fields) > 0 {
		return uuid.Nil, httpx.Validation(fields)
	}
	normFilters, _ := json.Marshal(f)
	contentJSON, _ := json.Marshal(content)
	var buttonJSON []byte
	if button != nil {
		buttonJSON, _ = json.Marshal(button)
	}
	if id == nil {
		var newID uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO marketing_broadcasts (campaign_id, title, audience, segment_id, filters, content, button,
			                                  created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8) RETURNING id`,
			campaignID, title, audience, segmentID, normFilters, contentJSON, buttonJSON, idPtr(adminID)).Scan(&newID)
		if db.IsForeignKeyViolation(err, "") {
			return uuid.Nil, httpx.Validation(map[string]string{"campaign_id": "not_found"})
		}
		return newID, err
	}
	_, err := tx.Exec(ctx, `
		UPDATE marketing_broadcasts SET campaign_id = $2, title = $3, audience = $4, segment_id = $5, filters = $6,
		       content = $7, button = $8, updated_by = $9, version = version + 1
		WHERE id = $1`, *id, campaignID, title, audience, segmentID, normFilters, contentJSON, buttonJSON, idPtr(adminID))
	if db.IsForeignKeyViolation(err, "") {
		return uuid.Nil, httpx.Validation(map[string]string{"campaign_id": "not_found"})
	}
	return *id, err
}

// broadcastFilters resolves the targeting of a broadcast (its saved segment
// or its own filters).
func broadcastFilters(ctx context.Context, q db.Querier, b Broadcast) (Filters, error) {
	raw := b.Filters
	if b.SegmentID != nil {
		seg, err := loadSegment(ctx, q, *b.SegmentID)
		if err != nil {
			return Filters{}, err
		}
		raw = seg.Filters
	}
	fields := map[string]string{}
	f := parseFilters(b.Audience, raw, fields)
	if len(fields) > 0 {
		return Filters{}, httpx.Validation(fields)
	}
	return f, nil
}

// snapshotRecipients freezes the audience of a broadcast: one queued row per
// reachable person, a suppressed row for each one who opted out or blocked
// the bot. The resolved filters are stored on the broadcast.
func snapshotRecipients(ctx context.Context, tx pgx.Tx, b Broadcast) (int, error) {
	f, err := broadcastFilters(ctx, tx, b)
	if err != nil {
		return 0, err
	}
	var args []any
	from, suppressed := audienceQuery(b.Audience, f, &args)
	args = append(args, b.ID)
	n := len(args)
	if _, err := tx.Exec(ctx, `
		INSERT INTO marketing_broadcast_recipients (broadcast_id, user_id, chat_id, language, status)
		SELECT $`+itoa(n)+`, u.id, u.telegram_user_id, COALESCE(p.language, 'kk'),
		       CASE WHEN `+suppressed+` THEN 'suppressed' ELSE 'queued' END`+from+`
		ON CONFLICT DO NOTHING`, args...); err != nil {
		return 0, fmt.Errorf("marketing: snapshot recipients: %w", err)
	}
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM marketing_broadcast_recipients WHERE broadcast_id = $1 AND NOT is_test AND status = 'queued'`,
		b.ID).Scan(&count); err != nil {
		return 0, err
	}
	resolved, _ := json.Marshal(f)
	if _, err := tx.Exec(ctx, `UPDATE marketing_broadcasts SET recipient_count = $2, filters = $3 WHERE id = $1`,
		b.ID, count, resolved); err != nil {
		return 0, err
	}
	return count, nil
}

// Broadcast actions.
const (
	BroadcastActionSchedule = "schedule"
	BroadcastActionSendNow  = "send_now"
	BroadcastActionPause    = "pause"
	BroadcastActionResume   = "resume"
	BroadcastActionCancel   = "cancel"
)

// transitionBroadcast applies an action to a locked broadcast. canApprove
// says whether the admin holds marketing.approve, required when the audience
// is larger than the approval threshold.
func transitionBroadcast(ctx context.Context, tx pgx.Tx, b Broadcast, action string, scheduledAt *time.Time,
	adminID uuid.UUID, canApprove bool, note string) error {
	invalid := func() error {
		return httpx.NewError(409, "invalid_transition", "broadcast cannot "+action+" from "+b.Status).
			WithDetails(map[string]any{"from": b.Status, "action": action})
	}
	threshold := settings.BroadcastApprovalThreshold(ctx, tx)
	needApproval := func(count int64) error {
		if int64(count) > threshold && !canApprove {
			return httpx.NewError(403, "approval_required", "this audience needs marketing.approve").
				WithDetails(map[string]any{"recipients": count, "threshold": threshold})
		}
		return nil
	}
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	switch action {
	case BroadcastActionSchedule:
		if b.Status != BroadcastDraft {
			return invalid()
		}
		if scheduledAt == nil || !scheduledAt.After(time.Now()) {
			return httpx.Validation(map[string]string{"scheduled_at": "must_be_future"})
		}
		f, err := broadcastFilters(ctx, tx, b)
		if err != nil {
			return err
		}
		est, err := countAudience(ctx, tx, b.Audience, f)
		if err != nil {
			return err
		}
		if err := needApproval(est.Reachable); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE marketing_broadcasts SET status = 'scheduled', scheduled_at = $2, approved_by = $3, approval_note = $4
			WHERE id = $1`, b.ID, *scheduledAt, idPtr(adminID), notePtr)
		return err
	case BroadcastActionSendNow:
		if b.Status != BroadcastDraft && b.Status != BroadcastScheduled {
			return invalid()
		}
		count, err := snapshotRecipients(ctx, tx, b)
		if err != nil {
			return err
		}
		if err := needApproval(int64(count)); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE marketing_broadcasts SET status = 'sending', started_at = now(), approved_by = $2,
			       approval_note = COALESCE($3, approval_note)
			WHERE id = $1`, b.ID, idPtr(adminID), notePtr)
		return err
	case BroadcastActionPause:
		if b.Status != BroadcastSending {
			return invalid()
		}
		_, err := tx.Exec(ctx, `UPDATE marketing_broadcasts SET status = 'paused' WHERE id = $1`, b.ID)
		return err
	case BroadcastActionResume:
		if b.Status != BroadcastPaused {
			return invalid()
		}
		_, err := tx.Exec(ctx, `UPDATE marketing_broadcasts SET status = 'sending' WHERE id = $1`, b.ID)
		return err
	case BroadcastActionCancel:
		if b.Status == BroadcastCompleted || b.Status == BroadcastCancelled || b.Status == BroadcastFailed {
			return invalid()
		}
		// Messages already accepted by Telegram cannot be recalled; the rest
		// are never sent.
		if _, err := tx.Exec(ctx, `
			UPDATE marketing_broadcast_recipients SET status = 'cancelled', locked_until = NULL
			WHERE broadcast_id = $1 AND status = 'queued'`, b.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE marketing_broadcasts SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, b.ID)
		return err
	}
	return httpx.Validation(map[string]string{"action": "invalid"})
}

// queueTest adds a test delivery of the broadcast to one person who started
// the audience's bot (found by Telegram id); it is sent even while the
// broadcast is a draft, never to anyone else.
func queueTest(ctx context.Context, tx pgx.Tx, b Broadcast, telegramID int64) (int64, error) {
	var userID uuid.UUID
	var lang string
	started := "u.passenger_bot_started_at"
	if b.Audience == AudienceDrivers {
		started = "u.driver_bot_started_at"
	}
	err := tx.QueryRow(ctx, `
		SELECT u.id, COALESCE(p.language, 'kk') FROM users u LEFT JOIN user_preferences p ON p.user_id = u.id
		WHERE u.telegram_user_id = $1 AND `+started+` IS NOT NULL`, telegramID).Scan(&userID, &lang)
	if db.IsNoRows(err) {
		return 0, httpx.Validation(map[string]string{"telegram_user_id": "bot_not_started"})
	}
	if err != nil {
		return 0, err
	}
	var recent int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM marketing_broadcast_recipients
		WHERE broadcast_id = $1 AND is_test AND created_at > now() - interval '1 hour'`, b.ID).Scan(&recent); err != nil {
		return 0, err
	}
	if recent >= 10 {
		return 0, httpx.TooManyRequests()
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO marketing_broadcast_recipients (broadcast_id, user_id, chat_id, language, is_test)
		VALUES ($1, $2, $3, $4, true) RETURNING id`, b.ID, userID, telegramID, lang).Scan(&id)
	return id, err
}

// BroadcastRecipient is a delivery as operators see it: no names or phones.
type BroadcastRecipient struct {
	ID         int64      `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	Language   string     `json:"language"`
	IsTest     bool       `json:"is_test"`
	Status     string     `json:"status"`
	Attempts   int        `json:"attempts"`
	HTTPStatus *int       `json:"http_status"`
	LastError  *string    `json:"last_error"`
	SentAt     *time.Time `json:"sent_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func listRecipients(ctx context.Context, q db.Querier, broadcastID uuid.UUID, status string, page httpx.PageParams) ([]BroadcastRecipient, int, error) {
	var total int
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM marketing_broadcast_recipients WHERE broadcast_id = $1 AND ($2 = '' OR status = $2)`,
		broadcastID, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.Query(ctx, `
		SELECT id, user_id, language, is_test, status, attempts, http_status, last_error, sent_at, updated_at
		FROM marketing_broadcast_recipients WHERE broadcast_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY is_test DESC, id LIMIT $3 OFFSET $4`, broadcastID, status, page.PageSize, page.Offset())
	if err != nil {
		return nil, 0, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (BroadcastRecipient, error) {
		var r BroadcastRecipient
		err := row.Scan(&r.ID, &r.UserID, &r.Language, &r.IsTest, &r.Status, &r.Attempts, &r.HTTPStatus,
			&r.LastError, &r.SentAt, &r.UpdatedAt)
		return r, err
	})
	return items, total, err
}
