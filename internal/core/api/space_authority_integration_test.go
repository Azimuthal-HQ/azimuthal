package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

func jsonReader(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewReader(b)
}

// Space-creation authority (ADR-0007, decision A4): org admin, or a lead of
// the owning team — the one sanctioned administrative use of the team
// metadata role. These are the only paths that may create a space.

func TestSpaceCreateAuthority_LeadOfOwningTeam(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	squad, err := ts.TeamService.Create(ctx, ts.OrgID, nil, "squad", "Squad", "")
	require.NoError(t, err)

	lead := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err = ts.TeamService.AddMember(ctx, squad.ID, lead.ID, ts.OrgID, "lead")
	require.NoError(t, err)
	leadTok := ts.tokenFor(t, lead.ID, lead.Email)

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err = ts.TeamService.AddMember(ctx, squad.ID, member.ID, ts.OrgID, "member")
	require.NoError(t, err)
	memberTok := ts.tokenFor(t, member.ID, member.Email)

	spacesPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)
	body := func(slug string) map[string]string {
		return map[string]string{
			"name": "Lead Space " + slug, "slug": slug, "type": "vector",
			"owner_team_id": squad.ID.String(),
		}
	}

	// A lead of the owning team creates the space (201) and — not being an
	// org admin — receives the creator auto-grant so the space is reachable.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.url(spacesPath), jsonReader(t, body("lead-space")))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+leadTok)
	r := ts.do(t, req)
	require.Equal(t, http.StatusCreated, r.StatusCode, "lead must create: %s", r.Body)
	var space struct {
		ID          string `json:"id"`
		OwnerTeamID string `json:"owner_team_id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &space))
	require.Equal(t, squad.ID.String(), space.OwnerTeamID)

	var grantCount int
	require.NoError(t, ts.DB.Pool.QueryRow(ctx,
		`SELECT count(*) FROM space_grants WHERE space_id = $1 AND subject_type = 'user' AND subject_id = $2 AND role = 'space_admin'`,
		uuid.MustParse(space.ID), lead.ID).Scan(&grantCount))
	require.Equal(t, 1, grantCount, "non-admin creator needs the auto-grant to reach their space")

	req, err = http.NewRequestWithContext(ctx, http.MethodGet,
		ts.url(fmt.Sprintf("%s/%s", spacesPath, space.ID)), http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+leadTok)
	r = ts.do(t, req)
	require.Equal(t, http.StatusOK, r.StatusCode, "creator must read their new space: %s", r.Body)

	// A plain member of the owning team may not create.
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, ts.url(spacesPath), jsonReader(t, body("member-space")))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+memberTok)
	r = ts.do(t, req)
	requireErrorCode(t, r, http.StatusForbidden, "FORBIDDEN")

	// A lead of a DIFFERENT team may not create under this owner either.
	other, err := ts.TeamService.Create(ctx, ts.OrgID, nil, "other-squad", "Other Squad", "")
	require.NoError(t, err)
	otherLead := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err = ts.TeamService.AddMember(ctx, other.ID, otherLead.ID, ts.OrgID, "lead")
	require.NoError(t, err)
	otherTok := ts.tokenFor(t, otherLead.ID, otherLead.Email)
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, ts.url(spacesPath), jsonReader(t, body("other-lead-space")))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+otherTok)
	r = ts.do(t, req)
	requireErrorCode(t, r, http.StatusForbidden, "FORBIDDEN")
}

func TestSpaceCreate_OwnerTeamValidation(t *testing.T) {
	ts := newTestServer(t)
	spacesPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)

	// A foreign-org team id is rejected with 400 even for an org admin.
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	foreign := testutil.DefaultTeamID(t, ts.DB.Pool, otherOrg.ID)
	r := ts.post(t, spacesPath, map[string]string{
		"name": "Bad Owner", "slug": "bad-owner", "type": "vector",
		"owner_team_id": foreign.String(),
	}, true)
	requireErrorCode(t, r, http.StatusBadRequest, "VALIDATION_ERROR")

	// Omitted owner_team_id falls back to the org default team.
	r = ts.post(t, spacesPath, map[string]string{
		"name": "Defaulted Owner", "slug": "defaulted-owner", "type": "vector",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "default owner: %s", r.Body)
	var space struct {
		OwnerTeamID string `json:"owner_team_id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &space))
	require.Equal(t, testutil.DefaultTeamID(t, ts.DB.Pool, ts.OrgID).String(), space.OwnerTeamID)
}

// The legacy space_members endpoints (metadata only) under manage_space:
// happy paths for add, list, remove.
func TestSpaceMembers_ManageSpaceHappyPath(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Members Space", "members-space", "vector")
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/members", ts.OrgID, spaceID)

	r := ts.post(t, base, map[string]string{"user_id": member.ID.String(), "role": "member"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "add member: %s", r.Body)

	r = ts.get(t, base, true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var members []struct {
		UserID string `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &members))
	found := false
	for _, m := range members {
		if m.UserID == member.ID.String() {
			found = true
		}
	}
	require.True(t, found, "added member must be listed: %s", r.Body)

	r = ts.delete(t, base+"/"+member.ID.String(), true)
	require.Equal(t, http.StatusNoContent, r.StatusCode)
}

// Grants list happy path: rows carry the subject's display identity.
func TestGrantsList_SubjectNames(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Grant List Space", "grant-list-space", "vector")
	ctx := context.Background()

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	squad, err := ts.TeamService.Create(ctx, ts.OrgID, nil, "grantees", "Grantees", "")
	require.NoError(t, err)

	spaceUUID := uuid.MustParse(spaceID)
	_, err = ts.GrantService.Create(ctx, ts.OrgID, spaceUUID, access.SubjectUser, member.ID, access.RoleViewer, ts.UserID)
	require.NoError(t, err)
	_, err = ts.GrantService.Create(ctx, ts.OrgID, spaceUUID, access.SubjectTeam, squad.ID, access.RoleAgent, ts.UserID)
	require.NoError(t, err)

	r := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants/", ts.OrgID, spaceID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "list grants: %s", r.Body)
	var rows []struct {
		SubjectType string `json:"subject_type"`
		SubjectName string `json:"subject_name"`
		Role        string `json:"role"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &rows))
	require.Len(t, rows, 2)
	byType := map[string]string{}
	for _, row := range rows {
		byType[row.SubjectType] = row.SubjectName
	}
	require.Equal(t, "Test User", byType["user"], "user grants carry the display name")
	require.Equal(t, "Grantees", byType["team"], "team grants carry the team name")
}

// Page comments — the codex read path swept in W4: create needs the comment
// capability (admin here via bypass), list returns what was written.
func TestPageComments_CreateAndList(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Page Comment Space", "page-comment-space", "codex")

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki", ts.OrgID, spaceID),
		map[string]string{"title": "Commented page", "content": "<p>hello</p>"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create page: %s", r.Body)
	var page struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &page))

	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s/comments", ts.OrgID, spaceID, page.ID)
	r = ts.post(t, base, map[string]string{"content": "first!"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create comment: %s", r.Body)

	r = ts.get(t, base, true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var comments []struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &comments))
	require.Len(t, comments, 1)
	require.Equal(t, "first!", comments[0].Content)
}

// Teams list ?parent_id= filters to one parent's children.
func TestTeamsList_ParentFilter(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	parent, err := ts.TeamService.Create(ctx, ts.OrgID, nil, "filter-parent", "Filter Parent", "")
	require.NoError(t, err)
	child, err := ts.TeamService.Create(ctx, ts.OrgID, &parent.ID, "filter-child", "Filter Child", "")
	require.NoError(t, err)
	_, err = ts.TeamService.Create(ctx, ts.OrgID, nil, "filter-root", "Filter Root", "")
	require.NoError(t, err)

	r := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/teams/?parent_id=%s", ts.OrgID, parent.ID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var rows []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &rows))
	require.Len(t, rows, 1, "exactly the one child: %s", r.Body)
	require.Equal(t, child.ID.String(), rows[0].ID)

	r = ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/teams/?parent_id=banana", ts.OrgID), true)
	requireErrorCode(t, r, http.StatusBadRequest, "BAD_REQUEST")
}
