package main

import (
	"sync"
	"time"
)

// rateLimiter is a per-key sliding-window limiter (in-memory, single instance).
// Adequate for a low-volume internal form; on the single prod node it is exact.
// If the service is ever scaled horizontally, swap this for a shared store.
type rateLimiter struct {
	mu    sync.Mutex
	limit int
	win   time.Duration
	hits  map[string][]time.Time
}

func newRateLimiter(limit int, win time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, win: win, hits: make(map[string][]time.Time)}
}

// allow records an attempt for key at now and reports whether it is within the
// limit over the trailing window. Expired timestamps are pruned on access.
func (rl *rateLimiter) allow(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := now.Add(-rl.win)
	kept := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= rl.limit {
		rl.hits[key] = kept
		return false
	}
	rl.hits[key] = append(kept, now)
	return true
}
