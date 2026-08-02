package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The v0.4.1 trust patch, link 3: logout revokes.
//
// WHAT WAS BROKEN, IN TWO LAYERS.
//
// The routing layer: POST /api/v1/auth/logout was mounted by
// authapi.Handler.Routes(), which NewRouter mounts outside its RequireAuth
// group, and OptionalAuth is mounted nowhere in this router. Nothing therefore
// put claims on the request context at that path, `auth.ClaimsFromContext`
// returned nil for every caller, and Handler.Logout's first branch answered
// 401 — to a valid bearer token exactly as to an anonymous request. The
// endpoint could not be used.
//
// The revocation layer, underneath it: Handler.Logout deleted database session
// rows and nothing else. That would have revoked nothing even had the route
// been reachable. The SPA authenticates with a stateless RS256 bearer JWT
// which the auth middleware validates from its signature plus a single
// token_generation read — the sessions table is not consulted, and in fact
// production never writes a row to it. A token copied out of localStorage by
// script therefore outlived every sign-out, until its own expiry.
//
// WHY THIS TEST IS SHAPED THIS WAY. The token is minted BEFORE the logout, via
// the real login endpoint, and the assertion is on the very NEXT request made
// with that same token. A test that logged out and then tried to log in again,
// or that minted a fresh token afterwards, would pass against a server that
// still honoured every token it had ever issued — which is precisely the state
// being fixed. loginAs is used rather than ts.tokenFor for the same reason:
// tokenFor mints at generation 0, so it would not notice a bump.
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

	// And the mechanism, asserted directly, so a future change that keeps the
	// 401 by some other means (an expiry, a session lookup) does not read as
	// this behaviour still being present.
	require.Equal(t, generationBefore+1, tokenGeneration(t, ts, person.ID),
		"logout must move token_generation past what the issued token claims")

	// The refresh token dies with it. It is not a database row — it is a
	// stateless JWT carrying the same generation claim — and Handler.Refresh
	// re-reads the live state, so the bump reaches it without any extra work.
	// Asserted rather than assumed: a refresh path that still minted a new
	// access token would hand the whole session straight back.
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

// TestAuthLogout_RevokesOnlyTheCaller: the bump is scoped to the caller's own
// id, taken from their verified claims. A logout that revoked more broadly —
// or that read a user id from anywhere the caller controls — would be a way to
// sign other people out.
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
