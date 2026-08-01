package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// D72, closed — this is the test that was written to prove it, now running.
//
// # The defect
//
// A new project item was created at the literal status "open". The seeded
// project workflow's states are backlog/todo/in_progress/in_review/done, so
// "open" named no state, so the item's FIRST transition resolved no edge — and a
// transition that resolved no edge was not gated. Guards, approvals and
// post-functions an administrator configured on the initial edge silently did
// not apply to it. Every subsequent move was gated normally, which is what made
// it so quiet: the feature demonstrably worked, on the second try.
//
// # How it is closed
//
// By option (e) of known-issues #30, the one that entry recommends: items and
// tickets are BORN in their space workflow's initial state, with `status` and
// `workflow_state_id` written together. projects.Handler.CreateItem resolves it
// through tiergate.Gate.InitialPosition, and a space with no workflow keeps the
// old literal default.
//
// The two cheaper candidates were rejected on their merits and are worth keeping
// on the record, because both look adequate from a distance:
//
//   - Migration 014's column DEFAULT was blamed by the original ledger entry and
//     is not the cause. CreateProjectItem names `status` in its INSERT column
//     list, so the DEFAULT is never evaluated by the application; changing it
//     would have altered nothing but raw-SQL test fixtures. D85 records the
//     correction.
//
//   - Falling back to the workflow's initial state inside Gate whenever the
//     status names no state conflates "never transitioned" with "renamed out
//     from under it", and would run the initial edge's post-functions — which
//     MUTATE — for a move that has nothing to do with them. The resolution this
//     phase ships is not that: TierService.ResolveFromState consults the stored
//     workflow_state_id BEFORE falling back, so a renamed state resolves exactly
//     and the fallback is reached only when neither recorded position resolves.
//     That distinction is what makes it sound, and it is only available because
//     the same phase repaired the D71 drift that made the state id meaningless.
func TestTierAPI_ANewItemsFirstTransitionIsGated(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()
	require.NoError(t, ts.WorkflowAdapter.SeedDefaultWorkflows(ctx, ts.OrgID))

	owner := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, owner.ID, "vector")
	require.NoError(t, ts.WorkflowAdapter.AssignDefaultWorkflowToSpace(ctx, ts.OrgID, "vector", space.ID))

	def, err := ts.WorkflowAdapter.GetDefaultWorkflow(ctx, ts.OrgID, "project_items")
	require.NoError(t, err)
	states, err := ts.WorkflowAdapter.ListStates(ctx, def.ID)
	require.NoError(t, err)
	byName := map[string]uuid.UUID{}
	for _, s := range states {
		byName[s.Name] = s.ID
	}

	// The edge OUT of the initial state. Once items are born in that state this
	// is the edge their first move traverses, and it is the one an administrator
	// would reach for when they say "nothing leaves the backlog without an
	// assignee".
	transitions, err := ts.WorkflowAdapter.ListTransitions(ctx, def.ID)
	require.NoError(t, err)
	var firstEdge uuid.UUID
	for _, tr := range transitions {
		if tr.FromStateID == byName["backlog"] && tr.ToStateID == byName["todo"] {
			firstEdge = tr.ID
		}
	}
	require.NotEqual(t, uuid.Nil, firstEdge, "the seeded project workflow must carry backlog -> todo")

	tier := adapters.NewWorkflowTierAdapter(generated.New(ts.DB.Pool))
	_, err = tier.CreateGuard(ctx, workflow.Guard{
		TransitionID: firstEdge,
		Class:        workflow.GuardValidatorClass,
		Kind:         workflow.GuardFieldRequired,
		FieldKey:     ptrTo(workflow.FieldAssigneeID),
		Position:     0,
	})
	require.NoError(t, err)

	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, space.ID)
	r := ts.post(t, base+"/projects/items", map[string]any{
		"title": "Born in the workflow", "kind": "task", "priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item: %s", r.Body)
	var created map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &created))
	itemID := created["id"].(string)

	// The item was created with no assignee, so the validator on its first edge
	// must refuse this move.
	//
	// Before the fix this answered 200: the item's status was "open", which names
	// no state, so no edge resolved and the guard never ran.
	require.Equal(t, "backlog", created["status"],
		"a new item must be born in its workflow's initial state, not at a status naming no state")

	r = ts.post(t, fmt.Sprintf("%s/projects/items/%s/status", base, itemID),
		map[string]any{"status": "todo"}, true)
	require.Equal(t, http.StatusUnprocessableEntity, r.StatusCode,
		"a new item's FIRST transition must be gated like every other: %s", r.Body)
	require.Contains(t, string(r.Body), "assignee",
		"the refusal must name the guard that produced it")
}
