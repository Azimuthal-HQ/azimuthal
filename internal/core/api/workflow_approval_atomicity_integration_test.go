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
)

// D91: an approver's verdict and the transition it releases are ONE write.
//
// # What was wrong
//
// The route recorded the decision through one call and applied the transition
// through another, with nothing between them. Two failures followed.
//
// A failure in the second half left the approval marked approved, the entity
// unmoved, and the request no longer pending — so nobody could decide it again
// and nothing would retry. The route's own error message described the state it
// had just created: "the approval was recorded but the transition could not be
// applied".
//
// And the apply was UNCONDITIONAL. An approval captures the status the entity
// held when the request was made and is decided whenever an approver gets to it.
// Writing the captured target without checking overwrites whatever the entity
// became in between — a blind write of stale over fresh, with an audit row
// asserting a move from a status the entity had already left.
//
// # Why these are integration tests
//
// Both are about transaction boundaries. A unit test with a fake applier can
// show the service passes the right arguments; only a real database can show
// that a failed apply ROLLED THE VERDICT BACK, because that is a property of the
// transaction rather than of the call.

// wfaApprovalFixture builds a beacon space whose open -> in_progress edge needs
// approval, a ticket sitting at open, and a pending request for that move.
type wfaApprovalFixture struct {
	base       string
	ticketPath string
	ticketID   uuid.UUID
	approvalID uuid.UUID
	approverTk string
}

func wfaFixture(t *testing.T, ts *testServer) wfaApprovalFixture {
	t.Helper()
	ctx := context.Background()

	spaceIDStr := wsnegWorkflowSpace(t, ts, "beacon", "Atomic Desk", "atomic-desk")
	spaceID := uuid.MustParse(spaceIDStr)
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", ts.OrgID, spaceIDStr)

	// The approver is an agent in the space: they must clear the route's own
	// transition floor to be able to act at all, and approval authority is data
	// on top of that, never instead of it.
	approver, approverTk := wsnegPersona(t, ts, spaceID, access.RoleAgent)

	edge := wfcEdge(t, ts, "tickets", "open", "in_progress")
	_, err := adapters.NewWorkflowTierAdapter(generated.New(ts.DB.Pool)).
		CreateApprover(ctx, workflow.Approver{
			TransitionID: edge,
			SubjectType:  workflow.ApproverUser,
			SubjectID:    approver.ID,
		})
	require.NoError(t, err)

	r := ts.post(t, base+"/tickets", map[string]any{
		"title": "Needs a decision", "priority": "medium",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
	ticketID := uuid.MustParse(decodeJSONMap(t, r.Body)["id"].(string))
	ticketPath := fmt.Sprintf("%s/tickets/%s", base, ticketID)

	// Requesting the guarded move creates the approval and does NOT move the
	// ticket. 202, not 200: the request was understood and recorded, and what
	// has not happened yet is the decision.
	r = ts.post(t, ticketPath+"/status", map[string]any{"status": "in_progress"}, true)
	require.Equal(t, http.StatusAccepted, r.StatusCode, "a guarded move must be pending: %s", r.Body)
	var pending struct {
		ApprovalID uuid.UUID `json:"approval_id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &pending))
	require.NotEqual(t, uuid.Nil, pending.ApprovalID)
	require.Equal(t, "open", wfcStatus(t, ts, ticketPath), "a pending approval does not move the item")

	return wfaApprovalFixture{
		base: base, ticketPath: ticketPath, ticketID: ticketID,
		approvalID: pending.ApprovalID, approverTk: approverTk,
	}
}

// wfaApprovalIsPending reads the decision columns straight from the table.
//
// Through the API a decided approval and a pending one differ only in fields a
// read model could compute; the question here is what is STORED, so the
// assertion goes to the row.
func wfaApprovalIsPending(t *testing.T, ts *testServer, id uuid.UUID) bool {
	t.Helper()
	var decidedAt *string
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT decided_at::text FROM workflow_approvals WHERE id = $1`, id).Scan(&decidedAt))
	return decidedAt == nil
}

// TestWorkflowApprovalAtomicity_AStaleApprovalCannotBlindlyOverwrite is the
// compare-and-swap, and it is the failure mode that loses data rather than
// merely confusing somebody.
//
// The ticket moves AFTER the approval is requested and BEFORE it is decided,
// which is ordinary: approvals are asynchronous by design. Approving then must
// not write the captured target over the ticket's real status.
//
// # The mutation test
//
// Remove `AND status = @expect_status` from UpdateTicketWorkflowState and the
// decision lands, the ticket is dragged back to in_progress from closed, and the
// audit trail claims a move from "open" that never happened.
func TestWorkflowApprovalAtomicity_AStaleApprovalCannotBlindlyOverwrite(t *testing.T) {
	ts := newTestServer(t)
	f := wfaFixture(t, ts)

	// The ticket moves on while the approval waits. open -> closed is a defined
	// edge and carries no approver, so it commits immediately.
	r := ts.post(t, f.ticketPath+"/status", map[string]any{"status": "closed"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "open -> closed: %s", r.Body)
	require.Equal(t, "closed", wfcStatus(t, ts, f.ticketPath))

	// Now the approver approves a move the ticket has outgrown.
	res := ts.postAs(t, f.approverTk,
		fmt.Sprintf("%s/workflow/approvals/%s/decide", f.base, f.approvalID),
		map[string]any{"decision": "approved"})
	require.Equal(t, http.StatusConflict, res.StatusCode,
		"a stale approval must not blind-overwrite: %s", res.Body)

	require.Equal(t, "closed", wfcStatus(t, ts, f.ticketPath),
		"the ticket must keep the status it actually reached, not the one the approval captured")

	// And the verdict rolled back with the write. This is the half that makes
	// the refusal recoverable: the approval is still pending, so somebody can
	// decide it against what the ticket really is now.
	require.True(t, wfaApprovalIsPending(t, ts, f.approvalID),
		"a refused apply must roll the verdict back, or the request is stranded decided-but-unapplied")
}

// TestWorkflowApprovalAtomicity_AFailedApplyLeavesNoDecidedButUnappliedApproval
// is the other half of D91: the verdict and the write share a transaction, so
// there is no ordering in which one lands without the other.
//
// # How the failure is injected
//
// By DELETING THE EDGE the approval was requested for, between the request and
// the decision. migration 047's ON DELETE SET NULL keeps the approval row and
// nulls its transition_id, so the decide path finds a request it cannot resolve
// — a real production state (an administrator editing a workflow with requests
// in flight), reached through the real admin route rather than through a fault
// injected into a double.
//
// The important assertion is not the status code. It is that the approval is
// still PENDING afterwards: the old two-call shape recorded the verdict first,
// so any later failure left it decided.
func TestWorkflowApprovalAtomicity_AFailedApplyLeavesNoDecidedButUnappliedApproval(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()
	f := wfaFixture(t, ts)

	edge := wfcEdge(t, ts, "tickets", "open", "in_progress")
	_, err := ts.DB.Pool.Exec(ctx, `DELETE FROM workflow_transitions WHERE id = $1`, edge)
	require.NoError(t, err)

	res := ts.postAs(t, f.approverTk,
		fmt.Sprintf("%s/workflow/approvals/%s/decide", f.base, f.approvalID),
		map[string]any{"decision": "approved"})
	require.Equal(t, http.StatusConflict, res.StatusCode,
		"an approval whose edge is gone is unresolvable: %s", res.Body)

	require.Equal(t, "open", wfcStatus(t, ts, f.ticketPath),
		"nothing may be written when the decision could not be honoured")
	require.True(t, wfaApprovalIsPending(t, ts, f.approvalID),
		"the verdict must not survive a failure to apply it")
}

// TestWorkflowApprovalAtomicity_TheHappyPathCommitsBothHalves keeps the two
// tests above from passing vacuously.
//
// Both of them assert that a decision did NOT land. Without this, a route that
// refused every decision would satisfy them completely.
func TestWorkflowApprovalAtomicity_TheHappyPathCommitsBothHalves(t *testing.T) {
	ts := newTestServer(t)
	f := wfaFixture(t, ts)

	res := ts.postAs(t, f.approverTk,
		fmt.Sprintf("%s/workflow/approvals/%s/decide", f.base, f.approvalID),
		map[string]any{"decision": "approved"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	require.Equal(t, "in_progress", wfcStatus(t, ts, f.ticketPath),
		"an approved transition must actually move the ticket")
	require.False(t, wfaApprovalIsPending(t, ts, f.approvalID),
		"and the verdict must be recorded")

	// The audit row commits with them, and names the approval that released the
	// move. A trail claiming a transition applied must roll back with it, which
	// is only meaningful if it is written in the same transaction.
	var payload []byte
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT payload FROM audit_log
		  WHERE entity_kind = 'ticket' AND entity_id = $1 AND action = 'ticket.status_changed'
		  ORDER BY created_at DESC LIMIT 1`, f.ticketID).Scan(&payload))
	var meta map[string]string
	require.NoError(t, json.Unmarshal(payload, &meta))
	require.Equal(t, "in_progress", meta["to"])
	require.Equal(t, f.approvalID.String(), meta["approval_id"],
		"the trail must say which approval released this move")
}

// TestWorkflowApprovalAtomicity_ADeclineRecordsTheVerdictAndMovesNothing pins
// the other verdict. A decline is the one path that legitimately writes the
// approval without writing the entity, so it must not be swept up by the
// all-or-nothing rule.
func TestWorkflowApprovalAtomicity_ADeclineRecordsTheVerdictAndMovesNothing(t *testing.T) {
	ts := newTestServer(t)
	f := wfaFixture(t, ts)

	res := ts.postAs(t, f.approverTk,
		fmt.Sprintf("%s/workflow/approvals/%s/decide", f.base, f.approvalID),
		map[string]any{"decision": "declined", "reason": "the release is frozen until Monday"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", res.Body)

	require.Equal(t, "open", wfcStatus(t, ts, f.ticketPath),
		"a decline moves nothing: the ticket never left its source status")
	require.False(t, wfaApprovalIsPending(t, ts, f.approvalID),
		"but the decline itself is recorded, with its reason")
}
