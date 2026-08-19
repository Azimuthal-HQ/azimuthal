package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// These exercise the rate-limit middleware directly (white-box, no database):
// the token bucket's own arithmetic is covered in internal/core/auth, so here
// the subject is the HTTP contract — the 429, the Retry-After, the empty-ish
// body, and that the bucket key is (class, IP) so neither dimension bleeds.

// okHandler is the downstream a passing request should reach.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// fire sends one request through the middleware from the given IP and returns
// the recorder.
func fire(h http.Handler, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = ip + ":54321"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestRateLimit_BurstThen429WithRetryAfter is the core HTTP contract: within the
// burst every request reaches the handler (200), and the one past it is refused
// with 429, a positive integer Retry-After, and a body of no more than the
// status phrase.
func TestRateLimit_BurstThen429WithRetryAfter(t *testing.T) {
	rl := newRateLimit(RateLimitConfig{Enabled: true, PerMinute: 30, Burst: 3})
	h := rl.For(RateClassLogin)(okHandler())

	for i := 0; i < 3; i++ {
		rr := fire(h, "203.0.113.7")
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d within burst: status %d, want 200", i+1, rr.Code)
		}
	}

	rr := fire(h, "203.0.113.7")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("request past the burst: status %d, want 429", rr.Code)
	}
	ra := rr.Header().Get("Retry-After")
	secs, err := strconv.Atoi(ra)
	if err != nil || secs < 1 {
		t.Fatalf("Retry-After = %q, want a positive integer number of seconds", ra)
	}
	if body := rr.Body.String(); body != http.StatusText(http.StatusTooManyRequests) {
		t.Errorf("429 body = %q, want no more than the status phrase %q",
			body, http.StatusText(http.StatusTooManyRequests))
	}
}

// TestRateLimit_UnderLimitPasses: a handful of requests below the burst all
// reach the handler and carry no Retry-After. This is the "check still asserts
// something if the limit were removed" partner to the burst test — deleting the
// limiter must not change THIS outcome, so on its own it proves nothing; it
// exists to pin that legitimate traffic is untouched.
func TestRateLimit_UnderLimitPasses(t *testing.T) {
	rl := newRateLimit(RateLimitConfig{Enabled: true, PerMinute: 30, Burst: 10})
	h := rl.For(RateClassLogin)(okHandler())

	for i := 0; i < 5; i++ {
		rr := fire(h, "203.0.113.8")
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d under the limit was refused: %d", i+1, rr.Code)
		}
		if ra := rr.Header().Get("Retry-After"); ra != "" {
			t.Errorf("an allowed request must not carry Retry-After, got %q", ra)
		}
	}
}

// TestRateLimit_TwoIPsDoNotShareABucket: exhausting one client IP leaves another
// untouched. Delete the IP from the bucket key and this fails.
func TestRateLimit_TwoIPsDoNotShareABucket(t *testing.T) {
	rl := newRateLimit(RateLimitConfig{Enabled: true, PerMinute: 30, Burst: 2})
	h := rl.For(RateClassLogin)(okHandler())

	// Drain IP A.
	fire(h, "198.51.100.1")
	fire(h, "198.51.100.1")
	if rr := fire(h, "198.51.100.1"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("IP A should be exhausted, got %d", rr.Code)
	}

	// IP B has its own full bucket.
	for i := 0; i < 2; i++ {
		if rr := fire(h, "198.51.100.2"); rr.Code != http.StatusOK {
			t.Fatalf("IP B request %d refused — buckets leak across IPs (%d)", i+1, rr.Code)
		}
	}
}

// TestRateLimit_TwoClassesDoNotShareABucket: the same IP hitting two different
// route classes draws on two different buckets. Fold the class out of the key
// and this fails.
func TestRateLimit_TwoClassesDoNotShareABucket(t *testing.T) {
	rl := newRateLimit(RateLimitConfig{Enabled: true, PerMinute: 30, Burst: 2})
	login := rl.For(RateClassLogin)(okHandler())
	invite := rl.For(RateClassInviteAccept)(okHandler())

	// Drain the login class for this IP.
	fire(login, "203.0.113.9")
	fire(login, "203.0.113.9")
	if rr := fire(login, "203.0.113.9"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("login class should be exhausted for this IP, got %d", rr.Code)
	}

	// The invite-accept class for the SAME IP is unaffected.
	for i := 0; i < 2; i++ {
		if rr := fire(invite, "203.0.113.9"); rr.Code != http.StatusOK {
			t.Fatalf("invite class request %d refused — classes share a bucket (%d)", i+1, rr.Code)
		}
	}
}

// TestRateLimit_DisabledIsAPassThrough: with Enabled false the middleware never
// refuses, whatever the volume, and adds no headers. This is the harness's
// resting state, so it must be genuinely inert.
func TestRateLimit_DisabledIsAPassThrough(t *testing.T) {
	rl := newRateLimit(RateLimitConfig{Enabled: false, PerMinute: 1, Burst: 1})
	h := rl.For(RateClassLogin)(okHandler())

	for i := 0; i < 20; i++ {
		rr := fire(h, "203.0.113.10")
		if rr.Code != http.StatusOK {
			t.Fatalf("disabled limiter refused request %d (%d)", i+1, rr.Code)
		}
	}
}

// TestRateLimit_LimitIsConfigDriven runs the knob as a table: the number of
// requests admitted before the first 429 equals the configured Burst.
func TestRateLimit_LimitIsConfigDriven(t *testing.T) {
	for _, burst := range []int{1, 2, 5, 10} {
		rl := newRateLimit(RateLimitConfig{Enabled: true, PerMinute: 30, Burst: burst})
		h := rl.For(RateClassLogin)(okHandler())

		admitted := 0
		for i := 0; i < burst+3; i++ {
			if fire(h, "203.0.113.11").Code == http.StatusOK {
				admitted++
			}
		}
		if admitted != burst {
			t.Errorf("Burst=%d admitted %d requests before refusing, want exactly %d",
				burst, admitted, burst)
		}
	}
}
