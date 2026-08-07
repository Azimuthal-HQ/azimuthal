package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// B1 per-session revocation: logout revokes THIS session, and only this one.
//
// WHAT CHANGED FROM THE v0.4.1 TRUST PATCH. That patch made logout revoke by
// bumping token_generation — the only lever available before this track, and a
// sledgehammer: it signed the user out on every device at once. B1 gives login
// a real sessions row and stamps its id into the token (`sid`); the middleware
// now refuses a token whose session is gone. Logout revokes the caller's OWN
// session row and stops bumping the generation, so a sign-out on one device
// leaves the others signed in. The org-wide behaviour moved to /auth/logout-all
// (TestAuthLogoutAll_* below).
//
// WHY THIS TEST IS SHAPED THIS WAY. The token is minted BEFORE the logout, via
// the real login endpoint, and the assertion is on the very NEXT request made
// with that same token. loginAs is used rather than ts.tokenFor because it goes
// through the production path that opens the session the logout then revokes.
func TestAuthLogout_RevokesTheToken(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	access, refresh := loginAs(t, ts, person.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, access),
		"premise: the token works before logout")

	generationBefore := tokenGeneration(t, ts, person.ID)

	r := ts.postAs(t, access, "/api/v1/auth/logout", nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "logout: %s", r.Body)

	// The whole point. The same token, on the request immediately after.
	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, access),
		"a token minted before logout must be refused on the next request")

	// And the MECHANISM, asserted directly so a future change cannot keep the
	// 401 by the wrong means: the generation is UNTOUCHED (single-device logout
	// is not the org-wide hammer), and the session row is revoked instead.
	require.Equal(t, generationBefore, tokenGeneration(t, ts, person.ID),
		"single-device logout must NOT move token_generation — that is logout-all's job")
	require.Equal(t, 1, revokedSessionCount(t, ts, person.ID),
		"logout must revoke exactly the caller's own session row")

	// The refresh token dies with it: it carries the same sid, and Handler.Refresh
	// re-reads the live session, so the revocation reaches it without extra work.
	// Asserted rather than assumed: a refresh path that ignored the session would
	// hand the whole session straight back.
	rr := ts.post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": refresh}, false)
	require.Equal(t, http.StatusUnauthorized, rr.StatusCode,
		"the refresh token issued with it must not survive logout: %s", rr.Body)

	// The account is untouched: this is a sign-out, not a lockout.
	var isActive bool
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT is_active FROM users WHERE id = $1`, person.ID).Scan(&isActive))
	require.True(t, isActive, "logout must not deactivate the account")

	fresh, _ := loginAs(t, ts, person.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, fresh),
		"signing in again after logout works")
}

// TestAuthLogout_RevokesOnlyTheCaller: the revocation is scoped to the caller's
// own session, taken from their verified claims. A logout that revoked more
// broadly — or that read a session id from anywhere the caller controls — would
// be a way to sign other people out.
func TestAuthLogout_RevokesOnlyTheCaller(t *testing.T) {
	ts := newTestServer(t)
	caller := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	bystander := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	callerTok, _ := loginAs(t, ts, caller.Email)
	bystanderTok, _ := loginAs(t, ts, bystander.Email)

	r := ts.postAs(t, callerTok, "/api/v1/auth/logout", nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "logout: %s", r.Body)

	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, callerTok))
	require.Equal(t, http.StatusOK, meWith(t, ts, bystanderTok),
		"one person's logout must not revoke another's session")
}

// revokedSessionCount returns how many of a user's session rows carry a
// revoked_at, so a test can assert the exact scope of a logout.
func revokedSessionCount(t *testing.T, ts *testServer, userID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NOT NULL`, userID).Scan(&n))
	return n
}

// TestAuthLogout_RefusesAnAlreadyRevokedToken pins the live-state check on this
// route: a token the middleware should already refuse does not get a 200
// "logged out" out of it.
//
// HONEST NOTE ON WHAT THIS DOES AND DOES NOT PROVE. It does not discriminate
// the route move, and it was measured rather than assumed: with /logout put
// back on the public mount it still passes, because the handler's own
// nil-claims branch answers 401 there for an unrelated reason.
//
// Two other tests do prove the move, and they are the ones to look at if this
// one starts failing for a reason that is not the live-state check:
// TestAuthLogoutIsAuthenticated in router_test.go, whose authenticated half
// fails the moment the route leaves the RequireAuth group; and
// TestReadPathSweep_GuardClassMatchesMiddleware, which since this change reads
// RequireAuth out of the real middleware chain and refuses a row that claims
// anything but `public` for a route without it. That sweep check did not exist
// when the defect shipped — which is exactly how a row saying `public` about
// an authenticated route survived every gate.
func TestAuthLogout_RefusesAnAlreadyRevokedToken(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	access, _ := loginAs(t, ts, person.Email)

	// An administrator signs them out from elsewhere first.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/force-logout", ts.OrgID, person.ID), nil, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "force-logout: %s", r.Body)

	r = ts.postAs(t, access, "/api/v1/auth/logout", nil)
	require.Equal(t, http.StatusUnauthorized, r.StatusCode,
		"logout with a token the middleware should already refuse: %s", r.Body)
}

func tokenGeneration(t *testing.T, ts *testServer, userID any) int {
	t.Helper()
	var generation int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT token_generation FROM users WHERE id = $1`, userID).Scan(&generation))
	return generation
}
