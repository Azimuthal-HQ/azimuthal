package workflow

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The approval surface added two read-time answers that no stored column
// carries: WHO must be told a decision is waiting, and WHETHER the person
// reading may make it. Both are derived from the same two facts CanDecide
// consults, and the tests below exist to keep them derived from those facts
// rather than from something more convenient that happens to agree today.

// ─── ApproverRecipients ───────────────────────────────────────────────────────

// A team approver notifies through ADR-0007 EFFECTIVE membership, not through
// the team's direct member rows.
//
// This is the asymmetry worth pinning. CanDecide accepts an actor whose
// effective set contains the team; if the notifier instead read direct members,
// somebody the gate would happily accept as an approver would never be told the
// approval existed. The failure is invisible from both ends — the approver sees
// no alert, the requester sees an item that will not move — so nothing surfaces
// it except a test that makes the two sources differ.
func TestApproverRecipients_TeamExpansionUsesTheEffectiveSet(t *testing.T) {
	t.Parallel()

	team := uuid.New()
	viaAncestor := uuid.New()
	f, edge := configured()
	f.approvers[edge] = []Approver{{TransitionID: edge, SubjectType: ApproverTeam, SubjectID: team}}
	// The store's effective expansion reports this person for the team even
	// though no direct membership row is modelled here.
	f.teamMembers[team] = []uuid.UUID{viaAncestor}

	got, err := NewTierService(f, &fakeApplier{store: f}).ApproverRecipients(context.Background(), uuid.New(), edge)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{viaAncestor}, got)
}

// Somebody named directly AND through a team is one recipient, not two.
func TestApproverRecipients_DeduplicatesAcrossSubjects(t *testing.T) {
	t.Parallel()

	person := uuid.New()
	team := uuid.New()
	other := uuid.New()
	f, edge := configured()
	f.approvers[edge] = []Approver{
		{TransitionID: edge, SubjectType: ApproverUser, SubjectID: person},
		{TransitionID: edge, SubjectType: ApproverTeam, SubjectID: team},
	}
	f.teamMembers[team] = []uuid.UUID{person, other}

	got, err := NewTierService(f, &fakeApplier{store: f}).ApproverRecipients(context.Background(), uuid.New(), edge)
	require.NoError(t, err)
	require.Len(t, got, 2, "being named twice is not a reason to be alerted twice")
	require.Contains(t, got, person)
	require.Contains(t, got, other)
}

// An approver subject kind this build cannot resolve contributes nobody.
//
// Same fail-closed direction as CanDecide for the same value. Here it is quiet
// rather than unsafe — nobody is told — which is why it needs saying out loud:
// the alternative, notifying everybody, would leak an approval to people the
// gate would refuse.
func TestApproverRecipients_UnknownSubjectTypeContributesNobody(t *testing.T) {
	t.Parallel()

	f, edge := configured()
	f.approvers[edge] = []Approver{
		{TransitionID: edge, SubjectType: ApproverSubjectType("role"), SubjectID: uuid.New()},
	}

	got, err := NewTierService(f, &fakeApplier{store: f}).ApproverRecipients(context.Background(), uuid.New(), edge)
	require.NoError(t, err)
	require.Empty(t, got)
}

// ─── MarkDecidable ────────────────────────────────────────────────────────────

// CanDecide is filled from the approver rows and the actor's effective teams —
// the same two facts the decide route enforces — so the button the client shows
// and the answer the server gives cannot disagree.
func TestMarkDecidable_AgreesWithTheDecideRoute(t *testing.T) {
	t.Parallel()

	approver := uuid.New()
	stranger := uuid.New()
	f, edge := configured()
	f.approvers[edge] = []Approver{{TransitionID: edge, SubjectType: ApproverUser, SubjectID: approver}}

	svc := NewTierService(f, &fakeApplier{store: f})
	gated, err := svc.Gate(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)
	require.NotNil(t, gated.Pending)

	orgID := uuid.New()

	forApprover, err := svc.MarkDecidable(context.Background(), orgID, approver, []Approval{*gated.Pending})
	require.NoError(t, err)
	require.True(t, forApprover[0].CanDecide)

	forStranger, err := svc.MarkDecidable(context.Background(), orgID, stranger, []Approval{*gated.Pending})
	require.NoError(t, err)
	require.False(t, forStranger[0].CanDecide)

	// And the claim is not merely cosmetic: the route refuses exactly the
	// person MarkDecidable said it would. A CanDecide computed from anything
	// else would let these two drift.
	// SpaceID is the approval's own, so the request reaches the authority
	// check. Left zero it would be refused as not-found first and the assertion
	// below would pass without the approver logic ever having run.
	_, err = svc.Decide(context.Background(), DecideRequest{
		OrgID: orgID, SpaceID: gated.Pending.SpaceID, ApprovalID: gated.Pending.ID,
		ActorID: stranger, Decision: DecisionApproved,
	})
	require.ErrorIs(t, err, ErrNotAnApprover)
}

// An approval whose edge has been deleted is decidable by nobody, and is still
// returned rather than hidden.
//
// migration 047 keeps the row (ON DELETE SET NULL) precisely so the requester
// can see their request is stuck. Dropping it from the list would restore the
// silence the record was preserved to break.
func TestMarkDecidable_ADeletedEdgeIsDecidableByNobodyAndStillListed(t *testing.T) {
	t.Parallel()

	orphan := Approval{ID: uuid.New(), TransitionID: nil, FromStatus: "open", ToStatus: "closed"}

	got, err := NewTierService(newFakeStore(), &fakeApplier{}).
		MarkDecidable(context.Background(), uuid.New(), uuid.New(), []Approval{orphan})
	require.NoError(t, err)
	require.Len(t, got, 1, "a stuck request must stay visible to the person who made it")
	require.False(t, got[0].CanDecide)
}
