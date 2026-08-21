package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	credlinksapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/credlinks"
)

// These prove the limiter is actually WIRED to POST /api/v1/auth/login through
// the real NewRouter — not just that the middleware works in isolation. The
// middleware runs before the handler, so an empty-body login (which the handler
// answers 400 without ever touching its services) lets this run with no
// database: under the limit the request reaches the handler (400), over it the
// middleware refuses (429).
//
// This is also the guard on the harness's safety: with rate limiting DISABLED —
// the RouterConfig zero value the integration harness uses — the same volume of
// logins must never 429, or the whole existing suite would flake.

func loginRouter(t *testing.T, rl api.RateLimitConfig) http.Handler {
	t.Helper()
	// A handler with nil services is enough: Login validates the request body
	// and returns 400 for an empty one before it dereferences anything.
	return api.NewRouter(api.RouterConfig{
		AuthHandler: authapi.NewHandler(nil, nil, nil, nil, nil, nil),
		RateLimit:   rl,
	})
}

func postLogin(t *testing.T, h http.Handler, ip string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
	req.RemoteAddr = ip + ":40000"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

func TestRateLimit_LoginIsWiredThroughTheRouter(t *testing.T) {
	h := loginRouter(t, api.RateLimitConfig{Enabled: true, PerMinute: 30, Burst: 2})

	// Burst of 2 reaches the handler (400 empty body); the third is refused by
	// the middleware with 429.
	codes := []int{
		postLogin(t, h, "203.0.113.50"),
		postLogin(t, h, "203.0.113.50"),
		postLogin(t, h, "203.0.113.50"),
	}
	if codes[0] != http.StatusBadRequest || codes[1] != http.StatusBadRequest {
		t.Fatalf("within-burst logins should reach the handler (400 empty body), got %v", codes[:2])
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("the login past the burst should be rate-limited (429), got %d — "+
			"the limiter is not wired to /auth/login", codes[2])
	}
}

func TestRateLimit_DisabledRouterNeverLimitsLogin(t *testing.T) {
	// The RouterConfig zero value: what the integration harness runs.
	h := loginRouter(t, api.RateLimitConfig{})

	for i := 0; i < 25; i++ {
		if code := postLogin(t, h, "203.0.113.51"); code == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate-limited with the limiter disabled — the harness would flake", i+1)
		}
	}
}

// The D1 credential-link public surface (forgot-password / inspect / consume) is
// wired to the limiter the same way login is. These prove it through the real
// NewRouter, with no database: forgot-password answers 400 on an empty email and
// inspect answers 400 on a malformed body — both before any service is touched —
// so the middleware's 429 is what changes past the burst.

func credlinkRouter(t *testing.T, rl api.RateLimitConfig) http.Handler {
	t.Helper()
	// nil services are enough: the fail-fast 400s above are reached before any
	// dereference. A real AuthHandler is passed only because the /auth block
	// registers its method values unconditionally.
	return api.NewRouter(api.RouterConfig{
		AuthHandler:           authapi.NewHandler(nil, nil, nil, nil, nil, nil),
		CredentialLinkHandler: credlinksapi.NewHandler(nil, nil, nil, nil),
		RateLimit:             rl,
	})
}

func postCred(t *testing.T, h http.Handler, path, body, ip string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = ip + ":40000"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

// TestRateLimit_CredentialLinksAreWiredThroughTheRouter proves both credential-
// link classes are attached — and to SEPARATE buckets. forgot-password (request
// class) is exhausted first; the same IP then still gets its full burst on
// inspect (redeem class), which it would not if the two shared a bucket or the
// redeem class were unwired.
func TestRateLimit_CredentialLinksAreWiredThroughTheRouter(t *testing.T) {
	h := credlinkRouter(t, api.RateLimitConfig{Enabled: true, PerMinute: 30, Burst: 2})

	const ip = "203.0.113.60"
	fp := "/api/v1/credential-links/forgot-password"

	// Burst of 2 reaches the handler (400 empty email); the third is refused.
	if c := postCred(t, h, fp, `{}`, ip); c != http.StatusBadRequest {
		t.Fatalf("forgot-password #1 should reach the handler (400), got %d", c)
	}
	if c := postCred(t, h, fp, `{}`, ip); c != http.StatusBadRequest {
		t.Fatalf("forgot-password #2 should reach the handler (400), got %d", c)
	}
	if c := postCred(t, h, fp, `{}`, ip); c != http.StatusTooManyRequests {
		t.Fatalf("forgot-password past the burst should be 429, got %d — the request class is not wired", c)
	}

	// The SAME IP on inspect (redeem class) still has its own full burst — proof
	// the two classes are separate buckets, not one. A malformed body fails fast
	// at 400 before the nil service is reached.
	insp := "/api/v1/credential-links/inspect"
	for i := 0; i < 2; i++ {
		if c := postCred(t, h, insp, `{`, ip); c != http.StatusBadRequest {
			t.Fatalf("inspect #%d should reach the handler (400), got %d — "+
				"the redeem class is unwired or shares forgot-password's bucket", i+1, c)
		}
	}
	if c := postCred(t, h, insp, `{`, ip); c != http.StatusTooManyRequests {
		t.Fatalf("inspect past the burst should be 429, got %d — the redeem class is not wired", c)
	}
}

func TestRateLimit_DisabledRouterNeverLimitsCredentialLinks(t *testing.T) {
	h := credlinkRouter(t, api.RateLimitConfig{})

	fp := "/api/v1/credential-links/forgot-password"
	for i := 0; i < 25; i++ {
		if c := postCred(t, h, fp, `{}`, "203.0.113.61"); c == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate-limited with the limiter disabled — the harness would flake", i+1)
		}
	}
}
