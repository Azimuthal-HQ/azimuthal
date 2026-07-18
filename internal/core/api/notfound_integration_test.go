package api_test

// Integration coverage for the P0 defect family "single-entity GET of a
// nonexistent ID returns 500 INTERNAL_ERROR instead of the documented 404".
//
// Root cause: most getters in internal/db/adapters returned pgx.ErrNoRows
// wrapped in an opaque fmt.Errorf, so the handlers' error mappers (which
// switch on the domain ErrNotFound sentinels) fell through to the 500 branch
// even though every route below declares @Failure 404. The fix maps
// pgx.ErrNoRows to the owning package's ErrNotFound at the adapter boundary,
// following the signing_key.go house pattern.
//
// Spec ref: docs/design/v0.3-ia-spec.md §2.6 — "Valid credentials, no
// access → 404 — never 403, do not leak existence" with the documented
// error shape {"error":{"code","message"}}.
//
// Each test here fails with a 500 before the adapter fix and passes after.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// requireNotFoundEnvelope asserts the documented 404 contract: status 404,
// JSON content type, and the {"error":{"code":"NOT_FOUND","message":...}}
// envelope.
func requireNotFoundEnvelope(t *testing.T, r httpResult) {
	t.Helper()
	require.Equal(t, http.StatusNotFound, r.StatusCode, "body: %s", r.Body)
	require.Contains(t, r.ContentType, "application/json", "body: %s", r.Body)

	var m map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &m), "body: %s", r.Body)
	errObj, ok := m["error"].(map[string]any)
	require.True(t, ok, "error response must have 'error' object, got: %s", r.Body)
	require.Equal(t, "NOT_FOUND", errObj["code"], "body: %s", r.Body)
	require.NotEmpty(t, errObj["message"], "body: %s", r.Body)
}

// projectSpaceFor creates a fresh project space and returns its base
// projects path.
func projectSpaceFor(t *testing.T, ts *testServer) string {
	t.Helper()
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "project")
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects", ts.OrgID, space.ID)
}

// TestIntegration_GetItem_NonexistentID_Returns404 covers
// GET /projects/items/{itemID} for an ID that does not exist.
func TestIntegration_GetItem_NonexistentID_Returns404(t *testing.T) {
	ts := newTestServer(t)
	base := projectSpaceFor(t, ts)

	r := ts.get(t, fmt.Sprintf("%s/items/%s", base, uuid.New()), true)
	requireNotFoundEnvelope(t, r)
}

// TestIntegration_GetSprint_NonexistentID_Returns404 covers
// GET /projects/sprints/{sprintID} for an ID that does not exist.
func TestIntegration_GetSprint_NonexistentID_Returns404(t *testing.T) {
	ts := newTestServer(t)
	base := projectSpaceFor(t, ts)

	r := ts.get(t, fmt.Sprintf("%s/sprints/%s", base, uuid.New()), true)
	requireNotFoundEnvelope(t, r)
}

// TestIntegration_GetActiveSprint_NoneActive_Returns404 covers
// GET /projects/sprints/active for a space with no active sprint — the
// repository contract's not-found case the Sprints page relies on.
func TestIntegration_GetActiveSprint_NoneActive_Returns404(t *testing.T) {
	ts := newTestServer(t)
	base := projectSpaceFor(t, ts)

	r := ts.get(t, base+"/sprints/active", true)
	requireNotFoundEnvelope(t, r)
}

// TestIntegration_GetTicket_NonexistentID_Returns404 covers
// GET /tickets/{ticketID} for an ID that does not exist.
func TestIntegration_GetTicket_NonexistentID_Returns404(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, user.ID, "service_desk")

	r := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets/%s", ts.OrgID, space.ID, uuid.New()), true)
	requireNotFoundEnvelope(t, r)
}

// TestIntegration_GetWorkflow_NonexistentID_Returns404 covers
// GET /orgs/{orgID}/workflows/{workflowID} for an ID that does not exist.
func TestIntegration_GetWorkflow_NonexistentID_Returns404(t *testing.T) {
	ts := newTestServer(t)

	r := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/workflows/%s", ts.OrgID, uuid.New()), true)
	requireNotFoundEnvelope(t, r)
}
