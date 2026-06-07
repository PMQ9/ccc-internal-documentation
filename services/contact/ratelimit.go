package main

import (
	"sync"
	"time"
)

// maxTrackedKeys caps how many distinct keys the limiter holds at once — a hard
// backstop against memory exhaustion if the key space is ever attacker-influenced
// (e.g. spoofed source IPs in X-Forwarded-For). It sits far above any real client
// diversity for an internal form, so a healthy service never reaches it; if it
// does, the oldest-seen keys are evicted. (issue #41)
const maxTrackedKeys = 50_000

// rateLimiter is a per-key sliding-window limiter (in-memory, single instance).
// Adequate for a low-volume internal form; on the single prod node it is exact.
// If the service is ever scaled horizontally, swap this for a shared store.
//
// Keys are evicted two ways so the map can't grow without bound: a lazy sweep
// (at most once per window) drops keys whose entire window has elapsed, and a
// hard maxTrackedKeys cap evicts the oldest-seen keys if the map still overflows.
// Reused for the per-IP, global circuit-breaker, and GitHub-daily limiters.
type rateLimiter struct {
	mu        sync.Mutex
	limit     int
	win       time.Duration
	hits      map[string][]time.Time
	lastSweep time.Time
}

func newRateLimiter(limit int, win time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, win: win, hits: make(map[string][]time.Time)}
}

// allow records an attempt for key at now and reports whether it is within the
// limit over the trailing window. Expired timestamps are pruned on access, and
// the map is swept/capped so distinct keys can't accumulate without bound.
func (rl *rateLimiter) allow(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Once per window, drop keys whose entire window has elapsed. This is what
	// bounds memory to "keys seen in the last window" instead of "keys ever
	// seen" — without it the map only ever grows (the original bug, issue #41).
	if now.Sub(rl.lastSweep) >= rl.win {
		rl.sweep(now)
	}

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

	// A brand-new key is about to be tracked: enforce the hard cap (sweep first,
	// then evict oldest-seen while still over) so a flood of distinct keys can't
	// OOM the process even between scheduled sweeps.
	if _, seen := rl.hits[key]; !seen && len(rl.hits) >= maxTrackedKeys {
		rl.sweep(now)
		for len(rl.hits) >= maxTrackedKeys {
			rl.evictOldest()
		}
	}

	rl.hits[key] = append(kept, now)
	return true
}

// sweep deletes every key whose timestamps have all aged out of the window.
// The slices are insertion-ordered (now is monotonic in practice), so the last
// element is the most recent hit; if even it has expired, the whole key has.
// Caller holds rl.mu.
func (rl *rateLimiter) sweep(now time.Time) {
	cutoff := now.Add(-rl.win)
	for k, ts := range rl.hits {
		if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
			delete(rl.hits, k)
		}
	}
	rl.lastSweep = now
}

// evictOldest removes the key whose most-recent hit is the oldest — the one
// least likely to still be rate-limited. Caller holds rl.mu, and only calls this
// at the maxTrackedKeys backstop, which a healthy internal form never reaches.
func (rl *rateLimiter) evictOldest() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, ts := range rl.hits {
		last := ts[len(ts)-1]
		if first || last.Before(oldest) {
			oldest, oldestKey, first = last, k, false
		}
	}
	if !first {
		delete(rl.hits, oldestKey)
	}
}
