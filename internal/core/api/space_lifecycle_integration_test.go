package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestIntegration_SpaceCreate_AssignsDefaultWorkflowByModule exercises the
// production wiring (WithWorkflowAssigner) end to end: creating a space
// through the API assigns the org's default workflow keyed on the rebranded
// module values — beacon → tickets workflow, vector → project_items
// workflow, codex → intentionally none.
func TestIntegration_SpaceCreate_AssignsDefaultWorkflowByModule(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()
	require.NoError(t, ts.WorkflowAdapter.SeedDefaultWorkflows(ctx, ts.OrgID))

	spacesPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)

	workflowID := func(spaceID string) *uuid.UUID {
		t.Helper()
		var wf *uuid.UUID
		err := ts.DB.Pool.QueryRow(ctx, "SELECT workflow_id FROM spaces WHERE id = $1", spaceID).Scan(&wf)
		require.NoError(t, err)
		return wf
	}

	create := func(typ, slug string) string {
		t.Helper()
		r := ts.post(t, spacesPath, map[string]any{
			"name": "Workflow " + typ,
			"slug": slug,
			"type": typ,
		}, true)
		require.Equalf(t, http.StatusCreated, r.StatusCode, "create %s: %s", typ, r.Body)
		var space struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &space))
		return space.ID
	}

	beaconID := create("beacon", "wf-beacon")
	require.NotNilf(t, workflowID(beaconID), "a beacon space must receive the default tickets workflow")

	vectorID := create("vector", "wf-vector")
	require.NotNilf(t, workflowID(vectorID), "a vector space must receive the default project_items workflow")

	codexID := create("codex", "wf-codex")
	require.Nilf(t, workflowID(codexID), "a codex space intentionally receives no workflow")
}

// TestIntegration_SpaceDelete_SoftDeletesAndDisappears covers the delete
// path: 204 on success, and the space is gone from reads afterwards.
func TestIntegration_SpaceDelete_SoftDeletesAndDisappears(t *testing.T) {
	ts := newTestServer(t)
	spacesPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)

	r := ts.post(t, spacesPath, map[string]any{
		"name": "Doomed Space",
		"slug": "doomed-space",
		"type": "vector",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode)
	var space struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &space))

	del := ts.delete(t, fmt.Sprintf("%s/%s", spacesPath, space.ID), true)
	require.Equal(t, http.StatusNoContent, del.StatusCode)

	get := ts.get(t, fmt.Sprintf("%s/%s", spacesPath, space.ID), true)
	require.Equal(t, http.StatusNotFound, get.StatusCode)

	list := ts.get(t, spacesPath, true)
	require.Equal(t, http.StatusOK, list.StatusCode)
	require.NotContainsf(t, string(list.Body), space.ID, "a soft-deleted space must not appear in the list")
}
