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

// Handler-level capability refinement above the create_items write floor
// (the "in-handler" rows of route_accounting_test.go): the edit_own/edit_any
// split on tickets, wiki pages, and project items; the agent-tier transition
// and sprint mutations; the comment capability. Every denial is proven
// positively — exact 403 status plus the FORBIDDEN error envelope — never by
// the mere absence of a success.

// writeCapFixture is a space with one persona per role tier: a viewer, a
// contributor, and an agent, each holding a direct user grant. The harness
// admin (org admin bypass) owns the space.
type writeCapFixture struct {
	ts      *testServer
	spaceID string

	viewerTok  string
	contrib    testutil.User
	contribTok string
	agent      testutil.User
	agentTok   string
}

func newWriteCapFixture(t *testing.T) *writeCapFixture {
	t.Helper()
	ts := newTestServer(t)
	f := &writeCapFixture{ts: ts}
	f.spaceID = createScopedSpace(t, ts, "Write Cap Space", "write-cap-space", "vector")
	spaceUUID := uuid.MustParse(f.spaceID)

	mk := func(role access.Role) (testutil.User, string) {
		u := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
		_, err := ts.GrantService.Create(context.Background(), ts.OrgID, spaceUUID,
			access.SubjectUser, u.ID, role, ts.UserID)
		require.NoError(t, err)
		return u, ts.tokenFor(t, u.ID, u.Email)
	}
	_, f.viewerTok = mk(access.RoleViewer)
	f.contrib, f.contribTok = mk(access.RoleContributor)
	f.agent, f.agentTok = mk(access.RoleAgent)
	return f
}

// requestAs issues a request authenticated as the given persona token.
func (f *writeCapFixture) requestAs(t *testing.T, token, method, path string, body any) httpResult {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.ts.url(path), reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return f.ts.do(t, req)
}

func (f *writeCapFixture) spacePath(suffix string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s%s", f.ts.OrgID, f.spaceID, suffix)
}

// requireAPIForbidden asserts the positive denial shape for capability
// failures: HTTP 403 carrying the documented FORBIDDEN error envelope — the
// 403 analogue of requireAPINotFound.
func requireAPIForbidden(t *testing.T, r httpResult) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, r.StatusCode, "denial must be 403, got body: %s", r.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "denial must carry the API error envelope")
	require.Equal(t, "FORBIDDEN", body.Error.Code)
}

// createTicketAs creates a ticket as the persona and returns its id.
func (f *writeCapFixture) createTicketAs(t *testing.T, token, title string) string {
	t.Helper()
	r := f.requestAs(t, token, http.MethodPost, f.spacePath("/tickets"),
		map[string]any{"title": title, "priority": "medium"})
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket: %s", r.Body)
	var resp struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &resp))
	require.NotEmpty(t, resp.ID)
	return resp.ID
}

// TestWriteCapability_ViewerBlockedAtWriteFloor: a viewer reads the space but
// every mutation dies at the create_items floor with a positive 403.
func TestWriteCapability_ViewerBlockedAtWriteFloor(t *testing.T) {
	f := newWriteCapFixture(t)

	// Premise: the space is readable to the viewer — the denial below is the
	// write floor, not a 404-invisible space.
	r := f.requestAs(t, f.viewerTok, http.MethodGet, f.spacePath("/tickets"), nil)
	require.Equal(t, http.StatusOK, r.StatusCode, "viewer must read tickets: %s", r.Body)

	requireAPIForbidden(t, f.requestAs(t, f.viewerTok, http.MethodPost, f.spacePath("/tickets"),
		map[string]any{"title": "Viewer Ticket", "priority": "medium"}))
}

// TestWriteCapability_TicketOwnership: contributors edit and delete only what
// they created; agents edit anything and hold the transition capability.
func TestWriteCapability_TicketOwnership(t *testing.T) {
	f := newWriteCapFixture(t)

	contribTicket := f.createTicketAs(t, f.contribTok, "Contributor Ticket")
	agentTicket := f.createTicketAs(t, f.agentTok, "Agent Ticket")

	patchBody := map[string]any{"title": "Edited", "description": "d", "priority": "medium"}

	// Contributor edits their own ticket.
	r := f.requestAs(t, f.contribTok, http.MethodPatch, f.spacePath("/tickets/"+contribTicket), patchBody)
	require.Equal(t, http.StatusOK, r.StatusCode, "contributor edits own ticket: %s", r.Body)

	// Contributor may not edit the agent's ticket, transition any ticket
	// (even their own), or delete the agent's ticket.
	requireAPIForbidden(t, f.requestAs(t, f.contribTok, http.MethodPatch,
		f.spacePath("/tickets/"+agentTicket), patchBody))
	requireAPIForbidden(t, f.requestAs(t, f.contribTok, http.MethodPost,
		f.spacePath("/tickets/"+contribTicket+"/status"), map[string]string{"status": "in_progress"}))
	requireAPIForbidden(t, f.requestAs(t, f.contribTok, http.MethodDelete,
		f.spacePath("/tickets/"+agentTicket), nil))

	// Agent edits the contributor's ticket and transitions it.
	r = f.requestAs(t, f.agentTok, http.MethodPatch, f.spacePath("/tickets/"+contribTicket),
		map[string]any{"title": "Agent Edit", "description": "d", "priority": "high"})
	require.Equal(t, http.StatusOK, r.StatusCode, "agent edits contributor ticket: %s", r.Body)
	r = f.requestAs(t, f.agentTok, http.MethodPost,
		f.spacePath("/tickets/"+contribTicket+"/status"), map[string]string{"status": "in_progress"})
	require.Equal(t, http.StatusOK, r.StatusCode, "agent transitions ticket: %s", r.Body)

	// Contributor deletes their own ticket — edit_own_items covers delete.
	r = f.requestAs(t, f.contribTok, http.MethodDelete, f.spacePath("/tickets/"+contribTicket), nil)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "contributor deletes own ticket: %s", r.Body)
}

// TestWriteCapability_SprintMutations: sprint creation is an agent-tier
// mutation (edit_any_item), above the contributor floor.
func TestWriteCapability_SprintMutations(t *testing.T) {
	f := newWriteCapFixture(t)

	sprintBody := map[string]any{"name": "Sprint 1", "goal": "ship"}

	requireAPIForbidden(t, f.requestAs(t, f.contribTok, http.MethodPost,
		f.spacePath("/projects/sprints"), sprintBody))

	r := f.requestAs(t, f.agentTok, http.MethodPost, f.spacePath("/projects/sprints"), sprintBody)
	require.Equal(t, http.StatusCreated, r.StatusCode, "agent creates sprint: %s", r.Body)
}

// TestWriteCapability_WikiOwnership: the edit_own/edit_any split on wiki
// pages, keyed on the page's author_id.
func TestWriteCapability_WikiOwnership(t *testing.T) {
	f := newWriteCapFixture(t)

	createPage := func(token, title string) (id string, version int32) {
		r := f.requestAs(t, token, http.MethodPost, f.spacePath("/wiki"),
			map[string]any{"title": title, "content": "body"})
		require.Equal(t, http.StatusCreated, r.StatusCode, "create page: %s", r.Body)
		var page struct {
			ID      string `json:"id"`
			Version int32  `json:"version"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &page))
		require.NotEmpty(t, page.ID)
		return page.ID, page.Version
	}

	contribPage, contribVer := createPage(f.contribTok, "Contributor Page")
	agentPage, _ := createPage(f.agentTok, "Agent Page")

	// Contributor updates their own page.
	r := f.requestAs(t, f.contribTok, http.MethodPut, f.spacePath("/wiki/"+contribPage),
		map[string]any{"title": "Contributor Page v2", "content": "edited", "expected_version": contribVer})
	require.Equal(t, http.StatusOK, r.StatusCode, "contributor edits own page: %s", r.Body)
	var updated struct {
		Version int32 `json:"version"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &updated))

	// Contributor may not update a page authored by the agent — the denial
	// fires before any version checking.
	requireAPIForbidden(t, f.requestAs(t, f.contribTok, http.MethodPut, f.spacePath("/wiki/"+agentPage),
		map[string]any{"title": "Hijack", "content": "nope", "expected_version": 1}))

	// Agent updates the contributor's page via edit_any_item.
	r = f.requestAs(t, f.agentTok, http.MethodPut, f.spacePath("/wiki/"+contribPage),
		map[string]any{"title": "Agent Edit", "content": "agent edit", "expected_version": updated.Version})
	require.Equal(t, http.StatusOK, r.StatusCode, "agent edits contributor page: %s", r.Body)
}

// TestWriteCapability_ProjectItemOwnership: the same ownership pattern on
// project items, keyed on reporter_id, plus the agent-only status change.
func TestWriteCapability_ProjectItemOwnership(t *testing.T) {
	f := newWriteCapFixture(t)

	createItem := func(token, title string) string {
		r := f.requestAs(t, token, http.MethodPost, f.spacePath("/projects/items"),
			map[string]any{"title": title, "kind": "task", "priority": "medium"})
		require.Equal(t, http.StatusCreated, r.StatusCode, "create item: %s", r.Body)
		var item struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &item))
		require.NotEmpty(t, item.ID)
		return item.ID
	}

	contribItem := createItem(f.contribTok, "Contributor Item")
	agentItem := createItem(f.agentTok, "Agent Item")

	patchBody := map[string]any{"title": "Edited Item", "description": "d", "priority": "medium"}

	// Contributor edits their own item; the agent's item is off limits.
	r := f.requestAs(t, f.contribTok, http.MethodPatch, f.spacePath("/projects/items/"+contribItem), patchBody)
	require.Equal(t, http.StatusOK, r.StatusCode, "contributor edits own item: %s", r.Body)
	requireAPIForbidden(t, f.requestAs(t, f.contribTok, http.MethodPatch,
		f.spacePath("/projects/items/"+agentItem), patchBody))

	// Status change is transition_any_item — agent tier, even on own items.
	requireAPIForbidden(t, f.requestAs(t, f.contribTok, http.MethodPost,
		f.spacePath("/projects/items/"+contribItem+"/status"), map[string]string{"status": "in_progress"}))
	r = f.requestAs(t, f.agentTok, http.MethodPost,
		f.spacePath("/projects/items/"+contribItem+"/status"), map[string]string{"status": "in_progress"})
	require.Equal(t, http.StatusOK, r.StatusCode, "agent changes item status: %s", r.Body)

	// Agent edits the contributor's item.
	r = f.requestAs(t, f.agentTok, http.MethodPatch, f.spacePath("/projects/items/"+contribItem),
		map[string]any{"title": "Agent Item Edit", "description": "d", "priority": "high"})
	require.Equal(t, http.StatusOK, r.StatusCode, "agent edits contributor item: %s", r.Body)
}

// TestWriteCapability_Comments: commenting needs the comment capability
// (contributor and above); a viewer's attempt dies with a positive 403.
func TestWriteCapability_Comments(t *testing.T) {
	f := newWriteCapFixture(t)

	ticketID := f.createTicketAs(t, f.contribTok, "Commented Ticket")
	commentPath := f.spacePath("/tickets/" + ticketID + "/comments")

	requireAPIForbidden(t, f.requestAs(t, f.viewerTok, http.MethodPost, commentPath,
		map[string]string{"content": "viewer says hi"}))

	r := f.requestAs(t, f.contribTok, http.MethodPost, commentPath,
		map[string]string{"content": "contributor says hi"})
	require.Equal(t, http.StatusCreated, r.StatusCode, "contributor comments: %s", r.Body)
}
