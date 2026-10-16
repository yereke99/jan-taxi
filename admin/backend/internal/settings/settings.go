// Package settings reads and writes the runtime-editable, non-secret
// configuration stored in app_settings. Values are JSON; every key an admin may
// edit is declared in the registry (registry.go) with its type, validation and
// default. Reads outside transactions go through a short in-process cache so hot
// paths (public config, order creation) do not hit the database on every call.
package settings

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// DefaultCacheTTL bounds how stale a cached value may be on other API replicas
// after an admin edit.
const DefaultCacheTTL = 5 * time.Second

// Localized is a text in the three supported languages.
type Localized struct {
	KK string `json:"kk"`
	RU string `json:"ru"`
	EN string `json:"en"`
}

// Get returns the text for lang, falling back to Kazakh.
func (l Localized) Get(lang string) string {
	switch lang {
	case "ru":
		return l.RU
	case "en":
		return l.EN
	default:
		return l.KK
	}
}

// IsZero reports whether all three languages are empty.
func (l Localized) IsZero() bool { return l.KK == "" && l.RU == "" && l.EN == "" }

// Int returns an integer setting or def when it is missing or malformed.
func Int(ctx context.Context, q db.Querier, key string, def int64) int64 {
	var v int64
	if !read(ctx, q, key, &v) {
		return def
	}
	return v
}

// Bool returns a boolean setting or def when it is missing or malformed.
func Bool(ctx context.Context, q db.Querier, key string, def bool) bool {
	var v bool
	if !read(ctx, q, key, &v) {
		return def
	}
	return v
}

// Strings returns a string-array setting or def when it is missing or malformed.
func Strings(ctx context.Context, q db.Querier, key string, def []string) []string {
	var v []string
	if !read(ctx, q, key, &v) || v == nil {
		return def
	}
	return v
}

// String returns a string setting or def when it is missing or malformed.
func String(ctx context.Context, q db.Querier, key string, def string) string {
	var v string
	if !read(ctx, q, key, &v) {
		return def
	}
	return v
}

// Text returns a localized-text setting or def when it is missing or malformed.
func Text(ctx context.Context, q db.Querier, key string, def Localized) Localized {
	var v Localized
	if !read(ctx, q, key, &v) {
		return def
	}
	return v
}

// JSON decodes the stored value into dst. found is false when the key has no
// row; err reports database or decoding failures.
func JSON(ctx context.Context, q db.Querier, key string, dst any) (found bool, err error) {
	raw, found, err := load(ctx, q, key)
	if err != nil || !found {
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return true, err
	}
	return true, nil
}

// read decodes key into dst and reports whether a usable value was found.
// Failures are logged and treated as "missing" so callers fall back to defaults.
func read(ctx context.Context, q db.Querier, key string, dst any) bool {
	found, err := JSON(ctx, q, key, dst)
	if err != nil {
		httpx.Logger(ctx).Warn("setting unreadable, using default", slog.String("key", key), slog.Any("err", err))
		return false
	}
	return found
}

// Set validates value against the registry and stores it. adminID may be
// uuid.Nil for system writes. It does not audit; admin handlers do that.
func Set(ctx context.Context, q db.Querier, key string, value any, adminID uuid.UUID) error {
	def, ok := Lookup(key)
	if !ok {
		return httpx.Validation(map[string]string{key: ReasonUnknown})
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return httpx.Validation(map[string]string{key: ReasonInvalidType})
	}
	norm, reason := def.Normalize(raw)
	if reason != "" {
		return httpx.Validation(map[string]string{key: reason})
	}
	if err := upsert(ctx, q, key, norm, adminID); err != nil {
		return err
	}
	Invalidate(key)
	return nil
}

// Reset deletes the stored value so the env or built-in default applies again.
func Reset(ctx context.Context, q db.Querier, key string) error {
	if _, ok := Lookup(key); !ok {
		return httpx.Validation(map[string]string{key: ReasonUnknown})
	}
	if _, err := q.Exec(ctx, `DELETE FROM app_settings WHERE key = $1`, key); err != nil {
		return httpx.Internal(err)
	}
	Invalidate(key)
	return nil
}

func upsert(ctx context.Context, q db.Querier, key string, value json.RawMessage, adminID uuid.UUID) error {
	var by *uuid.UUID
	if adminID != uuid.Nil {
		by = &adminID
	}
	_, err := q.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_by, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		key, []byte(value), by)
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}

// --- cache -------------------------------------------------------------------

type cacheEntry struct {
	raw     json.RawMessage
	found   bool
	expires time.Time
}

var cache = struct {
	mu  sync.RWMutex
	ttl time.Duration
	m   map[string]cacheEntry
}{m: map[string]cacheEntry{}}

func init() {
	// Test binaries create a fresh database per test and often edit app_settings
	// directly; caching across them would leak values between tests.
	if !testing.Testing() {
		cache.ttl = DefaultCacheTTL
	}
}

// SetCacheTTL changes the cache lifetime; 0 disables caching. Intended for
// tests and tooling.
func SetCacheTTL(d time.Duration) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.ttl = d
	clear(cache.m)
}

// Invalidate drops cached values for keys, or everything when no key is given.
func Invalidate(keys ...string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(keys) == 0 {
		clear(cache.m)
		return
	}
	for _, k := range keys {
		delete(cache.m, k)
	}
}

// load returns the raw stored value. Reads inside a transaction bypass the cache
// in both directions: they must see the transaction's own writes and must not
// publish uncommitted values to other requests.
func load(ctx context.Context, q db.Querier, key string) (json.RawMessage, bool, error) {
	_, inTx := q.(pgx.Tx)
	now := time.Now()
	if !inTx {
		cache.mu.RLock()
		e, ok := cache.m[key]
		ttl := cache.ttl
		cache.mu.RUnlock()
		if ok && ttl > 0 && now.Before(e.expires) {
			return e.raw, e.found, nil
		}
	}
	var raw []byte
	err := q.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&raw)
	found := true
	if db.IsNoRows(err) {
		found, err = false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !inTx {
		cache.mu.Lock()
		if cache.ttl > 0 {
			cache.m[key] = cacheEntry{raw: raw, found: found, expires: now.Add(cache.ttl)}
		}
		cache.mu.Unlock()
	}
	return raw, found, nil
}
