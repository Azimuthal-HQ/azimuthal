package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// M3 scoping contract: every space resource lives under exactly one URL
// convention — /api/v1/orgs/{orgID}/spaces/{spaceID}/... . The v0.1.x mix of
// space-only and org+space routes produced a string of 404s; these tests pin
// the single convention and the org↔space ownership check.

func createScopedSpace(t *testing.T, ts *testServer, name, slug, spaceType string) string {
	t.Helper()
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"name": name,
		"slug": slug,
		"type": spaceType,
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create space: %s", r.Body)
	var space struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &space))
	require.NotEmpty(t, space.ID)
	return space.ID
}

// TestScoping_TicketsUnderOrgAndSpace: the ticket tree is org+space scoped.
func TestScoping_TicketsUnderOrgAndSpace(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Scoped Desk", "scoped-desk", "service_desk")

	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, spaceID)

	r := ts.post(t, base, map[string]string{"title": "Scoped ticket", "priority": "high"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket via org+space URL: %s", r.Body)
	var ticket struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &ticket))

	r = ts.get(t, base+"/"+ticket.ID, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "get ticket via org+space URL: %s", r.Body)

	r = ts.get(t, base+"/kanban", true)
	require.Equal(t, http.StatusOK, r.StatusCode, "kanban via org+space URL: %s", r.Body)
}

// TestScoping_WikiAndProjectsAndWorkflowUnderOrgAndSpace: same convention for
// the other space resource trees.
func TestScoping_WikiAndProjectsAndWorkflowUnderOrgAndSpace(t *testing.T) {
	ts := newTestServer(t)

	wikiID := createScopedSpace(t, ts, "Scoped Wiki", "scoped-wiki", "wiki")
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki", ts.OrgID, wikiID),
		map[string]string{"title": "Scoped page", "content": "<p>hi</p>"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create wiki page via org+space URL: %s", r.Body)

	projID := createScopedSpace(t, ts, "Scoped Proj", "scoped-proj", "project")
	r = ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", ts.OrgID, projID),
		map[string]string{"title": "Scoped item", "kind": "task", "priority": "low"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create project item via org+space URL: %s", r.Body)

	r = ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/workflow/states", ts.OrgID, projID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "workflow states via org+space URL: %s", r.Body)

	r = ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, projID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "space lookup via org+space URL: %s", r.Body)
}

// TestScoping_SpaceOnlyRoutesRemoved: the old space-only convention is gone —
// exactly one convention exists. (404 comes from the SPA fallback, so we
// assert "not a JSON API hit" via status.)
func TestScoping_SpaceOnlyRoutesRemoved(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Legacy Desk", "legacy-desk", "service_desk")

	for _, path := range []string{
		fmt.Sprintf("/api/v1/spaces/%s", spaceID),
		fmt.Sprintf("/api/v1/spaces/%s/tickets", spaceID),
		fmt.Sprintf("/api/v1/spaces/%s/wiki", spaceID),
		fmt.Sprintf("/api/v1/spaces/%s/projects/items", spaceID),
		fmt.Sprintf("/api/v1/spaces/%s/workflow/states", spaceID),
	} {
		r := ts.get(t, path, true)
		require.Equal(t, http.StatusNotFound, r.StatusCode,
			"space-only route %s must be gone, got %d: %s", path, r.StatusCode, r.Body)
	}
}

// TestScoping_WrongOrgIs404: a space accessed under an org that does not own
// it must 404 — org+space scoping is an ownership check, not URL decoration.
func TestScoping_WrongOrgIs404(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Owned Desk", "owned-desk", "service_desk")
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)

	for _, path := range []string{
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", otherOrg.ID, spaceID),
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki", otherOrg.ID, spaceID),
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects/items", otherOrg.ID, spaceID),
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", otherOrg.ID, spaceID),
	} {
		r := ts.get(t, path, true)
		require.Equal(t, http.StatusNotFound, r.StatusCode,
			"wrong-org access %s must 404, got %d: %s", path, r.StatusCode, r.Body)
	}
}

// TestScoping_TicketCommentsRouteSurvivesStaticSubtree: the polymorphic
// comments route (/{entityType}/{entityID}/comments) must keep matching for
// entityType=tickets even though a static /tickets subtree now exists at the
// same router level — this guards chi's static-vs-param backtracking.
func TestScoping_TicketCommentsRouteSurvivesStaticSubtree(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Comment Desk", "comment-desk", "service_desk")

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, spaceID),
		map[string]string{"title": "Commented ticket", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket: %s", r.Body)
	var ticket struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &ticket))

	commentsURL := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets/%s/comments", ts.OrgID, spaceID, ticket.ID)
	r = ts.post(t, commentsURL, map[string]string{"content": "scoped comment"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create comment: %s", r.Body)

	r = ts.get(t, commentsURL, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "list comments: %s", r.Body)
}
