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

// P-W PR-B's server-side additions, through the fully wired router:
// can_decide, the per-entity approval history, and D74's org scoping.

// ─── can_decide ───────────────────────────────────────────────────────────────

// can_decide must agree with what the decide route actually does.
//
// It exists because approval authority is DATA — being named on the transition
// — so a client has no way to work out whether to offer an Approve button.
// If the flag and the route ever disagreed, the failure would be silent in both
// directions: a button that 403s, or a decision somebody could have made and was
// never offered.
func TestTierAPI_CanDecideAgreesWithTheDecideRoute(t *testing.T) {
	f := setupTierAPI(t)

	// The harness token belongs to ts.UserID. Name a DIFFERENT user as the
	// approver, so the caller is past the write floor and short of the
	// authority — the persona CLAUDE.md §2 requires for a gate like this. A
	// viewer would be refused upstream by RequireWriteFloor and would prove
	// nothing about workflow.CanDecide.
	stranger := testutil.CreateTestUser(t, f.ts.DB.Pool, f.ts.OrgID)
	approvalID := f.requestApproval(t, stranger.ID)

	listed := f.pendingApprovals(t)
	require.Len(t, listed, 1)
	require.Equal(t, approvalID, listed[0]["id"])
	require.Equal(t, false, listed[0]["can_decide"],
		"the caller is not a configured approver, so the surface must not offer the decision")

	// And the claim is not cosmetic: the route refuses exactly this caller.
	r := f.ts.post(t, f.approvalPath(approvalID), map[string]any{"decision": "approved"}, true)
	require.Equal(t, http.StatusForbidden, r.StatusCode, "%s", r.Body)
}

// The other direction: a named approver is told they may decide, and may.
func TestTierAPI_CanDecideIsTrueForANamedApprover(t *testing.T) {
	f := setupTierAPI(t)

	approvalID := f.requestApproval(t, f.ts.UserID)

	listed := f.pendingApprovals(t)
	require.Len(t, listed, 1)
	require.Equal(t, true, listed[0]["can_decide"])

	r := f.ts.post(t, f.approvalPath(approvalID), map[string]any{"decision": "approved"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
}

// ─── The per-entity approval history ──────────────────────────────────────────

// A declined approval leaves the PENDING list but stays readable per entity.
//
// This is the whole reason the per-entity route exists. The detail surface shows
// the decline reason, and if it were built on the space's pending list the
// reason would vanish at the moment it became relevant — the requester would
// see a blocked item and then nothing at all.
func TestTierAPI_ADeclinedApprovalLeavesThePendingListButKeepsItsReason(t *testing.T) {
	f := setupTierAPI(t)

	approvalID := f.requestApproval(t, f.ts.UserID)
	require.Len(t, f.pendingApprovals(t), 1, "it must be pending before it is decided")

	r := f.ts.post(t, f.approvalPath(approvalID),
		map[string]any{"decision": "declined", "reason": "the release is frozen"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)

	require.Empty(t, f.pendingApprovals(t),
		"a decided approval is no longer pending, which is why the space list cannot serve the detail page")

	history := f.entityApprovals(t, "ticket", f.ticketID.String())
	require.Len(t, history, 1, "the per-entity read must still carry it")
	require.Equal(t, "declined", history[0]["decision"])
	require.Equal(t, "the release is frozen", history[0]["reason"])
	require.Equal(t, "open", f.statusNow(t), "and the item never left its source status")
}

// The entity discriminator is validated, not guessed.
//
// tickets and project_items are separate tables (ADR-0003) and their ids are not
// unique across the pair, so an unrecognised entity type must be a 400 naming
// the two that exist rather than a silently empty list.
func TestTierAPI_EntityApprovalsRefusesAnUnknownEntityType(t *testing.T) {
	f := setupTierAPI(t)

	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/workflow/entities", f.ts.OrgID, f.spaceID)
	r := f.ts.get(t, fmt.Sprintf("%s/project_item/%s/approvals", base, f.ticketID), true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s", r.Body)
	require.Contains(t, string(r.Body), "ticket")
	require.Contains(t, string(r.Body), "item")
}

// ─── D74 ──────────────────────────────────────────────────────────────────────

// The pre-existing workflow routes are scoped to the org.
//
// Before this PR they resolved {workflowID} and acted on it without asking whose
// it was, so another organisation's workflow was readable, its states and
// transitions listable, and its edges deletable by id. PR #86 routed its own
// nine tier routes around the hole and recorded the rest; the admin editor made
// it unavoidable, because an editor cannot show a transition without listing
// transitions.
//
// Fails-before: with the workflowInOrg check removed, every subtest below sees
// 200 and the DELETE actually destroys the other org's edge.
func TestWorkflowRoutes_AreScopedToTheOrg(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	// A second org with its own workflow, reached through the FIRST org's URL
	// prefix using the first org's token.
	otherOrg := testutil.CreateTestOrg(t, f.ts.DB.Pool)
	require.NoError(t, f.ts.WorkflowAdapter.SeedDefaultWorkflows(ctx, otherOrg.ID))
	otherWF, err := f.ts.WorkflowAdapter.GetDefaultWorkflow(ctx, otherOrg.ID, "tickets")
	require.NoError(t, err)

	otherStates, err := f.ts.WorkflowAdapter.ListStates(ctx, otherWF.ID)
	require.NoError(t, err)
	require.NotEmpty(t, otherStates)
	otherTransitions, err := f.ts.WorkflowAdapter.ListTransitions(ctx, otherWF.ID)
	require.NoError(t, err)
	require.NotEmpty(t, otherTransitions)

	mine := fmt.Sprintf("/api/v1/orgs/%s/workflows/%s", f.ts.OrgID, otherWF.ID)

	// 404 rather than 403 throughout: a workflow in another org must not be
	// distinguishable from one that does not exist, because a 403 confirms the
	// id is real.
	for _, path := range []string{"", "/states", "/transitions"} {
		r := f.ts.get(t, mine+path, true)
		require.Equal(t, http.StatusNotFound, r.StatusCode,
			"GET %s must not reach another org's workflow: %s", path, r.Body)
	}

	// The write side matters more than the read side, and was open the same way.
	r := f.ts.delete(t, fmt.Sprintf("%s/transitions/%s", mine, otherTransitions[0].ID), true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "%s", r.Body)

	survivors, err := f.ts.WorkflowAdapter.ListTransitions(ctx, otherWF.ID)
	require.NoError(t, err)
	require.Len(t, survivors, len(otherTransitions),
		"the refused DELETE must have destroyed nothing in the other org")
}

// A child row cannot be deleted through a transition it does not belong to.
//
// resolveTransition proved workflow-in-org and transition-in-workflow, and then
// the handler deleted by the raw child id. Pairing one of your OWN transitions
// with a foreign guard id removed it — including across organisations.
func TestTierAPI_AGuardCannotBeDeletedThroughAnotherTransition(t *testing.T) {
	f := setupTierAPI(t)
	ctx := context.Background()

	guard, err := f.tier.CreateGuard(ctx, workflow.Guard{
		TransitionID: f.edge,
		Class:        workflow.GuardValidatorClass,
		Kind:         workflow.GuardActorIsAssignee,
	})
	require.NoError(t, err)

	// Another edge in the SAME workflow, so the request passes every scope check
	// the handler makes and only the child-belongs-to-parent check can refuse it.
	transitions, err := f.ts.WorkflowAdapter.ListTransitions(ctx, f.workflowID)
	require.NoError(t, err)
	var otherEdge uuid.UUID
	for _, tr := range transitions {
		if tr.ID != f.edge {
			otherEdge = tr.ID
			break
		}
	}
	require.NotEqual(t, uuid.Nil, otherEdge, "the seeded workflow must define more than one edge")

	r := f.ts.delete(t, fmt.Sprintf(
		"/api/v1/orgs/%s/workflows/%s/transitions/%s/guards/%s",
		f.ts.OrgID, f.workflowID, otherEdge, guard.ID), true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "%s", r.Body)

	still, err := f.tier.GuardsForTransition(ctx, f.edge)
	require.NoError(t, err)
	require.Len(t, still, 1, "the mis-scoped delete must have removed nothing")
}

// ─── Fixture helpers ──────────────────────────────────────────────────────────

func (f *tierAPIFixture) pendingApprovals(t *testing.T) []map[string]any {
	t.Helper()
	r := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/workflow/approvals",
		f.ts.OrgID, f.spaceID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &out))
	return out
}

func (f *tierAPIFixture) entityApprovals(t *testing.T, entityType, entityID string) []map[string]any {
	t.Helper()
	r := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/workflow/entities/%s/%s/approvals",
		f.ts.OrgID, f.spaceID, entityType, entityID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &out))
	return out
}
