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

// D72, written as the test that would prove it fixed.
//
// A new project item is created at status "open". The seeded project workflow's
// states are backlog/todo/in_progress/in_review/done, so "open" names no state,
// so the item's FIRST transition resolves no edge — and a transition that
// resolves no edge is not gated. Guards, approvals and post-functions an
// administrator configured on the initial edge silently do not apply to it.
// Every subsequent move is gated normally.
//
// That is a hole in the enforcement PR #86 shipped, and this phase's brief made
// disposing of it mandatory. It is recorded rather than closed, and this test is
// the record: prose in a ledger describes a defect, a failing test specifies it.
//
// # Why it is not closed here
//
// The ledger and the comment in workflow_tiers_integration_test.go both blamed
// migration 014's column default. That is wrong, and it matters, because it is
// the only candidate fix that would have been cheap: CreateProjectItem names
// `status` in its INSERT column list, so the DEFAULT is never evaluated by the
// application and changing it would alter nothing but raw-SQL test fixtures.
// The value is written at internal/core/projects/item.go:114.
//
// The candidates that would actually work were each rejected for a stated
// reason, not for effort:
//
//   - Falling back to the workflow's INITIAL state inside Gate conflates "never
//     transitioned" with "the status names no state for some other reason" —
//     precisely the case tier_service.go:246 exists to handle, since a state can
//     be renamed out from under an item. It would run the initial edge's
//     validators, approvals and POST-FUNCTIONS for a move that has nothing to do
//     with them; post-functions mutate, so this applies the WRONG rule rather
//     than a stricter one. It also fails a subtest of
//     TestGate_UntouchedWorkflowIsUnaffected, and making that pass would mean
//     weakening the assertion CLAUDE.md §2 forbids weakening.
//
//   - Using workflow_state_id as the discriminator is broken by D71: the legacy
//     /status route writes `status` alone, so the column stays NULL after
//     arbitrarily many moves and an item that went open -> todo -> in_progress
//     would still resolve "from = initial" on its fourth move. Making it correct
//     requires the D71 drift repair, which PR #86 explicitly deferred.
//
//   - Being BORN in the workflow's initial state is the right fix and is
//     sketched in known-issues #30. It is out of scope for THIS phase because it
//     changes the default status of every new project item across the product —
//     board columns, saved-view filters and the item detail's status <select>
//     all enumerate the literal "open" — while still not repairing a single row
//     created before it. A partial close that reads as a full one is worse than
//     an honest ledger entry.
//
// SKIP: a new item's status names no state in its own workflow, so its first
// transition resolves no edge and no tier applies. Closing it requires items and
// tickets to be created in their space workflow's initial state, writing both
// status and workflow_state_id — which changes the default status of every new
// project item product-wide and does not repair existing rows.
// Issue: docs/known-issues.md #30.
// Re-enable when: ItemService.CreateItem and TicketService.Create resolve the
// space workflow's initial state at creation (option (e) in #30), together with
// the backfill decision that entry raises for items already at "open".
func TestTierAPI_ANewItemsFirstTransitionIsGated(t *testing.T) {
	t.Skip("SKIP: D72 — a new item's first transition is ungated; see known-issues #30")

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
	// TODAY this answers 200: the item's status is "open", which names no state,
	// so no edge resolves and the guard never runs. When the fix lands the
	// status will be "backlog", the edge will resolve, and the guard will refuse
	// with 422.
	require.Equal(t, "backlog", created["status"],
		"a new item must be born in its workflow's initial state, not at a status naming no state")

	r = ts.post(t, fmt.Sprintf("%s/projects/items/%s/status", base, itemID),
		map[string]any{"status": "todo"}, true)
	require.Equal(t, http.StatusUnprocessableEntity, r.StatusCode,
		"a new item's FIRST transition must be gated like every other: %s", r.Body)
	require.Contains(t, string(r.Body), "assignee",
		"the refusal must name the guard that produced it")
}
