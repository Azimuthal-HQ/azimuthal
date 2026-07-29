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

// These tests drive the route the product actually calls — POST
// .../tickets/{id}/status — through the fully wired router. That is the whole
// point of the chokepoint: a tier test against the workflow engine's own
// endpoint would prove nothing, because no client calls it.

type tierAPIFixture struct {
	ts       *testServer
	spaceID  uuid.UUID
	ticketID uuid.UUID
	tier     *adapters.WorkflowTierAdapter
	edge     uuid.UUID // open -> in_progress on the space's workflow
}

func setupTierAPI(t *testing.T) *tierAPIFixture {
	t.Helper()

	ts := newTestServer(t)
	ctx := context.Background()

	// The harness org has no workflows: newTestServerOn wires the plain
	// org provisioner, not the ...WithWorkflows variant production uses. A tier
	// test that forgets this gets "no workflow assigned" and reads as passing.
	require.NoError(t, ts.WorkflowAdapter.SeedDefaultWorkflows(ctx, ts.OrgID))

	owner := testutil.CreateTestUser(t, ts.DB.Pool, ts.OrgID)
	space := testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, owner.ID, "beacon")
	spaceID := space.ID

	def, err := ts.WorkflowAdapter.GetDefaultWorkflow(ctx, ts.OrgID, "tickets")
	require.NoError(t, err)
	require.NoError(t, ts.WorkflowAdapter.AssignDefaultWorkflowToSpace(ctx, ts.OrgID, "beacon", spaceID))

	states, err := ts.WorkflowAdapter.ListStates(ctx, def.ID)
	require.NoError(t, err)
	byName := map[string]uuid.UUID{}
	for _, s := range states {
		byName[s.Name] = s.ID
	}
	transitions, err := ts.WorkflowAdapter.ListTransitions(ctx, def.ID)
	require.NoError(t, err)
	var edge uuid.UUID
	for _, tr := range transitions {
		if tr.FromStateID == byName["open"] && tr.ToStateID == byName["in_progress"] {
			edge = tr.ID
		}
	}
	require.NotEqual(t, uuid.Nil, edge)

	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceID)
	r := ts.post(t, base+"/tickets", map[string]any{"title": "Gated ticket", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket: %s", r.Body)
	var created map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &created))
	require.Equal(t, "open", created["status"], "new tickets start open")

	return &tierAPIFixture{
		ts:       ts,
		spaceID:  spaceID,
		ticketID: uuid.MustParse(created["id"].(string)),
		tier:     adapters.NewWorkflowTierAdapter(generated.New(ts.DB.Pool)),
		edge:     edge,
	}
}

// transition posts a status change through the real route and returns the
// status code and body.
func (f *tierAPIFixture) transition(t *testing.T, status string) (int, map[string]any) {
	t.Helper()
	r := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets/%s/status",
		f.ts.OrgID, f.spaceID, f.ticketID), map[string]any{"status": status}, true)
	var body map[string]any
	_ = json.Unmarshal(r.Body, &body)
	return r.StatusCode, body
}

// statusNow re-reads the ticket straight from the database, so an assertion
// about "nothing was written" is about the row and not about the response.
func (f *tierAPIFixture) statusNow(t *testing.T) string {
	t.Helper()
	var status string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT status FROM tickets WHERE id = $1`, f.ticketID).Scan(&status))
	return status
}

// ─── The untouched workflow ───────────────────────────────────────────────────

// A space nobody has configured transitions exactly as it did before migration
// 046. This is the guarantee every other test in this file is measured against.
func TestTierAPI_UnconfiguredWorkflowTransitionsNormally(t *testing.T) {
	f := setupTierAPI(t)

	code, _ := f.transition(t, "in_progress")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "in_progress", f.statusNow(t))
}

// ─── Validators ───────────────────────────────────────────────────────────────

// The core fails-before test: a validator refuses the transition, the refusal
// names its reason, and NOTHING is written — asserted by re-reading the row
// rather than trusting the response.
func TestTierAPI_ValidatorRefusesAndWritesNothing(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	// Before the guard exists, the transition succeeds. That is what makes the
	// refusal below attributable to the guard and not to something else.
	code, _ := f.transition(t, "in_progress")
	require.Equal(t, http.StatusOK, code)
	_, _ = f.transition(t, "open") // back to the start

	_, err := f.tier.CreateGuard(ctx, workflow.Guard{
		TransitionID: f.edge,
		Class:        workflow.GuardValidatorClass,
		Kind:         workflow.GuardFieldRequired,
		FieldKey:     ptrTo(workflow.FieldDueAt),
	})
	require.NoError(t, err)

	code, body := f.transition(t, "in_progress")
	require.Equal(t, http.StatusUnprocessableEntity, code,
		"a configured validator refuses with 422: the request was well formed and the edge exists")
	require.Equal(t, "open", f.statusNow(t), "a refused transition must write nothing")

	// ADR-0011's case for this tier rests on the engine explaining itself, so
	// the reason has to survive to the client rather than collapsing into a
	// generic message.
	errObj, _ := body["error"].(map[string]any)
	require.Equal(t, "VALIDATION_ERROR", errObj["code"],
		"VALIDATION_ERROR is what lets friendlyErrorMessage pass the sentence through")
	require.Contains(t, errObj["message"], "due date")
}

// Satisfying the validator lets the same transition through. Without this, the
// test above would pass against a build that refuses everything.
func TestTierAPI_SatisfiedValidatorCommits(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	_, err := f.tier.CreateGuard(ctx, workflow.Guard{
		TransitionID: f.edge,
		Class:        workflow.GuardValidatorClass,
		Kind:         workflow.GuardFieldRequired,
		FieldKey:     ptrTo(workflow.FieldDueAt),
	})
	require.NoError(t, err)

	code, _ := f.transition(t, "in_progress")
	require.Equal(t, http.StatusUnprocessableEntity, code)

	_, err = f.ts.DB.Pool.Exec(ctx, `UPDATE tickets SET due_at = now() WHERE id = $1`, f.ticketID)
	require.NoError(t, err)

	code, _ = f.transition(t, "in_progress")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "in_progress", f.statusNow(t))
}

// A guard on one edge must not gate a different edge.
func TestTierAPI_GuardAppliesOnlyToItsOwnEdge(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	_, err := f.tier.CreateGuard(ctx, workflow.Guard{
		TransitionID: f.edge, // open -> in_progress
		Class:        workflow.GuardValidatorClass,
		Kind:         workflow.GuardFieldRequired,
		FieldKey:     ptrTo(workflow.FieldDueAt),
	})
	require.NoError(t, err)

	// open -> closed is a different edge and carries no guard.
	code, _ := f.transition(t, "closed")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "closed", f.statusNow(t))
}

// ─── Approvals ────────────────────────────────────────────────────────────────

// A gated transition answers 202 and does not move the item. 202 rather than a
// 4xx because the request was understood and recorded — what has not happened
// is the decision.
func TestTierAPI_ApprovalBlocksTheTransition(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	_, err := f.tier.CreateApprover(ctx, workflow.Approver{
		TransitionID: f.edge,
		SubjectType:  workflow.ApproverUser,
		SubjectID:    uuid.New(), // somebody else entirely
	})
	require.NoError(t, err)

	code, body := f.transition(t, "in_progress")
	require.Equal(t, http.StatusAccepted, code)
	require.Equal(t, "pending_approval", body["status"])
	require.Equal(t, "open", f.statusNow(t),
		"the item must not move while approval is pending — a 'closed pending approval' item defeats the gate")

	// The request is recorded, once.
	pending, err := f.tier.PendingApprovalForEntity(ctx, workflow.ApprovalEntityTicket, f.ticketID)
	require.NoError(t, err)
	require.True(t, pending.IsPending())
	require.Equal(t, "open", pending.FromStatus)
	require.Equal(t, "in_progress", pending.ToStatus)

	// Pressing it again returns the same request rather than a second one.
	code, body2 := f.transition(t, "in_progress")
	require.Equal(t, http.StatusAccepted, code)
	require.Equal(t, body["approval_id"], body2["approval_id"])
}

// ─── Post-functions ───────────────────────────────────────────────────────────

// A post-function's effect commits with the status change. Both are read back
// from the database, because the contract is about what was written.
func TestTierAPI_PostFunctionCommitsWithTheStatus(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	assignee := testutil.CreateTestUser(t, f.ts.DB.Pool, f.ts.OrgID)
	_, err := f.tier.CreatePostFunction(ctx, workflow.PostFunction{
		TransitionID:   f.edge,
		Kind:           workflow.PostAssignTo,
		AssigneeUserID: &assignee.ID,
	})
	require.NoError(t, err)

	code, _ := f.transition(t, "in_progress")
	require.Equal(t, http.StatusOK, code)

	var status string
	var gotAssignee *uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(ctx,
		`SELECT status, assignee_id FROM tickets WHERE id = $1`, f.ticketID).Scan(&status, &gotAssignee))
	require.Equal(t, "in_progress", status)
	require.NotNil(t, gotAssignee, "the post-function must have run")
	require.Equal(t, assignee.ID, *gotAssignee)

	// The audit row records the transition AND what ran, in one flat payload —
	// audit_log payloads are map[string]string and the viewer renders nested
	// JSON as [object Object].
	var payload []byte
	require.NoError(t, f.ts.DB.Pool.QueryRow(ctx,
		`SELECT payload FROM audit_log WHERE entity_id = $1 AND action = 'ticket.status_changed'
		 ORDER BY created_at DESC LIMIT 1`, f.ticketID).Scan(&payload))
	var meta map[string]string
	require.NoError(t, json.Unmarshal(payload, &meta))
	require.Equal(t, "in_progress", meta["to"])
	require.Equal(t, "assign_to", meta["post_functions"])
	require.NotEmpty(t, meta["workflow_transition_id"])
}

// A post-function this build cannot perform aborts the transition. The status
// must be unchanged: a transition that committed without its configured action
// is the silent-skip failure the fixed action set exists to prevent.
func TestTierAPI_UnknownPostFunctionAbortsAndWritesNothing(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	// Written past the CHECK, the way a newer build would have written it.
	_, err := f.ts.DB.Pool.Exec(ctx,
		`ALTER TABLE workflow_transition_post_functions DROP CONSTRAINT workflow_transition_post_functions_kind_valid`)
	require.NoError(t, err)
	_, err = f.ts.DB.Pool.Exec(ctx,
		`ALTER TABLE workflow_transition_post_functions DROP CONSTRAINT workflow_transition_post_functions_shape_valid`)
	require.NoError(t, err)
	_, err = f.ts.DB.Pool.Exec(ctx,
		`INSERT INTO workflow_transition_post_functions (transition_id, kind) VALUES ($1, 'send_carrier_pigeon')`, f.edge)
	require.NoError(t, err)

	code, _ := f.transition(t, "in_progress")
	require.Equal(t, http.StatusUnprocessableEntity, code)
	require.Equal(t, "open", f.statusNow(t),
		"an unperformable action must abort the transition, not be skipped past")
}

func ptrTo[T any](v T) *T { return &v }
