package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The fail-closed proofs, against real PostgreSQL and through the real router.
//
// # What was wrong
//
// The workflow-tier engine let an administrator configure guards, conditions,
// validators, approvers and post-functions on a transition. The server then did
// not enforce them on the routes the product actually uses. A move that resolved
// to no edge — because the status named no state, or because the workflow
// defined no such edge — was written unchecked, and a CONDITION was evaluated
// nowhere at all, because the only function that evaluated one had no HTTP
// route. An administrator could configure ADR-0011's own Tier-1 example, see it
// in the admin UI with a badge reading "hides", and get a 2xx.
//
// That is worse than the feature being absent: an unenforceable approval control
// that looks enforceable is a false assurance, not a gap.
//
// # Why these are integration tests
//
// Every one of them is about what the SERVER does when a real client asks. A
// unit test against TierService proves the decision; only this proves the
// decision is reachable, that the route consults it, and that nothing was
// written when it refused. The re-read after each refusal is the half that
// cannot be faked — a handler that answered 422 and wrote anyway would pass a
// status-code assertion.

// wfcSpace seeds the org's workflows, creates a space of the given module
// through the API, and assigns the module's default workflow — the state a
// production space reaches through org provisioning.
func wfcSpace(t *testing.T, ts *testServer, module, name, slug string) (uuid.UUID, string) {
	t.Helper()
	spaceID := wsnegWorkflowSpace(t, ts, module, name, slug)
	return uuid.MustParse(spaceID), fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
}

// wfcEdge returns the id of the named edge in the space module's default
// workflow, failing rather than returning uuid.Nil if it is absent — a test that
// configured a guard on the zero UUID would guard nothing and pass.
func wfcEdge(t *testing.T, ts *testServer, appliesTo, from, to string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	def, err := ts.WorkflowAdapter.GetDefaultWorkflow(ctx, ts.OrgID, appliesTo)
	require.NoError(t, err)

	states, err := ts.WorkflowAdapter.ListStates(ctx, def.ID)
	require.NoError(t, err)
	byID := map[uuid.UUID]string{}
	for _, s := range states {
		byID[s.ID] = s.Name
	}

	transitions, err := ts.WorkflowAdapter.ListTransitions(ctx, def.ID)
	require.NoError(t, err)
	for _, tr := range transitions {
		if byID[tr.FromStateID] == from && byID[tr.ToStateID] == to {
			return tr.ID
		}
	}
	t.Fatalf("the seeded %s workflow must carry %s -> %s", appliesTo, from, to)
	return uuid.Nil
}

// wfcGuard attaches a guard directly through the store, which is what an
// administrator's POST does one layer up.
func wfcGuard(t *testing.T, ts *testServer, g workflow.Guard) workflow.Guard {
	t.Helper()
	created, err := adapters.NewWorkflowTierAdapter(generated.New(ts.DB.Pool)).
		CreateGuard(context.Background(), g)
	require.NoError(t, err)
	return created
}

// wfcStatus reads an entity's stored status back through the API.
func wfcStatus(t *testing.T, ts *testServer, path string) string {
	t.Helper()
	r := ts.get(t, path, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "read back: %s", r.Body)
	return decodeJSONMap(t, r.Body)["status"].(string)
}

// TestWorkflowFailsClosed_IllegalDirectTransitionIsRefusedAndNothingIsWritten
// is the core proof, on the route the frontend actually calls.
//
// # The mutation test
//
// Delete the target-state check in TierService.Gate and the first subtest's
// re-read finds "banana" stored — the Vector /status route wrote any string it
// was given, which is precisely the state this closes. Delete the edge check and
// the second subtest's re-read finds "done", a move the seeded project workflow
// does not define.
func TestWorkflowFailsClosed_IllegalDirectTransitionIsRefusedAndNothingIsWritten(t *testing.T) {
	ts := newTestServer(t)
	_, base := wfcSpace(t, ts, "vector", "Fail Closed Board", "fail-closed-board")

	r := ts.post(t, base+"/projects/items", map[string]any{
		"title": "Governed by its workflow", "kind": "task", "priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item: %s", r.Body)
	itemID := decodeJSONMap(t, r.Body)["id"].(string)
	itemPath := fmt.Sprintf("%s/projects/items/%s", base, itemID)

	// Born in the workflow's initial state, both position columns written.
	require.Equal(t, "backlog", wfcStatus(t, ts, itemPath))

	t.Run("a status the workflow does not name is refused", func(t *testing.T) {
		res := ts.post(t, itemPath+"/status", map[string]any{"status": "banana"}, true)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
			"a status naming no state must be refused: %s", res.Body)
		require.Contains(t, string(res.Body), "banana",
			"the refusal must name the value it refused")

		require.Equal(t, "backlog", wfcStatus(t, ts, itemPath),
			"a refused transition must write nothing — this route used to write any string")
	})

	t.Run("a move the workflow defines no edge for is refused", func(t *testing.T) {
		// backlog has edges to todo and in_progress only. done is reachable, but
		// only through in_progress -> in_review -> done.
		res := ts.post(t, itemPath+"/status", map[string]any{"status": "done"}, true)
		require.Equal(t, http.StatusConflict, res.StatusCode,
			"an undefined edge is the state machine's own refusal: %s", res.Body)
		requireErrorCode(t, res, http.StatusConflict, "INVALID_TRANSITION")

		require.Equal(t, "backlog", wfcStatus(t, ts, itemPath))
	})

	t.Run("a move the workflow does define still succeeds", func(t *testing.T) {
		// Without this the two subtests above would pass against a route that
		// refused everything, which is a broken product rather than an enforced
		// one.
		res := ts.post(t, itemPath+"/status", map[string]any{"status": "todo"}, true)
		require.Equal(t, http.StatusOK, res.StatusCode, "backlog -> todo is a defined edge: %s", res.Body)
		require.Equal(t, "todo", wfcStatus(t, ts, itemPath))
	})
}

// TestWorkflowFailsClosed_StatusAndWorkflowStateAreWrittenTogether is D71 on the
// write path: the two columns recording an entity's position must agree after
// every transition, not just after the ones that happen to take the transactional
// applier.
//
// Reading workflow_state_id straight from the table rather than from the API is
// deliberate. The column is the thing under test, and a read model that derived
// it from `status` would report agreement it had manufactured.
func TestWorkflowFailsClosed_StatusAndWorkflowStateAreWrittenTogether(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()
	_, base := wfcSpace(t, ts, "vector", "Paired Columns", "paired-columns")

	r := ts.post(t, base+"/projects/items", map[string]any{
		"title": "Both columns", "kind": "task", "priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	itemID := uuid.MustParse(decodeJSONMap(t, r.Body)["id"].(string))
	itemPath := fmt.Sprintf("%s/projects/items/%s", base, itemID)

	// stateName reads BOTH columns and resolves the state id to its name, so a
	// mismatch shows up as two different words rather than as a nil pointer.
	stateName := func(t *testing.T) (status, state string) {
		t.Helper()
		var st string
		var wsID *uuid.UUID
		require.NoError(t, ts.DB.Pool.QueryRow(ctx,
			`SELECT status, workflow_state_id FROM project_items WHERE id = $1`, itemID).
			Scan(&st, &wsID))
		require.NotNil(t, wsID,
			"workflow_state_id must never be NULL for an item in a space with a workflow")
		var name string
		require.NoError(t, ts.DB.Pool.QueryRow(ctx,
			`SELECT name FROM workflow_states WHERE id = $1`, *wsID).Scan(&name))
		return st, name
	}

	status, state := stateName(t)
	require.Equal(t, "backlog", status, "born in the initial state")
	require.Equal(t, status, state, "at creation the two columns must already agree")

	// Two hops, because one is not enough to catch the defect: the legacy route
	// wrote `status` alone, so the state id kept pointing at where the item had
	// been.
	for _, next := range []string{"in_progress", "in_review"} {
		res := ts.post(t, itemPath+"/status", map[string]any{"status": next}, true)
		require.Equal(t, http.StatusOK, res.StatusCode, "move to %s: %s", next, res.Body)

		status, state = stateName(t)
		require.Equal(t, next, status)
		require.Equal(t, status, state,
			"status and workflow_state_id must be written together, or the state id means nothing")
	}
}

// TestWorkflowFailsClosed_ConditionRefusesOnTheRealMutationRoute is the defect
// the external audit named most sharply: condition-class guards were
// configurable, schema-validated, audited and rendered in the admin UI with a
// badge reading "hides", and were evaluated on no reachable path.
//
// # The persona
//
// A CONTRIBUTOR proves nothing here — they are refused upstream by the route's
// own access.CapTransitionAnyItem check and never reach a guard. The subject has
// to clear that floor and fail the condition, so this uses an AGENT who is not
// the item's assignee against an actor_is_assignee condition. The same agent is
// then made the assignee and allowed through, which is what makes it a gate
// rather than a blanket refusal.
//
// # The mutation test
//
// Delete the GuardConditionClass evaluation in TierService.Gate and the first
// half answers 200 and the item moves.
func TestWorkflowFailsClosed_ConditionRefusesOnTheRealMutationRoute(t *testing.T) {
	ts := newTestServer(t)
	spaceID, base := wfcSpace(t, ts, "vector", "Condition Board", "condition-board")

	agent, agentTok := wsnegPersona(t, ts, spaceID, access.RoleAgent)

	edge := wfcEdge(t, ts, "project_items", "backlog", "in_progress")
	wfcGuard(t, ts, workflow.Guard{
		TransitionID: edge,
		Class:        workflow.GuardConditionClass,
		Kind:         workflow.GuardActorIsAssignee,
		Position:     0,
	})

	newItem := func(t *testing.T, assignee *uuid.UUID) string {
		t.Helper()
		body := map[string]any{"title": "Condition subject", "kind": "task", "priority": "medium"}
		if assignee != nil {
			body["assignee_id"] = assignee.String()
		}
		r := ts.post(t, base+"/projects/items", body, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
		return decodeJSONMap(t, r.Body)["id"].(string)
	}

	t.Run("an agent who is not the assignee is refused", func(t *testing.T) {
		id := newItem(t, nil)
		path := fmt.Sprintf("%s/projects/items/%s", base, id)

		res := ts.postAs(t, agentTok, path+"/status", map[string]any{"status": "in_progress"})
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode,
			"a configured condition must refuse on the route the product uses: %s", res.Body)
		require.Contains(t, string(res.Body), "assignee")

		require.Equal(t, "backlog", wfcStatus(t, ts, path),
			"the refusal must have written nothing")
	})

	t.Run("the assignee, who satisfies the same condition, is allowed", func(t *testing.T) {
		id := newItem(t, &agent.ID)
		path := fmt.Sprintf("%s/projects/items/%s", base, id)

		res := ts.postAs(t, agentTok, path+"/status", map[string]any{"status": "in_progress"})
		require.Equal(t, http.StatusOK, res.StatusCode,
			"the condition must gate, not blanket-refuse: %s", res.Body)
		require.Equal(t, "in_progress", wfcStatus(t, ts, path))
	})
}

// TestWorkflowFailsClosed_OfferedTransitionsAndTheMutationRouteAgree is the
// two-part proof, and neither half is sufficient alone.
//
// Routing the offering path to an endpoint makes conditions visible to the
// client. It does NOT make them enforced — a client is not a security boundary,
// and the mutation route is reachable with curl. So this asserts both: the
// endpoint omits the hidden move, AND posting that same move directly is
// refused.
func TestWorkflowFailsClosed_OfferedTransitionsAndTheMutationRouteAgree(t *testing.T) {
	ts := newTestServer(t)
	spaceID, base := wfcSpace(t, ts, "vector", "Offer Board", "offer-board")

	_, agentTok := wsnegPersona(t, ts, spaceID, access.RoleAgent)

	// Hide backlog -> in_progress from anyone who is not the assignee, and leave
	// backlog -> todo alone, so "hidden" is distinguishable from "nothing was
	// offered at all".
	wfcGuard(t, ts, workflow.Guard{
		TransitionID: wfcEdge(t, ts, "project_items", "backlog", "in_progress"),
		Class:        workflow.GuardConditionClass,
		Kind:         workflow.GuardActorIsAssignee,
	})

	r := ts.post(t, base+"/projects/items", map[string]any{
		"title": "Offered moves", "kind": "task", "priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	itemID := decodeJSONMap(t, r.Body)["id"].(string)
	itemPath := fmt.Sprintf("%s/projects/items/%s", base, itemID)

	res := ts.getAs(t, agentTok,
		fmt.Sprintf("%s/workflow/entities/item/%s/transitions", base, itemID))
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	var offering workflow.Offering
	require.NoError(t, json.Unmarshal(res.Body, &offering))
	require.False(t, offering.NoWorkflow)
	require.Equal(t, "backlog", offering.CurrentStatus)

	offered := map[string]bool{}
	for _, o := range offering.Transitions {
		offered[o.ToStatus] = true
	}
	require.True(t, offered["todo"], "an unguarded move must still be offered")
	require.False(t, offered["in_progress"],
		"a condition the actor fails must remove the move from the offer")

	// Half two. The client was never shown in_progress; the server refuses it
	// anyway, because the client is not what enforces this.
	direct := ts.postAs(t, agentTok, itemPath+"/status", map[string]any{"status": "in_progress"})
	require.Equal(t, http.StatusUnprocessableEntity, direct.StatusCode,
		"a move that was not offered must still be refused when posted directly: %s", direct.Body)
	require.Equal(t, "backlog", wfcStatus(t, ts, itemPath))
}

// TestWorkflowFailsClosed_UntouchedSpaceIsUnaffected is the guarantee on the
// other side: a space with NO workflow assigned behaves exactly as it did
// before workflows could be enforced.
//
// testutil.CreateTestSpace writes the row directly and never assigns a workflow,
// which is what makes it the right fixture here — it is the same shape as a
// space whose best-effort assignment failed, or whose workflow was deleted
// (spaces.workflow_id is ON DELETE SET NULL).
//
// Both halves matter. The Vector route had no state machine at all and must
// still have none; the Beacon route had a hardcoded one, including the
// migration-029 reverse edges, and must still run it.
func TestWorkflowFailsClosed_UntouchedSpaceIsUnaffected(t *testing.T) {
	ts := newTestServer(t)
	owner := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)

	t.Run("vector: no workflow means no state machine, exactly as before", func(t *testing.T) {
		space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, owner.ID, "vector")
		base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, space.ID)

		r := ts.post(t, base+"/projects/items", map[string]any{
			"title": "Ungoverned", "kind": "task", "priority": "medium",
		}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
		created := decodeJSONMap(t, r.Body)
		require.Equal(t, "open", created["status"],
			"with no workflow to place it, the item keeps the literal default")
		path := fmt.Sprintf("%s/projects/items/%s", base, created["id"].(string))

		// The pre-workflow behaviour of this route was: write whatever you are
		// given, with no validation whatsoever. That must be unchanged.
		res := ts.post(t, path+"/status", map[string]any{"status": "anything_at_all"}, true)
		require.Equal(t, http.StatusOK, res.StatusCode,
			"an unworkflowed vector space must accept any status, as it always did: %s", res.Body)
		require.Equal(t, "anything_at_all", wfcStatus(t, ts, path))
	})

	t.Run("beacon: no workflow means the hardcoded state machine still decides", func(t *testing.T) {
		space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, owner.ID, "beacon")
		base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, space.ID)

		r := ts.post(t, base+"/tickets", map[string]any{
			"title": "Ungoverned ticket", "priority": "medium",
		}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
		created := decodeJSONMap(t, r.Body)
		require.Equal(t, "open", created["status"])
		path := fmt.Sprintf("%s/tickets/%s", base, created["id"].(string))

		// open -> resolved skips in_progress. tickets.validTransitions has always
		// refused it with 409 INVALID_TRANSITION, and still must.
		res := ts.post(t, path+"/status", map[string]any{"status": "resolved"}, true)
		requireErrorCode(t, res, http.StatusConflict, "INVALID_TRANSITION")
		require.Equal(t, "open", wfcStatus(t, ts, path))

		// And the migration-029 reverse edges must not have been dropped by the
		// unification: resolved -> in_progress is a step BACK that the hardcoded
		// map deliberately permits.
		for _, hop := range []string{"in_progress", "resolved", "in_progress"} {
			res = ts.post(t, path+"/status", map[string]any{"status": hop}, true)
			require.Equal(t, http.StatusOK, res.StatusCode, "hop to %s: %s", hop, res.Body)
		}
		require.Equal(t, "in_progress", wfcStatus(t, ts, path),
			"the reverse edges migration 029 added must survive the unification")
	})
}

// TestWorkflowFailsClosed_TicketStateMachineMatchesTheSeededWorkflow is what
// makes the Beacon unification safe to claim rather than merely hope for.
//
// A beacon space WITH the default workflow is now adjudicated by that workflow;
// a space without one still runs tickets.validTransitions. Those two must agree
// edge for edge, or the same product answers the same question two ways
// depending on a column nobody looks at. Migration 029 and seedTicketWorkflow
// were written to make them agree; nothing checked it until now.
//
// It drives both machines through the ROUTE rather than comparing tables,
// because the claim is about behaviour: for every ordered pair of ticket
// statuses, a workflowed space and an unworkflowed space must answer alike.
func TestWorkflowFailsClosed_TicketStateMachineMatchesTheSeededWorkflow(t *testing.T) {
	ts := newTestServer(t)
	owner := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)

	_, governedBase := wfcSpace(t, ts, "beacon", "Governed Desk", "governed-desk")
	plain := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, owner.ID, "beacon")
	plainBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, plain.ID)

	statuses := []string{"open", "in_progress", "resolved", "closed"}

	// accepts reports whether a fresh ticket in `base`, walked to `from`, may
	// then move to `to`. A fresh ticket per probe, because a refused move in one
	// probe must not colour the next.
	accepts := func(t *testing.T, base, from, to string) bool {
		t.Helper()
		r := ts.post(t, base+"/tickets", map[string]any{
			"title": fmt.Sprintf("probe %s to %s", from, to), "priority": "medium",
		}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
		path := fmt.Sprintf("%s/tickets/%s", base, decodeJSONMap(t, r.Body)["id"].(string))

		if from != "open" {
			// Every state is reachable from open in one hop except `resolved`,
			// which needs in_progress first.
			if from == "resolved" {
				res := ts.post(t, path+"/status", map[string]any{"status": "in_progress"}, true)
				require.Equal(t, http.StatusOK, res.StatusCode, "setup hop: %s", res.Body)
			}
			res := ts.post(t, path+"/status", map[string]any{"status": from}, true)
			require.Equal(t, http.StatusOK, res.StatusCode, "setup to %s: %s", from, res.Body)
		}

		res := ts.post(t, path+"/status", map[string]any{"status": to}, true)
		return res.StatusCode == http.StatusOK
	}

	for _, from := range statuses {
		for _, to := range statuses {
			if from == to {
				// A self-move is a no-op both sides accept, and says nothing
				// about the edge sets.
				continue
			}
			t.Run(fmt.Sprintf("%s_to_%s", from, to), func(t *testing.T) {
				require.Equal(t,
					accepts(t, plainBase, from, to),
					accepts(t, governedBase, from, to),
					"the hardcoded map and the seeded ticket workflow must agree about %s -> %s; "+
						"migration 029 and seedTicketWorkflow keep them in lockstep and one was edited alone",
					from, to)
			})
		}
	}
}
