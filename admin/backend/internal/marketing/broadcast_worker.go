package marketing

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	mrand "math/rand/v2"
	"net"
	"net/url"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// Sender delivers one HTML message (notifications.TelegramSender).
type Sender interface {
	Send(ctx context.Context, bot notifications.Bot, chatID int64, text string, markup *telegram.InlineKeyboardMarkup) (int64, error)
}

// WorkerOptions configure the broadcast worker.
type WorkerOptions struct {
	PassengerMiniAppURL       string
	DriverMiniAppURL          string
	PassengerBotUsername      string
	DriverBotUsername         string
	PassengerMiniAppShortName string
	DriverMiniAppShortName    string
	Log                       *slog.Logger
	// BacklogLimit: while more operational messages than this wait in the
	// notification outbox, broadcasts yield [20].
	BacklogLimit int
	// Tick is the idle sleep between batches [1s].
	Tick time.Duration
}

// BroadcastWorker sends broadcasts (§23.7): its own token bucket per bot at
// marketing.broadcast_rate_per_s (default 4/s, below Telegram's ~30/s), a
// bot-wide pause on 429 retry_after, exponential backoff with jitter for
// transient errors, blocked users suppressed, and crash-safe leases on
// recipient rows. It never runs while operational messages queue up.
type BroadcastWorker struct {
	pool   *pgxpool.Pool
	sender Sender
	opt    WorkerOptions
	log    *slog.Logger

	limiters map[notifications.Bot]*rate.Limiter
	rate     float64
	paused   map[notifications.Bot]time.Time
}

// NewBroadcastWorker builds the worker.
func NewBroadcastWorker(pool *pgxpool.Pool, sender Sender, opt WorkerOptions) *BroadcastWorker {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.BacklogLimit <= 0 {
		opt.BacklogLimit = 20
	}
	if opt.Tick <= 0 {
		opt.Tick = time.Second
	}
	return &BroadcastWorker{pool: pool, sender: sender, opt: opt,
		log:      opt.Log.With(slog.String("component", "broadcast_worker")),
		limiters: map[notifications.Bot]*rate.Limiter{}, paused: map[notifications.Bot]time.Time{}}
}

const (
	broadcastLease       = 2 * time.Minute
	broadcastMaxAttempts = 5
)

// Run sends batches until ctx is cancelled.
func (w *BroadcastWorker) Run(ctx context.Context) error {
	for {
		n, err := w.RunOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.log.Error("broadcast batch failed", slog.Any("err", err))
		}
		wait := w.opt.Tick
		if n > 0 && err == nil {
			wait = 0
		}
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil
			case <-t.C:
			}
		}
	}
}

type delivery struct {
	id          int64
	broadcastID uuid.UUID
	userID      uuid.UUID
	chatID      int64
	lang        string
	isTest      bool
	attempts    int
	audience    string
	b           Broadcast
}

// RunOnce starts due scheduled broadcasts and sends one batch; it returns how
// many deliveries it attempted.
func (w *BroadcastWorker) RunOnce(ctx context.Context) (int, error) {
	enabled := settings.Marketing(ctx, w.pool).Broadcasts
	if enabled {
		if err := w.startDue(ctx); err != nil {
			return 0, err
		}
	}
	r := float64(settings.BroadcastRate(ctx, w.pool))
	if r != w.rate {
		w.rate, w.limiters = r, map[notifications.Bot]*rate.Limiter{}
	}
	var backlog int
	if err := w.pool.QueryRow(ctx, `
		SELECT count(*) FROM (SELECT 1 FROM notification_outbox
		                      WHERE status IN ('pending', 'sending') AND next_attempt_at <= now() LIMIT $1) x`,
		w.opt.BacklogLimit+1).Scan(&backlog); err != nil {
		return 0, err
	}
	if backlog > w.opt.BacklogLimit {
		return 0, nil // order messages first
	}
	batch, err := w.claim(ctx, enabled, max(int(r*2), 1))
	if err != nil {
		return 0, err
	}
	for _, d := range batch {
		if ctx.Err() != nil {
			w.release(context.WithoutCancel(ctx), d, 0)
			continue
		}
		w.deliver(ctx, d)
	}
	if err := w.finishBroadcasts(ctx); err != nil {
		return len(batch), err
	}
	return len(batch), nil
}

// startDue moves due scheduled broadcasts to sending, snapshotting their
// recipients.
func (w *BroadcastWorker) startDue(ctx context.Context) error {
	return db.WithTx(ctx, w.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id FROM marketing_broadcasts WHERE status = 'scheduled' AND scheduled_at <= now()
			FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			b, err := loadBroadcast(ctx, tx, id, true)
			if err != nil {
				return err
			}
			count, err := snapshotRecipients(ctx, tx, b)
			if err != nil {
				w.log.Error("broadcast snapshot failed", slog.String("broadcast", id.String()), slog.Any("err", err))
				if _, err := tx.Exec(ctx, `UPDATE marketing_broadcasts SET status = 'failed', completed_at = now() WHERE id = $1`, id); err != nil {
					return err
				}
				continue
			}
			// The audience grew past the approval threshold since a
			// non-approver scheduled it: nothing is sent.
			if b.ApprovedBy == nil && int64(count) > settings.BroadcastApprovalThreshold(ctx, tx) {
				w.log.Warn("scheduled broadcast needs an approver", slog.String("broadcast", id.String()), slog.Int("recipients", count))
				if _, err := tx.Exec(ctx, `
					UPDATE marketing_broadcast_recipients SET status = 'cancelled' WHERE broadcast_id = $1 AND NOT is_test AND status = 'queued'`, id); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `
					UPDATE marketing_broadcasts SET status = 'failed', completed_at = now(), approval_note = 'approval_required'
					WHERE id = $1`, id); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE marketing_broadcasts SET status = 'sending', started_at = now() WHERE id = $1`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// claim leases up to limit due deliveries: test sends always, others only of
// sending broadcasts while broadcasts are enabled. A lease that expired (a
// crashed worker) is claimed again.
func (w *BroadcastWorker) claim(ctx context.Context, enabled bool, limit int) ([]delivery, error) {
	var out []delivery
	err := db.WithTx(ctx, w.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT r.id, r.broadcast_id, r.user_id, r.chat_id, r.language, r.is_test, r.attempts, b.audience
			FROM marketing_broadcast_recipients r JOIN marketing_broadcasts b ON b.id = r.broadcast_id
			WHERE r.next_attempt_at <= now()
			  AND (r.status = 'queued' OR (r.status = 'sending' AND r.locked_until < now()))
			  AND $1 AND (r.is_test OR b.status = 'sending')
			ORDER BY r.is_test DESC, r.id
			LIMIT $2 FOR UPDATE OF r SKIP LOCKED`, enabled, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (delivery, error) {
			var d delivery
			err := row.Scan(&d.id, &d.broadcastID, &d.userID, &d.chatID, &d.lang, &d.isTest, &d.attempts, &d.audience)
			return d, err
		})
		if err != nil || len(out) == 0 {
			return err
		}
		ids := make([]int64, len(out))
		for i, d := range out {
			ids[i] = d.id
		}
		_, err = tx.Exec(ctx, `
			UPDATE marketing_broadcast_recipients SET status = 'sending', attempts = attempts + 1,
			       locked_until = now() + make_interval(secs => $2)
			WHERE id = ANY($1)`, ids, broadcastLease.Seconds())
		return err
	})
	if err != nil {
		return nil, err
	}
	cache := map[uuid.UUID]Broadcast{}
	for i := range out {
		b, ok := cache[out[i].broadcastID]
		if !ok {
			if b, err = loadBroadcast(ctx, w.pool, out[i].broadcastID, false); err != nil {
				return nil, err
			}
			cache[b.ID] = b
		}
		out[i].b = b
		out[i].attempts++
	}
	return out, nil
}

func botOf(audience string) notifications.Bot {
	if audience == AudienceDrivers {
		return notifications.DriverBot
	}
	return notifications.PassengerBot
}

// optOutLabel is the callback button every broadcast carries ("mkt_off").
var optOutLabel = map[string]string{"kk": "Жарнамадан бас тарту", "ru": "Отписаться от рассылки", "en": "Unsubscribe"}

// OptOutCallback is the callback data of the unsubscribe button; the bots
// handle it (POST /internal/v1/bots/{bot}/users/{tg_id}/marketing).
const OptOutCallback = "mkt_off"

// render builds the message text and keyboard in the recipient's language.
func (w *BroadcastWorker) render(d delivery) (string, *telegram.InlineKeyboardMarkup) {
	text := d.b.Content.Get(d.lang)
	if text == "" {
		text = d.b.Content.KK
	}
	text = html.EscapeString(text)
	if d.isTest {
		text = "🧪 TEST\n" + text
	}
	var rows [][]telegram.InlineKeyboardButton
	if d.b.Button != nil {
		base := w.opt.PassengerMiniAppURL
		bot, shortName := w.opt.PassengerBotUsername, w.opt.PassengerMiniAppShortName
		if d.audience == AudienceDrivers {
			base = w.opt.DriverMiniAppURL
			bot, shortName = w.opt.DriverBotUsername, w.opt.DriverMiniAppShortName
		}
		label := d.b.Button.Label.Get(d.lang)
		if label == "" {
			label = d.b.Button.Label.KK
		}
		// A launch announcement uses Telegram's direct Mini App link so it
		// can request fullscreen before the app loads. Manual broadcasts keep
		// their existing web_app keyboard, as does a missing bot configuration.
		if link := cities.DirectLink(bot, shortName, d.b.Button.StartParam); d.b.IsAutomatic && link != "" {
			rows = append(rows, []telegram.InlineKeyboardButton{{Text: label, URL: link}})
		} else if link, ok := miniAppLink(base, d.b.Button.StartParam); ok {
			rows = append(rows, []telegram.InlineKeyboardButton{{Text: label, WebApp: &telegram.WebAppInfo{URL: link}}})
		}
	}
	label := optOutLabel[d.lang]
	if label == "" {
		label = optOutLabel["kk"]
	}
	rows = append(rows, []telegram.InlineKeyboardButton{{Text: label, CallbackData: OptOutCallback}})
	return text, &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

var miniAppParamRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// miniAppLink is <miniAppURL>?startapp=<param>; Telegram accepts only HTTPS
// web_app URLs, so other URLs give no button.
func miniAppLink(base, param string) (string, bool) {
	if !miniAppParamRe.MatchString(param) {
		return "", false
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	q := u.Query()
	q.Set("startapp", param)
	u.RawQuery = q.Encode()
	return u.String(), true
}

// deliver sends one message and records the outcome. Nothing here runs
// inside a database transaction while Telegram is called.
func (w *BroadcastWorker) deliver(ctx context.Context, d delivery) {
	bot := botOf(d.audience)
	if until, ok := w.paused[bot]; ok && time.Now().Before(until) {
		w.release(ctx, d, time.Until(until))
		return
	}
	// A person who opted out or was blocked after the snapshot is skipped.
	var suppressed bool
	blocked := "u.passenger_bot_blocked_at"
	if bot == notifications.DriverBot {
		blocked = "u.driver_bot_blocked_at"
	}
	if err := w.pool.QueryRow(ctx, `
		SELECT p.marketing_opt_out_at IS NOT NULL OR `+blocked+` IS NOT NULL OR u.is_blocked
		FROM users u LEFT JOIN user_preferences p ON p.user_id = u.id WHERE u.id = $1`, d.userID).Scan(&suppressed); err != nil {
		w.release(ctx, d, 5*time.Second)
		return
	}
	if suppressed {
		w.finish(ctx, d, RecipientSuppressed, nil, "opted out or blocked", 0, 0)
		return
	}
	if d.b.IsAutomatic {
		eligible, err := automaticRecipientEligible(ctx, w.pool, d.b, d.userID, bot)
		if err != nil {
			w.release(ctx, d, 5*time.Second)
			return
		}
		if !eligible {
			w.finish(ctx, d, RecipientSuppressed, nil, "campaign unavailable, already earned or Mini App active", 0, 0)
			return
		}
	}
	lim := w.limiters[bot]
	if lim == nil {
		lim = rate.NewLimiter(rate.Limit(w.rate), 1)
		w.limiters[bot] = lim
	}
	if err := lim.Wait(ctx); err != nil {
		w.release(context.WithoutCancel(ctx), d, 0)
		return
	}
	text, markup := w.render(d)
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	msgID, err := w.sender.Send(sendCtx, bot, d.chatID, text, markup)
	cancel()
	if err == nil {
		w.finish(ctx, d, RecipientSent, nil, "", msgID, 0)
		return
	}
	var code *int
	var ae *telegram.APIError
	if errors.As(err, &ae) {
		c := ae.Code
		code = &c
	}
	msg := sanitize(err)
	switch {
	case ctx.Err() != nil:
		w.release(context.WithoutCancel(ctx), d, 0)
	case isRetryAfter(err):
		ra, _ := telegram.RetryAfter(err)
		w.paused[bot] = time.Now().Add(ra)
		w.log.Warn("broadcast flood control", slog.String("bot", string(bot)), slog.Duration("retry_after", ra))
		w.release(ctx, d, ra) // flood control says nothing about the message: no attempt spent
	case telegram.IsBlocked(err):
		w.finish(ctx, d, RecipientBlocked, code, msg, 0, 0)
		col := "passenger_bot_blocked_at"
		if bot == notifications.DriverBot {
			col = "driver_bot_blocked_at"
		}
		if _, err := w.pool.Exec(context.WithoutCancel(ctx),
			`UPDATE users SET `+col+` = COALESCE(`+col+`, now()) WHERE id = $1`, d.userID); err != nil {
			w.log.Error("mark blocked failed", slog.Any("err", err))
		}
	case (telegram.IsRetryable(err) || isTransient(err)) && d.attempts < broadcastMaxAttempts:
		w.finish(ctx, d, RecipientQueued, code, msg, 0, backoff(d.attempts))
	default:
		w.finish(ctx, d, RecipientFailed, code, msg, 0, 0)
	}
}

func isRetryAfter(err error) bool {
	_, ok := telegram.RetryAfter(err)
	return ok
}

func isTransient(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne)
}

// backoff: 5 s doubling per attempt up to 10 min, with equal jitter.
func backoff(attempt int) time.Duration {
	d := 10 * time.Minute
	if attempt >= 1 && attempt < 20 {
		if e := 5 * time.Second << (attempt - 1); e < d {
			d = e
		}
	}
	half := d / 2
	return half + time.Duration(mrand.Int64N(int64(half)+1))
}

var tokenRe = regexp.MustCompile(`\d{5,}:[A-Za-z0-9_-]{20,}`)

func sanitize(err error) string {
	s := tokenRe.ReplaceAllString(err.Error(), "<redacted>")
	if len(s) > 500 {
		s = s[:500]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// release returns a leased delivery to the queue without spending an attempt.
func (w *BroadcastWorker) release(ctx context.Context, d delivery, delay time.Duration) {
	if _, err := w.pool.Exec(ctx, `
		UPDATE marketing_broadcast_recipients
		SET status = 'queued', attempts = GREATEST(attempts - 1, 0), locked_until = NULL,
		    next_attempt_at = now() + make_interval(secs => $2)
		WHERE id = $1 AND status = 'sending'`, d.id, max(delay, 0).Seconds()); err != nil {
		w.log.Error("release delivery failed", slog.Int64("delivery", d.id), slog.Any("err", err))
	}
}

func (w *BroadcastWorker) finish(ctx context.Context, d delivery, status string, code *int, msg string, msgID int64, delay time.Duration) {
	ctx = context.WithoutCancel(ctx)
	var errText *string
	if msg != "" {
		errText = &msg
	}
	var mid *int64
	if msgID != 0 {
		mid = &msgID
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE marketing_broadcast_recipients
		SET status = $2, http_status = $3, last_error = $4, telegram_message_id = COALESCE($5, telegram_message_id),
		    sent_at = CASE WHEN $2 = 'sent' THEN now() ELSE sent_at END, locked_until = NULL,
		    next_attempt_at = now() + make_interval(secs => $6)
		WHERE id = $1 AND status = 'sending'`, d.id, status, code, errText, mid, delay.Seconds()); err != nil {
		w.log.Error("record delivery failed", slog.Int64("delivery", d.id), slog.Any("err", err))
	}
}

// finishBroadcasts completes sending broadcasts with nothing left to send.
func (w *BroadcastWorker) finishBroadcasts(ctx context.Context) error {
	// Deliveries a cancelled or failed broadcast still holds (re-queued by the
	// batch in flight at the cancel, or leased by a crashed worker) are closed.
	if _, err := w.pool.Exec(ctx, `
		UPDATE marketing_broadcast_recipients r SET status = 'cancelled', locked_until = NULL
		FROM marketing_broadcasts b
		WHERE b.id = r.broadcast_id AND b.status IN ('cancelled', 'failed') AND NOT r.is_test
		  AND (r.status = 'queued' OR (r.status = 'sending' AND r.locked_until < now()))`); err != nil {
		return fmt.Errorf("marketing: close cancelled deliveries: %w", err)
	}
	_, err := w.pool.Exec(ctx, `
		UPDATE marketing_broadcasts b SET status = 'completed', completed_at = now()
		WHERE b.status = 'sending' AND NOT EXISTS (
			SELECT 1 FROM marketing_broadcast_recipients r
			WHERE r.broadcast_id = b.id AND NOT r.is_test AND r.status IN ('queued', 'sending'))`)
	if err != nil {
		return fmt.Errorf("marketing: finish broadcasts: %w", err)
	}
	return nil
}
