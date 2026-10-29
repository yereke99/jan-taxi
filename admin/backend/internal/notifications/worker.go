package notifications

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"net"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// Sender delivers one rendered message (HTML parse mode) with the given bot and
// returns the Telegram message id. Errors are classified with pkg/telegram's
// IsBlocked / IsRetryable / RetryAfter.
type Sender interface {
	Send(ctx context.Context, bot Bot, chatID int64, text string, markup *telegram.InlineKeyboardMarkup) (messageID int64, err error)
}

// Editor rewrites a message sent earlier (outbox rows of kind "edit"); a nil
// markup removes the inline keyboard. Senders without it cannot apply edits,
// which then end as dead.
type Editor interface {
	Edit(ctx context.Context, bot Bot, chatID, messageID int64, text string, markup *telegram.InlineKeyboardMarkup) error
}

// errNoEditor is recorded for edit rows when the Sender cannot edit.
var errNoEditor = errors.New("notifications: sender cannot edit messages")

// editRetryDelay is how long an edit waits for its original message to be sent.
const editRetryDelay = 2 * time.Second

// Options configure a Worker. Zero values take the defaults in brackets.
type Options struct {
	InstanceID          string // lease owner name [hostname-random]
	PassengerMiniAppURL string // inline web_app buttons of the passenger bot (no button when empty)
	DriverMiniAppURL    string // inline web_app buttons of the driver bot
	Log                 *slog.Logger
	BatchSize           int           // rows claimed per RunOnce [50]
	PerBotRate          float64       // messages per second per bot [25]
	PerChatInterval     time.Duration // minimum gap between messages to one chat [1s]
	Concurrency         int           // parallel sends per batch [4]
	LeaseDuration       time.Duration // claim lease [60s]
	SendTimeout         time.Duration // per Bot API call [15s]
	MinBackoff          time.Duration // first retry delay after 5xx/network errors [5s]
	MaxBackoff          time.Duration // retry delay cap [10m]
	MaxAge              time.Duration // older messages are not retried any more [24h]
	PollInterval        time.Duration // idle sleep in Run [1s]
	// Photos reads stored images for photo messages (§22.6); without it
	// such messages are sent as text.
	Photos PhotoStore
}

func (o *Options) defaults() {
	if o.InstanceID == "" {
		host, _ := os.Hostname()
		var b [4]byte
		_, _ = rand.Read(b[:])
		o.InstanceID = fmt.Sprintf("%s-%s", host, hex.EncodeToString(b[:]))
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	setDefault(&o.BatchSize, 50)
	setDefault(&o.Concurrency, 4)
	if o.PerBotRate <= 0 {
		o.PerBotRate = 25
	}
	setDefault(&o.PerChatInterval, time.Second)
	setDefault(&o.LeaseDuration, 60*time.Second)
	setDefault(&o.SendTimeout, 15*time.Second)
	setDefault(&o.MinBackoff, 5*time.Second)
	setDefault(&o.MaxBackoff, 10*time.Minute)
	setDefault(&o.MaxAge, 24*time.Hour)
	setDefault(&o.PollInterval, time.Second)
}

func setDefault[T int | time.Duration](v *T, def T) {
	if *v <= 0 {
		*v = def
	}
}

// Worker drains notification_outbox. Several workers (or API replicas) may run
// at once: rows are claimed with FOR UPDATE SKIP LOCKED under a lease, and a
// row whose lease expired (crashed worker) is claimed again.
//
// Outcomes per attempt (each recorded in notification_attempts):
//   - success → sent, telegram_message_id stored;
//   - 429 → pending at now()+retry_after and the whole bot pauses until then; the
//     attempt is not counted against max_attempts because flood control says
//     nothing about the message itself (messages older than MaxAge become dead);
//   - 403 → dead and users.<bot>_bot_blocked_at = now();
//   - 5xx / network / timeout → pending with exponential backoff and jitter
//     (MinBackoff..MaxBackoff), dead once attempts >= max_attempts or older than MaxAge;
//   - any other error (4xx, unknown template, render failure) → dead.
type Worker struct {
	pool   *pgxpool.Pool
	sender Sender
	opt    Options
	log    *slog.Logger
	lim    *limiter
}

// NewWorker creates a worker; see Options for defaults.
func NewWorker(pool *pgxpool.Pool, sender Sender, opt Options) *Worker {
	opt.defaults()
	return &Worker{
		pool:   pool,
		sender: sender,
		opt:    opt,
		log:    opt.Log.With(slog.String("component", "notification_worker")),
		lim:    newLimiter(opt.PerBotRate, opt.PerChatInterval),
	}
}

// Run processes batches until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	failures := 0
	for {
		n, err := w.RunOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		wait := w.opt.PollInterval
		switch {
		case err != nil:
			failures++
			wait = min(time.Duration(failures)*w.opt.PollInterval*2, time.Minute)
			w.log.Error("notification batch failed", slog.Any("err", err))
		case n >= w.opt.BatchSize:
			failures, wait = 0, 0
		default:
			failures = 0
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

// Drain runs batches until no due rows remain or budget elapses. It is meant as
// the body of a periodic job (e.g. every second under an advisory lock).
func (w *Worker) Drain(ctx context.Context, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for {
		n, err := w.RunOnce(ctx)
		if err != nil {
			return err
		}
		if n < w.opt.BatchSize || ctx.Err() != nil || time.Now().After(deadline) {
			return nil
		}
	}
}

// outboxRow is a claimed notification.
type outboxRow struct {
	id          uuid.UUID
	kind        string
	refID       *uuid.UUID
	bot         Bot
	userID      *uuid.UUID
	chatID      int64
	template    string
	params      map[string]any
	attempts    int
	maxAttempts int
	createdAt   time.Time
}

// editTarget is the original message of an edit row.
type editTarget struct {
	status    string
	messageID *int64
	template  string
	params    map[string]any
}

// RunOnce claims one batch, sends it and records the outcomes. It returns the
// number of rows claimed.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	leaseStart := time.Now()
	rows, err := w.claim(ctx)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	margin := min(10*time.Second, w.opt.LeaseDuration/4)
	deadline := leaseStart.Add(w.opt.LeaseDuration - margin)
	langs := w.languages(ctx, rows)
	overrides := w.overrides(ctx, rows)

	sem := make(chan struct{}, w.opt.Concurrency)
	var wg sync.WaitGroup
	for _, m := range rows {
		now := time.Now()
		if ctx.Err() != nil || now.After(deadline) {
			w.reschedule(ctx, m, 0)
			continue
		}
		var (
			msg    Message
			target *editTarget
			err    error
		)
		if m.kind == KindEdit {
			target, err = w.editTarget(ctx, m)
			switch {
			case err != nil:
				w.reschedule(ctx, m, editRetryDelay)
				continue
			case (target.status == StatusPending || target.status == StatusSending) && now.Sub(m.createdAt) < w.opt.MaxAge:
				// The original is not out yet: edit it once it is.
				w.reschedule(ctx, m, editRetryDelay)
				continue
			case target.status != StatusSent || target.messageID == nil:
				w.finish(ctx, m, now, outcome{kind: "fatal", status: StatusCancelled,
					errText: "original message was not delivered (" + target.status + ")"})
				continue
			}
			msg, err = w.renderEdit(m, *target, langs, overrides)
		} else {
			msg, err = w.render(m, langs, overrides)
		}
		if err != nil {
			w.finish(ctx, m, now, outcome{kind: "fatal", status: StatusDead, errText: err.Error()})
			continue
		}
		if until, paused := w.lim.pausedUntil(m.bot, now); paused {
			w.reschedule(ctx, m, until.Sub(now))
			continue
		}
		if next, ok := w.lim.reserveChat(m.bot, m.chatID, now); !ok {
			w.reschedule(ctx, m, next.Sub(now))
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(m outboxRow, msg Message, target *editTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			w.deliver(ctx, m, msg, target, deadline)
		}(m, msg, target)
	}
	wg.Wait()
	return len(rows), nil
}

func (w *Worker) claim(ctx context.Context) ([]outboxRow, error) {
	rows, err := w.pool.Query(ctx, `
		WITH due AS MATERIALIZED (
			SELECT id FROM notification_outbox
			WHERE (status = 'pending' AND next_attempt_at <= now())
			   OR (status = 'sending' AND locked_until < now())
			ORDER BY next_attempt_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE notification_outbox o
		SET status = 'sending',
		    locked_until = now() + $3 * interval '1 millisecond',
		    locked_by = $2,
		    attempts = o.attempts + 1
		FROM due WHERE o.id = due.id
		RETURNING o.id, o.kind, o.ref_outbox_id, o.bot, o.user_id, o.chat_id, o.template, o.params, o.attempts,
		          o.max_attempts, o.created_at`,
		w.opt.BatchSize, w.opt.InstanceID, w.opt.LeaseDuration.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("notifications: claim: %w", err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var (
			m   outboxRow
			bot string
			raw []byte
		)
		if err := rows.Scan(&m.id, &m.kind, &m.refID, &bot, &m.userID, &m.chatID, &m.template, &raw, &m.attempts,
			&m.maxAttempts, &m.createdAt); err != nil {
			return nil, fmt.Errorf("notifications: claim scan: %w", err)
		}
		m.bot = Bot(bot)
		m.params = decodeParams(raw) // nil is rendered as missing params → dead with a clear error
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notifications: claim: %w", err)
	}
	// Oldest first, so per-chat ordering follows creation order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].createdAt.Equal(out[j].createdAt) {
			return out[i].id.String() < out[j].id.String()
		}
		return out[i].createdAt.Before(out[j].createdAt)
	})
	return out, nil
}

func decodeParams(raw []byte) map[string]any {
	var params map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&params); err != nil {
		return nil
	}
	return params
}

// editTarget loads the original message of an edit row.
func (w *Worker) editTarget(ctx context.Context, m outboxRow) (*editTarget, error) {
	if m.refID == nil {
		return &editTarget{status: StatusDead}, nil
	}
	var (
		t   editTarget
		raw []byte
	)
	err := w.pool.QueryRow(ctx, `
		SELECT status, telegram_message_id, template, params FROM notification_outbox WHERE id = $1`, *m.refID).
		Scan(&t.status, &t.messageID, &t.template, &raw)
	if db.IsNoRows(err) {
		return &editTarget{status: StatusDead}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("notifications: load edit target: %w", err)
	}
	t.params = decodeParams(raw)
	return &t, nil
}

// maxMessageRunes is Telegram's message length limit.
const maxMessageRunes = 4096

// renderEdit renders an edit: the edit template's text above the original
// message, and the edit template's keyboard (none removes the original one).
func (w *Worker) renderEdit(m outboxRow, target editTarget, langs map[uuid.UUID]string, overrides map[overrideKey]string) (Message, error) {
	status, err := w.render(m, langs, overrides)
	if err != nil {
		return Message{}, err
	}
	orig, err := w.render(outboxRow{bot: m.bot, userID: m.userID, template: target.template, params: target.params},
		langs, overrides)
	if err != nil {
		return status, nil // the status line alone still tells the driver
	}
	text := status.Text + "\n\n" + orig.Text
	if utf8.RuneCountInString(text) > maxMessageRunes {
		text = status.Text
	}
	return Message{Text: text, Markup: status.Markup}, nil
}

func (w *Worker) languages(ctx context.Context, rows []outboxRow) map[uuid.UUID]string {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, m := range rows {
		if m.userID != nil {
			ids = append(ids, *m.userID)
		}
	}
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out
	}
	rs, err := w.pool.Query(ctx, `SELECT user_id, language FROM user_preferences WHERE user_id = ANY($1)`, ids)
	if err != nil {
		w.log.Warn("load languages failed; using default", slog.Any("err", err))
		return out
	}
	defer rs.Close()
	for rs.Next() {
		var id uuid.UUID
		var lang string
		if rs.Scan(&id, &lang) == nil {
			out[id] = lang
		}
	}
	return out
}

func (w *Worker) overrides(ctx context.Context, rows []outboxRow) map[overrideKey]string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range rows {
		if !seen[m.template] {
			seen[m.template] = true
			keys = append(keys, m.template)
		}
	}
	// Edits re-render the message they change; its template is not known
	// here, so the offer template is always loaded.
	if !seen[TplOrderAvailableDriver] {
		keys = append(keys, TplOrderAvailableDriver)
	}
	ov, err := loadOverrides(ctx, w.pool, keys)
	if err != nil {
		w.log.Warn("load template overrides failed; using defaults", slog.Any("err", err))
		return nil
	}
	return ov
}

func (w *Worker) render(m outboxRow, langs map[uuid.UUID]string, overrides map[overrideKey]string) (Message, error) {
	t, ok := Lookup(m.template)
	if !ok {
		return Message{}, fmt.Errorf("unknown template %q", m.template)
	}
	if !t.AllowsBot(m.bot) {
		return Message{}, fmt.Errorf("template %s cannot be sent by bot %s", t.Key, m.bot)
	}
	lang := DefaultLanguage
	if m.userID != nil {
		if l, ok := langs[*m.userID]; ok && validLanguage(l) {
			lang = l
		}
	}
	body, invalid := pickBody(t, lang, overrides)
	if invalid {
		w.log.Warn("template override no longer valid; using default",
			slog.String("template", t.Key), slog.String("lang", lang))
	}
	url := w.opt.PassengerMiniAppURL
	if m.bot == DriverBot {
		url = w.opt.DriverMiniAppURL
	}
	return t.Render(lang, body, m.params, url)
}

func (w *Worker) deliver(ctx context.Context, m outboxRow, msg Message, target *editTarget, deadline time.Time) {
	if err := w.lim.waitBot(ctx, m.bot); err != nil {
		w.reschedule(ctx, m, 0)
		return
	}
	now := time.Now()
	if until, paused := w.lim.pausedUntil(m.bot, now); paused {
		w.reschedule(ctx, m, until.Sub(now))
		return
	}
	if now.After(deadline) {
		// Never send on an expiring lease: another worker may claim the row.
		w.reschedule(ctx, m, 0)
		return
	}
	sctx, cancel := context.WithTimeout(ctx, w.opt.SendTimeout)
	recipientTx, authErr := w.lockContactOrder(sctx, m)
	if authErr == nil && recipientTx == nil {
		recipientTx, authErr = w.lockReviewNotification(sctx, m)
	}
	if authErr != nil {
		cancel()
		if errors.Is(authErr, errContactAccessRevoked) {
			w.finish(ctx, m, now, outcome{kind: "fatal", status: StatusCancelled, errText: "order contact access superseded"})
		} else if errors.Is(authErr, errReviewNoLongerCurrent) {
			w.finish(ctx, m, now, outcome{kind: "fatal", status: StatusCancelled, errText: "application review notification superseded"})
		} else {
			w.log.Warn("notification recipient authorization failed", slog.String("outbox_id", m.id.String()), slog.Any("err", authErr))
			w.reschedule(ctx, m, w.opt.MinBackoff)
		}
		return
	}
	var (
		msgID int64
		err   error
	)
	if target != nil {
		msgID = *target.messageID
		if ed, ok := w.sender.(Editor); ok {
			err = ed.Edit(sctx, m.bot, m.chatID, msgID, msg.Text, msg.Markup)
			if telegram.IsMessageNotModified(err) {
				err = nil // already shows this text (e.g. the bot edited it first)
			}
		} else {
			err = errNoEditor
		}
	} else if photo, ok := w.photo(sctx, m, msg); ok {
		msgID, err = w.sendPhoto(sctx, m, msg, photo)
	} else {
		msgID, err = w.sender.Send(sctx, m.bot, m.chatID, msg.Text, msg.Markup)
	}
	cancel()
	releaseContactLock(recipientTx)
	w.finish(ctx, m, now, w.classify(ctx, m, msgID, err))
}

// photo returns the photo to send with msg when photos can be sent.
func (w *Worker) photo(ctx context.Context, m outboxRow, msg Message) (telegram.InputPhoto, bool) {
	if msg.Photo == nil || w.opt.Photos == nil || utf8.RuneCountInString(msg.Text) > maxCaptionRunes {
		return telegram.InputPhoto{}, false
	}
	if _, ok := w.sender.(PhotoSender); !ok {
		return telegram.InputPhoto{}, false
	}
	if id, err := w.opt.Photos.TelegramFileID(ctx, *msg.Photo, m.bot); err == nil && id != "" {
		return telegram.InputPhoto{FileID: id}, true
	}
	data, err := w.opt.Photos.ReadPhoto(ctx, *msg.Photo)
	if err != nil {
		w.log.Warn("photo unavailable; sending text", slog.String("outbox_id", m.id.String()), slog.Any("err", err))
		return telegram.InputPhoto{}, false
	}
	return telegram.InputPhoto{Data: data, FileName: "photo.jpg"}, true
}

// sendPhoto sends msg as a photo with its text as the caption and caches an
// uploaded photo's file id. A photo Telegram refuses (a 4xx other than rate
// limiting or a blocked chat) does not cost the message: it goes out as text.
func (w *Worker) sendPhoto(ctx context.Context, m outboxRow, msg Message, photo telegram.InputPhoto) (int64, error) {
	msgID, fileID, err := w.sender.(PhotoSender).SendPhoto(ctx, m.bot, m.chatID, photo, msg.Text, msg.Markup)
	if err == nil {
		if photo.FileID == "" && fileID != "" {
			if rerr := w.opt.Photos.RememberTelegramFileID(context.WithoutCancel(ctx), *msg.Photo, m.bot, fileID); rerr != nil {
				w.log.Warn("cache photo file id failed", slog.Any("err", rerr))
			}
		}
		return msgID, nil
	}
	var ae *telegram.APIError
	if errors.As(err, &ae) && ae.Code >= 400 && ae.Code < 500 && !telegram.IsBlocked(err) && !isRetryAfter(err) {
		w.log.Warn("photo refused; sending text", slog.String("outbox_id", m.id.String()), slog.String("err", sanitizeError(err)))
		return w.sender.Send(ctx, m.bot, m.chatID, msg.Text, msg.Markup)
	}
	return 0, err
}

// outcome is the result of one attempt.
type outcome struct {
	kind       string // notification_attempts.outcome: sent | retry | fatal
	status     string // new outbox status
	httpStatus *int
	errText    string
	retryAfter *int          // seconds, for 429
	delay      time.Duration // until next attempt when status is pending
	refund     bool          // do not count this attempt against max_attempts
	messageID  int64
	blockUser  bool
}

func (w *Worker) classify(ctx context.Context, m outboxRow, msgID int64, err error) outcome {
	if err == nil {
		return outcome{kind: "sent", status: StatusSent, messageID: msgID}
	}
	o := outcome{errText: sanitizeError(err)}
	var ae *telegram.APIError
	if errors.As(err, &ae) {
		code := ae.Code
		o.httpStatus = &code
	}
	expired := time.Since(m.createdAt) > w.opt.MaxAge
	switch {
	case ctx.Err() != nil && errors.Is(err, context.Canceled):
		// Shutting down mid-send: retry soon without spending an attempt.
		o.kind, o.status, o.refund = "retry", StatusPending, true
	case isRetryAfter(err):
		ra, _ := telegram.RetryAfter(err)
		w.lim.pause(m.bot, time.Now().Add(ra))
		secs := int((ra + time.Second - 1) / time.Second)
		o.retryAfter = &secs
		if expired {
			o.kind, o.status = "fatal", StatusDead
		} else {
			o.kind, o.status, o.delay, o.refund = "retry", StatusPending, ra, true
		}
	case telegram.IsBlocked(err):
		o.kind, o.status, o.blockUser = "fatal", StatusDead, true
	case telegram.IsRetryable(err) || isTransient(err):
		if m.attempts >= m.maxAttempts || expired {
			o.kind, o.status = "fatal", StatusDead
		} else {
			o.kind, o.status, o.delay = "retry", StatusPending, w.backoff(m.attempts)
		}
	default:
		o.kind, o.status = "fatal", StatusDead
	}
	return o
}

// isTransient covers timeouts and network failures from Senders that do not
// wrap them the way pkg/telegram does.
func isTransient(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne)
}

func isRetryAfter(err error) bool {
	_, ok := telegram.RetryAfter(err)
	return ok
}

// backoff returns the delay before retry number attempt+1: exponential from
// MinBackoff, capped at MaxBackoff, with "equal jitter" (half fixed, half random)
// so a burst of failures does not retry in lockstep.
func (w *Worker) backoff(attempt int) time.Duration {
	d := w.opt.MaxBackoff
	if attempt >= 1 && attempt < 32 {
		if exp := w.opt.MinBackoff << (attempt - 1); exp > 0 && exp < d {
			d = exp
		}
	}
	half := d / 2
	d = half + time.Duration(mrand.Int64N(int64(half)+1))
	return max(min(d, w.opt.MaxBackoff), w.opt.MinBackoff)
}

// finish records an attempt and moves the row to its next state. It uses a
// context detached from cancellation so outcomes are persisted during shutdown.
func (w *Worker) finish(ctx context.Context, m outboxRow, started time.Time, o outcome) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var errText *string
	if o.errText != "" {
		errText = &o.errText
	}
	updated := false
	err := db.WithTx(ctx, w.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO notification_attempts (outbox_id, attempt, started_at, outcome, http_status, error, retry_after_s)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			m.id, m.attempts, started, o.kind, o.httpStatus, errText, o.retryAfter); err != nil {
			return err
		}
		var (
			tag interface{ RowsAffected() int64 }
			err error
		)
		switch o.status {
		case StatusSent:
			tag, err = tx.Exec(ctx, `
				UPDATE notification_outbox
				SET status = 'sent', sent_at = now(), telegram_message_id = $3, last_error = NULL,
				    locked_until = NULL, locked_by = NULL
				WHERE id = $1 AND locked_by = $2 AND status = 'sending'`, m.id, w.opt.InstanceID, o.messageID)
		case StatusPending:
			refund := 0
			if o.refund {
				refund = 1
			}
			tag, err = tx.Exec(ctx, `
				UPDATE notification_outbox
				SET status = 'pending', next_attempt_at = now() + $3 * interval '1 millisecond',
				    attempts = GREATEST(attempts - $4, 0), last_error = $5, locked_until = NULL, locked_by = NULL
				WHERE id = $1 AND locked_by = $2 AND status = 'sending'`,
				m.id, w.opt.InstanceID, o.delay.Milliseconds(), refund, errText)
		case StatusCancelled:
			tag, err = tx.Exec(ctx, `
				UPDATE notification_outbox
				SET status = 'cancelled', last_error = $3, locked_until = NULL, locked_by = NULL
				WHERE id = $1 AND locked_by = $2 AND status = 'sending'`, m.id, w.opt.InstanceID, errText)
		default:
			tag, err = tx.Exec(ctx, `
				UPDATE notification_outbox
				SET status = 'dead', last_error = $3, locked_until = NULL, locked_by = NULL
				WHERE id = $1 AND locked_by = $2 AND status = 'sending'`, m.id, w.opt.InstanceID, errText)
		}
		if err != nil {
			return err
		}
		updated = tag.RowsAffected() == 1
		if o.blockUser && m.userID != nil {
			if _, err := tx.Exec(ctx, blockedColumnSQL(m.bot), *m.userID); err != nil {
				return err
			}
		}
		return nil
	})
	attrs := []any{slog.String("outbox_id", m.id.String()), slog.String("template", m.template),
		slog.String("bot", string(m.bot)), slog.String("outcome", o.kind), slog.Int("attempt", m.attempts)}
	switch {
	case err != nil:
		w.log.Error("record notification outcome failed", append(attrs, slog.Any("err", err))...)
	case !updated:
		w.log.Warn("notification lease lost before outcome was recorded", attrs...)
	case o.kind != "sent":
		w.log.Warn("notification not delivered", append(attrs, slog.String("status", o.status), slog.String("err", o.errText))...)
	}
}

// reschedule returns a claimed row to pending without counting an attempt
// (rate limits, bot pause, shutdown or an expiring lease).
func (w *Worker) reschedule(ctx context.Context, m outboxRow, delay time.Duration) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, err := w.pool.Exec(ctx, `
		UPDATE notification_outbox
		SET status = 'pending', attempts = GREATEST(attempts - 1, 0),
		    next_attempt_at = now() + $3 * interval '1 millisecond', locked_until = NULL, locked_by = NULL
		WHERE id = $1 AND locked_by = $2 AND status = 'sending'`,
		m.id, w.opt.InstanceID, max(delay, 0).Milliseconds())
	if err != nil {
		w.log.Error("reschedule notification failed", slog.String("outbox_id", m.id.String()), slog.Any("err", err))
	}
}

func blockedColumnSQL(b Bot) string {
	if b == DriverBot {
		return `UPDATE users SET driver_bot_blocked_at = now() WHERE id = $1`
	}
	return `UPDATE users SET passenger_bot_blocked_at = now() WHERE id = $1`
}

// tokenRe matches bot tokens ("123456:ABC...") as a last line of defence: the
// telegram client already redacts them, but error text is stored and shown to admins.
var tokenRe = regexp.MustCompile(`\d{5,}:[A-Za-z0-9_-]{20,}`)

const maxErrorText = 500

func sanitizeError(err error) string {
	s := tokenRe.ReplaceAllString(err.Error(), "<redacted>")
	if len(s) > maxErrorText {
		s = s[:maxErrorText]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}
