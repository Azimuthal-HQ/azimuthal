package api_test

// Integration coverage for the P0 defect "kanban board drag-and-drop is not
// wired". The board's drag handler now persists a ticket's column move through
// POST /tickets/{id}/status; these tests pin that API path against a real
// database — the exact transition the drag performs, its persistence on a
// fresh read (what a page reload sees), and the state-machine rejection the
// board must surface rather than swallow.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// requireJSONList unmarshals a JSON array body into dst, failing the test on
// malformed JSON.
func requireJSONList(t *testing.T, body []byte, dst *[]map[string]any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(body, dst), "body: %s", body)
}

// createKanbanTicket creates a service-desk space plus one ticket and returns
// the ticket's URL base and ID.
func createKanbanTicket(t *testing.T, ts *testServer) (spaceBase string, ticketID string) {
	t.Helper()
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "service_desk")
	spaceBase = fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, space.ID)

	r := ts.post(t, spaceBase+"/tickets", map[string]any{
		"title":    "Draggable ticket",
		"priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket: %s", r.Body)

	body := decodeJSONMap(t, r.Body)
	require.Equal(t, "open", body["status"], "new tickets start open")
	return spaceBase, body["id"].(string)
}

// TestKanbanDrag_StatusTransitionPersists_Regression covers the API-level
// status transition behind a kanban column move: open → in_progress succeeds,
// and a fresh read — what the board fetches after a page reload — returns the
// new status.
func TestKanbanDrag_StatusTransitionPersists_Regression(t *testing.T) {
	ts := newTestServer(t)
	spaceBase, ticketID := createKanbanTicket(t, ts)

	r := ts.post(t, fmt.Sprintf("%s/tickets/%s/status", spaceBase, ticketID),
		map[string]any{"status": "in_progress"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "transition: %s", r.Body)
	require.Equal(t, "in_progress", decodeJSONMap(t, r.Body)["status"])

	// Fresh single read persists.
	r = ts.get(t, fmt.Sprintf("%s/tickets/%s", spaceBase, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, "in_progress", decodeJSONMap(t, r.Body)["status"])

	// The list read — the board's own data source — reflects it too.
	r = ts.get(t, spaceBase+"/tickets", true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var list []map[string]any
	requireJSONList(t, r.Body, &list)
	found := false
	for _, tk := range list {
		if tk["id"] == ticketID {
			found = true
			require.Equal(t, "in_progress", tk["status"])
		}
	}
	require.True(t, found, "transitioned ticket missing from list: %s", r.Body)
}

// TestKanbanDrag_InvalidTransition_Returns409 pins the state-machine
// rejection: open → resolved skips in_progress and must return 409 with the
// documented error shape. The board surfaces this error and rolls the card
// back instead of silently keeping a state the server refused.
func TestKanbanDrag_InvalidTransition_Returns409(t *testing.T) {
	ts := newTestServer(t)
	spaceBase, ticketID := createKanbanTicket(t, ts)

	r := ts.post(t, fmt.Sprintf("%s/tickets/%s/status", spaceBase, ticketID),
		map[string]any{"status": "resolved"}, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "response: %s", r.Body)
	errObj := decodeErrorObject(t, r.Body)
	require.Equal(t, "INVALID_TRANSITION", errObj["code"], "body: %s", r.Body)
	require.NotEmpty(t, errObj["message"])

	// The refused transition must not have been persisted.
	r = ts.get(t, fmt.Sprintf("%s/tickets/%s", spaceBase, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, "open", decodeJSONMap(t, r.Body)["status"])
}
