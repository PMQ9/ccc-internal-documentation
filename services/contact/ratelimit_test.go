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
