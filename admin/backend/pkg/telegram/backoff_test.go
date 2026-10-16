package telegram

import (
	"testing"
	"time"
)

func TestBackoffBoundsAndGrowth(t *testing.T) {
	tests := []struct {
		name     string
		min, max time.Duration
	}{
		{"defaults", 0, 0},
		{"bots", time.Second, 60 * time.Second},
		{"small", 10 * time.Millisecond, 80 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := Backoff{Min: tt.min, Max: tt.max}
			lo, hi := tt.min, tt.max
			if lo == 0 {
				lo, hi = time.Second, 60*time.Second
			}
			if first := b.Next(); first != lo {
				t.Fatalf("first delay = %v, want %v", first, lo)
			}
			ceiling := lo
			for i := 1; i < 200; i++ {
				ceiling = min(ceiling*2, hi)
				d := b.Next()
				if d < lo || d > ceiling {
					t.Fatalf("attempt %d: delay %v outside [%v, %v]", i, d, lo, ceiling)
				}
			}
			// After many attempts the sequence must be capped and still jittered.
			seen := map[time.Duration]bool{}
			for i := 0; i < 50; i++ {
				d := b.Next()
				if d > hi {
					t.Fatalf("delay %v above max %v", d, hi)
				}
				seen[d] = true
			}
			if len(seen) < 2 {
				t.Fatal("expected jittered delays")
			}
			b.Reset()
			if d := b.Next(); d != lo {
				t.Fatalf("after Reset delay = %v, want %v", d, lo)
			}
		})
	}
}
