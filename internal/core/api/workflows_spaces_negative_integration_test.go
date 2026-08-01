package api_test

// Negative coverage for the two handlers that carry the org's workflow
// definitions (internal/core/api/workflows/handler.go) and its space
// governance surface (internal/core/api/spaces/handler.go).
//
// Every test here targets a refusal branch: a path parameter that will not
// parse, a body that will not decode, a validation rule, a capability gate, or
// a state-machine rejection. Each asserts the EXACT status and the documented
// error envelope — a "not 500" assertion would pass with the branch deleted,
// which spec §2's negative-test question forbids.
//
// Persona note (CLAUDE.md §2). The transition capability is
// transition_any_item, which sits at agent; the write floor on the ticket and
// project subtrees is create_items, which sits at contributor. A viewer is
// refused by the floor, so a "viewer is refused" test would pass with the
// in-handler gate deleted. The persona for those gates is therefore a
// CONTRIBUTOR. The space-governance gates want manage_space (space_admin) and
// their subtree carries no write floor at all, so the persona there is an
// AGENT — the highest role short of the capability under test.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// wsnegRaw sends rawBody verbatim. Every DecodeJSON failure branch in these two
// handlers needs a body no Go marshaller would ever produce, so the ordinary
// helpers (which marshal a value) cannot reach them.
func wsnegRaw(t *testing.T, ts *testServer, method, token, path, rawBody string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, ts.url(path), strings.NewReader(rawBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return ts.do(t, req)
}

// wsnegRequireError asserts the status, the error code AND the exact message.
// The message carries weight in this file: several branches answer 404 for
// different reasons — "no workflow assigned to space" and "ticket not found"
// are the same status from adjacent lines — so a test that checked only the
// status would pass with the handler answering the wrong one.
func wsnegRequireError(t *testing.T, r httpResult, status int, code, message string) {
	t.Helper()
	require.Equal(t, status, r.StatusCode, "body: %s", r.Body)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "error envelope expected, got: %s", r.Body)
	require.Equal(t, code, body.Error.Code)
	require.Equal(t, message, body.Error.Message)
}

// wsnegPersona creates an org member holding exactly `role` on the space, and
// returns the user plus a token for them.
func wsnegPersona(t *testing.T, ts *testServer, spaceID uuid.UUID, role access.Role) (testutil.User, string) {
	t.Helper()
	u := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err := ts.GrantService.Create(context.Background(), ts.OrgID, spaceID,
		access.SubjectUser, u.ID, role, ts.UserID)
	require.NoError(t, err)
	return u, ts.tokenFor(t, u.ID, u.Email)
}

// wsnegWorkflowSpace seeds the org's default workflows, creates a space of the
// given module through the API, and assigns the module's default workflow to
// it — the state a production space reaches through org provisioning.
func wsnegWorkflowSpace(t *testing.T, ts *testServer, module, name, slug string) string {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, ts.WorkflowAdapter.SeedDefaultWorkflows(ctx, ts.OrgID))
	spaceID := createScopedSpace(t, ts, name, slug, module)
	require.NoError(t, ts.WorkflowAdapter.AssignDefaultWorkflowToSpace(
		ctx, ts.OrgID, module, uuid.MustParse(spaceID)))
	return spaceID
}

// wsnegStateIDs returns the seeded default workflow's states by name.
func wsnegStateIDs(t *testing.T, ts *testServer, appliesTo string) map[string]uuid.UUID {
	t.Helper()
	ctx := context.Background()
	wf, err := ts.WorkflowAdapter.GetDefaultWorkflow(ctx, ts.OrgID, appliesTo)
	require.NoError(t, err)
	states, err := ts.WorkflowAdapter.ListStates(ctx, wf.ID)
	require.NoError(t, err)
	require.NotEmpty(t, states)
	out := make(map[string]uuid.UUID, len(states))
	for _, s := range states {
		out[s.Name] = s.ID
	}
	return out
}

// wsnegCreateTicket creates one ticket in the space and returns its ID.
func wsnegCreateTicket(t *testing.T, ts *testServer, spaceBase, title string) string {
	t.Helper()
	r := ts.post(t, spaceBase+"/tickets", map[string]any{"title": title, "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket: %s", r.Body)
	return decodeJSONMap(t, r.Body)["id"].(string)
}

// wsnegCreateItem creates one project item in the space and returns its ID.
func wsnegCreateItem(t *testing.T, ts *testServer, spaceBase, title string) string {
	t.Helper()
	r := ts.post(t, spaceBase+"/projects/items",
		map[string]any{"title": title, "kind": "task", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create item: %s", r.Body)
	return decodeJSONMap(t, r.Body)["id"].(string)
}

// ─── Workflows: org-scoped definition surface ─────────────────────────────────

// TestWorkflowSpacesNeg_WorkflowMutations_RequireOrgAdmin pins the guard
// classification of the workflow-admin surface against the real router: every
// mutation is org-admin, every read is org-member.
//
// Defect it catches: a workflow mutation registered inside the /workflows group
// WITHOUT its own r.With(adminGuard). Since #64 nothing is inherited from the
// group, so a forgotten guard leaves workflow editing — the state machine every
// ticket in the organisation moves through — open to any org member. The reads
// asserted alongside are what make the 403s mean "the guard fired" rather than
// "this member cannot see workflows at all".
func TestWorkflowSpacesNeg_WorkflowMutations_RequireOrgAdmin(t *testing.T) {
	ts := newTestServer(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/workflows", ts.OrgID)

	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memTok := ts.tokenFor(t, member.ID, member.Email)

	// An admin-owned workflow with one state and one self-referential-free
	// transition, so the member's refusals land on real resources.
	r := ts.post(t, base, map[string]any{"name": "Guarded WF", "applies_to": "tickets"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create workflow: %s", r.Body)
	wfID := decodeJSONMap(t, r.Body)["id"].(string)

	r = ts.post(t, base+"/"+wfID+"/states",
		map[string]any{"name": "open", "category": "todo", "position": 0, "is_initial": true}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create state: %s", r.Body)
	stateA := decodeJSONMap(t, r.Body)["id"].(string)

	r = ts.post(t, base+"/"+wfID+"/states",
		map[string]any{"name": "done", "category": "done", "position": 1}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create state 2: %s", r.Body)
	stateB := decodeJSONMap(t, r.Body)["id"].(string)

	r = ts.post(t, base+"/"+wfID+"/transitions",
		map[string]any{"name": "Finish", "from_state_id": stateA, "to_state_id": stateB}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create transition: %s", r.Body)
	transitionID := decodeJSONMap(t, r.Body)["id"].(string)

	// Reads: an ordinary member sees the definitions.
	for _, path := range []string{
		base, base + "/" + wfID, base + "/" + wfID + "/states", base + "/" + wfID + "/transitions",
	} {
		got := ts.getAs(t, memTok, path)
		require.Equalf(t, http.StatusOK, got.StatusCode, "member read of %s: %s", path, got.Body)
	}

	// Mutations: every one is refused with 403 FORBIDDEN, never 404 and never a
	// silent success. 403 rather than 404 because the surface's existence is
	// already known to members — they just read it.
	const adminOnly = "organization admin required"
	wsnegRequireError(t, ts.postAs(t, memTok, base,
		map[string]any{"name": "Sneaky", "applies_to": "tickets"}), http.StatusForbidden, "FORBIDDEN", adminOnly)
	wsnegRequireError(t, ts.requestAs(t, memTok, http.MethodPut, base+"/"+wfID,
		map[string]any{"name": "Renamed", "applies_to": "tickets"}), http.StatusForbidden, "FORBIDDEN", adminOnly)
	wsnegRequireError(t, ts.deleteAs(t, memTok, base+"/"+wfID),
		http.StatusForbidden, "FORBIDDEN", adminOnly)
	wsnegRequireError(t, ts.postAs(t, memTok, base+"/"+wfID+"/states",
		map[string]any{"name": "sneaky", "category": "todo", "position": 9}), http.StatusForbidden, "FORBIDDEN", adminOnly)
	wsnegRequireError(t, ts.deleteAs(t, memTok, base+"/"+wfID+"/states/"+stateB),
		http.StatusForbidden, "FORBIDDEN", adminOnly)
	wsnegRequireError(t, ts.postAs(t, memTok, base+"/"+wfID+"/transitions",
		map[string]any{"name": "Sneaky", "from_state_id": stateB, "to_state_id": stateA}), http.StatusForbidden, "FORBIDDEN", adminOnly)
	wsnegRequireError(t, ts.deleteAs(t, memTok, base+"/"+wfID+"/transitions/"+transitionID),
		http.StatusForbidden, "FORBIDDEN", adminOnly)

	// The refusals changed nothing: the workflow, both states and the
	// transition are all still there.
	r = ts.get(t, base+"/"+wfID, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "workflow survived the refused delete: %s", r.Body)
	require.Equal(t, "Guarded WF", decodeJSONMap(t, r.Body)["name"], "refused PUT must not have renamed it")

	r = ts.get(t, base+"/"+wfID+"/states", true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var states []map[string]any
	requireJSONList(t, r.Body, &states)
	require.Len(t, states, 2, "refused state create/delete must have changed nothing")

	r = ts.get(t, base+"/"+wfID+"/transitions", true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var transitions []map[string]any
	requireJSONList(t, r.Body, &transitions)
	require.Len(t, transitions, 1, "refused transition create/delete must have changed nothing")
}

// TestWorkflowSpacesNeg_WorkflowWrites_MalformedBodies covers the DecodeJSON
// refusal on every org-scoped workflow write.
//
// Defect it catches: a handler that ignored the decode error and carried on
// with a zero-value request struct. That is not a hypothetical shape — the
// struct's zero value passes straight into the repository, so a swallowed
// decode error on CreateWorkflow writes a nameless workflow with an empty
// applies_to, and on UpdateWorkflow it BLANKS an existing one. 400 BAD_REQUEST,
// not VALIDATION_ERROR: the body never became a request at all.
func TestWorkflowSpacesNeg_WorkflowWrites_MalformedBodies(t *testing.T) {
	ts := newTestServer(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/workflows", ts.OrgID)

	r := ts.post(t, base, map[string]any{"name": "Decode WF", "applies_to": "tickets"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create workflow: %s", r.Body)
	wfID := decodeJSONMap(t, r.Body)["id"].(string)

	const truncated = `{"name":`
	const notAnObject = `["name"]`

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create workflow, truncated", http.MethodPost, base, truncated},
		{"create workflow, wrong JSON kind", http.MethodPost, base, notAnObject},
		{"update workflow, truncated", http.MethodPut, base + "/" + wfID, truncated},
		{"create state, truncated", http.MethodPost, base + "/" + wfID + "/states", truncated},
		{"create transition, truncated", http.MethodPost, base + "/" + wfID + "/transitions", truncated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wsnegRaw(t, ts, tc.method, ts.Token, tc.path, tc.body)
			wsnegRequireError(t, got, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		})
	}

	// Nothing the refused bodies named was written, and the workflow they
	// targeted still carries the name it was created with.
	r = ts.get(t, base+"/"+wfID, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "Decode WF", decodeJSONMap(t, r.Body)["name"])

	r = ts.get(t, base, true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var workflows []map[string]any
	requireJSONList(t, r.Body, &workflows)
	require.Len(t, workflows, 1, "a refused create must not have written a nameless workflow")
}

// TestWorkflowSpacesNeg_WorkflowWrites_RequiredFields covers the field-level
// refusals: they answer 400 with VALIDATION_ERROR, a distinct code from the
// BAD_REQUEST above, because the body decoded fine and the *content* is wrong.
//
// Defect it catches: dropping any of these checks writes a row the schema
// happily accepts but nothing downstream can use — a workflow with no name and
// an empty applies_to (which the CHECK constraint then rejects as a 500), or a
// state with no category. The empty-string cases matter as much as the absent
// ones: a check written as `req.Name == ""` and a check written as a
// presence-only pointer test behave differently, and only one of them is here.
func TestWorkflowSpacesNeg_WorkflowWrites_RequiredFields(t *testing.T) {
	ts := newTestServer(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/workflows", ts.OrgID)

	r := ts.post(t, base, map[string]any{"name": "Validation WF", "applies_to": "tickets"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create workflow: %s", r.Body)
	wfID := decodeJSONMap(t, r.Body)["id"].(string)

	cases := []struct {
		name    string
		path    string
		body    map[string]any
		message string
	}{
		{"workflow, no fields at all", base, map[string]any{}, "name and applies_to are required"},
		{"workflow, no applies_to", base, map[string]any{"name": "Nameless target"}, "name and applies_to are required"},
		{"workflow, no name", base, map[string]any{"applies_to": "tickets"}, "name and applies_to are required"},
		{"workflow, empty name", base, map[string]any{"name": "", "applies_to": "tickets"}, "name and applies_to are required"},
		{"state, no fields at all", base + "/" + wfID + "/states", map[string]any{}, "name and category are required"},
		{"state, no category", base + "/" + wfID + "/states", map[string]any{"name": "open"}, "name and category are required"},
		{"state, no name", base + "/" + wfID + "/states", map[string]any{"category": "todo"}, "name and category are required"},
		{"state, empty category", base + "/" + wfID + "/states", map[string]any{"name": "open", "category": ""}, "name and category are required"},
		{"transition, no name", base + "/" + wfID + "/transitions", map[string]any{"from_state_id": uuid.New(), "to_state_id": uuid.New()}, "name is required"},
		{"transition, empty name", base + "/" + wfID + "/transitions", map[string]any{"name": ""}, "name is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ts.post(t, tc.path, tc.body, true)
			wsnegRequireError(t, got, http.StatusBadRequest, "VALIDATION_ERROR", tc.message)
		})
	}

	// None of the refused writes landed.
	r = ts.get(t, base, true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var workflows []map[string]any
	requireJSONList(t, r.Body, &workflows)
	require.Len(t, workflows, 1, "refused workflow creates must have written nothing")

	r = ts.get(t, base+"/"+wfID+"/states", true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var states []map[string]any
	requireJSONList(t, r.Body, &states)
	require.Empty(t, states, "refused state creates must have written nothing")

	r = ts.get(t, base+"/"+wfID+"/transitions", true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var transitions []map[string]any
	requireJSONList(t, r.Body, &transitions)
	require.Empty(t, transitions, "refused transition creates must have written nothing")
}

// ─── Workflows: space-scoped read surface ─────────────────────────────────────

// TestWorkflowSpacesNeg_SpaceWorkflow_AssignedAndUnassigned covers both answers
// of GET /spaces/{spaceID}/workflow and its states sibling.
//
// Defect it catches: the assigned case reaches respond.JSON, which nothing
// exercised before — a handler that returned the 404 unconditionally, or
// returned a zero-valued workflow because the `:one` query's no-rows error was
// swallowed, would have looked fully covered. The codex half pins the other
// direction: a module that deliberately has no workflow must 404, not 200 with
// an empty object, and its state list is an empty ARRAY rather than an error.
func TestWorkflowSpacesNeg_SpaceWorkflow_AssignedAndUnassigned(t *testing.T) {
	ts := newTestServer(t)
	beaconID := wsnegWorkflowSpace(t, ts, "beacon", "Neg Desk", "neg-desk")
	beaconBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, beaconID)

	r := ts.get(t, beaconBase+"/workflow", true)
	require.Equal(t, http.StatusOK, r.StatusCode, "assigned space workflow: %s", r.Body)
	wf := decodeJSONMap(t, r.Body)
	require.Equal(t, "tickets", wf["applies_to"], "a beacon space carries the ticket workflow")
	require.Equal(t, "Default Service Desk", wf["name"])
	require.NotEmpty(t, wf["id"])

	r = ts.get(t, beaconBase+"/workflow/states", true)
	require.Equal(t, http.StatusOK, r.StatusCode, "space workflow states: %s", r.Body)
	var states []map[string]any
	requireJSONList(t, r.Body, &states)
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = s["name"].(string)
	}
	require.Equal(t, []string{"open", "in_progress", "resolved", "closed"}, names,
		"states must come back in position order — the board renders them as columns")
	require.Equal(t, true, states[0]["is_initial"])

	// A codex space is deliberately skipped by workflow assignment.
	codexID := createScopedSpace(t, ts, "Neg Wiki", "neg-wiki", "codex")
	codexBase := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, codexID)

	wsnegRequireError(t, ts.get(t, codexBase+"/workflow", true),
		http.StatusNotFound, "NOT_FOUND", "no workflow assigned to space")

	r = ts.get(t, codexBase+"/workflow/states", true)
	require.Equal(t, http.StatusOK, r.StatusCode, "unassigned states: %s", r.Body)
	var codexStates []map[string]any
	requireJSONList(t, r.Body, &codexStates)
	require.Empty(t, codexStates, "no workflow means an empty list, not an error")
}

// ─── Workflows: the ticket transition endpoint ────────────────────────────────

// TestWorkflowSpacesNeg_TicketTransition_Rejections walks every refusal of
// POST /tickets/{ticketID}/workflow-state.
//
// Defect it catches: each row is a distinct branch that answers with a distinct
// message, and two of them share status 404 — "no workflow assigned to space"
// and "ticket not found". Asserting the message is what stops a handler that
// reordered the two lookups (or lost one) from passing. The 409 row is the
// state machine itself: open→resolved has no edge in the seeded workflow, and
// the follow-up read proves the refusal wrote nothing.
func TestWorkflowSpacesNeg_TicketTransition_Rejections(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "beacon", "Neg Trans Desk", "neg-trans-desk")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	states := wsnegStateIDs(t, ts, "tickets")
	ticketID := wsnegCreateTicket(t, ts, base, "Transition target")

	// A ticket id that is not a UUID at all.
	wsnegRequireError(t, ts.post(t, base+"/tickets/not-a-uuid/workflow-state",
		map[string]any{"state_id": states["in_progress"]}, true),
		http.StatusBadRequest, "BAD_REQUEST", "invalid ticket ID")

	// A body that will not decode.
	wsnegRequireError(t, wsnegRaw(t, ts, http.MethodPost, ts.Token,
		fmt.Sprintf("%s/tickets/%s/workflow-state", base, ticketID), `{"state_id":`),
		http.StatusBadRequest, "BAD_REQUEST", "invalid request body")

	// A well-formed ticket id naming no ticket.
	wsnegRequireError(t, ts.post(t, fmt.Sprintf("%s/tickets/%s/workflow-state", base, uuid.New()),
		map[string]any{"state_id": states["in_progress"]}, true),
		http.StatusNotFound, "NOT_FOUND", "ticket not found")

	// A target the initial state has no edge to: open→resolved must go through
	// in_progress.
	wsnegRequireError(t, ts.post(t, fmt.Sprintf("%s/tickets/%s/workflow-state", base, ticketID),
		map[string]any{"state_id": states["resolved"]}, true),
		http.StatusConflict, "INVALID_TRANSITION",
		// The refusal NAMES the two states, which is what makes it actionable:
		// "invalid workflow transition" left an administrator guessing which edge
		// was missing from a graph they had built themselves.
		`this space's workflow defines no move from "open" to "resolved"`)

	r := ts.get(t, fmt.Sprintf("%s/tickets/%s", base, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "open", decodeJSONMap(t, r.Body)["status"],
		"a refused transition must leave the ticket where it was")
}

// TestWorkflowSpacesNeg_TicketTransition_SpaceWithoutWorkflowIs404 is the other
// 404: the space itself has no workflow assigned, so there is nothing to
// transition through.
//
// Defect it catches: a handler that skipped the space-workflow lookup would
// carry a zero UUID into ValidateTransition, which finds no edges and answers
// 409 "invalid workflow transition" — a misleading conflict for what is
// actually a missing configuration. The distinct message is the whole point.
func TestWorkflowSpacesNeg_TicketTransition_SpaceWithoutWorkflowIs404(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Unwired Desk", "unwired-desk", "beacon")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	ticketID := wsnegCreateTicket(t, ts, base, "Orphan ticket")

	wsnegRequireError(t, ts.post(t, fmt.Sprintf("%s/tickets/%s/workflow-state", base, ticketID),
		map[string]any{"state_id": uuid.New()}, true),
		http.StatusNotFound, "NOT_FOUND", "no workflow assigned to space")
}

// TestWorkflowSpacesNeg_TicketTransition_ContributorIsRefused is the capability
// gate, exercised with the persona CLAUDE.md §2 requires.
//
// transition_any_item sits at agent; the subtree's write floor is create_items,
// at contributor. A CONTRIBUTOR therefore clears the floor and is refused by
// the handler's own access.Can — which is the only persona that proves the gate
// exists. A viewer would be stopped by the middleware and would pass this test
// with the in-handler check deleted. Mutation-checked in both directions: with
// the gate intact the contributor gets 403; with it removed the contributor
// transitions the ticket and the final assertion below fails.
func TestWorkflowSpacesNeg_TicketTransition_ContributorIsRefused(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "beacon", "Cap Desk", "cap-desk")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	states := wsnegStateIDs(t, ts, "tickets")
	ticketID := wsnegCreateTicket(t, ts, base, "Contributor cannot move me")

	_, contribTok := wsnegPersona(t, ts, uuid.MustParse(spaceID), access.RoleContributor)

	// The contributor is genuinely past the write floor: they may create a
	// ticket in this space. Without this the 403 below would be ambiguous.
	r := ts.postAs(t, contribTok, base+"/tickets",
		map[string]any{"title": "Contributor's own ticket", "priority": "medium"})
	require.Equal(t, http.StatusCreated, r.StatusCode,
		"a contributor clears the create_items floor: %s", r.Body)

	wsnegRequireError(t, ts.postAs(t, contribTok,
		fmt.Sprintf("%s/tickets/%s/workflow-state", base, ticketID),
		map[string]any{"state_id": states["in_progress"]}),
		http.StatusForbidden, "FORBIDDEN", "insufficient permissions")

	r = ts.get(t, fmt.Sprintf("%s/tickets/%s", base, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, "open", decodeJSONMap(t, r.Body)["status"],
		"the refused transition must not have moved the ticket")
}

// TestWorkflowSpacesNeg_TicketTransition_SecondHopUsesStoredState covers the
// branch that reads the ticket's CURRENT workflow state instead of falling back
// to the workflow's initial state.
//
// Defect it catches: if the handler ignored tickets.workflow_state_id and always
// started from the initial state, the second hop below would be evaluated as
// open→resolved — which the seeded workflow has no edge for — and would answer
// 409. The test therefore fails the moment that branch is removed, and it also
// pins the inverse mistake: a ticket that has never been transitioned still has
// a NULL state and must fall back, which the first hop exercises.
func TestWorkflowSpacesNeg_TicketTransition_SecondHopUsesStoredState(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "beacon", "Two Hop Desk", "two-hop-desk")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	states := wsnegStateIDs(t, ts, "tickets")
	ticketID := wsnegCreateTicket(t, ts, base, "Two hops")
	path := fmt.Sprintf("%s/tickets/%s/workflow-state", base, ticketID)

	// Hop one: workflow_state_id is NULL, so the handler falls back to the
	// initial state (open) and open→in_progress is a real edge.
	r := ts.post(t, path, map[string]any{"state_id": states["in_progress"]}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "first hop: %s", r.Body)
	require.Equal(t, "in_progress", decodeJSONMap(t, r.Body)["status"],
		"the status column is kept in sync with the workflow state")

	// Hop two: valid only from in_progress. If the stored state were ignored,
	// this is open→resolved and answers 409.
	r = ts.post(t, path, map[string]any{"state_id": states["resolved"]}, true)
	require.Equal(t, http.StatusOK, r.StatusCode,
		"the second hop must start from the ticket's stored state: %s", r.Body)
	require.Equal(t, "resolved", decodeJSONMap(t, r.Body)["status"])

	r = ts.get(t, fmt.Sprintf("%s/tickets/%s", base, ticketID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, "resolved", decodeJSONMap(t, r.Body)["status"], "the move persisted")
}

// ─── Workflows: the project-item transition endpoint ──────────────────────────

// TestWorkflowSpacesNeg_ItemTransition_Rejections is the project-item twin of
// TestWorkflowSpacesNeg_TicketTransition_Rejections. tickets and project_items
// stay split (ADR-0003), so the two handlers are separate code with separate
// branches — a fix applied to one and not the other is exactly the drift this
// pair exists to catch.
func TestWorkflowSpacesNeg_ItemTransition_Rejections(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "vector", "Neg Trans Board", "neg-trans-board")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	states := wsnegStateIDs(t, ts, "project_items")
	itemID := wsnegCreateItem(t, ts, base, "Transition target")

	wsnegRequireError(t, ts.post(t, base+"/projects/items/not-a-uuid/workflow-state",
		map[string]any{"state_id": states["in_progress"]}, true),
		http.StatusBadRequest, "BAD_REQUEST", "invalid item ID")

	wsnegRequireError(t, wsnegRaw(t, ts, http.MethodPost, ts.Token,
		fmt.Sprintf("%s/projects/items/%s/workflow-state", base, itemID), `{"state_id":`),
		http.StatusBadRequest, "BAD_REQUEST", "invalid request body")

	wsnegRequireError(t, ts.post(t, fmt.Sprintf("%s/projects/items/%s/workflow-state", base, uuid.New()),
		map[string]any{"state_id": states["in_progress"]}, true),
		http.StatusNotFound, "NOT_FOUND", "item not found")

	// backlog (the initial state) has edges to todo and in_progress only.
	wsnegRequireError(t, ts.post(t, fmt.Sprintf("%s/projects/items/%s/workflow-state", base, itemID),
		map[string]any{"state_id": states["done"]}, true),
		http.StatusConflict, "INVALID_TRANSITION",
		`this space's workflow defines no move from "backlog" to "done"`)

	r := ts.get(t, fmt.Sprintf("%s/projects/items/%s", base, itemID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	require.Equal(t, "backlog", decodeJSONMap(t, r.Body)["status"],
		"a refused transition must leave the item's status alone")
}

// TestWorkflowSpacesNeg_ItemTransition_SpaceWithoutWorkflowIs404 — the item-side
// missing-configuration 404. See the ticket twin for the defect it catches.
func TestWorkflowSpacesNeg_ItemTransition_SpaceWithoutWorkflowIs404(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Unwired Board", "unwired-board", "vector")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	itemID := wsnegCreateItem(t, ts, base, "Orphan item")

	wsnegRequireError(t, ts.post(t, fmt.Sprintf("%s/projects/items/%s/workflow-state", base, itemID),
		map[string]any{"state_id": uuid.New()}, true),
		http.StatusNotFound, "NOT_FOUND", "no workflow assigned to space")
}

// TestWorkflowSpacesNeg_ItemTransition_ContributorIsRefused — the item-side
// transition_any_item gate, with the same contributor persona and the same
// two-way mutation reasoning as the ticket twin.
func TestWorkflowSpacesNeg_ItemTransition_ContributorIsRefused(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "vector", "Cap Board", "cap-board")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	states := wsnegStateIDs(t, ts, "project_items")
	itemID := wsnegCreateItem(t, ts, base, "Contributor cannot move me")

	_, contribTok := wsnegPersona(t, ts, uuid.MustParse(spaceID), access.RoleContributor)

	r := ts.postAs(t, contribTok, base+"/projects/items",
		map[string]any{"title": "Contributor's own item", "kind": "task", "priority": "medium"})
	require.Equal(t, http.StatusCreated, r.StatusCode,
		"a contributor clears the create_items floor: %s", r.Body)

	wsnegRequireError(t, ts.postAs(t, contribTok,
		fmt.Sprintf("%s/projects/items/%s/workflow-state", base, itemID),
		map[string]any{"state_id": states["in_progress"]}),
		http.StatusForbidden, "FORBIDDEN", "insufficient permissions")

	r = ts.get(t, fmt.Sprintf("%s/projects/items/%s", base, itemID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, "backlog", decodeJSONMap(t, r.Body)["status"],
		"the refused transition must not have moved the item")
}

// TestWorkflowSpacesNeg_ItemTransition_SecondHopUsesStoredState — the item-side
// stored-state branch. If project_items.workflow_state_id were ignored, the
// second hop would be evaluated as backlog→in_review, which has no edge, and
// would answer 409.
func TestWorkflowSpacesNeg_ItemTransition_SecondHopUsesStoredState(t *testing.T) {
	ts := newTestServer(t)
	spaceID := wsnegWorkflowSpace(t, ts, "vector", "Two Hop Board", "two-hop-board")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	states := wsnegStateIDs(t, ts, "project_items")
	itemID := wsnegCreateItem(t, ts, base, "Two hops")
	path := fmt.Sprintf("%s/projects/items/%s/workflow-state", base, itemID)

	r := ts.post(t, path, map[string]any{"state_id": states["in_progress"]}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "first hop: %s", r.Body)
	require.Equal(t, "in_progress", decodeJSONMap(t, r.Body)["status"])

	r = ts.post(t, path, map[string]any{"state_id": states["in_review"]}, true)
	require.Equal(t, http.StatusOK, r.StatusCode,
		"the second hop must start from the item's stored state: %s", r.Body)
	require.Equal(t, "in_review", decodeJSONMap(t, r.Body)["status"])

	r = ts.get(t, fmt.Sprintf("%s/projects/items/%s", base, itemID), true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, "in_review", decodeJSONMap(t, r.Body)["status"], "the move persisted")
}

// ─── Spaces: the manage_space gates ───────────────────────────────────────────

// TestWorkflowSpacesNeg_SpaceGovernance_RequiresManageSpace covers every
// in-handler manage_space check on the space surface at once.
//
// Persona: an AGENT. The /spaces subtree carries spaceGuard and readableGuard
// but NO write floor, so nothing upstream refuses an agent — every 403 below is
// the handler's own access.Can and nothing else. Agent is also the highest role
// short of space_admin, so this pins the gate at exactly the right rung rather
// than merely "some role is needed".
//
// Defect it catches: dropping any one of these checks hands space governance —
// renaming, deleting, and the membership list — to any contributor or agent who
// can merely read the space. The GET control proves the agent is genuinely
// inside the space, so the 403s are refusals and not "this space does not exist
// for you" in disguise, and the follow-up read proves nothing was mutated.
func TestWorkflowSpacesNeg_SpaceGovernance_RequiresManageSpace(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Governed Space", "governed-space", "vector")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)

	agent, agentTok := wsnegPersona(t, ts, uuid.MustParse(spaceID), access.RoleAgent)
	outsider := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)

	// Control: the agent can read the space. Without this the 403s below would
	// be indistinguishable from a readability failure.
	r := ts.getAs(t, agentTok, base)
	require.Equal(t, http.StatusOK, r.StatusCode, "the agent reads the space: %s", r.Body)
	require.Equal(t, "Governed Space", decodeJSONMap(t, r.Body)["name"])

	// ...and can read the member list, which carries no capability check.
	r = ts.getAs(t, agentTok, base+"/members")
	require.Equal(t, http.StatusOK, r.StatusCode, "member list is not gated: %s", r.Body)

	wsnegRequireError(t, ts.getAs(t, agentTok, base+"/summary"),
		http.StatusForbidden, "FORBIDDEN", "insufficient permissions")
	wsnegRequireError(t, ts.requestAs(t, agentTok, http.MethodPut, base,
		map[string]any{"name": "Renamed By Agent"}),
		http.StatusForbidden, "FORBIDDEN", "manage_space required")
	wsnegRequireError(t, ts.deleteAs(t, agentTok, base),
		http.StatusForbidden, "FORBIDDEN", "manage_space required")
	wsnegRequireError(t, ts.postAs(t, agentTok, base+"/members",
		map[string]any{"user_id": outsider.ID, "role": "member"}),
		http.StatusForbidden, "FORBIDDEN", "manage_space required")
	wsnegRequireError(t, ts.deleteAs(t, agentTok, fmt.Sprintf("%s/members/%s", base, agent.ID)),
		http.StatusForbidden, "FORBIDDEN", "manage_space required")

	// Nothing changed: the space is still there, still named what it was, and
	// the outsider was never enrolled.
	r = ts.get(t, base, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "the space survived the refused delete: %s", r.Body)
	require.Equal(t, "Governed Space", decodeJSONMap(t, r.Body)["name"],
		"the refused PUT must not have renamed it")

	r = ts.get(t, base+"/members", true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var members []map[string]any
	requireJSONList(t, r.Body, &members)
	for _, m := range members {
		require.NotEqual(t, outsider.ID.String(), m["user_id"],
			"the refused AddMember must not have enrolled anyone")
	}
}

// ─── Spaces: body validation ──────────────────────────────────────────────────

// TestWorkflowSpacesNeg_SpaceCreate_BodyAndKeyValidation covers the create
// refusals that sit before the first write.
//
// Defect it catches: every one of these rejections happens before CreateSpaceTx
// runs, so a lost check does not merely change a status code — it reaches the
// database. A malformed body becomes a space with no name and no slug; a
// lowercase key trips the spaces_key_format CHECK and surfaces as a 500; a
// non-UUID owner_team_id would have to be defaulted or dropped, silently
// re-homing the space. The final directory read proves no row was written.
func TestWorkflowSpacesNeg_SpaceCreate_BodyAndKeyValidation(t *testing.T) {
	ts := newTestServer(t)
	spacesPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)

	// A body that will not decode.
	wsnegRequireError(t, wsnegRaw(t, ts, http.MethodPost, ts.Token, spacesPath, `{"name":`),
		http.StatusBadRequest, "BAD_REQUEST", "invalid request body")

	// An explicit key that is not 1–10 uppercase alphanumerics.
	for _, badKey := range []string{"lower", "has space", "TOOLONGAKEY", "WITH-DASH"} {
		wsnegRequireError(t, ts.post(t, spacesPath, map[string]any{
			"name": "Bad Key Space", "slug": "bad-key-space", "type": "vector", "key": badKey,
		}, true), http.StatusBadRequest, "VALIDATION_ERROR",
			"key must be 1–10 uppercase letters or digits (e.g. HR, COM, IT2)")
	}

	// An owner_team_id that is not a UUID.
	wsnegRequireError(t, ts.post(t, spacesPath, map[string]any{
		"name": "Bad Team Space", "slug": "bad-team-space", "type": "vector",
		"owner_team_id": "not-a-uuid",
	}, true), http.StatusBadRequest, "VALIDATION_ERROR", "invalid owner_team_id")

	// Nothing above was written.
	r := ts.get(t, spacesPath, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	var directory []map[string]any
	requireJSONList(t, r.Body, &directory)
	require.Empty(t, directory, "every refused create must have written nothing")
}

// TestWorkflowSpacesNeg_SpaceCreate_PunctuationOnlyNameDerivesFallbackKey
// covers deriveKey's empty-result fallback.
//
// Defect it catches: a name that survives the "name is required" check but
// contains no alphanumeric character at all derives the empty string. Without
// the fallback the empty key fails validKey and the caller is told their key is
// invalid — for a key they never sent — or, if the regex were ever loosened,
// the empty key reaches the spaces_key_format CHECK and becomes a 500.
func TestWorkflowSpacesNeg_SpaceCreate_PunctuationOnlyNameDerivesFallbackKey(t *testing.T) {
	ts := newTestServer(t)

	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]any{
		"name": "!!! ???", "slug": "punctuation-only", "type": "vector",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode,
		"a punctuation-only name must still produce a usable key: %s", r.Body)
	require.Equal(t, "SPACE", decodeJSONMap(t, r.Body)["key"],
		"deriveKey falls back to SPACE when the name yields no alphanumerics")
}

// TestWorkflowSpacesNeg_SpaceUpdate_BodyValidation covers decodeSpaceUpdate.
//
// Defect it catches: PUT is a full replace, so a lost check here overwrites
// live data with the zero value — an accepted empty name blanks the space's
// name org-wide, and an accepted bad key trips spaces_key_format as a 500 after
// the other fields have already been considered. The read-back proves the
// refusals left the record intact.
func TestWorkflowSpacesNeg_SpaceUpdate_BodyValidation(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Update Target", "update-target", "vector")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)

	wsnegRequireError(t, wsnegRaw(t, ts, http.MethodPut, ts.Token, base, `{"name":`),
		http.StatusBadRequest, "BAD_REQUEST", "invalid request body")

	wsnegRequireError(t, ts.put(t, base, map[string]any{"name": ""}, true),
		http.StatusBadRequest, "VALIDATION_ERROR", "name is required")

	wsnegRequireError(t, ts.put(t, base, map[string]any{"name": "Still Here", "key": "lower"}, true),
		http.StatusBadRequest, "VALIDATION_ERROR", "key must be 1–10 uppercase letters or digits")

	wsnegRequireError(t, ts.put(t, base, map[string]any{"name": "Still Here", "visibility": "public"}, true),
		http.StatusBadRequest, "VALIDATION_ERROR",
		"visibility must be one of 'hidden', 'discoverable', or 'org'")

	r := ts.get(t, base, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	space := decodeJSONMap(t, r.Body)
	require.Equal(t, "Update Target", space["name"], "no refused update may have landed")
	require.Equal(t, "discoverable", space["visibility"])
}

// TestWorkflowSpacesNeg_UpdateOrg_NameIsRequired covers the org-update
// validation rule.
//
// Defect it catches: organizations.name is NOT NULL but not non-empty, so
// without this check a PUT-shaped PATCH that omits the name blanks the
// organisation's display name for everyone in it, and the database accepts it.
func TestWorkflowSpacesNeg_UpdateOrg_NameIsRequired(t *testing.T) {
	ts := newTestServer(t)
	orgPath := fmt.Sprintf("/api/v1/orgs/%s", ts.OrgID)

	before := ts.get(t, orgPath, true)
	require.Equal(t, http.StatusOK, before.StatusCode, "%s", before.Body)
	originalName := decodeJSONMap(t, before.Body)["name"]
	require.NotEmpty(t, originalName)

	for _, body := range []map[string]any{{}, {"name": ""}, {"description": "no name here"}} {
		wsnegRequireError(t, ts.patch(t, orgPath, body, true),
			http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
	}

	after := ts.get(t, orgPath, true)
	require.Equal(t, http.StatusOK, after.StatusCode)
	require.Equal(t, originalName, decodeJSONMap(t, after.Body)["name"],
		"a refused org update must not have blanked the name")
}

// TestWorkflowSpacesNeg_AddMember_RoleIsRequired covers the membership
// validation rule.
//
// Defect it catches: space_members.role is NOT NULL with a DEFAULT, so an empty
// role does NOT fail the insert — it writes the literal empty string, producing
// a member row whose role matches nothing the product understands. The check is
// the only thing standing between the request and that row.
func TestWorkflowSpacesNeg_AddMember_RoleIsRequired(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Member Target", "member-target", "vector")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	newcomer := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)

	for _, body := range []map[string]any{
		{"user_id": newcomer.ID},
		{"user_id": newcomer.ID, "role": ""},
	} {
		wsnegRequireError(t, ts.post(t, base+"/members", body, true),
			http.StatusBadRequest, "VALIDATION_ERROR", "role is required")
	}

	r := ts.get(t, base+"/members", true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	var members []map[string]any
	requireJSONList(t, r.Body, &members)
	for _, m := range members {
		require.NotEqual(t, newcomer.ID.String(), m["user_id"],
			"a refused AddMember must not have written a roleless row")
	}
}

// TestWorkflowSpacesNeg_RemoveMember_InvalidUserID covers the user-id parse in
// RemoveMember — the one path parameter on this surface that no middleware
// validates first (spaceID is parsed by the space guard, but userID is the
// handler's alone).
//
// Defect it catches: without the parse check the zero UUID would be passed to
// RemoveSpaceMember, which deletes by (space_id, user_id) — a no-op today, but
// a silent 204 telling the operator a removal happened when it did not.
func TestWorkflowSpacesNeg_RemoveMember_InvalidUserID(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Remove Target", "remove-target", "vector")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)

	wsnegRequireError(t, ts.delete(t, base+"/members/not-a-uuid", true),
		http.StatusBadRequest, "BAD_REQUEST", "invalid user ID")
}

// TestWorkflowSpacesNeg_SpaceDelete_OverLongTicketRefDeletesNothing covers the
// ticket-reference precondition on space deletion.
//
// Defect it catches: the reference is resolved BEFORE the soft delete precisely
// so a rejected reference means nothing happened. Move that resolution after
// the delete — or drop the `if !ok { return }` — and the space is destroyed
// while the caller is told their request was invalid, with no audit row naming
// what was destroyed. The read-back is the half of this test that matters.
func TestWorkflowSpacesNeg_SpaceDelete_OverLongTicketRefDeletesNothing(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Ref Guarded", "ref-guarded", "vector")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)

	overLong := strings.Repeat("A", 201)
	wsnegRequireError(t, ts.delete(t, base+"?ticket_ref="+overLong, true),
		http.StatusBadRequest, "VALIDATION_ERROR", "ticket_ref must be 200 characters or fewer")

	r := ts.get(t, base, true)
	require.Equal(t, http.StatusOK, r.StatusCode,
		"a refused ticket_ref must leave the space undeleted: %s", r.Body)
	require.Equal(t, "Ref Guarded", decodeJSONMap(t, r.Body)["name"])

	// A reference of exactly the cap is accepted, so the refusal above is the
	// length rule firing rather than the parameter being rejected outright.
	atCap := strings.Repeat("A", 200)
	r = ts.delete(t, base+"?ticket_ref="+atCap, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode,
		"a reference at the cap is valid: %s", r.Body)
}
