package notifications

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// limiter enforces Telegram's broadcast limits in process: a token bucket per
// bot (≤ PerBotRate msg/s), a minimum interval between messages to the same
// chat, and a bot-wide pause after a 429.
type limiter struct {
	mu       sync.Mutex
	perBot   map[Bot]*rate.Limiter
	paused   map[Bot]time.Time
	chats    map[chatKey]time.Time // next time a message may go to the chat
	interval time.Duration
	botRate  float64
}

type chatKey struct {
	bot  Bot
	chat int64
}

const maxTrackedChats = 50_000

func newLimiter(perBotRate float64, chatInterval time.Duration) *limiter {
	return &limiter{
		perBot:   make(map[Bot]*rate.Limiter),
		paused:   make(map[Bot]time.Time),
		chats:    make(map[chatKey]time.Time),
		interval: chatInterval,
		botRate:  perBotRate,
	}
}

// reserveChat claims the next send slot for the chat. When the chat is still
// cooling down it returns the time the next message may go and false.
func (l *limiter) reserveChat(bot Bot, chat int64, now time.Time) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := chatKey{bot, chat}
	if next, ok := l.chats[k]; ok && now.Before(next) {
		return next, false
	}
	if len(l.chats) >= maxTrackedChats {
		for ck, next := range l.chats {
			if !now.Before(next) {
				delete(l.chats, ck)
			}
		}
	}
	l.chats[k] = now.Add(l.interval)
	return time.Time{}, true
}

// pause stops sends for bot until the given time (Telegram flood control).
func (l *limiter) pause(bot Bot, until time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until.After(l.paused[bot]) {
		l.paused[bot] = until
	}
}

// pausedUntil reports whether bot is paused at now and until when.
func (l *limiter) pausedUntil(bot Bot, now time.Time) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	until, ok := l.paused[bot]
	if !ok || !now.Before(until) {
		return time.Time{}, false
	}
	return until, true
}

// waitBot blocks until the bot's token bucket allows one more message.
func (l *limiter) waitBot(ctx context.Context, bot Bot) error {
	l.mu.Lock()
	lim, ok := l.perBot[bot]
	if !ok {
		lim = rate.NewLimiter(rate.Limit(l.botRate), 1)
		l.perBot[bot] = lim
	}
	l.mu.Unlock()
	return lim.Wait(ctx)
}
