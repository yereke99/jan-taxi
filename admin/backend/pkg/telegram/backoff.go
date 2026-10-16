package telegram

import (
	"math/rand/v2"
	"time"
)

// Backoff produces exponentially growing, fully jittered delays: the n-th call to
// Next returns a uniform random duration in [Min, min(Max, Min*2^n)]. The zero
// value uses 1s..60s. Backoff is not safe for concurrent use.
type Backoff struct {
	Min, Max time.Duration
	attempt  int
}

// Next returns the delay before the next retry and advances the sequence.
func (b *Backoff) Next() time.Duration {
	lo, hi := b.Min, b.Max
	if lo <= 0 {
		lo = time.Second
	}
	if hi < lo {
		hi = max(lo, 60*time.Second)
	}
	ceiling := lo
	for i := 0; i < b.attempt && ceiling < hi; i++ {
		ceiling *= 2
	}
	ceiling = min(ceiling, hi)
	if ceiling < hi {
		b.attempt++
	}
	if ceiling == lo {
		return lo
	}
	return lo + rand.N(ceiling-lo+1)
}

// Reset starts the sequence again after a success.
func (b *Backoff) Reset() { b.attempt = 0 }
