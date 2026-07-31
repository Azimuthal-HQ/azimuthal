package api_test

// Domain-refusal coverage for the workflow definition surface
// (internal/core/api/workflows/handler.go) and the space governance surface
// (internal/core/api/spaces/handler.go).
//
// The previous pass (workflows_spaces_negative_integration_test.go) took the
// refusals the handlers write themselves: a path parameter that will not
// parse, a body that will not decode, a validation rule, a capability gate,
// the state machine's own 409. This file takes the layer under those — the
// refusals that come from the DATABASE and reach the caller through the
// handler's error arm. Every one of them is a real constraint doing its job:
//
//   workflows           UNIQUE (org_id, name), CHECK applies_to
//   workflow_states     UNIQUE (workflow_id, name), UNIQUE (workflow_id,
//                       position), the partial unique index idx_workflow_initial,
//                       CHECK category
//   workflow_transitions UNIQUE (workflow_id, from, to), FK to workflow_states
//   tickets/project_items FK workflow_state_id → workflow_states (NO ACTION)
//   spaces              idx_spaces_org_key
//   space_members       UNIQUE (space_id, user_id)
//
// A note on what these tests assert, because it matters. Every handler below
// funnels its repository error into a single 500 INTERNAL_ERROR arm, so a
// client-caused conflict — a duplicate workflow name, a key already taken —
// is reported as a server fault rather than a 409. That mapping is wrong and
// is reported to the maintainer rather than fixed here (spec §5: a
// disagreement that would change a decision is raised, not resolved). These
// tests pin the behaviour that actually matters and is NOT in dispute: the
// write was refused, the response was not a success, and the stored record is
// exactly what it was before the request. Every one of them carries a
// read-back for that reason — the status assertion alone would survive a
// handler that answered 500 *after* corrupting the row.
//
// Each test states the defect it catches. None of them would pass with the
// error arm they target deleted: deleting it drops the handler through to the
// success response (201/200/204) with a body describing a row that does not
// exist, and both the status assertion and the read-back fail.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// wfsdWorkflowBase is the org-scoped workflow definition surface.
func wfsdWorkflowBase(ts *testServer) string {
	return fmt.Sprintf("/api/v1/orgs/%s/workflows", ts.OrgID)
}

// wfsdCreateWorkflow creates a workflow through the API and returns its ID.
func wfsdCreateWorkflow(t *testing.T, ts *testServer, name, appliesTo string) string {
	t.Helper()
	r := ts.post(t, wfsdWorkflowBase(ts), map[string]any{"name": name, "applies_to": appliesTo}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create workflow %q: %s", name, r.Body)
	return decodeJSONMap(t, r.Body)["id"].(string)
}

// wfsdCreateState adds a state to a workflow through the API and returns its ID.
func wfsdCreateState(t *testing.T, ts *testServer, wfID, name, category string, position int, initial bool) string {
	t.Helper()
	r := ts.post(t, wfsdWorkflowBase(ts)+"/"+wfID+"/states", map[string]any{
		"name": name, "category": category, "position": position, "is_initial": initial,
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create state %q: %s", name, r.Body)
	return decodeJSONMap(t, r.Body)["id"].(string)
}

// wfsdListLen returns the length of a JSON array response, failing on any
// status other than 200.
func wfsdListLen(t *testing.T, ts *testServer, path string) int {
	t.Helper()
	r := ts.get(t, path, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "GET %s: %s", path, r.Body)
	var rows []map[string]any
	requireJSONList(t, r.Body, &rows)
	return len(rows)
}

// wfsdAssignWorkflowToSpace points a space at an arbitrary workflow. There is
// no API route for this — production reaches the same state through
// AssignDefaultWorkflowToSpace at space creation — so the fixture writes the
// column the same query does (queries/workflows.sql, AssignWorkflowToSpace).
func wfsdAssignWorkflowToSpace(t *testing.T, ts *testServer, spaceID, workflowID string) {
	t.Helper()
	_, err := ts.DB.Pool.Exec(t.Context(),
		`UPDATE spaces SET workflow_id = $1 WHERE id = $2`,
		uuid.MustParse(workflowID), uuid.MustParse(spaceID))
	require.NoError(t, err)
}

// ─── Workflows: the definition surface ────────────────────────────────────────

// TestWfSpaceDomain_WorkflowCreate_RefusedByTheDatabaseWritesNothing drives
// CreateWorkflow's repository error arm with the two refusals a client can
// actually cause: a name already used in the organisation (UNIQUE
// (org_id, name), migration 016) and an applies_to outside the CHECK.
//
// Defect it catches: the handler validates only that name and applies_to are
// non-empty, so the database is the sole thing standing between the request
// and a duplicate or unusable workflow row. Delete the error arm and the
// handler answers 201 with a workflow.Workflow whose ID is the zero UUID —
// the adapter only assigns w.ID from the RETURNING row on success — so the
// caller is handed a workflow that was never written. The count read-back is
// what catches it; the status assertion alone would not distinguish "refused"
// from "refused after writing".
func TestWfSpaceDomain_WorkflowCreate_RefusedByTheDatabaseWritesNothing(t *testing.T) {
	ts := newTestServer(t)
	base := wfsdWorkflowBase(ts)

	wfsdCreateWorkflow(t, ts, "Domain WF", "tickets")
	require.Equal(t, 1, wfsdListLen(t, ts, base), "one workflow to start")

	// Same org, same name: UNIQUE (org_id, name).
	wsnegRequireError(t, ts.post(t, base, map[string]any{
		"name": "Domain WF", "applies_to": "project_items",
	}, true), http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create workflow")

	// applies_to outside CHECK (applies_to IN ('tickets','project_items','both')).
	wsnegRequireError(t, ts.post(t, base, map[string]any{
		"name": "Bogus Target WF", "applies_to": "unicorns",
	}, true), http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create workflow")

	require.Equal(t, 1, wfsdListLen(t, ts, base),
		"neither refused create may have written a workflow")

	// The control: 'both' IS in the CHECK, so the refusal above is the
	// constraint firing rather than the handler rejecting every applies_to it
	// was not already tested with.
	wfsdCreateWorkflow(t, ts, "Both Targets WF", "both")
	require.Equal(t, 2, wfsdListLen(t, ts, base))
}

// TestWfSpaceDomain_WorkflowUpdate_UnknownIDAndNameCollisionAreRefused drives
// UpdateWorkflow's error arm.
//
// Defect it catches: UpdateWorkflow is a `:one` with RETURNING, so an ID that
// names no workflow comes back as pgx.ErrNoRows — the ONLY signal that the
// PUT changed nothing. Delete the error arm and the handler answers 200 with
// the request echoed back as though it had been saved: the client sees its
// own edit confirmed against a workflow that does not exist. The second half
// pins the same arm on a name collision, where the read-back proves the
// target kept its own name rather than being renamed and then reported as a
// failure.
func TestWfSpaceDomain_WorkflowUpdate_UnknownIDAndNameCollisionAreRefused(t *testing.T) {
	ts := newTestServer(t)
	base := wfsdWorkflowBase(ts)

	first := wfsdCreateWorkflow(t, ts, "Original WF", "tickets")
	wfsdCreateWorkflow(t, ts, "Occupied Name", "project_items")

	// A well-formed ID naming no workflow.
	//
	// This answered 500 INTERNAL_ERROR until P-W PR-B, because the handler ran
	// the UPDATE and reported pgx.ErrNoRows as an internal failure. Closing D74
	// put an org-scope check in front of it, so an id that names no workflow OF
	// THIS ORG — which includes one that names no workflow at all — is now a
	// 404, and one instance of the known-issues #24 class (a handler answering
	// 500 where its own annotation promises a 4xx) is closed with it.
	//
	// The property this test exists to pin is unchanged and unweakened: an
	// update naming no workflow must be REFUSED, never echoed back as saved.
	ghost := uuid.New().String()
	wsnegRequireError(t, ts.requestAs(t, ts.Token, http.MethodPut, base+"/"+ghost,
		map[string]any{"name": "Resurrected", "applies_to": "tickets"}),
		http.StatusNotFound, "NOT_FOUND", "workflow not found")

	// The refused PUT did not conjure the workflow into existence.
	wsnegRequireError(t, ts.get(t, base+"/"+ghost, true),
		http.StatusNotFound, "NOT_FOUND", "workflow not found")

	// Renaming onto another workflow's name: UNIQUE (org_id, name).
	wsnegRequireError(t, ts.requestAs(t, ts.Token, http.MethodPut, base+"/"+first,
		map[string]any{"name": "Occupied Name", "applies_to": "tickets"}),
		http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update workflow")

	r := ts.get(t, base+"/"+first, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "Original WF", decodeJSONMap(t, r.Body)["name"],
		"a refused rename must leave the workflow's name alone")
	require.Equal(t, 2, wfsdListLen(t, ts, base), "and must not have added one")
}

// TestWfSpaceDomain_StateCreate_EveryStateConstraintRefusesTheWrite drives
// CreateState's error arm across all four constraints workflow_states carries.
// They are grouped deliberately: the handler has ONE error arm for all of
// them, and a test that drove only the duplicate name would leave the other
// three constraints unexercised while the arm read as covered.
//
// Defect it catches: the handler checks only that name and category are
// non-empty. Nothing in Go stops a second initial state, a duplicate position,
// or a category the board cannot render — idx_workflow_initial, UNIQUE
// (workflow_id, position) and the category CHECK do. Delete the error arm and
// each of these answers 201 with a state whose ID is the zero UUID, and the
// board silently gains a column that is not in the workflow. Two of these are
// worse than cosmetic: a second is_initial row makes GetInitialState
// ambiguous, and a duplicate position makes the column order
// non-deterministic.
func TestWfSpaceDomain_StateCreate_EveryStateConstraintRefusesTheWrite(t *testing.T) {
	ts := newTestServer(t)
	wfID := wfsdCreateWorkflow(t, ts, "State Constraints WF", "tickets")
	statesPath := wfsdWorkflowBase(ts) + "/" + wfID + "/states"

	wfsdCreateState(t, ts, wfID, "open", "todo", 0, true)

	cases := []struct {
		name       string
		body       map[string]any
		constraint string
	}{
		{
			"duplicate state name",
			map[string]any{"name": "open", "category": "todo", "position": 5},
			"UNIQUE (workflow_id, name)",
		},
		{
			"duplicate position",
			map[string]any{"name": "closed", "category": "done", "position": 0},
			"UNIQUE (workflow_id, position)",
		},
		{
			"second initial state",
			map[string]any{"name": "triage", "category": "todo", "position": 7, "is_initial": true},
			"idx_workflow_initial",
		},
		{
			"category outside the CHECK",
			map[string]any{"name": "parked", "category": "limbo", "position": 8},
			"CHECK category IN ('todo','in_progress','done')",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wsnegRequireError(t, ts.post(t, statesPath, tc.body, true),
				http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create state")
		})
	}

	require.Equal(t, 1, wfsdListLen(t, ts, statesPath),
		"not one of the four refused states may have been written")

	// The control: a state that breaks none of the four constraints is
	// accepted, so the refusals above are the constraints firing rather than
	// state creation being broken outright.
	wfsdCreateState(t, ts, wfID, "resolved", "done", 1, false)
	require.Equal(t, 2, wfsdListLen(t, ts, statesPath))
}

// TestWfSpaceDomain_StateDelete_RefusedWhileATicketSitsInIt drives
// DeleteState's error arm through tickets.workflow_state_id, which references
// workflow_states with no ON DELETE action (migration 016) — so PostgreSQL
// refuses to remove a state that a ticket is currently in.
//
// Defect it catches: the FK is the only thing preventing a state deletion
// from stranding every ticket in it. Delete the handler's error arm and the
// endpoint answers 204 — the operator is told the column is gone, the audit
// trail of their change says it succeeded, and the state is still there with
// the ticket still in it. The read-backs are the assertion: the state is still
// listed and the ticket has not moved.
func TestWfSpaceDomain_StateDelete_RefusedWhileATicketSitsInIt(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "beacon", "State FK Desk", "state-fk-desk")
	spaceBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	states := wsnegStateIDs(t, ts, "tickets")
	ticketID := wsnegCreateTicket(t, ts, spaceBase, "Occupant")

	// Move the ticket so tickets.workflow_state_id actually references the
	// state under test — a ticket that has never transitioned holds NULL and
	// would not block anything.
	r := ts.post(t, fmt.Sprintf("%s/tickets/%s/workflow-state", spaceBase, ticketID),
		map[string]any{"state_id": states["in_progress"]}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "seed transition: %s", r.Body)
	require.Equal(t, "in_progress", decodeJSONMap(t, r.Body)["status"])

	wf, err := ts.WorkflowAdapter.GetDefaultWorkflow(t.Context(), ts.OrgID, "tickets")
	require.NoError(t, err)
	statesPath := fmt.Sprintf("%s/%s/states", wfsdWorkflowBase(ts), wf.ID)
	before := wfsdListLen(t, ts, statesPath)

	wsnegRequireError(t, ts.delete(t, fmt.Sprintf("%s/%s", statesPath, states["in_progress"]), true),
		http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete state")

	require.Equal(t, before, wfsdListLen(t, ts, statesPath),
		"the refused delete must have removed nothing")

	r = ts.get(t, fmt.Sprintf("%s/tickets/%s", spaceBase, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "in_progress", decodeJSONMap(t, r.Body)["status"],
		"the ticket must still be in the state that could not be deleted")
}

// TestWfSpaceDomain_WorkflowDelete_RefusedWhileATicketSitsInItsStates drives
// DeleteWorkflow's error arm. workflow_states cascades from workflows, but
// tickets.workflow_state_id does not cascade — so deleting the workflow tries
// to cascade away a state a ticket references, and the FK refuses the whole
// statement.
//
// Defect it catches: this is the state machine every ticket in the module
// moves through. Delete the error arm and the endpoint answers 204 for a
// workflow that is still assigned to the space, so the administration UI shows
// it gone while every board in the organisation keeps rendering from it. The
// read-back through GET is what catches that; the 204 alone reads as success.
func TestWfSpaceDomain_WorkflowDelete_RefusedWhileATicketSitsInItsStates(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "beacon", "WF FK Desk", "wf-fk-desk")
	spaceBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	states := wsnegStateIDs(t, ts, "tickets")
	ticketID := wsnegCreateTicket(t, ts, spaceBase, "Occupant")

	r := ts.post(t, fmt.Sprintf("%s/tickets/%s/workflow-state", spaceBase, ticketID),
		map[string]any{"state_id": states["in_progress"]}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "seed transition: %s", r.Body)

	wf, err := ts.WorkflowAdapter.GetDefaultWorkflow(t.Context(), ts.OrgID, "tickets")
	require.NoError(t, err)
	wfPath := fmt.Sprintf("%s/%s", wfsdWorkflowBase(ts), wf.ID)

	wsnegRequireError(t, ts.delete(t, wfPath, true),
		http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete workflow")

	r = ts.get(t, wfPath, true)
	require.Equal(t, http.StatusOK, r.StatusCode,
		"the workflow must have survived the refused delete: %s", r.Body)
	require.Equal(t, "Default Service Desk", decodeJSONMap(t, r.Body)["name"])

	// And the space still resolves through it.
	r = ts.get(t, spaceBase+"/workflow", true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, wf.ID.String(), decodeJSONMap(t, r.Body)["id"],
		"the space is still pointed at the workflow that could not be deleted")
}

// TestWfSpaceDomain_TransitionCreate_UnknownEndpointAndDuplicateEdgeRefused
// drives CreateTransition's error arm: an edge whose from_state_id names no
// state (FK to workflow_states) and a second copy of an edge that already
// exists (UNIQUE (workflow_id, from_state_id, to_state_id)).
//
// Defect it catches: the handler validates only that the transition has a
// name — the two state IDs are taken on trust and written straight through.
// Delete the error arm and the first case answers 201 for an edge the
// database refused, and the second silently duplicates an edge, which
// ValidateTransition then walks twice. The transition count read-back is the
// assertion: exactly one edge exists at the end, not two and not three.
func TestWfSpaceDomain_TransitionCreate_UnknownEndpointAndDuplicateEdgeRefused(t *testing.T) {
	ts := newTestServer(t)
	wfID := wfsdCreateWorkflow(t, ts, "Edge WF", "tickets")
	from := wfsdCreateState(t, ts, wfID, "open", "todo", 0, true)
	to := wfsdCreateState(t, ts, wfID, "done", "done", 1, false)
	transitionsPath := wfsdWorkflowBase(ts) + "/" + wfID + "/transitions"

	// from_state_id names no state at all: FK violation.
	wsnegRequireError(t, ts.post(t, transitionsPath, map[string]any{
		"name": "From Nowhere", "from_state_id": uuid.New(), "to_state_id": to,
	}, true), http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create transition")

	// to_state_id names no state: the other end of the same FK.
	wsnegRequireError(t, ts.post(t, transitionsPath, map[string]any{
		"name": "To Nowhere", "from_state_id": from, "to_state_id": uuid.New(),
	}, true), http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create transition")

	require.Equal(t, 0, wfsdListLen(t, ts, transitionsPath),
		"neither refused edge may have been written")

	r := ts.post(t, transitionsPath, map[string]any{
		"name": "Finish", "from_state_id": from, "to_state_id": to,
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "the real edge: %s", r.Body)

	// The same pair again, under a different name: UNIQUE (workflow_id, from, to).
	wsnegRequireError(t, ts.post(t, transitionsPath, map[string]any{
		"name": "Finish Again", "from_state_id": from, "to_state_id": to,
	}, true), http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create transition")

	require.Equal(t, 1, wfsdListLen(t, ts, transitionsPath),
		"the duplicate edge must not have been written beside the real one")
}

// ─── Workflows: the transition endpoints' initial-state fallback ──────────────

// TestWfSpaceDomain_TicketTransition_WorkflowWithNoInitialStateIsRefused drives
// the failure arm of the initial-state fallback in
// ApplyWorkflowTransitionToTicket.
//
// A ticket that has never been transitioned holds NULL in workflow_state_id,
// so the handler asks the workflow for its initial state to know where the
// ticket is starting from. A workflow with no initial state — nothing in the
// schema requires one; idx_workflow_initial caps it at one, not at least one —
// makes that lookup fail.
//
// Defect it catches: without the error arm, `initial` is the nil *State and
// the next line dereferences it — the request panics rather than answering.
// The failure is also silent in the ordinary tests, because every seeded
// workflow has an initial state; only a workflow assembled by hand reaches
// it. The read-back proves the refusal left the ticket alone.
func TestWfSpaceDomain_TicketTransition_WorkflowWithNoInitialStateIsRefused(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "beacon", "No Initial Desk", "no-initial-desk")
	spaceBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	ticketID := wsnegCreateTicket(t, ts, spaceBase, "Nowhere to start")

	// A workflow whose only state is explicitly not initial.
	strayWF := wfsdCreateWorkflow(t, ts, "No Initial WF", "tickets")
	target := wfsdCreateState(t, ts, strayWF, "somewhere", "todo", 0, false)
	wfsdAssignWorkflowToSpace(t, ts, spaceID, strayWF)

	wsnegRequireError(t, ts.post(t, fmt.Sprintf("%s/tickets/%s/workflow-state", spaceBase, ticketID),
		map[string]any{"state_id": target}, true),
		http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get initial state")

	r := ts.get(t, fmt.Sprintf("%s/tickets/%s", spaceBase, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "open", decodeJSONMap(t, r.Body)["status"],
		"a ticket whose workflow has no initial state must not have moved")
}

// TestWfSpaceDomain_ItemTransition_WorkflowWithNoInitialStateIsRefused is the
// project-item twin. tickets and project_items stay split (ADR-0003), so the
// two handlers carry separate copies of this fallback — a fix or a regression
// in one and not the other is exactly the drift this pair exists to catch.
func TestWfSpaceDomain_ItemTransition_WorkflowWithNoInitialStateIsRefused(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "vector", "No Initial Board", "no-initial-board")
	spaceBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	itemID := wsnegCreateItem(t, ts, spaceBase, "Nowhere to start")

	strayWF := wfsdCreateWorkflow(t, ts, "No Initial Item WF", "project_items")
	target := wfsdCreateState(t, ts, strayWF, "somewhere", "todo", 0, false)
	wfsdAssignWorkflowToSpace(t, ts, spaceID, strayWF)

	wsnegRequireError(t, ts.post(t, fmt.Sprintf("%s/projects/items/%s/workflow-state", spaceBase, itemID),
		map[string]any{"state_id": target}, true),
		http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get initial state")

	r := ts.get(t, fmt.Sprintf("%s/projects/items/%s", spaceBase, itemID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "open", decodeJSONMap(t, r.Body)["status"],
		"an item whose workflow has no initial state must not have moved")
}

// ─── Spaces: the create-path error triage ─────────────────────────────────────

// TestWfSpaceDomain_SpaceCreate_UnexpectedDatabaseErrorIsNotAConflict drives
// the `!isUnique` arm of Create's error switch, and pins it against the two
// conflict arms beside it.
//
// The switch has three outcomes for one error value: a key conflict (409, or
// a silent retry for a derived key), a slug conflict (409), and everything
// else (500). A NUL byte in the name is the everything-else case — PostgreSQL
// rejects a NUL in a text value outright, which is an encoding error and not
// a constraint violation at all.
//
// Defect it catches: the arms are distinguished by inspecting the pgconn
// error, so an error that is NOT a unique violation must not be read as one.
// Collapse the switch — treat any error as a conflict — and this request
// answers 409 "a space with this key already exists" for a key nothing else
// holds, sending the operator to hunt a collision that does not exist. The
// two controls in the same test are what make that assertion mean something:
// with the discrimination intact, the genuine key collision beside it still
// answers 409.
func TestWfSpaceDomain_SpaceCreate_UnexpectedDatabaseErrorIsNotAConflict(t *testing.T) {
	ts := newTestServer(t)
	spacesPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)

	// Control one: an ordinary space, holding key DUP.
	r := ts.post(t, spacesPath, map[string]any{
		"name": "Holder", "slug": "holder", "type": "vector", "key": "DUP",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "control space: %s", r.Body)

	// Control two: an explicit key already held is a client conflict — 409,
	// not 500. (idx_spaces_org_key.)
	wsnegRequireError(t, ts.post(t, spacesPath, map[string]any{
		"name": "Key Thief", "slug": "key-thief", "type": "vector", "key": "DUP",
	}, true), http.StatusConflict, "CONFLICT",
		"a space with this key already exists in the organization")

	// The case under test: not a constraint violation at all.
	wsnegRequireError(t, ts.post(t, spacesPath, map[string]any{
		"name": "Null\x00Byte", "slug": "null-byte", "type": "vector", "key": "NB",
	}, true), http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create space")

	// Only the control space exists: neither refusal wrote a row.
	require.Equal(t, 1, wfsdListLen(t, ts, spacesPath),
		"a refused create must leave the directory as it was")
}

// TestWfSpaceDomain_SpaceCreate_WithNoOrgDefaultTeamIsRefused drives
// resolveOwnerTeam's GetDefault failure arm: a request that names no
// owner_team_id falls back to the organisation's default team, and an org
// without one cannot answer.
//
// Defect it catches: every space row carries a NOT NULL owner_team_id, and
// the ownership is what the whole ADR-0007 creation authority is decided
// against. Delete this arm and the zero-value teams.Team flows on: the
// authority check consults a team with a nil ID, and the insert either trips
// the owner_team_id FK or — worse, if the FK ever relaxed — writes a space
// owned by nobody. The directory read-back proves the refusal wrote nothing.
func TestWfSpaceDomain_SpaceCreate_WithNoOrgDefaultTeamIsRefused(t *testing.T) {
	ts := newTestServer(t)
	spacesPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)

	// Remove the org's default team. Nothing owns it yet — no space has been
	// created in this org — so the delete is clean.
	team := testutil.DefaultTeamID(t, ts.DB.Pool, ts.OrgID)
	_, err := ts.DB.Pool.Exec(t.Context(), `DELETE FROM teams WHERE id = $1`, team)
	require.NoError(t, err)

	wsnegRequireError(t, ts.post(t, spacesPath, map[string]any{
		"name": "Ownerless", "slug": "ownerless", "type": "vector",
	}, true), http.StatusInternalServerError, "INTERNAL_ERROR", "org default team missing")

	require.Equal(t, 0, wfsdListLen(t, ts, spacesPath),
		"a space with no resolvable owning team must not have been created")
}

// ─── Spaces: the update and membership error arms ─────────────────────────────

// TestWfSpaceDomain_SpaceUpdate_KeyCollisionLeavesBothSpacesIntact drives
// Update's UpdateSpace error arm through idx_spaces_org_key — the unique
// index on (org_id, key) for live spaces.
//
// Defect it catches: the update path validates the key's SHAPE
// (decodeSpaceUpdate) but never its availability, so the index is the only
// thing stopping two spaces in one organisation from answering to the same
// key. Keys are what ticket references are built from, so a collision is not
// cosmetic. Delete the error arm and the handler answers 200 with the
// requested key echoed back — the caller believes the rename happened, and
// every ticket reference they build from it points at the wrong space. Both
// read-backs are the assertion: the target kept its own key AND the holder
// kept theirs.
func TestWfSpaceDomain_SpaceUpdate_KeyCollisionLeavesBothSpacesIntact(t *testing.T) {
	ts := newTestServer(t)
	spacesPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)

	r := ts.post(t, spacesPath, map[string]any{
		"name": "Holder", "slug": "key-holder", "type": "vector", "key": "TAKEN",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "holder: %s", r.Body)
	holderID := decodeJSONMap(t, r.Body)["id"].(string)

	r = ts.post(t, spacesPath, map[string]any{
		"name": "Mover", "slug": "key-mover", "type": "vector", "key": "MINE",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "mover: %s", r.Body)
	moverID := decodeJSONMap(t, r.Body)["id"].(string)
	moverPath := spacesPath + "/" + moverID

	wsnegRequireError(t, ts.put(t, moverPath, map[string]any{
		"name": "Mover", "key": "TAKEN",
	}, true), http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update space")

	r = ts.get(t, moverPath, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "MINE", decodeJSONMap(t, r.Body)["key"],
		"the refused update must have left the mover's key alone")

	r = ts.get(t, spacesPath+"/"+holderID, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "TAKEN", decodeJSONMap(t, r.Body)["key"],
		"and must not have disturbed the space that holds the key")

	// The control: a key nobody holds is accepted, so the refusal above is
	// the collision and not the update path refusing every key change.
	r = ts.put(t, moverPath, map[string]any{"name": "Mover", "key": "FREE"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "an unheld key is accepted: %s", r.Body)
	require.Equal(t, "FREE", decodeJSONMap(t, r.Body)["key"])
}

// TestWfSpaceDomain_AddMember_UnknownUserIsRefusedAndReAddIsAnUpsert covers
// both answers AddSpaceMember can give: the FK to users refuses a user ID that
// names nobody (the handler's error arm), while a user already enrolled is
// deliberately NOT a conflict — the query is an upsert
// (ON CONFLICT (space_id, user_id) DO UPDATE SET role), so re-adding someone
// changes their role in place.
//
// Defect it catches, first half: the handler validates only that a role was
// supplied — the user ID is written straight through, and the FK is the only
// thing that notices it names nobody. Delete the error arm and the endpoint
// answers 201 with a zero-valued member row, so the membership list the caller
// is shown claims an enrolment the database refused.
//
// Defect it catches, second half: the upsert is load-bearing and invisible
// from the handler. Lose the ON CONFLICT clause and the re-add becomes a
// unique violation reported as a 500; change it to DO NOTHING and the `:one`
// returns no rows, which is ALSO a 500 — and in both cases the role change
// that this endpoint is the only route for silently stops working. Asserting
// the returned row id is unchanged is what distinguishes "updated in place"
// from "a second row for the same person".
func TestWfSpaceDomain_AddMember_UnknownUserIsRefusedAndReAddIsAnUpsert(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Membership Target", "membership-target", "vector")
	membersPath := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/members", ts.OrgID, spaceID)

	newcomer := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	r := ts.post(t, membersPath, map[string]any{"user_id": newcomer.ID, "role": "member"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "first enrolment: %s", r.Body)
	firstRowID := decodeJSONMap(t, r.Body)["id"]
	require.NotEmpty(t, firstRowID)
	after := wfsdListLen(t, ts, membersPath)

	// A user ID that names nobody: FK to users(id).
	wsnegRequireError(t, ts.post(t, membersPath,
		map[string]any{"user_id": uuid.New(), "role": "member"}, true),
		http.StatusInternalServerError, "INTERNAL_ERROR", "failed to add member")

	require.Equal(t, after, wfsdListLen(t, ts, membersPath),
		"the refused enrolment must not have added a row")

	// The same user again, with a different role: the upsert updates in place.
	r = ts.post(t, membersPath, map[string]any{"user_id": newcomer.ID, "role": "space_admin"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "re-add: %s", r.Body)
	updated := decodeJSONMap(t, r.Body)
	require.Equal(t, "space_admin", updated["role"], "the re-add must have changed the role")
	require.Equal(t, firstRowID, updated["id"],
		"and must have updated the existing row rather than written a second one")

	require.Equal(t, after, wfsdListLen(t, ts, membersPath),
		"the membership list must not have grown")
}

// TestWfSpaceDomain_UpdateOrg_UnexpectedDatabaseErrorLeavesTheNameAlone
// drives UpdateOrg's UpdateOrganization error arm.
//
// Defect it catches: this endpoint replaces the organisation's display name
// for everyone in it, and the handler's only guard is that the name is
// non-empty. Delete the error arm and the handler answers 200 with the
// zero-valued generated.Organization the failed `:one` returned — an empty
// ID, an empty name, zero timestamps — which any client that stores the
// response then believes. The read-back is the assertion: the organisation
// still carries the name it had.
func TestWfSpaceDomain_UpdateOrg_UnexpectedDatabaseErrorLeavesTheNameAlone(t *testing.T) {
	ts := newTestServer(t)
	orgPath := fmt.Sprintf("/api/v1/orgs/%s", ts.OrgID)

	before := ts.get(t, orgPath, true)
	require.Equal(t, http.StatusOK, before.StatusCode, "%s", before.Body)
	originalName := decodeJSONMap(t, before.Body)["name"]
	require.NotEmpty(t, originalName)

	// A NUL byte is not representable in a PostgreSQL text value, so the
	// UPDATE fails on the value rather than on any constraint.
	wsnegRequireError(t, ts.patch(t, orgPath, map[string]any{"name": "Null\x00Byte Corp"}, true),
		http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update organization")

	after := ts.get(t, orgPath, true)
	require.Equal(t, http.StatusOK, after.StatusCode, "%s", after.Body)
	require.Equal(t, originalName, decodeJSONMap(t, after.Body)["name"],
		"a refused org update must not have changed the name")

	// The control: an ordinary rename still goes through, so the refusal
	// above is the database rejecting that value and not the endpoint being
	// broken.
	r := ts.patch(t, orgPath, map[string]any{"name": "Renamed Corp"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "Renamed Corp", decodeJSONMap(t, r.Body)["name"])
}
