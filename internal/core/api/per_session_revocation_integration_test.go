package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// B1 per-session revocation, end to end against the wired router and real
// PostgreSQL. Sign-out used to be all-or-nothing: the only revocation lever was
// a token_generation bump, which killed every device at once. These tests pin
// the property that replaces it — a session is a row, and revoking one row
// leaves the others alone.

// TestPerSession_LogoutOnOneDeviceLeavesTheOtherSignedIn is the headline. Two
// live sessions for ONE user (two logins, two sids); logout on session A;
// A's next request 401s while B's still succeeds. Before B1 this was
// impossible — logout bumped the generation, so B would have died with A.
func TestPerSession_LogoutOnOneDeviceLeavesTheOtherSignedIn(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	// Two devices sign in independently. Each login opens its own session row.
	deviceA, _ := loginAs(t, ts, person.Email)
	deviceB, _ := loginAs(t, ts, person.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, deviceA), "premise: device A works")
	require.Equal(t, http.StatusOK, meWith(t, ts, deviceB), "premise: device B works")

	// Sign out on device A only.
	r := ts.postAs(t, deviceA, "/api/v1/auth/logout", nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "logout on A: %s", r.Body)

	// The whole point of the track, in two lines: A is dead, B is alive.
	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, deviceA),
		"device A's token must be refused after its own logout")
	require.Equal(t, http.StatusOK, meWith(t, ts, deviceB),
		"device B must stay signed in — a phone logout must not kill the desktop")

	// Exactly one of the two session rows is revoked.
	require.Equal(t, 1, revokedSessionCount(t, ts, person.ID),
		"logout must revoke exactly one of the two sessions")

	// The generation never moved — proof the mechanism is per-session, not the
	// old org-wide hammer.
	require.Equal(t, 0, tokenGeneration(t, ts, person.ID),
		"single-device logout must not bump token_generation")
}

// TestPerSession_RevokedSessionStaysRevoked: re-presenting the token after
// logout keeps failing. A revocation that a retry could undo would be no
// revocation at all.
func TestPerSession_RevokedSessionStaysRevoked(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	access, _ := loginAs(t, ts, person.Email)
	require.Equal(t, http.StatusOK, ts.postAs(t, access, "/api/v1/auth/logout", nil).StatusCode)

	// Three tries, all refused — the revoked_at is permanent, not a one-shot.
	for i := 0; i < 3; i++ {
		require.Equal(t, http.StatusUnauthorized, meWith(t, ts, access),
			"a revoked session must stay revoked on every subsequent request")
	}
}

// TestPerSession_ExpiredSessionRejected: a token whose session row has passed
// its expires_at is refused, exactly as a revoked one is. The JWT itself is
// still within its own exp — it is the session that has aged out.
func TestPerSession_ExpiredSessionRejected(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	access, _ := loginAs(t, ts, person.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, access), "premise: token works before expiry")

	// Age the session out from under the still-valid JWT. Login created exactly
	// one session for this user.
	_, err := ts.DB.Pool.Exec(t.Context(),
		`UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE user_id = $1 AND revoked_at IS NULL`,
		person.ID)
	require.NoError(t, err)

	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, access),
		"a token whose session has expired must be refused")
}

// TestPerSession_LogoutAllKillsEveryDevice: logout-all is the org-wide hammer
// plain logout used to be. Both sessions die, and the generation is bumped —
// so even a session whose row logout-all somehow missed would still be refused
// by the generation gate.
func TestPerSession_LogoutAllKillsEveryDevice(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	deviceA, refreshA := loginAs(t, ts, person.Email)
	deviceB, _ := loginAs(t, ts, person.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, deviceA))
	require.Equal(t, http.StatusOK, meWith(t, ts, deviceB))

	genBefore := tokenGeneration(t, ts, person.ID)

	r := ts.postAs(t, deviceA, "/api/v1/auth/logout-all", nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "logout-all: %s", r.Body)

	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, deviceA),
		"logout-all must kill the device that called it")
	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, deviceB),
		"logout-all must kill every other device too")

	require.Equal(t, genBefore+1, tokenGeneration(t, ts, person.ID),
		"logout-all must bump token_generation")

	// The refresh token dies with the rest.
	rr := ts.post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": refreshA}, false)
	require.Equal(t, http.StatusUnauthorized, rr.StatusCode,
		"logout-all must revoke refresh tokens too: %s", rr.Body)

	// The account stays active — this is a sign-out, not a lockout.
	fresh, _ := loginAs(t, ts, person.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, fresh),
		"signing in again after logout-all works")
}

// TestPerSession_LogoutAllRequiresAuth: logout-all sits behind RequireAuth, the
// same as logout. An anonymous caller is refused; a valid bearer is let through.
// Mirrors TestAuthLogoutIsAuthenticated for the new route so the sweep's
// user-scoped classification is backed by both directions.
func TestPerSession_LogoutAllRequiresAuth(t *testing.T) {
	ts := newTestServer(t)

	r := ts.post(t, "/api/v1/auth/logout-all", nil, false)
	require.Equal(t, http.StatusUnauthorized, r.StatusCode,
		"anonymous logout-all must be refused")

	r = ts.postAs(t, ts.Token, "/api/v1/auth/logout-all", nil)
	require.Equal(t, http.StatusOK, r.StatusCode,
		"an authenticated logout-all must be let through: %s", r.Body)
}

// TestPerSession_DeactivationRevokesIndependentOfSessions: the generation bump
// still works on its own. A deactivation refuses the token by the generation
// gate even though the session arm would also catch it — the two are
// independent levers, and this pins that the generation one did not quietly
// become dependent on the session one.
func TestPerSession_DeactivationRevokesIndependentOfSessions(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "admin")

	tok, _ := loginAs(t, ts, target.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, tok))

	// Deactivate keeps the generation bump (and revokes sessions in the same tx).
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "deactivate: %s", r.Body)

	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, tok),
		"deactivation must refuse the token")
	require.GreaterOrEqual(t, tokenGeneration(t, ts, target.ID), 1,
		"deactivation must still bump token_generation")
}
