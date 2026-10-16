package runtime

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/botapi"
)

const nbsp = " "

// FormatMoney renders whole tenge as "1 500 ₸" with no-break spaces, the
// same format as the notification templates.
func FormatMoney(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	b.WriteString(sign)
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(nbsp)
		}
		b.WriteRune(r)
	}
	return b.String() + nbsp + "₸"
}

// FormatPhone renders "+77071234567" as "+7 707 123 45 67"; other input is
// returned unchanged.
func FormatPhone(e164 string) string {
	if len(e164) != 12 || !strings.HasPrefix(e164, "+7") {
		return e164
	}
	return "+7 " + e164[2:5] + " " + e164[5:8] + " " + e164[8:10] + " " + e164[10:]
}

// FormatDistance renders metres as "650 м" / "1,8 км" (kk, ru) or
// "650 m" / "1.8 km" (en).
func FormatDistance(m int, lang string) string {
	unitM, unitKM, sep := "м", "км", ","
	if Lang(lang) == "en" {
		unitM, unitKM, sep = "m", "km", "."
	}
	if m < 1000 {
		return strconv.Itoa(max(m, 0)) + nbsp + unitM
	}
	tenths := (m + 50) / 100
	s := strconv.Itoa(tenths / 10)
	if tenths%10 != 0 {
		s += sep + strconv.Itoa(tenths%10)
	}
	return s + nbsp + unitKM
}

// ConfigSource returns the bots' runtime configuration (*botapi.Client).
type ConfigSource interface {
	Config(ctx context.Context) (botapi.Config, error)
}

// ConfigCache keeps the internal API configuration (support contacts, Mini
// App URLs) for a while so replies do not need an extra call each time. When
// a refresh fails, the last known value is used.
type ConfigCache struct {
	src ConfigSource
	ttl time.Duration
	log *slog.Logger

	mu      sync.Mutex
	value   botapi.Config
	fetched time.Time
	ok      bool
}

// NewConfigCache returns a cache refreshing every ttl.
func NewConfigCache(src ConfigSource, ttl time.Duration, log *slog.Logger) *ConfigCache {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ConfigCache{src: src, ttl: ttl, log: log}
}

// Get returns the configuration and whether any value is known.
func (c *ConfigCache) Get(ctx context.Context) (botapi.Config, bool) {
	c.mu.Lock()
	fresh := c.ok && time.Since(c.fetched) < c.ttl
	value, ok := c.value, c.ok
	c.mu.Unlock()
	if fresh {
		return value, true
	}
	cfg, err := c.src.Config(ctx)
	if err != nil {
		c.log.Warn("load bot config failed", slog.String("err", err.Error()))
		return value, ok
	}
	c.mu.Lock()
	c.value, c.fetched, c.ok = cfg, time.Now(), true
	c.mu.Unlock()
	return cfg, true
}
