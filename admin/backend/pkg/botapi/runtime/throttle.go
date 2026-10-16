package runtime

import (
	"sync"
	"time"
)

// Throttle allows one event per key per interval (e.g. one error explanation
// per chat every 10 minutes for live-location updates).
type Throttle struct {
	mu    sync.Mutex
	every time.Duration
	last  map[int64]time.Time
	now   func() time.Time
}

// NewThrottle returns a Throttle with the given interval.
func NewThrottle(every time.Duration) *Throttle {
	return &Throttle{every: every, last: map[int64]time.Time{}, now: time.Now}
}

// Allow reports whether the event for key may happen now and records it.
func (t *Throttle) Allow(key int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if last, ok := t.last[key]; ok && now.Sub(last) < t.every {
		return false
	}
	t.last[key] = now
	if len(t.last) > 10000 {
		for k, v := range t.last {
			if now.Sub(v) >= t.every {
				delete(t.last, k)
			}
		}
	}
	return true
}

// Reset forgets key, so the next event is allowed.
func (t *Throttle) Reset(key int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.last, key)
}
