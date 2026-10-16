// Package runtime is the shared runtime of the two Telegram bots: the
// single-consumer long-polling loop (lease, claim, offset), the health
// endpoint, outgoing message helpers, keyboards, button matching and texts.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// DefaultAllowedUpdates are the update types both bots handle (§13).
var DefaultAllowedUpdates = []string{"message", "edited_message", "callback_query", "my_chat_member"}

// Defaults of PollerConfig (§13).
const (
	DefaultLeaseTTL       = 30 * time.Second
	DefaultRenewEvery     = 10 * time.Second
	DefaultPollTimeout    = 30
	DefaultPollLimit      = 50
	DefaultHandlerTimeout = 20 * time.Second

	// maxLeaseMargin bounds how early polling stops before an unrenewed lease
	// runs out, so a new holder never overlaps with this one.
	maxLeaseMargin = 3 * time.Second
)

// TelegramAPI is the part of the Bot API the poller uses.
type TelegramAPI interface {
	GetUpdates(ctx context.Context, p telegram.GetUpdatesParams) ([]telegram.Update, error)
	DeleteWebhook(ctx context.Context, dropPending bool) error
}

// StateAPI is the part of the internal bot API the poller uses.
type StateAPI interface {
	AcquireLease(ctx context.Context, instanceID string, ttl time.Duration) (botapi.Lease, error)
	SaveOffset(ctx context.Context, instanceID string, lastUpdateID int64) (int64, error)
	ClaimUpdate(ctx context.Context, updateID int64) (bool, error)
}

// Handler processes one update. Its context is cancelled after the handler
// timeout but not by shutdown, so an update in progress is always finished.
type Handler interface {
	HandleUpdate(ctx context.Context, u telegram.Update) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, u telegram.Update) error

// HandleUpdate calls f.
func (f HandlerFunc) HandleUpdate(ctx context.Context, u telegram.Update) error { return f(ctx, u) }

// PollerConfig configures a Poller. Zero durations use the defaults.
type PollerConfig struct {
	Telegram   TelegramAPI
	State      StateAPI
	Handler    Handler
	InstanceID string
	Log        *slog.Logger

	LeaseTTL       time.Duration
	RenewEvery     time.Duration
	PollTimeout    int // seconds; 0 means DefaultPollTimeout, negative means short polling (tests)
	Limit          int
	AllowedUpdates []string
	HandlerTimeout time.Duration

	// MinBackoff/MaxBackoff bound network-error backoff (1s..60s);
	// ConflictBackoff is the first delay after a Telegram 409 (2s).
	MinBackoff, MaxBackoff, ConflictBackoff time.Duration

	// OnLeader runs in its own goroutine whenever this instance gains the lease.
	OnLeader func(ctx context.Context)
}

// Poller runs getUpdates for one bot token while it holds the lease.
type Poller struct {
	cfg    PollerConfig
	log    *slog.Logger
	margin time.Duration

	mu            sync.Mutex
	leader        bool
	gen           uint64
	leaderSince   time.Time
	leaseUntil    time.Time
	lastProcessed int64
	pollCancel    context.CancelFunc
	deleteWebhook bool
	lastPoll      time.Time
	lastProgress  time.Time
	lastLeaseTry  time.Time
	started       time.Time

	running atomic.Bool
	wake    chan struct{}
	recheck chan struct{}
}

// NewPoller validates cfg and applies defaults.
func NewPoller(cfg PollerConfig) (*Poller, error) {
	switch {
	case cfg.Telegram == nil || cfg.State == nil || cfg.Handler == nil:
		return nil, errors.New("runtime: telegram, state and handler are required")
	case strings.TrimSpace(cfg.InstanceID) == "":
		return nil, errors.New("runtime: instance id is required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = DefaultLeaseTTL
	}
	if cfg.RenewEvery <= 0 {
		cfg.RenewEvery = DefaultRenewEvery
	}
	if cfg.RenewEvery >= cfg.LeaseTTL {
		return nil, errors.New("runtime: lease must be renewed more often than its TTL")
	}
	switch {
	case cfg.PollTimeout == 0:
		cfg.PollTimeout = DefaultPollTimeout
	case cfg.PollTimeout < 0:
		cfg.PollTimeout = 0
	}
	if cfg.Limit <= 0 || cfg.Limit > 100 {
		cfg.Limit = DefaultPollLimit
	}
	if cfg.AllowedUpdates == nil {
		cfg.AllowedUpdates = DefaultAllowedUpdates
	}
	if cfg.HandlerTimeout <= 0 {
		cfg.HandlerTimeout = DefaultHandlerTimeout
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = max(cfg.MinBackoff, 60*time.Second)
	}
	if cfg.ConflictBackoff <= 0 {
		cfg.ConflictBackoff = 2 * time.Second
	}
	return &Poller{
		cfg:     cfg,
		log:     cfg.Log,
		margin:  min(maxLeaseMargin, cfg.LeaseTTL/10),
		wake:    make(chan struct{}, 1),
		recheck: make(chan struct{}, 1),
	}, nil
}

// Run keeps the lease and polls until ctx is cancelled. An update being
// handled when ctx is cancelled is finished and its offset stored; the lease
// is not released (it expires), so a restarted instance resumes quickly.
func (p *Poller) Run(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("runtime: poller already running")
	}
	defer p.running.Store(false)
	p.mu.Lock()
	p.started = time.Now()
	p.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); p.leaseLoop(ctx) }()
	go func() { defer wg.Done(); p.pollLoop(ctx) }()
	wg.Wait()
	return nil
}

// --- lease ----------------------------------------------------------------------

func (p *Poller) leaseLoop(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.RenewEvery)
	defer ticker.Stop()
	p.checkLease(ctx)
	for {
		expiry := time.NewTimer(p.untilStepDown())
		select {
		case <-ctx.Done():
			expiry.Stop()
			return
		case <-ticker.C:
			p.checkLease(ctx)
		case <-p.recheck:
			p.checkLease(ctx)
		case <-expiry.C:
			p.mu.Lock()
			if p.leader && !time.Now().Before(p.leaseUntil.Add(-p.margin)) {
				p.stepDownLocked("lease could not be renewed")
			}
			p.mu.Unlock()
		}
		expiry.Stop()
	}
}

// untilStepDown is the time left before an unrenewed lease must be given up.
func (p *Poller) untilStepDown() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.leader {
		return time.Hour
	}
	return max(time.Until(p.leaseUntil.Add(-p.margin)), 0)
}

func (p *Poller) checkLease(ctx context.Context) {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, p.cfg.RenewEvery)
	lease, err := p.cfg.State.AcquireLease(cctx, p.cfg.InstanceID, p.cfg.LeaseTTL)
	cancel()

	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastLeaseTry = time.Now()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		p.log.Warn("bot lease check failed", slog.String("err", err.Error()))
		if p.leader && !time.Now().Before(p.leaseUntil.Add(-p.margin)) {
			p.stepDownLocked("lease could not be renewed")
		}
		return
	}
	if !lease.Acquired {
		if p.leader {
			p.stepDownLocked("lease taken by another instance")
		}
		return
	}
	p.leaseUntil = start.Add(p.cfg.LeaseTTL)
	if p.leader {
		return
	}
	p.leader = true
	p.gen++
	p.leaderSince = time.Now()
	p.lastProcessed = max(p.lastProcessed, lease.LastUpdateID)
	p.deleteWebhook = true
	p.log.Info("polling lease acquired", slog.String("instance", p.cfg.InstanceID),
		slog.Int64("last_update_id", p.lastProcessed))
	select {
	case p.wake <- struct{}{}:
	default:
	}
	if p.cfg.OnLeader != nil {
		go p.cfg.OnLeader(ctx)
	}
}

func (p *Poller) stepDownLocked(reason string) {
	p.leader = false
	if p.pollCancel != nil {
		p.pollCancel()
		p.pollCancel = nil
	}
	p.log.Warn("polling stopped", slog.String("reason", reason), slog.String("instance", p.cfg.InstanceID))
}

func (p *Poller) requestRecheck() {
	select {
	case p.recheck <- struct{}{}:
	default:
	}
}

// --- polling --------------------------------------------------------------------

// waitLeader blocks until this instance holds the lease; it returns the
// leadership generation, or false when ctx ends.
func (p *Poller) waitLeader(ctx context.Context) (uint64, bool) {
	for {
		p.mu.Lock()
		leader, gen := p.leader, p.gen
		p.mu.Unlock()
		if leader {
			return gen, true
		}
		select {
		case <-ctx.Done():
			return 0, false
		case <-p.wake:
		}
	}
}

func (p *Poller) stillLeader(gen uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.leader && p.gen == gen
}

// beginPoll registers the cancel func of a poll so a lease loss aborts it.
func (p *Poller) beginPoll(gen uint64, cancel context.CancelFunc) (offset int64, deleteWebhook, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.leader || p.gen != gen {
		return 0, false, false
	}
	p.pollCancel = cancel
	deleteWebhook, p.deleteWebhook = p.deleteWebhook, false
	return p.lastProcessed + 1, deleteWebhook, true
}

func (p *Poller) endPoll(gen uint64, success bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gen == gen {
		p.pollCancel = nil
	}
	if success {
		p.lastPoll = time.Now()
		p.lastProgress = p.lastPoll
	}
}

func (p *Poller) pollLoop(ctx context.Context) {
	netBackoff := telegram.Backoff{Min: p.cfg.MinBackoff, Max: p.cfg.MaxBackoff}
	conflictBackoff := telegram.Backoff{Min: p.cfg.ConflictBackoff, Max: p.cfg.MaxBackoff}
	for {
		gen, ok := p.waitLeader(ctx)
		if !ok {
			return
		}
		pctx, cancel := context.WithCancel(ctx)
		offset, deleteWebhook, ok := p.beginPoll(gen, cancel)
		if !ok {
			cancel()
			continue
		}
		if deleteWebhook {
			if err := p.cfg.Telegram.DeleteWebhook(pctx, false); err != nil {
				cancel()
				p.endPoll(gen, false)
				p.mu.Lock()
				p.deleteWebhook = true
				p.mu.Unlock()
				if ctx.Err() != nil {
					return
				}
				p.log.Warn("deleteWebhook failed", slog.String("err", err.Error()))
				sleep(ctx, p.retryDelay(err, &netBackoff))
				continue
			}
		}
		updates, err := p.cfg.Telegram.GetUpdates(pctx, telegram.GetUpdatesParams{
			Offset: offset, Limit: p.cfg.Limit, Timeout: p.cfg.PollTimeout, AllowedUpdates: p.cfg.AllowedUpdates,
		})
		cancel()
		p.endPoll(gen, err == nil)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !p.stillLeader(gen) {
				continue // the lease was lost while polling
			}
			if telegram.IsConflict(err) {
				p.log.Warn("telegram reports another consumer", slog.String("err", err.Error()))
				if strings.Contains(strings.ToLower(err.Error()), "webhook") {
					p.mu.Lock()
					p.deleteWebhook = true
					p.mu.Unlock()
				}
				p.requestRecheck()
				sleep(ctx, conflictBackoff.Next())
				continue
			}
			p.log.Warn("getUpdates failed", slog.String("err", err.Error()))
			sleep(ctx, p.retryDelay(err, &netBackoff))
			continue
		}
		netBackoff.Reset()
		conflictBackoff.Reset()
		if !p.processBatch(ctx, gen, updates) {
			sleep(ctx, netBackoff.Next())
		}
	}
}

// retryDelay honours retry_after on 429 and otherwise backs off exponentially.
func (p *Poller) retryDelay(err error, b *telegram.Backoff) time.Duration {
	if d, ok := telegram.RetryAfter(err); ok {
		return d
	}
	return b.Next()
}

// processBatch claims and handles updates in order and stores the offset of
// the last one handled. It returns false when a claim could not be made; the
// remaining updates are fetched again by the next poll.
func (p *Poller) processBatch(ctx context.Context, gen uint64, updates []telegram.Update) bool {
	p.mu.Lock()
	last := p.lastProcessed
	p.mu.Unlock()
	start := last
	complete := true
	for _, u := range updates {
		if ctx.Err() != nil || !p.stillLeader(gen) {
			break
		}
		if u.UpdateID <= last {
			continue
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		claimed, err := p.cfg.State.ClaimUpdate(cctx, u.UpdateID)
		cancel()
		if err != nil {
			p.log.Warn("claim update failed", slog.Int64("update_id", u.UpdateID), slog.String("err", err.Error()))
			complete = false
			break
		}
		if claimed {
			p.handle(ctx, u)
		} else {
			p.log.Debug("update already processed", slog.Int64("update_id", u.UpdateID))
		}
		last = u.UpdateID
		p.mu.Lock()
		p.lastProcessed = max(p.lastProcessed, last)
		p.lastProgress = time.Now()
		p.mu.Unlock()
	}
	if last > start {
		p.saveOffset(ctx, last)
	}
	return complete
}

func (p *Poller) handle(ctx context.Context, u telegram.Update) {
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.cfg.HandlerTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("update handler panicked", slog.Int64("update_id", u.UpdateID),
				slog.String("panic", fmt.Sprint(r)), slog.String("stack", string(debug.Stack())))
		}
	}()
	if err := p.cfg.Handler.HandleUpdate(hctx, u); err != nil {
		p.log.Warn("update handler failed", slog.Int64("update_id", u.UpdateID), slog.String("err", err.Error()))
	}
}

func (p *Poller) saveOffset(ctx context.Context, last int64) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := p.cfg.State.SaveOffset(sctx, p.cfg.InstanceID, last); err != nil {
		p.log.Warn("save offset failed", slog.Int64("last_update_id", last), slog.String("err", err.Error()))
		if botapi.IsCode(err, "conflict") {
			p.requestRecheck()
		}
	}
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
