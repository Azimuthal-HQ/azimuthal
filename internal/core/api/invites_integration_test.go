package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// P2.5 W2 invites. Failure mode 1: the raw token is generated with
// crypto/rand, returned once, and never persisted — a database leak must
// not yield usable invites. Failure mode 6: accepting an invite for an
// email that already has an account adds a membership; never a second
// user, never a second org.

type createdInvite struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	OrgRole   string    `json:"org_role"`
	InviteURL string    `json:"invite_url"`
}

type inviteOutcome struct {
	Email  string         `json:"email"`
	Status string         `json:"status"`
	Invite *createdInvite `json:"invite"`
}

// createInvite invites one email and returns the outcome row.
func createInvite(t *testing.T, ts *testServer, email string, extra map[string]any) inviteOutcome {
	t.Helper()
	body := map[string]any{"emails": []string{email}}
	for k, v := range extra {
		body[k] = v
	}
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID), body, true)
	require.Contains(t, []int{http.StatusCreated, http.StatusOK}, r.StatusCode, "create invite: %s", r.Body)
	var outcomes []inviteOutcome
	require.NoError(t, json.Unmarshal(r.Body, &outcomes))
	require.Len(t, outcomes, 1)
	return outcomes[0]
}

// rawTokenFromURL extracts the raw token from an invite URL.
func rawTokenFromURL(t *testing.T, inviteURL string) string {
	t.Helper()
	i := strings.LastIndex(inviteURL, "/")
	require.Positive(t, i, "invite_url must contain the token path segment: %s", inviteURL)
	return inviteURL[i+1:]
}

func TestInvites_TokenHashedAtRest_RawNeverPersisted(t *testing.T) {
	ts := newTestServer(t)
	out := createInvite(t, ts, "hashed-at-rest@example.com", nil)
	require.Equal(t, "created", out.Status)
	raw := rawTokenFromURL(t, out.Invite.InviteURL)
	require.NotEmpty(t, raw)

	var stored string
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT token_hash FROM invites WHERE id = $1`, out.Invite.ID).Scan(&stored))

	// The stored value is the SHA-256 of the raw token — and therefore not
	// the raw token itself. Holding the database contents does not let you
	// accept the invite.
	sum := sha256.Sum256([]byte(raw))
	require.Equal(t, hex.EncodeToString(sum[:]), stored, "token_hash must be SHA-256(raw)")
	require.NotEqual(t, raw, stored, "the raw token must never be persisted")

	// And nothing else on the row leaks it either: the only token-bearing
	// column is token_hash (schema fact), asserted above.
}

func TestInvites_AcceptNewEmail_CreatesAccountAndMembership(t *testing.T) {
	ts := newTestServer(t)
	email := "fresh-face@example.com"
	out := createInvite(t, ts, email, nil)
	raw := rawTokenFromURL(t, out.Invite.InviteURL)

	// The acceptance page can inspect the invite with the raw token.
	r := ts.get(t, "/api/v1/invites/"+raw, false)
	require.Equal(t, http.StatusOK, r.StatusCode, "inspect: %s", r.Body)
	var insp struct {
		Email    string `json:"email"`
		State    string `json:"state"`
		Existing bool   `json:"existing_account"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &insp))
	require.Equal(t, email, insp.Email)
	require.Equal(t, "active", insp.State)
	require.False(t, insp.Existing)

	r = ts.post(t, "/api/v1/invites/accept", map[string]string{
		"token": raw, "display_name": "Fresh Face", "password": "a-strong-password",
	}, false)
	require.Equal(t, http.StatusOK, r.StatusCode, "accept: %s", r.Body)
	var acc struct {
		Status          string    `json:"status"`
		ExistingAccount bool      `json:"existing_account"`
		OrgID           uuid.UUID `json:"org_id"`
		AccessToken     string    `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &acc))
	require.Equal(t, "joined", acc.Status)
	require.False(t, acc.ExistingAccount)
	require.Equal(t, ts.OrgID, acc.OrgID)
	require.NotEmpty(t, acc.AccessToken, "a fresh account is auto-signed-in")

	// The minted token works against the API.
	require.Equal(t, http.StatusOK, ts.getAs(t, acc.AccessToken, "/api/v1/auth/me").StatusCode)

	// Membership with the invite's role, enrolled in the default team as
	// primary (ADR-0006: never teamless).
	var role string
	var userID uuid.UUID
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT m.role, m.user_id FROM memberships m JOIN users u ON u.id = m.user_id
		 WHERE m.org_id = $1 AND u.email = $2`, ts.OrgID, email).Scan(&role, &userID))
	require.Equal(t, "member", role)
	var isPrimary bool
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT tm.is_primary FROM team_members tm JOIN teams tt ON tt.id = tm.team_id
		 WHERE tm.user_id = $1 AND tm.org_id = $2 AND tt.is_default`, userID, ts.OrgID).Scan(&isPrimary))
	require.True(t, isPrimary, "invite acceptance must enrol the default team as primary")

	// The invite is consumed: a second accept fails with 410.
	r = ts.post(t, "/api/v1/invites/accept", map[string]string{
		"token": raw, "display_name": "Fresh Face", "password": "a-strong-password",
	}, false)
	require.Equal(t, http.StatusGone, r.StatusCode, "double-accept must 410: %s", r.Body)

	// A fresh login with the chosen password works.
	r = ts.post(t, "/api/v1/auth/login", map[string]string{"email": email, "password": "a-strong-password"}, false)
	require.Equal(t, http.StatusOK, r.StatusCode, "login after acceptance: %s", r.Body)
}

func TestInvites_AcceptExistingAccount_AddsMembershipOnly(t *testing.T) {
	ts := newTestServer(t)

	// An account that already exists — in a DIFFERENT org.
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	existing := testutil.CreateTestUserWithRole(t, ts.DB.Pool, otherOrg.ID, "member")

	var usersBefore, orgsBefore int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&usersBefore))
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(), `SELECT count(*) FROM organizations`).Scan(&orgsBefore))

	out := createInvite(t, ts, existing.Email, map[string]any{"org_role": "admin"})
	require.Equal(t, "created", out.Status)
	raw := rawTokenFromURL(t, out.Invite.InviteURL)

	// Inspection reports the existing account so the page asks to confirm
	// joining rather than to register.
	r := ts.get(t, "/api/v1/invites/"+raw, false)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var insp struct {
		Existing bool `json:"existing_account"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &insp))
	require.True(t, insp.Existing)

	// Accept with no registration fields.
	r = ts.post(t, "/api/v1/invites/accept", map[string]string{"token": raw}, false)
	require.Equal(t, http.StatusOK, r.StatusCode, "accept for existing account: %s", r.Body)
	var acc struct {
		ExistingAccount bool      `json:"existing_account"`
		AccessToken     string    `json:"access_token"`
		UserID          uuid.UUID `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &acc))
	require.True(t, acc.ExistingAccount)
	require.Empty(t, acc.AccessToken, "no tokens for an existing account — they sign in with their own password")
	require.Equal(t, existing.ID, acc.UserID, "the membership must attach to the EXISTING account")

	// Failure mode 6 core: no second user, no second org.
	var usersAfter, orgsAfter int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&usersAfter))
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(), `SELECT count(*) FROM organizations`).Scan(&orgsAfter))
	require.Equal(t, usersBefore, usersAfter, "acceptance for an existing email must not create a user")
	require.Equal(t, orgsBefore, orgsAfter, "acceptance must never create an org")

	// Exactly one account holds the email, and it now has the membership at
	// the invite's role.
	var emailAccounts int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, existing.Email).Scan(&emailAccounts))
	require.Equal(t, 1, emailAccounts)
	var role string
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT role FROM memberships WHERE org_id = $1 AND user_id = $2`, ts.OrgID, existing.ID).Scan(&role))
	require.Equal(t, "admin", role)

	// The existing account can use the new org after a normal login.
	tok, _ := loginAs(t, ts, existing.Email)
	require.Equal(t, http.StatusOK,
		ts.getAs(t, tok, fmt.Sprintf("/api/v1/orgs/%s/", ts.OrgID)).StatusCode,
		"the joined org is reachable for the existing account")
}

func TestInvites_RevokedAndExpiredCannotBeAccepted(t *testing.T) {
	ts := newTestServer(t)

	// Revoked.
	out := createInvite(t, ts, "revoke-me@example.com", nil)
	raw := rawTokenFromURL(t, out.Invite.InviteURL)
	r := ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/invites/%s", ts.OrgID, out.Invite.ID), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "revoke: %s", r.Body)
	r = ts.post(t, "/api/v1/invites/accept", map[string]string{
		"token": raw, "display_name": "X", "password": "long-enough-pass",
	}, false)
	require.Equal(t, http.StatusGone, r.StatusCode, "revoked invite must 410: %s", r.Body)

	// Expired (window moved into the past — no sweeper involved).
	out = createInvite(t, ts, "expire-me@example.com", nil)
	raw = rawTokenFromURL(t, out.Invite.InviteURL)
	_, err := ts.DB.Pool.Exec(t.Context(),
		`UPDATE invites SET expires_at = now() - interval '1 minute' WHERE id = $1`, out.Invite.ID)
	require.NoError(t, err)
	r = ts.post(t, "/api/v1/invites/accept", map[string]string{
		"token": raw, "display_name": "X", "password": "long-enough-pass",
	}, false)
	require.Equal(t, http.StatusGone, r.StatusCode, "expired invite must 410: %s", r.Body)

	// Neither produced a user or membership.
	var count int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM users WHERE email IN ('revoke-me@example.com','expire-me@example.com')`).Scan(&count))
	require.Zero(t, count, "dead invites must create nothing")
}

func TestInvites_ResendRotatesToken_OldLinkDies(t *testing.T) {
	ts := newTestServer(t)
	out := createInvite(t, ts, "resend-me@example.com", nil)
	oldRaw := rawTokenFromURL(t, out.Invite.InviteURL)

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/invites/%s/resend", ts.OrgID, out.Invite.ID), nil, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "resend: %s", r.Body)
	var resent createdInvite
	require.NoError(t, json.Unmarshal(r.Body, &resent))
	newRaw := rawTokenFromURL(t, resent.InviteURL)
	require.NotEqual(t, oldRaw, newRaw, "resend must rotate the token")

	// The old link is dead the moment resend commits.
	require.Equal(t, http.StatusNotFound, ts.get(t, "/api/v1/invites/"+oldRaw, false).StatusCode,
		"the pre-resend token must stop working")
	require.Equal(t, http.StatusOK, ts.get(t, "/api/v1/invites/"+newRaw, false).StatusCode)
}

func TestInvites_DuplicateAndMemberOutcomes(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	// Bulk request mixing a fresh email, an existing member, an invalid
	// address, and (below) a duplicate — per-email outcomes, not a batch
	// failure: invite creation is admin convenience, not an access change.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID), map[string]any{
		"emails": []string{"bulk-fresh@example.com", member.Email, "not-an-email"},
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "bulk create: %s", r.Body)
	var outcomes []inviteOutcome
	require.NoError(t, json.Unmarshal(r.Body, &outcomes))
	require.Len(t, outcomes, 3)
	require.Equal(t, "created", outcomes[0].Status)
	require.Equal(t, "already_member", outcomes[1].Status)
	require.Equal(t, "invalid_email", outcomes[2].Status)

	// A second invite for an actively invited email reports already_invited
	// (case-insensitively — the DB index backstops the race).
	out := createInvite(t, ts, "BULK-FRESH@example.com", nil)
	require.Equal(t, "already_invited", out.Status)
}

func TestInvites_AdminSurface404ForNonAdmins(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memTok := ts.tokenFor(t, member.ID, member.Email)

	// The invite admin surface does not exist for non-admins: 404, not 403.
	r := ts.getAs(t, memTok, fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID))
	require.Equal(t, http.StatusNotFound, r.StatusCode, "member must see 404: %s", r.Body)
	r = ts.postAs(t, memTok, fmt.Sprintf("/api/v1/orgs/%s/invites", ts.OrgID),
		map[string]any{"emails": []string{"x@example.com"}})
	require.Equal(t, http.StatusNotFound, r.StatusCode)
}
