// Package routing owns the OSRM client, the geocoding adapters (admin
// gazetteer first, then Photon / Nominatim / 2GIS), the /geo/* endpoints and
// POST /routes/preview, which turns validated stops into a priced fare quote.
package routing

import (
	"container/list"
	"sync"
	"time"
)

// ttlCache is a small, size-bounded, concurrency-safe cache whose entries
// expire after a fixed TTL. When full, the oldest entry is evicted.
type ttlCache[K comparable, V any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	max   int
	now   func() time.Time
	items map[K]*list.Element
	order *list.List // front = oldest
}

type cacheEntry[K comparable, V any] struct {
	key     K
	value   V
	expires time.Time
}

func newTTLCache[K comparable, V any](ttl time.Duration, maxEntries int) *ttlCache[K, V] {
	return &ttlCache[K, V]{ttl: ttl, max: max(maxEntries, 1), now: time.Now,
		items: map[K]*list.Element{}, order: list.New()}
}

func (c *ttlCache[K, V]) get(key K) (V, bool) {
	var zero V
	if c == nil || c.ttl <= 0 {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	e := el.Value.(*cacheEntry[K, V])
	if !c.now().Before(e.expires) {
		c.order.Remove(el)
		delete(c.items, key)
		return zero, false
	}
	return e.value, true
}

func (c *ttlCache[K, V]) set(key K, value V) {
	if c == nil || c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	exp := c.now().Add(c.ttl)
	if el, ok := c.items[key]; ok {
		e := el.Value.(*cacheEntry[K, V])
		e.value, e.expires = value, exp
		c.order.MoveToBack(el)
		return
	}
	c.items[key] = c.order.PushBack(&cacheEntry[K, V]{key: key, value: value, expires: exp})
	for len(c.items) > c.max {
		front := c.order.Front()
		c.order.Remove(front)
		delete(c.items, front.Value.(*cacheEntry[K, V]).key)
	}
}

func (c *ttlCache[K, V]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// breaker is a minimal circuit breaker: after threshold consecutive failures
// it rejects calls for cooldown, then lets a single probe through; a
// successful probe closes it, a failed one re-opens it.
type breaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	now       func() time.Time
	failures  int
	openUntil time.Time
	probing   bool
}

func newBreaker(threshold int, cooldown time.Duration) *breaker {
	return &breaker{threshold: max(threshold, 1), cooldown: cooldown, now: time.Now}
}

// allow reports whether a call may proceed.
func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < b.threshold {
		return true
	}
	if b.probing || b.now().Before(b.openUntil) {
		return false
	}
	b.probing = true
	return true
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures, b.probing = 0, false
}

func (b *breaker) failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.probing = false
	if b.failures >= b.threshold {
		b.openUntil = b.now().Add(b.cooldown)
	}
}

// abort ends a call that says nothing about the provider's health (e.g. the
// caller went away), so a pending probe does not keep the breaker open.
func (b *breaker) abort() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
}

// open reports whether the breaker currently rejects calls.
func (b *breaker) open() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures >= b.threshold && (b.probing || b.now().Before(b.openUntil))
}
