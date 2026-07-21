package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The person picker endpoint (P2.5 W5): org-member scoped — space admins
// operate the grants panel without being org admins, so this is the one
// member-visible route of the administration package. Spec §2.6 rows.

func TestMemberSearch_EndpointMatrix(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memTok := ts.tokenFor(t, member.ID, member.Email)
	path := fmt.Sprintf("/api/v1/orgs/%s/members/search?q=%s", ts.OrgID, member.Email[:8])

	// No credentials → 401.
	require.Equal(t, http.StatusUnauthorized, ts.getAs(t, "", path).StatusCode)

	// A member of another org → 404 (no membership, existence not leaked).
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	stranger := testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
	strTok := ts.tokenFor(t, stranger.ID, stranger.Email)
	require.Equal(t, http.StatusNotFound, ts.getAs(t, strTok, path).StatusCode)

	// A plain member CAN search (this is deliberately not admin-gated).
	r := ts.getAs(t, memTok, path)
	require.Equal(t, http.StatusOK, r.StatusCode, "member search: %s", r.Body)
	var refs []struct {
		ID          uuid.UUID `json:"id"`
		Email       string    `json:"email"`
		DisplayName string    `json:"display_name"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &refs))
	require.Len(t, refs, 1, "the query prefix matches exactly the seeded member")
	require.Equal(t, member.ID, refs[0].ID)
	requireAdminSnakeCase(t, r.Body)
}

func TestMemberSearch_MatchesNameAndEmail_ExcludesDeactivated(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	// Give the target a distinctive display name.
	_, err := ts.DB.Pool.Exec(t.Context(),
		`UPDATE users SET display_name = 'Zarathustra Quimby' WHERE id = $1`, target.ID)
	require.NoError(t, err)

	search := func(q string) []json.RawMessage {
		t.Helper()
		r := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/members/search?q=%s", ts.OrgID, q), true)
		require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
		var refs []json.RawMessage
		require.NoError(t, json.Unmarshal(r.Body, &refs))
		return refs
	}

	// Case-insensitive name match and email match both find them.
	require.Len(t, search("zarathustra"), 1)
	require.Len(t, search(target.Email[:10]), 1)
	// A non-matching query finds nothing.
	require.Empty(t, search("no-such-person-here"))

	// Deactivated members leave the picker (they cannot be granted anything
	// they could use).
	dr := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusNoContent, dr.StatusCode)
	require.Empty(t, search("zarathustra"), "deactivated members must not appear in the picker")
}
