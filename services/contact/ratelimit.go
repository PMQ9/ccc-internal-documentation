package main

import (
	"sort"
	"sync"
	"time"
)

// maxTrackedKeys caps how many distinct keys the limiter holds at once — a hard
// backstop against memory exhaustion if the key space is ever attacker-influenced
// (e.g. spoofed source IPs in X-Forwarded-For). It sits far above any real client
// diversity for an internal form, so a healthy service never reaches it; if it
// does, the oldest-seen keys are evicted in a batch. (issue #41)
const maxTrackedKeys = 50_000

// rateLimiter is a per-key sliding-window limiter (in-memory, single instance).
// Adequate for a low-volume internal form; on the single prod node it is exact.
// If the service is ever scaled horizontally, swap this for a shared store.
//
// Keys are evicted two ways so the map can't grow without bound: a lazy sweep
// (at most once per window) drops keys whose entire window has elapsed, and a
// hard maxKeys cap batch-evicts the oldest-seen keys if the map still overflows.
// Reused for the per-IP, global circuit-breaker, and GitHub-daily limiters.
type rateLimiter struct {
	mu        sync.Mutex
	limit     int
	win       time.Duration
	maxKeys   int // hard cap on distinct keys; injectable so the cap path is testable
	hits      map[string][]time.Time
	lastSweep time.Time
}

func newRateLimiter(limit int, win time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, win: win, maxKeys: maxTrackedKeys, hits: make(map[string][]time.Time)}
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
	// then, if still over, batch-evict down to a low-water mark) so a flood of
	// distinct keys can't OOM the process even between scheduled sweeps.
	if _, seen := rl.hits[key]; !seen && len(rl.hits) >= rl.maxKeys {
		rl.sweep(now)
		if len(rl.hits) >= rl.maxKeys {
			rl.evictBatch(rl.maxKeys - rl.maxKeys/10) // drop to ~90%, see evictBatch
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

// evictBatch removes the oldest-seen keys (by most-recent hit) in a SINGLE pass
// until the map is back to target, rather than one-at-a-time. The old per-key
// scan was O(n) per evicted key, so a steady distinct-key flood at the cap pinned
// the mutex at ~O(n) per request; one batched O(n log n) pass amortizes that to
// ~O(1) per insertion (it runs once per ~maxKeys/10 new keys). Evicting the
// oldest-seen first drops the keys least likely to still be rate-limited. Caller
// holds rl.mu, and this runs only at the maxKeys backstop a healthy internal form
// never reaches. (issue #43)
func (rl *rateLimiter) evictBatch(target int) {
	if target < 0 {
		target = 0
	}
	drop := len(rl.hits) - target
	if drop <= 0 {
		return
	}
	type keyAge struct {
		key  string
		last time.Time
	}
	ages := make([]keyAge, 0, len(rl.hits))
	for k, ts := range rl.hits {
		var last time.Time
		if len(ts) > 0 {
			last = ts[len(ts)-1]
		}
		ages = append(ages, keyAge{k, last})
	}
	sort.Slice(ages, func(i, j int) bool { return ages[i].last.Before(ages[j].last) })
	for i := 0; i < drop; i++ {
		delete(rl.hits, ages[i].key)
	}
}
