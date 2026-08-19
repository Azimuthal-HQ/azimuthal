package api

import (
	"io"
	"math"
	"net/http"
	"strconv"

	authapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// RateLimitConfig configures the auth-surface rate limiter.
//
// It is a VALUE field on RouterConfig, not a wired collaborator, and that is
// deliberate: the zero value (Enabled false) is a working "disabled", so the
// integration harness leaves it unset and every existing test is untouched,
// while TestHarness_NoDarkDependencies — which only inspects nil-able kinds —
// skips it. Production fills it in from internal/config; see cmd/server/main.go.
type RateLimitConfig struct {
	// Enabled turns the limiter on. When false, For returns a pass-through and
	// no bucket is ever consulted.
	Enabled bool
	// PerMinute is the sustained per-key refill; Burst is the reservoir. Both
	// are validated (> 0 when Enabled) at config Load, so a router built from a
	// loaded config cannot carry a limiter that refuses everything.
	PerMinute int
	Burst     int
}

// Rate-limit route classes. Each names one bucket dimension: a bucket is keyed
// by (class, client IP), so two classes never share a budget and two IPs never
// do. These cover the unauthenticated, auth-critical surface only — the API at
// large is deliberately NOT blanketed, so the P2 read-path query budget is
// untouched.
const (
	// RateClassLogin guards POST /api/v1/auth/login against credential stuffing.
	RateClassLogin = "login"
	// RateClassInviteAccept guards the public invite routes (inspect + accept)
	// against raw-token guessing.
	RateClassInviteAccept = "invite-accept"
	// RateClassPortalRequestLink guards the portal's request-link endpoint
	// against email-enumeration-by-timing and sign-in-link spam.
	RateClassPortalRequestLink = "portal-request-link"
	// RateClassPortalRedeem guards portal magic-link redemption against token
	// guessing — the portal's actual authentication step.
	RateClassPortalRedeem = "portal-redeem"
	// RateClassForgotPassword guards the internal-user credential-link
	// forgot-password endpoint (POST /api/v1/credential-links/forgot-password).
	// It is the email-send/enumeration vector — a caller naming addresses to
	// learn which ones receive a reset — so it is the internal analog of the
	// portal's request-link class.
	RateClassForgotPassword = "forgot-password"
	// RateClassCredentialRedeem guards the credential-link token-use endpoints
	// (POST /api/v1/credential-links/inspect and /consume) against raw-token
	// guessing; consume is the actual sign-in. It is the internal analog of the
	// portal's redeem class, kept distinct from it so an internal reset and an
	// external portal redemption from the same IP do not share a budget.
	RateClassCredentialRedeem = "credential-redeem"
)

// rateLimit turns a shared token-bucket limiter into per-class chi middleware.
// One limiter backs every class; the class is folded into the bucket key, so a
// single set of operator knobs governs all of them while keeping the buckets
// disjoint.
type rateLimit struct {
	limiter auth.RateLimiter // nil => disabled; For returns a pass-through
}

// newRateLimit builds the provider from config. A disabled config yields a
// provider whose For is the identity middleware, so callers wire it
// unconditionally and the router shape is the same on and off.
func newRateLimit(cfg RateLimitConfig) *rateLimit {
	if !cfg.Enabled {
		return &rateLimit{}
	}
	return &rateLimit{limiter: auth.NewTokenBucketLimiter(cfg.PerMinute, cfg.Burst)}
}

// For returns middleware that rate-limits its route under class, keyed by
// (class, client IP). The IP comes from authapi.ClientIP — the SAME extraction
// the audit trail records ip_address with — so there is one parser, not a second
// that could disagree with it about who the caller is.
//
// A refusal answers 429 with a Retry-After (seconds, rounded up, never below 1)
// and a body of no more than the status phrase. It deliberately says nothing
// about whether the credential, address, or account existed: the refusal is
// about rate, and leaking anything else here would hand an attacker the very
// oracle the limit exists to close.
func (rl *rateLimit) For(class string) func(http.Handler) http.Handler {
	if rl == nil || rl.limiter == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// NUL separates the class from the IP so no class name and no
			// address can collide into another bucket's key.
			key := class + "\x00" + authapi.ClientIP(r)
			ok, retryAfter := rl.limiter.Allow(key)
			if !ok {
				secs := int(math.Ceil(retryAfter.Seconds()))
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, http.StatusText(http.StatusTooManyRequests))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
