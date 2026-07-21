package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// P2.5 W2 lifecycle paths beyond session control: primary team changes,
// role changes, removal, and their validation edges.

func TestPeople_ChangePrimaryTeam_EnrolsAndMoves(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	team, err := ts.TeamService.Create(t.Context(), ts.OrgID, nil, "pt-team", "Primary Target", "")
	require.NoError(t, err)

	// The target is not a member of the team yet — the admin change
	// auto-enrols them and marks it primary.
	r := ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID),
		map[string]string{"primary_team_id": team.ID.String()}, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "change primary team: %s", r.Body)

	var isPrimary bool
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT is_primary FROM team_members WHERE team_id = $1 AND user_id = $2`,
		team.ID, target.ID).Scan(&isPrimary))
	require.True(t, isPrimary, "the new team must be primary")

	// Exactly one primary per user per org — the default team lost the flag.
	var primaries int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM team_members WHERE user_id = $1 AND org_id = $2 AND is_primary`,
		target.ID, ts.OrgID).Scan(&primaries))
	require.Equal(t, 1, primaries)

	// The People list surfaces the move.
	pr := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/users", ts.OrgID), true)
	require.Equal(t, http.StatusOK, pr.StatusCode)
	require.Contains(t, string(pr.Body), `"primary_team_name":"Primary Target"`)
}

func TestPeople_ChangePrimaryTeam_RejectsForeignAndDeadTeams(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	// A team from another org is not a valid target — 400, whole change
	// rejected.
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	otherOwner := testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
	_ = otherOwner
	foreignTeam := testutil.DefaultTeamID(t, ts.DB.Pool, otherOrg.ID)
	r := ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID),
		map[string]string{"primary_team_id": foreignTeam.String()}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "foreign team must 400: %s", r.Body)

	// Empty body → validation error (provide something to change).
	r = ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID), map[string]string{}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode)

	// Unknown target user → 404.
	r = ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, "00000000-0000-0000-0000-00000000dead"),
		map[string]string{"org_role": "admin"}, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode)
}

func TestPeople_LifecycleValidationEdges(t *testing.T) {
	ts := newTestServer(t)
	target := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	// Reactivating an already-active account is a state conflict, surfaced
	// as such rather than silently succeeding.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/reactivate", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "reactivate active: %s", r.Body)

	// Promote then demote round-trips through the membership row.
	r = ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID),
		map[string]string{"org_role": "admin"}, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode)
	var role string
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT role FROM memberships WHERE org_id = $1 AND user_id = $2`, ts.OrgID, target.ID).Scan(&role))
	require.Equal(t, "admin", role)
	r = ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID),
		map[string]string{"org_role": "member"}, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode)

	// Removal drops membership, team rows, and grants in this org; the user
	// row survives.
	spaceID := createScopedSpace(t, ts, "Removal Space", "removal-space", "vector")
	gr := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants", ts.OrgID, spaceID),
		map[string]string{"subject_type": "user", "subject_id": target.ID.String(), "role": "viewer"}, true)
	require.Equal(t, http.StatusCreated, gr.StatusCode, "seed grant: %s", gr.Body)

	r = ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, target.ID), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "remove: %s", r.Body)

	var memberships, teamRows, grants, users int
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM memberships WHERE org_id = $1 AND user_id = $2`, ts.OrgID, target.ID).Scan(&memberships))
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM team_members WHERE org_id = $1 AND user_id = $2`, ts.OrgID, target.ID).Scan(&teamRows))
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM space_grants WHERE org_id = $1 AND subject_type = 'user' AND subject_id = $2`,
		ts.OrgID, target.ID).Scan(&grants))
	require.NoError(t, ts.DB.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NULL`, target.ID).Scan(&users))
	require.Zero(t, memberships, "membership must be gone")
	require.Zero(t, teamRows, "team memberships must be gone")
	require.Zero(t, grants, "grants must be gone")
	require.Equal(t, 1, users, "the user record survives removal — attribution stays intact")

	// Acting on them again in this org is now 404 (not a member).
	r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/force-logout", ts.OrgID, target.ID), nil, true)
	require.Equal(t, http.StatusNotFound, r.StatusCode)
}
