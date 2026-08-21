package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// DeleteWorkflow's in-use guard. A workflow is an org object every assigned
// space shares, and spaces.workflow_id is ON DELETE SET NULL (migration 016), so
// a plain delete silently unassigned every space that used it and stranded their
// tickets' workflow_state_id values. The guard refuses with 409 while any live
// space is assigned the workflow, naming the count.

// assignWorkflowToSpace assigns a workflow to a space by the same column the
// product uses (spaces.workflow_id, AssignWorkflowToSpace).
func assignWorkflowToSpace(t *testing.T, ts *testServer, workflowID, spaceID uuid.UUID) {
	t.Helper()
	_, err := ts.DB.Pool.Exec(t.Context(),
		`UPDATE spaces SET workflow_id = $1 WHERE id = $2`, workflowID, spaceID)
	require.NoError(t, err)
}

// A workflow assigned to a space cannot be deleted; the refusal is a 409 that
// names how many spaces block it, and clears once the space is reassigned.
func TestWorkflowDelete_RefusedWhileAssignedToSpaces_NamesCountAndClears(t *testing.T) {
	ts := newTestServer(t)
	ctx := t.Context()

	wf := &workflow.Workflow{OrgID: ts.OrgID, Name: "Deletable", AppliesTo: "tickets"}
	require.NoError(t, ts.WorkflowAdapter.CreateWorkflow(ctx, wf))
	require.NotEqual(t, "00000000-0000-0000-0000-000000000000", wf.ID.String())

	owner := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, owner.ID, "beacon")
	assignWorkflowToSpace(t, ts, wf.ID, space.ID)

	path := fmt.Sprintf("/api/v1/orgs/%s/workflows/%s", ts.OrgID, wf.ID)

	// Refused while assigned, with the count named.
	r := ts.delete(t, path, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "%s", r.Body)
	code, msg, reqID := errEnvelope(t, r.Body)
	require.Equal(t, string(respond.CodeConflict), code, "the in-use refusal is a CONFLICT envelope")
	require.Contains(t, msg, "1 space", "the refusal must name the count of assigned spaces")
	require.NotEmpty(t, reqID)

	// The workflow is still there — the refusal wrote nothing.
	_, err := ts.WorkflowAdapter.GetWorkflow(ctx, wf.ID)
	require.NoError(t, err, "a refused delete must leave the workflow intact")

	// Reassign the space away, and the same delete now succeeds.
	assignWorkflowToSpaceNull(t, ts, space.ID)
	r = ts.delete(t, path, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "%s", r.Body)
	_, err = ts.WorkflowAdapter.GetWorkflow(ctx, wf.ID)
	require.Error(t, err, "the workflow is gone once nothing uses it")
}

// The count is honest: two assigned spaces are reported as two, pluralised.
// This is the negative-test guard — a hard-coded "1 space" message would pass
// the test above and fail here.
func TestWorkflowDelete_CountReflectsEveryAssignedSpace(t *testing.T) {
	ts := newTestServer(t)
	ctx := t.Context()

	wf := &workflow.Workflow{OrgID: ts.OrgID, Name: "Shared", AppliesTo: "tickets"}
	require.NoError(t, ts.WorkflowAdapter.CreateWorkflow(ctx, wf))

	owner := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	for range 2 {
		space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, owner.ID, "beacon")
		assignWorkflowToSpace(t, ts, wf.ID, space.ID)
	}
	// A soft-deleted space assigned the workflow is NOT counted — an admin
	// cannot reassign it, so naming it would be unactionable.
	deletedSpace := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, owner.ID, "beacon")
	assignWorkflowToSpace(t, ts, wf.ID, deletedSpace.ID)
	_, err := ts.DB.Pool.Exec(ctx, `UPDATE spaces SET deleted_at = now() WHERE id = $1`, deletedSpace.ID)
	require.NoError(t, err)

	path := fmt.Sprintf("/api/v1/orgs/%s/workflows/%s", ts.OrgID, wf.ID)
	r := ts.delete(t, path, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "%s", r.Body)
	_, msg, _ := errEnvelope(t, r.Body)
	require.Contains(t, msg, "2 spaces", "two live spaces must be reported as two, and the soft-deleted one ignored: %q", msg)
}

// assignWorkflowToSpaceNull clears a space's workflow assignment.
func assignWorkflowToSpaceNull(t *testing.T, ts *testServer, spaceID uuid.UUID) {
	t.Helper()
	_, err := ts.DB.Pool.Exec(t.Context(),
		`UPDATE spaces SET workflow_id = NULL WHERE id = $1`, spaceID)
	require.NoError(t, err)
}
