package main

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

var rlBase = time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)

// allow up to limit within the trailing window, deny over, reset once it elapses.
func TestRateLimiterWindow(t *testing.T) {
	rl := newRateLimiter(3, time.Hour)
	for i := 0; i < 3; i++ {
		if !rl.allow("k", rlBase.Add(time.Duration(i)*time.Minute)) {
			t.Fatalf("hit %d should be allowed", i+1)
		}
	}
	if rl.allow("k", rlBase.Add(4*time.Minute)) {
		t.Error("4th hit within the window should be denied")
	}
	if !rl.allow("k", rlBase.Add(61*time.Minute)) {
		t.Error("hit after the window elapsed should be allowed again")
	}
}

// The lazy sweep drops keys whose entire window has aged out, bounding the map to
// "keys seen in the last window" rather than "keys ever seen".
func TestRateLimiterSweepDropsExpiredKeys(t *testing.T) {
	rl := newRateLimiter(5, time.Hour)
	rl.allow("a", rlBase)
	rl.allow("b", rlBase.Add(time.Minute))
	if got := len(rl.hits); got != 2 {
		t.Fatalf("tracked keys = %d, want 2", got)
	}
	rl.allow("c", rlBase.Add(2*time.Hour)) // a full window later -> sweep fires
	if _, ok := rl.hits["a"]; ok {
		t.Error("key a should have been swept after its window elapsed")
	}
	if _, ok := rl.hits["b"]; ok {
		t.Error("key b should have been swept after its window elapsed")
	}
	if _, ok := rl.hits["c"]; !ok {
		t.Error("key c should still be tracked")
	}
}

// At the hard cap, a distinct-key flood (none aged out) stays bounded via the
// batch eviction rather than growing without limit. (issue #43)
func TestRateLimiterCapBatchEvicts(t *testing.T) {
	rl := newRateLimiter(1, time.Hour)
	rl.maxKeys = 10 // shrink so the eviction path is exercised cheaply
	for i := 0; i < 200; i++ {
		rl.allow("key-"+strconv.Itoa(i), rlBase.Add(time.Duration(i)*time.Nanosecond))
	}
	if len(rl.hits) > rl.maxKeys {
		t.Errorf("tracked keys = %d, must stay <= maxKeys %d under a distinct-key flood", len(rl.hits), rl.maxKeys)
	}
}

// evictBatch keeps the most-recent target keys and drops the oldest-seen in one pass.
func TestEvictBatchDropsOldest(t *testing.T) {
	rl := newRateLimiter(1, time.Hour)
	for i := 0; i < 10; i++ {
		rl.hits["key-"+strconv.Itoa(i)] = []time.Time{rlBase.Add(time.Duration(i) * time.Minute)}
	}
	rl.evictBatch(4)
	if len(rl.hits) != 4 {
		t.Fatalf("after evictBatch(4) size = %d, want 4", len(rl.hits))
	}
	if _, ok := rl.hits["key-0"]; ok {
		t.Error("oldest key (key-0) should have been evicted")
	}
	if _, ok := rl.hits["key-9"]; !ok {
		t.Error("newest key (key-9) should survive")
	}
}

// Concurrent access must be race-free (the shared limiters are used from many
// request goroutines). Meaningful under `go test -race`. (issue #43)
func TestRateLimiterConcurrent(t *testing.T) {
	rl := newRateLimiter(1000, time.Hour)
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rl.allow("shared", rlBase)                                // contend on one key
				rl.allow("g"+strconv.Itoa(g)+"-"+strconv.Itoa(i), rlBase) // and distinct keys
			}
		}(g)
	}
	wg.Wait()
}

func TestEvictBatchTargetZero(t *testing.T) {
	rl := newRateLimiter(1, time.Hour)
	for i := 0; i < 5; i++ {
		rl.hits["key-"+strconv.Itoa(i)] = []time.Time{rlBase.Add(time.Duration(i) * time.Minute)}
	}
	rl.evictBatch(0)
	if len(rl.hits) != 0 {
		t.Errorf("evictBatch(0) left %d keys, want 0", len(rl.hits))
	}
}

func TestEvictBatchTargetNegative(t *testing.T) {
	rl := newRateLimiter(1, time.Hour)
	for i := 0; i < 3; i++ {
		rl.hits["key-"+strconv.Itoa(i)] = []time.Time{rlBase}
	}
	rl.evictBatch(-5)
	if len(rl.hits) != 0 {
		t.Errorf("evictBatch(-5) left %d keys, want 0", len(rl.hits))
	}
}

func TestEvictBatchBelowTargetNoOp(t *testing.T) {
	rl := newRateLimiter(1, time.Hour)
	for i := 0; i < 3; i++ {
		rl.hits["key-"+strconv.Itoa(i)] = []time.Time{rlBase}
	}
	rl.evictBatch(10)
	if len(rl.hits) != 3 {
		t.Errorf("evictBatch(10) changed size to %d, want 3 (no-op when below target)", len(rl.hits))
	}
}

func TestRateLimiterKeyNotFound(t *testing.T) {
	rl := newRateLimiter(5, time.Hour)
	if !rl.allow("new-key", rlBase) {
		t.Error("first hit for a brand-new key should be allowed")
	}
}

func TestRateLimiterConcurrentBurst(t *testing.T) {
	rl := newRateLimiter(100, time.Hour)
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				rl.allow("burst", rlBase)
			}
		}()
	}
	wg.Wait()
	// At most 100 should have been allowed.
	if len(rl.hits["burst"]) > 100 {
		t.Errorf("burst key has %d hits, want <= 100", len(rl.hits["burst"]))
	}
}

func TestRateLimiterSweepDoesNotDeleteActiveKeys(t *testing.T) {
	rl := newRateLimiter(5, time.Hour)
	rl.allow("active", rlBase)
	rl.allow("active", rlBase.Add(30*time.Minute))
	rl.allow("active", rlBase.Add(55*time.Minute))
	rl.allow("new", rlBase.Add(90*time.Minute)) // triggers sweep
	if _, ok := rl.hits["active"]; !ok {
		t.Error("active key should survive a sweep")
	}
	if len(rl.hits["active"]) != 3 {
		t.Errorf("active key lost %d hits, want 3", len(rl.hits["active"]))
	}
}

func TestRateLimiterWindowReset(t *testing.T) {
	rl := newRateLimiter(2, time.Hour)
	if !rl.allow("k", rlBase) {
		t.Fatal("hit 1 should be allowed")
	}
	if !rl.allow("k", rlBase.Add(30*time.Minute)) {
		t.Fatal("hit 2 should be allowed")
	}
	if rl.allow("k", rlBase.Add(30*time.Minute)) {
		t.Fatal("hit 3 should be denied")
	}
	// After the window elapses, both hits drop and a new one should be allowed.
	if !rl.allow("k", rlBase.Add(61*time.Minute)) {
		t.Fatal("hit 4 after window should be allowed")
	}
}
