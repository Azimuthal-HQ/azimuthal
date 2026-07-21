package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// getAs GETs a path with an arbitrary bearer token (persona-scoped variant
// of testServer.get).
func (ts *testServer) getAs(t *testing.T, token, path string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.url(path), nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return ts.do(t, req)
}

// P2.5 session control (failure mode 2): RS256 tokens are stateless, so
// without the token_generation check a deactivated user stays authenticated
// until token expiry. Every test here holds a token minted BEFORE the
// administrative action and asserts the very next request fails — a test
// that logs in after deactivation and watches login fail would prove
// nothing about that hole.

// loginAs signs in through the real login endpoint, returning the token the
// production path mints (with the user's CURRENT generation — tokenFor
// would mint generation 0, which is exactly what these tests must not do
// after a bump).
func loginAs(t *testing.T, ts *testServer, email string) (access, refresh string) {
	t.Helper()
	r := ts.post(t, "/api/v1/auth/login", map[string]string{
		"email": email, "password": "testpassword123",
	}, false)
	require.Equal(t, http.StatusOK, r.StatusCode, "login as %s: %s", email, r.Body)
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body))
	return body.AccessToken, body.RefreshToken
}

// meWith GETs /auth/me with the given token.
func meWith(t *testing.T, ts *testServer, token string) int {
	t.Helper()
	return ts.getAs(t, token, "/api/v1/auth/me").StatusCode
}

func TestSessionControl_DeactivatedUsersNextRequestFails(t *testing.T) {
	ts := newTestServer(t)
	// A second admin so last-admin protection does not block the scenario.
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "admin")

	// Token minted BEFORE deactivation, via the real login path.
	tok, _ := loginAs(t, ts, target.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, tok), "premise: token works before deactivation")

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "deactivate: %s", r.Body)

	// The very next request with the pre-deactivation token fails.
	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, tok),
		"a token minted before deactivation must die on the next request")

	// Sign-in is blocked too.
	r = ts.post(t, "/api/v1/auth/login", map[string]string{
		"email": target.Email, "password": "testpassword123",
	}, false)
	require.Equal(t, http.StatusUnauthorized, r.StatusCode, "deactivated login must 401")

	// Reactivation restores sign-in — but never the old tokens.
	r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/reactivate", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "reactivate: %s", r.Body)
	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, tok),
		"reactivation must not resurrect tokens minted before deactivation")
	tok2, _ := loginAs(t, ts, target.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, tok2), "fresh login after reactivation works")
}

func TestSessionControl_ForceLogoutStandalone_UserStaysActive(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	tok, _ := loginAs(t, ts, target.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, tok))

	// Force logout is a standalone action on an ACTIVE user.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/force-logout", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "force-logout: %s", r.Body)

	// Every outstanding token dies instantly...
	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, tok),
		"force logout must kill tokens minted before it")

	// ...but the account stays active: they simply sign in again.
	var isActive bool
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT is_active FROM users WHERE id = $1`, target.ID).Scan(&isActive))
	require.True(t, isActive, "force logout must not deactivate the account")
	tok2, _ := loginAs(t, ts, target.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, tok2), "fresh login after force logout works")
}

func TestSessionControl_StaleGenerationClaimRejected(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	// Mint a token carrying generation 0, then move the column past it
	// directly — the middleware must reject the stale claim regardless of
	// which administrative path did the bump.
	tok := ts.tokenFor(t, target.ID, target.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, tok))

	_, err := ts.DB.Pool.Exec(t.Context(),
		`UPDATE users SET token_generation = token_generation + 1 WHERE id = $1`, target.ID)
	require.NoError(t, err)

	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, tok),
		"a stale token_generation claim must be rejected")
}

func TestSessionControl_PasswordChangeIncrementsGeneration(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	tok, _ := loginAs(t, ts, target.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, tok))

	var genBefore int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT token_generation FROM users WHERE id = $1`, target.ID).Scan(&genBefore))

	// Every password-change path flows through UpdateUserPasswordHash (the
	// admin CLI reset included); the generation bump rides in the statement.
	newHash := "bcrypt-hash-after-change"
	q := generated.New(ts.DB.Pool)
	require.NoError(t, q.UpdateUserPasswordHash(t.Context(), generated.UpdateUserPasswordHashParams{
		ID: target.ID, PasswordHash: &newHash,
	}))

	var genAfter int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT token_generation FROM users WHERE id = $1`, target.ID).Scan(&genAfter))
	require.Equal(t, genBefore+1, genAfter, "password change must increment token_generation")

	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, tok),
		"a password change must sign out other sessions")
}

func TestSessionControl_RefreshRevokedOnDeactivation(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "admin")

	_, refresh := loginAs(t, ts, target.Email)

	// Premise: the refresh token works before deactivation.
	r := ts.post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": refresh}, false)
	require.Equal(t, http.StatusOK, r.StatusCode, "premise: refresh works: %s", r.Body)

	dr := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusNoContent, dr.StatusCode, "deactivate: %s", dr.Body)

	// The refresh token minted before deactivation is dead.
	r = ts.post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": refresh}, false)
	require.Equal(t, http.StatusUnauthorized, r.StatusCode,
		"refresh must be revoked by deactivation, got %d: %s", r.StatusCode, r.Body)
}

func TestSessionControl_RefreshRejectsStaleGeneration(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	_, refresh := loginAs(t, ts, target.Email)

	fr := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/force-logout", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusNoContent, fr.StatusCode)

	r := ts.post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": refresh}, false)
	require.Equal(t, http.StatusUnauthorized, r.StatusCode,
		"a refresh token carrying a stale generation must be rejected")
}

func TestSessionControl_DBSessionsRevokedOnDeactivation(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "admin")

	// A DB cookie session for the cookie path (the other credential kind).
	_, err := ts.DB.Pool.Exec(t.Context(),
		`INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, 'sc-hash', now() + interval '1 day')`,
		target.ID)
	require.NoError(t, err)

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode)

	var revoked int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NOT NULL`, target.ID).Scan(&revoked))
	require.Equal(t, 1, revoked, "deactivation must revoke DB sessions in the same transaction")
}
