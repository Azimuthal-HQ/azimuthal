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
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The approval cycle and the tier configuration API, driven through the fully
// wired router. The fixture is setupTierAPI from
// workflow_tiers_integration_test.go.

// approvalPath is the decide endpoint for one request.
func (f *tierAPIFixture) approvalPath(id string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/workflow/approvals/%s/decide", f.ts.OrgID, f.spaceID, id)
}

// requestApproval gates the edge on the given approver and presses the
// transition, returning the pending approval's id.
func (f *tierAPIFixture) requestApproval(t *testing.T, approver uuid.UUID) string {
	t.Helper()
	_, err := f.tier.CreateApprover(context.Background(), workflow.Approver{
		TransitionID: f.edge, SubjectType: workflow.ApproverUser, SubjectID: approver,
	})
	require.NoError(t, err)

	code, body := f.transition(t, "in_progress")
	require.Equal(t, http.StatusAccepted, code)
	return body["approval_id"].(string)
}

// ─── The full cycle, both verdicts ────────────────────────────────────────────

// Approving applies the transition the request captured — and only then does
// the item move.
func TestTierAPI_ApprovalCycle_Approve(t *testing.T) {
	f := setupTierAPI(t)

	// The harness token belongs to ts.UserID, so naming that user the approver
	// is what lets the same client decide.
	approvalID := f.requestApproval(t, f.ts.UserID)
	require.Equal(t, "open", f.statusNow(t), "the request alone must not move the item")

	r := f.ts.post(t, f.approvalPath(approvalID), map[string]any{"decision": "approved"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)

	var decided map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &decided))
	require.Equal(t, "approved", decided["decision"])
	require.Equal(t, "in_progress", f.statusNow(t), "approval applies the captured transition")

	// A second decision on the same request is refused, in either direction.
	// The reason is supplied so the already-decided check is what refuses this,
	// not the decline-needs-a-reason check that runs before it (migration 050).
	r = f.ts.post(t, f.approvalPath(approvalID),
		map[string]any{"decision": "declined", "reason": "changed my mind"}, true)
	require.Equal(t, http.StatusConflict, r.StatusCode)
	require.Equal(t, "in_progress", f.statusNow(t))
}

// Declining records the verdict and applies nothing. The item never left its
// source status, which is what "decline returns the item to the source status"
// means when the gate blocks rather than moves.
func TestTierAPI_ApprovalCycle_Decline(t *testing.T) {
	f := setupTierAPI(t)

	approvalID := f.requestApproval(t, f.ts.UserID)

	// A bare decline is refused before anything is written (migration 050): a
	// decline the requester cannot read is the silent no-op this tier exists to
	// prevent, arriving one layer later than the guards.
	r := f.ts.post(t, f.approvalPath(approvalID), map[string]any{"decision": "declined"}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s", r.Body)
	require.Equal(t, "open", f.statusNow(t))

	r = f.ts.post(t, f.approvalPath(approvalID),
		map[string]any{"decision": "declined", "reason": "the release is frozen until Monday"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)

	var decided map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &decided))
	require.Equal(t, "declined", decided["decision"])
	require.Equal(t, "the release is frozen until Monday", decided["reason"],
		"the reason must survive the round trip, or the surface has nothing to show")
	require.Equal(t, "open", f.statusNow(t), "a declined transition leaves the item exactly where it was")

	// The request is decided, so the item is free to ask again — the partial
	// unique index excludes decided rows precisely so history can accumulate.
	code, _ := f.transition(t, "in_progress")
	require.Equal(t, http.StatusAccepted, code)
}

// The persona that proves the check: someone holding transition_any_item — past
// the route's own write floor, and able to move the item on any ungated edge —
// who is not a configured approver. Authority to decide is DATA, not a
// capability, so the capability they hold buys them nothing here.
func TestTierAPI_OnlyAConfiguredApproverMayDecide(t *testing.T) {
	f := setupTierAPI(t)

	approvalID := f.requestApproval(t, uuid.New()) // somebody else entirely

	r := f.ts.post(t, f.approvalPath(approvalID), map[string]any{"decision": "approved"}, true)
	require.Equal(t, http.StatusForbidden, r.StatusCode, "a non-approver must be refused: %s", r.Body)
	require.Equal(t, "open", f.statusNow(t), "a refused decision must not move the item")
}

// An unrecognised verdict is refused rather than guessed at — the rule
// access.ParseRole states for roles, applied to decisions.
func TestTierAPI_DecisionVocabularyIsClosed(t *testing.T) {
	f := setupTierAPI(t)
	approvalID := f.requestApproval(t, f.ts.UserID)

	for _, bad := range []string{"", "APPROVED", "maybe", "pending"} {
		r := f.ts.post(t, f.approvalPath(approvalID), map[string]any{"decision": bad}, true)
		require.Equal(t, http.StatusBadRequest, r.StatusCode, "decision %q must be refused", bad)
	}
	require.Equal(t, "open", f.statusNow(t))
}

// The pending list is what the board reads to mark an item blocked, so it must
// show the request to anyone who can see the space — not only to approvers.
func TestTierAPI_PendingApprovalsAreVisibleInTheSpace(t *testing.T) {
	f := setupTierAPI(t)
	approvalID := f.requestApproval(t, uuid.New())

	r := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/workflow/approvals", f.ts.OrgID, f.spaceID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)

	var pending []map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &pending))
	require.Len(t, pending, 1)
	require.Equal(t, approvalID, pending[0]["id"])
	require.Equal(t, "open", pending[0]["from_status"])
	require.Equal(t, "in_progress", pending[0]["to_status"])
}

// ─── Configuration API ────────────────────────────────────────────────────────

// The configuration surface refuses anything outside the closed vocabulary
// before it is written, so a caller gets a sentence rather than a constraint
// violation. Each case is also refused by a CHECK one layer down.
func TestTierAPI_GuardCreateRefusesUnknownVocabulary(t *testing.T) {
	f := setupTierAPI(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/workflows/%s/transitions/%s/guards",
		f.ts.OrgID, f.workflowID, f.edge)

	cases := map[string]map[string]any{
		"unknown kind":               {"guard_class": "validator", "kind": "actor_is_lucky"},
		"unknown class":              {"guard_class": "sometimes", "kind": "actor_is_assignee"},
		"unknown field":              {"guard_class": "validator", "kind": "field_required", "field_key": "story_points"},
		"priority is not requirable": {"guard_class": "validator", "kind": "field_required", "field_key": "priority"},
		"missing parameter":          {"guard_class": "validator", "kind": "field_required"},
		"capability outside the guard subset": {
			"guard_class": "condition", "kind": "actor_has_capability", "capability": "read_items",
		},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r := f.ts.post(t, base, body, true)
			require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s", r.Body)
		})
	}

	// A well-formed guard is accepted, so the refusals above are about the
	// vocabulary and not about the endpoint being broken.
	r := f.ts.post(t, base, map[string]any{
		"guard_class": "validator", "kind": "field_required", "field_key": "due_at",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
}

// The two post-function kinds ADR-0011 names but this build cannot perform are
// refused at the API, so neither can ever reach the transition path.
func TestTierAPI_PostFunctionCreateRefusesUnbuiltActions(t *testing.T) {
	f := setupTierAPI(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/workflows/%s/transitions/%s/post-functions",
		f.ts.OrgID, f.workflowID, f.edge)

	for _, kind := range []string{"add_comment", "transition_linked_item"} {
		r := f.ts.post(t, base, map[string]any{"kind": kind}, true)
		require.Equal(t, http.StatusBadRequest, r.StatusCode,
			"%q is ADR-sanctioned but not built here and must not be writable: %s", kind, r.Body)
	}

	// And the one field a post-function must never write, because doing so
	// would overwrite author-written prose on every transition.
	r := f.ts.post(t, base, map[string]any{
		"kind": "set_field", "field_key": "description", "field_value": "overwritten",
	}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s", r.Body)

	r = f.ts.post(t, base, map[string]any{
		"kind": "set_field", "field_key": "due_at", "field_value": "2026-08-01T09:30:00Z",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
}

// Approver-by-role is named by ADR-0011 and has no representation in this
// product. It is refused rather than approximated.
func TestTierAPI_ApproverCreateRefusesRoleSubjects(t *testing.T) {
	f := setupTierAPI(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/workflows/%s/transitions/%s/approvers",
		f.ts.OrgID, f.workflowID, f.edge)

	r := f.ts.post(t, base, map[string]any{"subject_type": "role", "subject_id": uuid.New().String()}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s", r.Body)

	r = f.ts.post(t, base, map[string]any{"subject_type": "user", "subject_id": f.ts.UserID.String()}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)

	// The same subject twice is a conflict, not a duplicate row.
	r = f.ts.post(t, base, map[string]any{"subject_type": "user", "subject_id": f.ts.UserID.String()}, true)
	require.Equal(t, http.StatusConflict, r.StatusCode, "%s", r.Body)
}

// A workflow belonging to another org is not reachable by id from this org's
// path. The pre-existing workflow routes do not make this check; every route
// this phase adds does, so the new surface does not widen the exposure.
func TestTierAPI_TierRoutesAreScopedToTheOrg(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	otherOrg := testutil.CreateTestOrg(t, f.ts.DB.Pool)
	require.NoError(t, f.ts.WorkflowAdapter.SeedDefaultWorkflows(ctx, otherOrg.ID))
	otherWF, err := f.ts.WorkflowAdapter.GetDefaultWorkflow(ctx, otherOrg.ID, "tickets")
	require.NoError(t, err)

	// The caller's own org id in the path, another org's workflow id after it.
	r := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/workflows/%s/transitions/%s/guards",
		f.ts.OrgID, otherWF.ID, f.edge), true)
	require.Equal(t, http.StatusNotFound, r.StatusCode,
		"a workflow in another org must be indistinguishable from one that does not exist: %s", r.Body)
}
