package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	coreteams "github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

func jsonRawReader(s string) io.Reader { return strings.NewReader(s) }

// API-level negatives for the team surface. The store-level matrix already
// pins cycle/depth/deletion semantics; these prove the HTTP mappings — and
// above all that cross-org team ids are indistinguishable from nonexistent
// ones (existence never leaks).

func TestTeamsAPI_CrossOrgTeamIs404(t *testing.T) {
	ts := newTestServer(t)
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	foreignTeam := testutil.DefaultTeamID(t, ts.DB.Pool, otherOrg.ID)

	base := fmt.Sprintf("/api/v1/orgs/%s/teams/%s", ts.OrgID, foreignTeam)
	requireErrorCode(t, ts.get(t, base, true), http.StatusNotFound, "NOT_FOUND")
	requireErrorCode(t, ts.patch(t, base, map[string]string{"name": "X"}, true), http.StatusNotFound, "NOT_FOUND")
	requireErrorCode(t, ts.delete(t, base, true), http.StatusNotFound, "NOT_FOUND")
	requireErrorCode(t, ts.get(t, base+"/members", true), http.StatusNotFound, "NOT_FOUND")

	// A random team id behaves identically — the two cases must be
	// indistinguishable.
	random := fmt.Sprintf("/api/v1/orgs/%s/teams/%s", ts.OrgID, uuid.NewString())
	requireErrorCode(t, ts.get(t, random, true), http.StatusNotFound, "NOT_FOUND")
}

func TestTeamsAPI_ReparentErrorsMapTo400(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	a, err := ts.TeamService.Create(ctx, ts.OrgID, nil, "map-a", "Map A", "")
	require.NoError(t, err)
	b, err := ts.TeamService.Create(ctx, ts.OrgID, &a.ID, "map-b", "Map B", "")
	require.NoError(t, err)

	base := fmt.Sprintf("/api/v1/orgs/%s/teams", ts.OrgID)

	// Cycle: A under its own child B.
	r := ts.patch(t, fmt.Sprintf("%s/%s", base, a.ID), map[string]any{"parent_id": b.ID.String()}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")

	// Depth: build a chain to 5 and hang the A subtree (height 2) under it.
	parent := ""
	var parentID *uuid.UUID
	for i := 1; i <= 4; i++ {
		team, err := ts.TeamService.Create(ctx, ts.OrgID, parentID, fmt.Sprintf("map-d%d", i), fmt.Sprintf("Map D%d", i), "")
		require.NoError(t, err)
		id := team.ID
		parentID = &id
		parent = team.ID.String()
	}
	r = ts.patch(t, fmt.Sprintf("%s/%s", base, a.ID), map[string]any{"parent_id": parent}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")

	// Unparseable parent_id.
	r = ts.patch(t, fmt.Sprintf("%s/%s", base, a.ID), map[string]any{"parent_id": "banana"}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")

	// The default team may not be reparented.
	def := testutil.DefaultTeamID(t, ts.DB.Pool, ts.OrgID)
	r = ts.patch(t, fmt.Sprintf("%s/%s", base, def), map[string]any{"parent_id": a.ID.String()}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
}

func TestTeamsAPI_DeleteOwnsSpacesIs409(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	owner, err := ts.TeamService.Create(ctx, ts.OrgID, nil, "space-owner", "Space Owner", "")
	require.NoError(t, err)

	// Create a space owned by the team via the API.
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"name": "Owned By Team", "slug": "owned-by-team", "type": "vector",
		"owner_team_id": owner.ID.String(),
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create owned space: %s", r.Body)

	r = ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/teams/%s", ts.OrgID, owner.ID), true)
	requireErrorCode(t, r, http.StatusConflict, "CONFLICT")

	// The default team can never be deleted (400, distinct from the 409s).
	def := testutil.DefaultTeamID(t, ts.DB.Pool, ts.OrgID)
	r = ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/teams/%s", ts.OrgID, def), true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
}

func TestTeamsAPI_MemberEndpointValidation(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	squad, err := ts.TeamService.Create(ctx, ts.OrgID, nil, "member-val", "Member Val", "")
	require.NoError(t, err)
	base := fmt.Sprintf("/api/v1/orgs/%s/teams/%s/members", ts.OrgID, squad.ID)

	// Unparseable user id in the URL.
	r := ts.put(t, base+"/banana", map[string]string{"role": "member"}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "BAD_REQUEST")
	requireErrorCode(t, ts.delete(t, base+"/banana", true), http.StatusBadRequest, "BAD_REQUEST")

	// Removing someone who is not a member → 404.
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	requireErrorCode(t, ts.delete(t, base+"/"+member.ID.String(), true), http.StatusNotFound, "NOT_FOUND")

	// Malformed body on PUT (sent raw — it must not be marshallable).
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		ts.url(base+"/"+member.ID.String()), jsonRawReader("{bad"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ts.Token)
	requireErrorCode(t, ts.do(t, req), http.StatusBadRequest, "BAD_REQUEST")

	// is_primary: true routes through SetPrimary — verified on the row.
	r = ts.put(t, base+"/"+member.ID.String(), map[string]any{"role": "member", "is_primary": true}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "put member primary: %s", r.Body)
	got, err := ts.TeamService.GetMember(ctx, squad.ID, member.ID)
	require.NoError(t, err)
	require.True(t, got.IsPrimary)
}

func TestDirectory_ModuleAndTeamFilters(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	squad, err := ts.TeamService.Create(ctx, ts.OrgID, nil, "dir-squad", "Dir Squad", "")
	require.NoError(t, err)

	mkSpace := func(name, slug, typ string, owner *coreteams.Team) string {
		body := map[string]string{"name": name, "slug": slug, "type": typ}
		if owner != nil {
			body["owner_team_id"] = owner.ID.String()
		}
		r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), body, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "create %s: %s", name, r.Body)
		var space struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &space))
		return space.ID
	}
	vectorDefault := mkSpace("Dir Vector Default", "dir-vector-default", "vector", nil)
	codexSquad := mkSpace("Dir Codex Squad", "dir-codex-squad", "codex", &squad)

	ids := func(r httpResult) []string {
		require.Equal(t, http.StatusOK, r.StatusCode, "directory: %s", r.Body)
		var rows []struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &rows))
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return out
	}

	// module filter: only the codex space comes back.
	got := ids(ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces?module=codex", ts.OrgID), true))
	require.Equal(t, []string{codexSquad}, got)

	// team filter: only the squad-owned space.
	got = ids(ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces?team_id=%s", ts.OrgID, squad.ID), true))
	require.Equal(t, []string{codexSquad}, got)

	// combined: squad + vector → empty; default team + vector → the default space.
	got = ids(ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces?module=vector&team_id=%s", ts.OrgID, squad.ID), true))
	require.Empty(t, got)
	def := testutil.DefaultTeamID(t, ts.DB.Pool, ts.OrgID)
	got = ids(ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces?module=vector&team_id=%s", ts.OrgID, def), true))
	require.Equal(t, []string{vectorDefault}, got)

	// invalid team_id → 400.
	requireErrorCode(t, ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces?team_id=banana", ts.OrgID), true),
		http.StatusBadRequest, "BAD_REQUEST")
}

// Malformed-JSON rows of the §2.6 matrix for the remaining new mutations.
func TestGrantsAPI_MalformedBodies(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Malformed Space", "malformed-space", "vector")
	ctx := context.Background()

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	grant, err := ts.GrantService.Create(ctx, ts.OrgID, uuid.MustParse(spaceID),
		access.SubjectUser, member.ID, access.RoleViewer, ts.UserID)
	require.NoError(t, err)

	raw := func(method, path string) httpResult {
		req, err := http.NewRequestWithContext(ctx, method, ts.url(path), jsonRawReader("{bad"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+ts.Token)
		return ts.do(t, req)
	}

	requireErrorCode(t, raw(http.MethodPatch,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants/%s", ts.OrgID, spaceID, grant.ID)),
		http.StatusBadRequest, "BAD_REQUEST")
	requireErrorCode(t, raw(http.MethodPost,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants/", ts.OrgID, spaceID)),
		http.StatusBadRequest, "BAD_REQUEST")
	requireErrorCode(t, raw(http.MethodPost,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/members", ts.OrgID, spaceID)),
		http.StatusBadRequest, "BAD_REQUEST")
	requireErrorCode(t, raw(http.MethodPost,
		fmt.Sprintf("/api/v1/orgs/%s/teams/", ts.OrgID)),
		http.StatusBadRequest, "BAD_REQUEST")
	requireErrorCode(t, raw(http.MethodPatch,
		fmt.Sprintf("/api/v1/orgs/%s/", ts.OrgID)),
		http.StatusBadRequest, "BAD_REQUEST")
}

func TestSpaceUpdate_GovernanceValidation(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Gov Val Space", "gov-val-space", "vector")
	path := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)

	// Unknown visibility value.
	r := ts.put(t, path, map[string]string{"name": "Gov Val Space", "visibility": "sorta-public"}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")

	// Foreign-org owner team.
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	foreign := testutil.DefaultTeamID(t, ts.DB.Pool, otherOrg.ID)
	r = ts.put(t, path, map[string]string{"name": "Gov Val Space", "owner_team_id": foreign.String()}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")

	// Unparseable owner team id.
	r = ts.put(t, path, map[string]string{"name": "Gov Val Space", "owner_team_id": "banana"}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")
}
