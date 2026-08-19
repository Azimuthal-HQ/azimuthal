package auth

import (
	"sync"
	"time"
)

// RateLimiter decides whether a keyed request may proceed now, and when it may
// not, how long the caller should wait before a token is available again. The
// retryAfter is what the HTTP layer turns into a Retry-After header.
//
// This replaces the P2 stub (a bare Allow(key) bool that nothing implemented
// and nothing called). The concrete implementation is TokenBucketLimiter.
type RateLimiter interface {
	Allow(key string) (allowed bool, retryAfter time.Duration)
}

// bucket is one key's token reservoir. tokens is a float so a fractional refill
// accumulates across calls instead of being lost to truncation.
type bucket struct {
	tokens float64
	last   time.Time
}

// TokenBucketLimiter is an in-memory, per-key token bucket.
//
// PROCESS-LOCAL BY DESIGN. Azimuthal is a single binary with no shared cache —
// there is no Redis and none is planned for this — so the limiter's state lives
// in this process and nowhere else. A multi-replica deployment therefore shares
// nothing: each replica limits independently, and the effective limit is
// per-instance rather than per-cluster. That is honest for the scale this
// targets (one binary, one operator), and a load balancer that pins a client to
// a replica keeps it close to the configured number; a round-robin one loosens
// it by roughly the replica count. Documented rather than hidden.
//
// The zero value is not usable; construct with NewTokenBucketLimiter.
type TokenBucketLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	// rate is the sustained refill in tokens per second; burst is the reservoir
	// capacity (the largest instantaneous burst a fresh key is allowed).
	rate  float64
	burst float64

	// now is the clock, injectable so tests are deterministic rather than
	// sleeping. Production uses time.Now.
	now func() time.Time

	// lastPrune bounds how often the idle-bucket sweep runs; pruneInterval is
	// that bound. See Allow — a key that is never seen again would otherwise
	// leave its bucket in the map forever, so an attacker rotating source
	// addresses could grow it without limit.
	lastPrune     time.Time
	pruneInterval time.Duration
}

// NewTokenBucketLimiter builds a limiter that admits perMinute requests per key
// in the steady state, absorbing bursts up to burst. Both are validated by the
// config layer (both > 0 when rate limiting is enabled); a non-positive value
// here would make the limiter refuse everything or never refill, so callers
// outside config must pass sane values.
func NewTokenBucketLimiter(perMinute, burst int) *TokenBucketLimiter {
	return newTokenBucketLimiterWithClock(perMinute, burst, time.Now)
}

func newTokenBucketLimiterWithClock(perMinute, burst int, now func() time.Time) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		buckets:       make(map[string]*bucket),
		rate:          float64(perMinute) / 60.0,
		burst:         float64(burst),
		now:           now,
		lastPrune:     now(),
		pruneInterval: 5 * time.Minute,
	}
}

// Allow refills the key's bucket for the elapsed time, then spends one token if
// one is available. When none is, it reports how long until the next whole
// token arrives, which the caller returns as Retry-After.
func (l *TokenBucketLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.pruneLocked(now)

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
			b.last = now
		}
	}

	if b.tokens >= 1 {
		b.tokens -= 1
		return true, 0
	}

	// Not enough for a whole token: time until the reservoir reaches 1.
	wait := (1 - b.tokens) / l.rate
	return false, time.Duration(wait * float64(time.Second))
}

// pruneLocked drops buckets that have refilled to capacity. A full bucket
// recreated on its next access is identical to the one dropped, so this never
// changes a limiting decision — it only reclaims memory for keys that have gone
// quiet. It runs at most once per pruneInterval. Caller holds l.mu.
func (l *TokenBucketLimiter) pruneLocked(now time.Time) {
	if now.Sub(l.lastPrune) < l.pruneInterval {
		return
	}
	l.lastPrune = now
	for key, b := range l.buckets {
		refilled := min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
		if refilled >= l.burst {
			delete(l.buckets, key)
		}
	}
}
