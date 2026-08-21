package auth

import (
	"testing"
	"time"
)

// fakeClock is a hand-cranked clock so the bucket's refill behaviour is tested
// by advancing time explicitly rather than by sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newFakeClock() *fakeClock               { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

// TestTokenBucket_BurstThenDenies: a fresh key admits exactly `burst` requests
// back-to-back, and the next one is refused with a positive Retry-After.
//
// This is the load-bearing assertion of the whole feature, and it fails if the
// bucket starts empty, starts over-full, or forgets to spend a token.
func TestTokenBucket_BurstThenDenies(t *testing.T) {
	clk := newFakeClock()
	l := newTokenBucketLimiterWithClock(30, 5, clk.now)

	for i := 0; i < 5; i++ {
		ok, retry := l.Allow("k")
		if !ok {
			t.Fatalf("request %d within burst was denied", i+1)
		}
		if retry != 0 {
			t.Fatalf("request %d allowed but reported Retry-After %v", i+1, retry)
		}
	}

	ok, retry := l.Allow("k")
	if ok {
		t.Fatal("the request past the burst was allowed")
	}
	if retry <= 0 {
		t.Fatalf("a denied request must carry a positive Retry-After, got %v", retry)
	}
}

// TestTokenBucket_RefillsOverTime: after exhaustion the key is refused, and once
// enough time has passed for a token to accrue it is admitted again.
//
// Deleting the refill line in Allow leaves the key refused forever, which this
// catches.
func TestTokenBucket_RefillsOverTime(t *testing.T) {
	clk := newFakeClock()
	l := newTokenBucketLimiterWithClock(60, 1, clk.now) // 1 token/sec, burst 1

	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("first request should be allowed from a full bucket")
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("second immediate request should be denied (burst is 1)")
	}

	clk.advance(1100 * time.Millisecond) // one token's worth, plus slack
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("after a full second a refilled token should admit the request")
	}
}

// TestTokenBucket_RetryAfterTracksRate: the reported wait to the next token is
// 1/rate, so a slower configured rate produces a longer Retry-After.
func TestTokenBucket_RetryAfterTracksRate(t *testing.T) {
	for _, tc := range []struct {
		perMinute int
		wantSec   float64
	}{
		{60, 1.0},  // 1 token/sec  -> ~1s
		{30, 2.0},  // 0.5 token/sec -> ~2s
		{120, 0.5}, // 2 token/sec  -> ~0.5s
	} {
		clk := newFakeClock()
		l := newTokenBucketLimiterWithClock(tc.perMinute, 1, clk.now)
		l.Allow("k") // drain the single token
		ok, retry := l.Allow("k")
		if ok {
			t.Fatalf("perMinute=%d: expected the second request to be denied", tc.perMinute)
		}
		got := retry.Seconds()
		if got < tc.wantSec*0.9 || got > tc.wantSec*1.1 {
			t.Errorf("perMinute=%d: Retry-After %.3fs, want ~%.3fs", tc.perMinute, got, tc.wantSec)
		}
	}
}

// TestTokenBucket_KeysAreIndependent: exhausting one key does not spend another
// key's tokens. Two client IPs, or two route classes, must not share a bucket —
// this is that invariant at the bucket layer.
func TestTokenBucket_KeysAreIndependent(t *testing.T) {
	clk := newFakeClock()
	l := newTokenBucketLimiterWithClock(30, 3, clk.now)

	// Drain key A completely.
	for i := 0; i < 3; i++ {
		l.Allow("A")
	}
	if ok, _ := l.Allow("A"); ok {
		t.Fatal("key A should be exhausted")
	}

	// Key B still has its full burst.
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("B"); !ok {
			t.Fatalf("key B request %d denied — buckets are leaking across keys", i+1)
		}
	}
}

// TestTokenBucket_BurstIsConfigDriven: the number of immediately-admitted
// requests equals the configured burst, across a table of settings. This is the
// "limit is config-driven" case expressed at the bucket: change the knob, change
// the behaviour.
func TestTokenBucket_BurstIsConfigDriven(t *testing.T) {
	for _, burst := range []int{1, 2, 5, 10, 25} {
		clk := newFakeClock()
		l := newTokenBucketLimiterWithClock(30, burst, clk.now)

		admitted := 0
		for i := 0; i < burst+5; i++ {
			if ok, _ := l.Allow("k"); ok {
				admitted++
			}
		}
		if admitted != burst {
			t.Errorf("burst=%d: admitted %d back-to-back requests, want exactly %d", burst, admitted, burst)
		}
	}
}

// TestTokenBucket_PrunePreservesPartialState is the guard on the memory sweep:
// it must reclaim only buckets that have refilled to capacity, because those
// recreate identically. A bucket only part-way refilled must survive the sweep
// with its partial state, or pruning would hand an attacker free tokens by
// simply pausing past the prune interval.
//
// perMinute=1, burst=10 means a full refill takes 600s — longer than the 5-min
// prune interval — so the sweep runs while the drained bucket is only partly
// back.
func TestTokenBucket_PrunePreservesPartialState(t *testing.T) {
	clk := newFakeClock()
	l := newTokenBucketLimiterWithClock(1, 10, clk.now) // 1/min, full refill = 600s

	for i := 0; i < 10; i++ {
		l.Allow("k") // drain
	}

	// 6 minutes: past the 5-min prune interval, but only ~6 tokens refilled.
	clk.advance(6 * time.Minute)
	ok, _ := l.Allow("k") // triggers the prune, then spends one of the ~6 tokens
	if !ok {
		t.Fatal("after 6 minutes at least one token should have refilled")
	}

	// If the prune had wrongly dropped this partly-refilled bucket, the key
	// would have reset to the full burst of 10. Prove it did not: only ~5 tokens
	// remain (6 refilled, 1 just spent), so it must run dry well before 10 more.
	admitted := 1
	for i := 0; i < 10; i++ {
		if ok, _ := l.Allow("k"); ok {
			admitted++
		}
	}
	if admitted >= 10 {
		t.Errorf("prune reset a partly-refilled bucket to full: admitted %d, expected ~6", admitted)
	}
}
