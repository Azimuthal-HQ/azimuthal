package tiergate

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
)

// The approval-requested notification is emitted HERE, at the chokepoint, and
// not in the four handlers that can change an item's status. These tests pin
// that placement and the conditions it is gated on.
//
// PR #86's premise that notifications were already wired was false — no
// enqueuer, no kind string and no call site existed anywhere in the workflow
// packages — so everything below is new ground rather than regression cover.

// stubTierStore answers only the one read notifyApprovers needs.
//
// The embedded interface is nil ON PURPOSE: any method these tests do not model
// panics rather than returning a zero value. A fake that answered every method
// with nil would let this test pass while exercising a path nobody wrote down,
// which is the failure mode CLAUDE.md §2 calls a test that cannot fail.
type stubTierStore struct {
	workflow.TierStore
	approvers []workflow.Approver
}

func (s stubTierStore) ApproversForTransition(context.Context, uuid.UUID) ([]workflow.Approver, error) {
	return s.approvers, nil
}

// recordingEnqueuer captures what would have been sent.
type recordingEnqueuer struct{ sent []jobs.NotificationArgs }

func (r *recordingEnqueuer) EnqueueNotification(_ context.Context, a jobs.NotificationArgs) error {
	r.sent = append(r.sent, a)
	return nil
}

func (r *recordingEnqueuer) recipients() []string {
	out := make([]string, 0, len(r.sent))
	for _, a := range r.sent {
		out = append(out, a.UserID)
	}
	return out
}

// gateFor builds a Gate whose transition is approved by the given users.
func gateFor(rec *recordingEnqueuer, transitionID uuid.UUID, approvers ...uuid.UUID) *Gate {
	rows := make([]workflow.Approver, len(approvers))
	for i, a := range approvers {
		rows[i] = workflow.Approver{
			TransitionID: transitionID,
			SubjectType:  workflow.ApproverUser,
			SubjectID:    a,
		}
	}
	svc := workflow.NewTierService(stubTierStore{approvers: rows}, stubApplier{})
	// The workflow resolver is never reached: these tests call notifyApprovers
	// directly with an already-decided GateResult, so nil is honest rather than
	// convenient — if the code started resolving a workflow here, it would
	// panic instead of quietly working.
	return New(svc, nil, rec)
}

func pendingResult(transitionID, entityID uuid.UUID, isNew bool) workflow.TransitionDecision {
	return workflow.TransitionDecision{
		TransitionID: &transitionID,
		PendingIsNew: isNew,
		Pending: &workflow.Approval{
			ID:         uuid.New(),
			EntityType: workflow.ApprovalEntityTicket,
			EntityID:   entityID,
			FromStatus: "open",
			ToStatus:   "in_progress",
		},
	}
}

func request(actorID, spaceID uuid.UUID) Request {
	return Request{OrgID: uuid.New(), SpaceID: spaceID, ActorID: actorID}
}

// A retry must not re-alert. Two people pressing the same guarded button is the
// ordinary case, and the second press returns the FIRST person's still-pending
// approval rather than creating a second one. Notifying again on every retry
// turns one decision into a stream of identical alerts, which is how people
// learn to ignore the real one.
//
// This is the test that dies if PendingIsNew is dropped from the condition.
func TestNotifyApprovers_OnlyForARequestThisCallCreated(t *testing.T) {
	t.Parallel()

	approver, transitionID := uuid.New(), uuid.New()

	t.Run("a newly created request notifies", func(t *testing.T) {
		t.Parallel()
		rec := &recordingEnqueuer{}
		gateFor(rec, transitionID, approver).notifyApprovers(
			context.Background(),
			request(uuid.New(), uuid.New()),
			pendingResult(transitionID, uuid.New(), true),
		)
		require.Equal(t, []string{approver.String()}, rec.recipients())
	})

	t.Run("an already-outstanding request does not", func(t *testing.T) {
		t.Parallel()
		rec := &recordingEnqueuer{}
		gateFor(rec, transitionID, approver).notifyApprovers(
			context.Background(),
			request(uuid.New(), uuid.New()),
			pendingResult(transitionID, uuid.New(), false),
		)
		require.Empty(t, rec.sent,
			"a retry returns the existing approval; re-notifying trains people to ignore the alert")
	})

	t.Run("a transition that was never gated does not", func(t *testing.T) {
		t.Parallel()
		rec := &recordingEnqueuer{}
		gateFor(rec, transitionID, approver).notifyApprovers(
			context.Background(),
			request(uuid.New(), uuid.New()),
			workflow.TransitionDecision{TransitionID: &transitionID},
		)
		require.Empty(t, rec.sent, "no approval was requested, so there is nobody to tell")
	})
}

// The requester is not told they are waiting on themselves.
//
// Somebody can legitimately be both — an approver who moves an item through an
// edge they also police — and the useful message in that case is the decision
// one, which they still get.
func TestNotifyApprovers_SkipsTheRequester(t *testing.T) {
	t.Parallel()

	selfApprover, other, transitionID := uuid.New(), uuid.New(), uuid.New()

	rec := &recordingEnqueuer{}
	gateFor(rec, transitionID, selfApprover, other).notifyApprovers(
		context.Background(),
		request(selfApprover, uuid.New()),
		pendingResult(transitionID, uuid.New(), true),
	)

	require.Equal(t, []string{other.String()}, rec.recipients())
}

// The notification carries what the bell needs to navigate: the ITEM, not the
// approval record, plus the space that makes the route buildable.
func TestNotifyApprovers_CarriesTheItemAndItsSpace(t *testing.T) {
	t.Parallel()

	approver, transitionID := uuid.New(), uuid.New()
	entityID, spaceID := uuid.New(), uuid.New()

	rec := &recordingEnqueuer{}
	gateFor(rec, transitionID, approver).notifyApprovers(
		context.Background(),
		request(uuid.New(), spaceID),
		pendingResult(transitionID, entityID, true),
	)

	require.Len(t, rec.sent, 1)
	got := rec.sent[0]
	require.Equal(t, KindApprovalRequested, got.EventKind)
	require.Equal(t, entityID.String(), got.ResourceID,
		"there is no approval page to land on; the decision is made from the item")
	require.Equal(t, spaceID.String(), got.SpaceID,
		"without the space the bell cannot build a route and the notification is dead")
	require.Equal(t, "ticket", got.EntityKind,
		"the entity vocabulary migration 047 matched to the audit log's words")
	require.Contains(t, got.Message, "open")
	require.Contains(t, got.Message, "in_progress")
}

// stubApplier satisfies the applier seam without doing anything: these tests
// exercise the NOTIFICATION fan-out, which runs after Gate and never decides an
// approval. A call here would mean the test had drifted into a path it does not
// cover, so it reports an error rather than a zero value.
type stubApplier struct{}

func (stubApplier) ApplyTransition(context.Context, workflow.ApplyInput) error {
	return errors.New("stubApplier: notify tests do not apply transitions")
}

func (stubApplier) DecideAndApply(
	context.Context, workflow.DecideAndApplyInput,
) (workflow.Approval, error) {
	return workflow.Approval{}, errors.New("stubApplier: notify tests do not decide approvals")
}
