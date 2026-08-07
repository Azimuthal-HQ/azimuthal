package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
)

// fakeTierStore stands in for the database so the chokepoint's decisions can be
// tested exhaustively and fast. It stubs the TierStore INTERFACE, not the
// database: the persistence half is covered against real PostgreSQL in
// internal/db/adapters/workflow_tiers_integration_test.go, per the split
// CLAUDE.md §2 describes.
type fakeTierStore struct {
	guards     map[uuid.UUID][]Guard
	approvers  map[uuid.UUID][]Approver
	postFns    map[uuid.UUID][]PostFunction
	states     map[string]*State
	transition *Transition
	// extraTransitions lets a test give one from-state several outgoing edges,
	// which is what OfferedTransitions has to filter.
	extraTransitions []*Transition
	teams            []uuid.UUID
	// teamMembers is the inverse of teams: which users a given team contains,
	// for the approval notification fan-out.
	teamMembers map[uuid.UUID][]uuid.UUID

	approvals map[uuid.UUID]Approval
	pending   map[uuid.UUID]uuid.UUID // entityID -> approvalID

	createApprovalCalls int
}

func newFakeStore() *fakeTierStore {
	return &fakeTierStore{
		guards:      map[uuid.UUID][]Guard{},
		approvers:   map[uuid.UUID][]Approver{},
		postFns:     map[uuid.UUID][]PostFunction{},
		states:      map[string]*State{},
		approvals:   map[uuid.UUID]Approval{},
		pending:     map[uuid.UUID]uuid.UUID{},
		teamMembers: map[uuid.UUID][]uuid.UUID{},
	}
}

func (f *fakeTierStore) GuardsForTransition(_ context.Context, id uuid.UUID) ([]Guard, error) {
	return f.guards[id], nil
}
func (f *fakeTierStore) GuardsForWorkflow(context.Context, uuid.UUID) ([]Guard, error) {
	return nil, nil
}
func (f *fakeTierStore) CreateGuard(_ context.Context, g Guard) (Guard, error)   { return g, nil }
func (f *fakeTierStore) DeleteGuard(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f *fakeTierStore) PostFunctionsForTransition(_ context.Context, id uuid.UUID) ([]PostFunction, error) {
	return f.postFns[id], nil
}
func (f *fakeTierStore) PostFunctionsForWorkflow(context.Context, uuid.UUID) ([]PostFunction, error) {
	return nil, nil
}
func (f *fakeTierStore) CreatePostFunction(_ context.Context, p PostFunction) (PostFunction, error) {
	return p, nil
}
func (f *fakeTierStore) DeletePostFunction(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f *fakeTierStore) ApproversForTransition(_ context.Context, id uuid.UUID) ([]Approver, error) {
	return f.approvers[id], nil
}
func (f *fakeTierStore) ApproversForWorkflow(context.Context, uuid.UUID) ([]Approver, error) {
	return nil, nil
}
func (f *fakeTierStore) CreateApprover(_ context.Context, a Approver) (Approver, error) {
	return a, nil
}
func (f *fakeTierStore) DeleteApprover(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f *fakeTierStore) CreateApproval(_ context.Context, a Approval) (Approval, error) {
	f.createApprovalCalls++
	if _, exists := f.pending[a.EntityID]; exists {
		// Stands in for migration 047's partial unique index.
		return Approval{}, ErrApprovalPending
	}
	a.ID = uuid.New()
	f.approvals[a.ID] = a
	f.pending[a.EntityID] = a.ID
	return a, nil
}

func (f *fakeTierStore) PendingApprovalForEntity(_ context.Context, _ ApprovalEntityType, entityID uuid.UUID) (Approval, error) {
	id, ok := f.pending[entityID]
	if !ok {
		return Approval{}, ErrNotFound
	}
	return f.approvals[id], nil
}

// GetApprovalInSpace honours spaceID rather than accepting and ignoring it.
//
// A double that took the parameter and still looked up by id alone would
// satisfy the interface and report success for exactly the cross-space request
// the real query refuses — the lying-double shape this repository has already
// shipped once. The integration suite is where the predicate itself is proven;
// this is what stops the unit tests asserting its opposite.
func (f *fakeTierStore) GetApprovalInSpace(_ context.Context, spaceID, id uuid.UUID) (Approval, error) {
	a, ok := f.approvals[id]
	if !ok || a.SpaceID != spaceID {
		return Approval{}, ErrNotFound
	}
	return a, nil
}

func (f *fakeTierStore) DecideApproval(
	_ context.Context, spaceID, id, by uuid.UUID, d Decision, reason *string,
) (Approval, error) {
	a, ok := f.approvals[id]
	if !ok || a.SpaceID != spaceID {
		return Approval{}, ErrNotFound
	}
	if !a.IsPending() {
		return Approval{}, ErrApprovalAlreadyDecided
	}
	now := a.RequestedAt
	a.DecidedBy, a.DecidedAt, a.Decision = &by, &now, &d
	// The reason is written into the stored row and read back from it, never
	// echoed straight from the argument. A double that returned `reason` while
	// leaving f.approvals[id] untouched would let a test assert a decline
	// reason survived when nothing had been stored — the lying-double shape
	// this repository has already shipped once.
	a.Reason = reason
	f.approvals[id] = a
	delete(f.pending, a.EntityID)
	return f.approvals[id], nil
}

func (f *fakeTierStore) ApprovalsForEntity(context.Context, uuid.UUID, ApprovalEntityType, uuid.UUID) ([]Approval, error) {
	return nil, nil
}
func (f *fakeTierStore) PendingApprovalsForSpace(context.Context, uuid.UUID) ([]Approval, error) {
	return nil, nil
}
func (f *fakeTierStore) PendingApprovalCountForTransition(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}

func (f *fakeTierStore) StateByName(_ context.Context, _ uuid.UUID, name string) (*State, error) {
	s, ok := f.states[name]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

func (f *fakeTierStore) TransitionBetween(_ context.Context, _, from, to uuid.UUID) (*Transition, error) {
	if f.transition == nil || f.transition.FromStateID != from || f.transition.ToStateID != to {
		return nil, ErrNotFound
	}
	return f.transition, nil
}

// StateByID answers from the same map StateByName reads, so a test cannot set up
// a store where the two disagree about which states exist.
func (f *fakeTierStore) StateByID(_ context.Context, _, stateID uuid.UUID) (*State, error) {
	for _, s := range f.states {
		if s.ID == stateID {
			return s, nil
		}
	}
	return nil, ErrNotFound
}

// InitialState returns the state a test marked initial, or ErrNotFound. It is
// NOT defaulted to the first state in the map: a workflow with no initial state
// is a real configuration (migration 016 permits zero) and the fallback under
// test must be able to see that.
func (f *fakeTierStore) InitialState(_ context.Context, _ uuid.UUID) (*State, error) {
	for _, s := range f.states {
		if s.IsInitial {
			return s, nil
		}
	}
	return nil, ErrNotFound
}

func (f *fakeTierStore) TransitionsFrom(_ context.Context, _, from uuid.UUID) ([]*Transition, error) {
	out := []*Transition{}
	if f.transition != nil && f.transition.FromStateID == from {
		out = append(out, f.transition)
	}
	for _, t := range f.extraTransitions {
		if t.FromStateID == from {
			out = append(out, t)
		}
	}
	return out, nil
}

// fakeApplier stands in for the transactional applier.
//
// It RECORDS what it was asked to write rather than reporting success blindly,
// so a test can assert the compare-and-swap value and the target actually
// travelled — a double that returned nil and remembered nothing would let
// "the verdict and the transition commit together" pass with neither happening.
type fakeApplier struct {
	store *fakeTierStore
	calls []DecideAndApplyInput
	// err, when set, is what DecideAndApply returns; it stands in for a lost
	// compare-and-swap or a failed effect write.
	err error
}

func (a *fakeApplier) ApplyTransition(context.Context, ApplyInput) error { return a.err }

func (a *fakeApplier) DecideAndApply(ctx context.Context, in DecideAndApplyInput) (Approval, error) {
	a.calls = append(a.calls, in)
	if a.err != nil {
		// The real adapter rolls the whole transaction back, so the verdict is
		// NOT recorded either. The double must do the same or a test asserting
		// "a failed apply leaves the approval pending" would pass against a
		// double that had already decided it.
		return Approval{}, a.err
	}
	return a.store.DecideApproval(ctx, in.SpaceID, in.ApprovalID, in.ActorID, in.Decision, in.Reason)
}

// svc builds the service under test over this store, with a recording applier.
func (f *fakeTierStore) svc() (*TierService, *fakeApplier) {
	ap := &fakeApplier{store: f}
	return NewTierService(f, ap), ap
}

func (f *fakeTierStore) EffectiveTeamIDs(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	return f.teams, nil
}

// EffectiveTeamMemberIDs is the inverse read, answered from an explicit map so
// a test can express "this team contains these people" without the fake
// inventing an expansion rule of its own. An unconfigured team is empty, which
// is the honest answer for a team nobody has put anybody in.
func (f *fakeTierStore) EffectiveTeamMemberIDs(
	_ context.Context, _, teamID uuid.UUID,
) ([]uuid.UUID, error) {
	return f.teamMembers[teamID], nil
}

// configured returns a store with one real edge, open -> in_progress, and
// `open` marked as the workflow's initial state.
func configured() (*fakeTierStore, uuid.UUID) {
	f := newFakeStore()
	open := &State{ID: uuid.New(), Name: "open", IsInitial: true}
	inProgress := &State{ID: uuid.New(), Name: "in_progress"}
	f.states["open"] = open
	f.states["in_progress"] = inProgress
	edge := uuid.New()
	f.transition = &Transition{ID: edge, FromStateID: open.ID, ToStateID: inProgress.ID, Name: "Start Progress"}
	return f, edge
}

func gateReq(actorID uuid.UUID) GateRequest {
	return GateRequest{
		OrgID:         uuid.New(),
		SpaceID:       uuid.New(),
		WorkflowID:    uuid.New(),
		EntityType:    ApprovalEntityTicket,
		EntityID:      uuid.New(),
		CurrentStatus: "open",
		TargetStatus:  "in_progress",
		ActorID:       actorID,
	}
}

// ─── A space with no workflow is untouched ────────────────────────────────────

// TestGate_UntouchedWorkflowIsUnaffected is the untouched-space guarantee: a
// space with no workflow assigned must see exactly the behaviour of the build
// before migration 046, and an edge nobody has configured must still carry no
// restriction.
//
// # What this test used to also assert, and why that changed
//
// It carried two further subtests — "the status names no state" and "the
// workflow defines no such edge" — asserting that both answered "nothing
// applies" and let the write through. That was the fail-open this phase exists
// to close: an administrator could configure an approval on an edge and the move
// they were guarding would resolve to no edge at all and be written unchecked.
// Those two cases are now REFUSALS, and they are asserted as such in
// TestGate_FailsClosedWhenTheWorkflowDoesNotDefineTheMove.
//
// This is a deliberate change of requirement on a maintainer's instruction, not
// an assertion weakened to make something pass: the two subtests were moved and
// inverted, and the behaviour they now describe is strictly stricter.
func TestGate_UntouchedWorkflowIsUnaffected(t *testing.T) {
	t.Parallel()

	actor := uuid.New()

	t.Run("the space has no workflow", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		req := gateReq(actor)
		req.WorkflowID = uuid.Nil

		svc, _ := f.svc()
		got, err := svc.Gate(context.Background(), req)
		require.NoError(t, err)
		require.True(t, got.NoWorkflow, "no workflow means the caller's own rule governs")
		require.Nil(t, got.Refused)
		require.Nil(t, got.Pending)
		require.Empty(t, got.Effects)
		require.Nil(t, got.TransitionID)
	})

	t.Run("the edge carries no configuration", func(t *testing.T) {
		t.Parallel()
		f, edge := configured()

		svc, _ := f.svc()
		got, err := svc.Gate(context.Background(), gateReq(actor))
		require.NoError(t, err)
		require.False(t, got.NoWorkflow)
		require.Nil(t, got.Refused)
		require.Nil(t, got.Pending)
		require.Empty(t, got.Effects)
		require.Equal(t, edge, *got.TransitionID)
	})
}

// ─── Fail closed ──────────────────────────────────────────────────────────────

// TestGate_FailsClosedWhenTheWorkflowDoesNotDefineTheMove is the phase's core
// proof, one subtest per way the previous build answered "nothing applies" and
// let the write through.
//
// Each asserts a NAMED check rather than merely "some refusal", because the
// three are different answers with different HTTP codes, and a test that
// accepted any refusal would pass if they collapsed into one.
func TestGate_FailsClosedWhenTheWorkflowDoesNotDefineTheMove(t *testing.T) {
	t.Parallel()

	actor := uuid.New()

	t.Run("the target status names no state", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		req := gateReq(actor)
		req.TargetStatus = "banana"

		svc, _ := f.svc()
		got, err := svc.Gate(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, got.Refused, "a status the workflow does not name must be refused")
		require.Equal(t, CheckUnknownTargetState, got.Refused.Check)
		require.Contains(t, got.Refused.Reason, "banana")
		require.Nil(t, got.TransitionID)
	})

	t.Run("the workflow defines no such edge", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		f.states["closed"] = &State{ID: uuid.New(), Name: "closed"}
		req := gateReq(actor)
		req.TargetStatus = "closed"

		svc, _ := f.svc()
		got, err := svc.Gate(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, got.Refused, "a move the workflow does not define must be refused")
		require.Equal(t, CheckNoSuchTransition, got.Refused.Check)
		require.Nil(t, got.TransitionID)
	})

	// The entity's status names no state AND it carries no state id, so the
	// workflow cannot place it — and the workflow declares no initial state to
	// fall back to. Refusing beats guessing: a workflow nobody can leave is
	// safer than one nobody is held to.
	t.Run("the workflow declares no initial state", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		f.states["open"].IsInitial = false
		req := gateReq(actor)
		req.CurrentStatus = "renamed_out_from_under_it"

		svc, _ := f.svc()
		got, err := svc.Gate(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, got.Refused)
		require.Equal(t, CheckNoCurrentState, got.Refused.Check)
	})
}

// TestResolveFromState_PlacesTheEntityThreeWays pins the resolution order,
// which is what makes the initial-state fallback sound rather than the
// conflation known-issues #30 rejects.
//
// The middle case is the one that matters: an entity whose STATE WAS RENAMED
// still resolves exactly, through its stored id, so the fallback is never
// reached for it. Delete the stored-id branch and this subtest fails by
// resolving to the initial state instead.
func TestResolveFromState_PlacesTheEntityThreeWays(t *testing.T) {
	t.Parallel()

	t.Run("by status name, which wins", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		other := f.states["in_progress"].ID

		svc, _ := f.svc()
		got, err := svc.ResolveFromState(context.Background(), uuid.New(), "open", &other)
		require.NoError(t, err)
		require.Equal(t, "open", got.Name, "the status text is the authority when it resolves")
	})

	t.Run("by stored state id when the state was renamed", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		renamed := f.states["in_progress"].ID

		svc, _ := f.svc()
		got, err := svc.ResolveFromState(context.Background(), uuid.New(), "the_old_name", &renamed)
		require.NoError(t, err)
		require.Equal(t, "in_progress", got.Name,
			"a renamed state must resolve through the stored id, not fall back to initial")
	})

	t.Run("by initial state when nothing else places it", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()

		svc, _ := f.svc()
		got, err := svc.ResolveFromState(context.Background(), uuid.New(), "open_but_not_a_state", nil)
		require.NoError(t, err)
		require.Equal(t, "open", got.Name, "an entity with no recorded position starts at the beginning")
	})

	t.Run("nothing at all when the workflow has no initial state", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		f.states["open"].IsInitial = false

		svc, _ := f.svc()
		got, err := svc.ResolveFromState(context.Background(), uuid.New(), "nope", nil)
		require.NoError(t, err)
		require.Nil(t, got)
	})
}

// TestGate_SameStatusIsANoOpRatherThanARefusal covers the one case a strict
// state machine would get wrong: no workflow defines an edge from a state to
// itself, so asking for the status the entity already has would be refused as
// "no such transition" — a request that changes nothing turned into an error
// every previous build accepted.
func TestGate_SameStatusIsANoOpRatherThanARefusal(t *testing.T) {
	t.Parallel()

	f, _ := configured()
	req := gateReq(uuid.New())
	req.TargetStatus = "open"

	svc, _ := f.svc()
	got, err := svc.Gate(context.Background(), req)
	require.NoError(t, err)
	require.Nil(t, got.Refused, "asking for the status it already has is not an illegal move")
	require.True(t, got.NoOp)
	require.Nil(t, got.TransitionID, "nothing was traversed, so no edge is reported")
}

// TestOfferedTransitions_ConditionsHideAndValidatorsDoNot is the READ half of
// the two-part fix. A condition removes the move from the offer; a validator
// leaves it there to be refused with a reason.
//
// Deleting the condition evaluation makes the first assertion fail; deleting the
// validator's exemption makes the second fail. Neither passes vacuously.
func TestOfferedTransitions_ConditionsHideAndValidatorsDoNot(t *testing.T) {
	t.Parallel()

	f, edge := configured()
	// A second edge out of `open`, so "hidden" is distinguishable from "there
	// were never any".
	closed := &State{ID: uuid.New(), Name: "closed"}
	f.states["closed"] = closed
	other := uuid.New()
	f.extraTransitions = append(f.extraTransitions, &Transition{
		ID: other, FromStateID: f.states["open"].ID, ToStateID: closed.ID, Name: "Close",
	})

	// A condition the actor cannot satisfy on one edge; a validator they cannot
	// satisfy on the other.
	f.guards[edge] = []Guard{{
		ID: uuid.New(), TransitionID: edge,
		Class: GuardConditionClass, Kind: GuardActorIsAssignee,
	}}
	f.guards[other] = []Guard{{
		ID: uuid.New(), TransitionID: other,
		Class: GuardValidatorClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldDueAt),
	}}

	svc, _ := f.svc()
	got, err := svc.OfferedTransitions(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)
	require.False(t, got.NoWorkflow)
	require.Equal(t, "open", got.CurrentStatus)

	offered := map[string]bool{}
	for _, o := range got.Transitions {
		offered[o.ToStatus] = true
	}
	require.False(t, offered["in_progress"], "a condition the actor fails must HIDE the transition")
	require.True(t, offered["closed"],
		"a validator must NOT hide the transition — it is offered and then refused with a reason")
}

// TestOfferedTransitions_ReportsWhenAnOfferNeedsApproval covers the flag the
// client renders before the click. Without it the only honest UI is a button
// that sometimes answers 202 for reasons the user cannot see.
func TestOfferedTransitions_ReportsWhenAnOfferNeedsApproval(t *testing.T) {
	t.Parallel()

	f, edge := configured()
	f.approvers[edge] = []Approver{{
		ID: uuid.New(), TransitionID: edge, SubjectType: ApproverUser, SubjectID: uuid.New(),
	}}

	svc, _ := f.svc()
	got, err := svc.OfferedTransitions(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)
	require.Len(t, got.Transitions, 1)
	require.True(t, got.Transitions[0].RequiresApproval)
	require.Equal(t, "in_progress", got.Transitions[0].ToStatus)
}

// TestOfferedTransitions_NoWorkflowIsDistinguishableFromNothingOffered keeps
// the client able to fall back. "This space has no workflow, use your own
// vocabulary" and "the workflow offers you nothing" are opposite instructions,
// and collapsing them into an empty list strands the picker.
func TestOfferedTransitions_NoWorkflowIsDistinguishableFromNothingOffered(t *testing.T) {
	t.Parallel()

	f, _ := configured()
	req := gateReq(uuid.New())
	req.WorkflowID = uuid.Nil

	svc, _ := f.svc()
	got, err := svc.OfferedTransitions(context.Background(), req)
	require.NoError(t, err)
	require.True(t, got.NoWorkflow)
	require.Empty(t, got.Transitions)
	require.NotNil(t, got.Transitions, "an empty offer is [] on the wire, never null")
}

// ─── Validators refuse, and name themselves ───────────────────────────────────

func TestGate_ValidatorRefusesAndNamesItself(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	f, edge := configured()
	f.guards[edge] = []Guard{{
		ID: uuid.New(), TransitionID: edge,
		Class: GuardValidatorClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldDueAt),
	}}

	req := gateReq(actor) // Entity has no due date.
	got, err := NewTierService(f, &fakeApplier{store: f}).Gate(context.Background(), req)

	require.NoError(t, err, "a refusal is an outcome, not an error")
	require.NotNil(t, got.Refused)
	require.Equal(t, GuardFieldRequired, got.Refused.Kind)
	require.Contains(t, got.Refused.Reason, "due date")
	require.Nil(t, got.Pending, "a refused transition must not create an approval request")
	require.Empty(t, got.Effects, "a refused transition must run no post-functions")
	require.Zero(t, f.createApprovalCalls, "nothing may be written when a validator refuses")
}

// Satisfying the validator lets the same transition through. Without this the
// refusal test would pass against a Gate that refuses everything.
func TestGate_SatisfiedValidatorProceeds(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	f, edge := configured()
	f.guards[edge] = []Guard{{
		ID: uuid.New(), TransitionID: edge,
		Class: GuardValidatorClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldDueAt),
	}}

	req := gateReq(actor)
	req.Entity.DueAt = ptr(timeFixture())

	got, err := NewTierService(f, &fakeApplier{store: f}).Gate(context.Background(), req)
	require.NoError(t, err)
	require.Nil(t, got.Refused)
}

// A CONDITION refuses at commit time as well as hiding at offer time.
//
// # This test used to assert the opposite, and the opposite was the defect
//
// It read "a condition is evaluated when transitions are offered, not when one
// commits", which is a fair reading of ADR-0011 and rested on a premise that was
// false in the shipped build: nothing offered transitions over HTTP, so a
// condition was evaluated NOWHERE. An administrator could configure ADR-0011's
// own Tier-1 example, see it in the admin UI with a badge reading "hides", and
// have it hide nothing from anybody.
//
// The fix is two-part and this is the second half. OfferedTransitions makes the
// premise true by shipping the offering path on a route; Gate enforces the
// condition anyway, because a server that assumes the client filtered is not
// enforcing anything — the mutation route is reachable with curl.
//
// The old test's concern remains valid and is answered elsewhere: a user should
// not be shown a move they cannot make. That is what the picker is for, not what
// the server's refusal is for.
func TestGate_ConditionsRefuseAtCommitTimeToo(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	assignee := uuid.New()
	f, edge := configured()
	f.guards[edge] = []Guard{{
		ID: uuid.New(), TransitionID: edge,
		Class: GuardConditionClass, Kind: GuardActorIsAssignee,
	}}

	svc, _ := f.svc()

	// The actor is not the assignee, so the condition is unsatisfied and the
	// move is refused rather than written.
	req := gateReq(actor)
	req.Entity.AssigneeID = &assignee
	got, err := svc.Gate(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, got.Refused, "an unsatisfied condition must refuse a direct transition")
	require.Equal(t, CheckGuard, got.Refused.Check)
	require.Equal(t, GuardConditionClass, got.Refused.Class)

	// And it is a real gate rather than a blanket refusal: the assignee, who
	// satisfies the same condition, is allowed through.
	req = gateReq(assignee)
	req.Entity.AssigneeID = &assignee
	got, err = svc.Gate(context.Background(), req)
	require.NoError(t, err)
	require.Nil(t, got.Refused)
	require.Equal(t, edge, *got.TransitionID)
}

// The capability persona that matters: someone past the route's own
// transition_any_item floor who still lacks the guarded capability. A viewer
// would prove nothing — they never reach the guard.
func TestGate_CapabilityGuardRefusesTheAgentWhoLacksIt(t *testing.T) {
	t.Parallel()

	f, edge := configured()
	f.guards[edge] = []Guard{{
		ID: uuid.New(), TransitionID: edge,
		Class: GuardValidatorClass, Kind: GuardActorHasCapability, Capability: ptr(access.CapManageSpace),
	}}
	svc := NewTierService(f, &fakeApplier{store: f})

	refused := gateReq(uuid.New())
	refused.ActorCapabilities = map[Capability]struct{}{access.CapTransitionAnyItem: {}}
	got, err := svc.Gate(context.Background(), refused)
	require.NoError(t, err)
	require.NotNil(t, got.Refused, "clearing the transition floor is not clearing the guard")

	permitted := gateReq(uuid.New())
	permitted.ActorCapabilities = map[Capability]struct{}{
		access.CapTransitionAnyItem: {}, access.CapManageSpace: {},
	}
	got, err = svc.Gate(context.Background(), permitted)
	require.NoError(t, err)
	require.Nil(t, got.Refused)
}

// ─── Approvals ────────────────────────────────────────────────────────────────

func TestGate_ApprovalBlocksAndRecordsTheRequest(t *testing.T) {
	t.Parallel()

	approver := uuid.New()
	f, edge := configured()
	f.approvers[edge] = []Approver{{ID: uuid.New(), TransitionID: edge, SubjectType: ApproverUser, SubjectID: approver}}

	req := gateReq(uuid.New())
	got, err := NewTierService(f, &fakeApplier{store: f}).Gate(context.Background(), req)

	require.NoError(t, err)
	require.Nil(t, got.Refused)
	require.NotNil(t, got.Pending, "a gated transition must report a pending request, not a refusal")
	require.True(t, got.Pending.IsPending())
	require.Empty(t, got.Effects, "post-functions must not run until the approval is granted")

	// The captured source status is what the record restores; the item itself
	// never moved.
	require.Equal(t, "open", got.Pending.FromStatus)
	require.Equal(t, "in_progress", got.Pending.ToStatus)
}

// Two people pressing the same guarded button is ordinary. The second gets the
// existing request back, not an error and not a duplicate.
func TestGate_ASecondRequestReturnsTheExistingOne(t *testing.T) {
	t.Parallel()

	f, edge := configured()
	f.approvers[edge] = []Approver{{SubjectType: ApproverUser, SubjectID: uuid.New()}}
	svc := NewTierService(f, &fakeApplier{store: f})
	req := gateReq(uuid.New())

	first, err := svc.Gate(context.Background(), req)
	require.NoError(t, err)

	second, err := svc.Gate(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, second.Pending)
	require.Equal(t, first.Pending.ID, second.Pending.ID, "the same request, not a second one")
}

func TestDecide_ApprovalCycle(t *testing.T) {
	t.Parallel()

	approver := uuid.New()
	stranger := uuid.New()
	f, edge := configured()
	f.approvers[edge] = []Approver{{TransitionID: edge, SubjectType: ApproverUser, SubjectID: approver}}
	target := uuid.New()
	f.postFns[edge] = []PostFunction{{ID: uuid.New(), Kind: PostAssignTo, AssigneeUserID: &target}}

	svc, applier := f.svc()
	gated, err := svc.Gate(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)
	require.NotNil(t, gated.Pending)

	// A stranger cannot decide. Authority is data, not a role.
	_, err = svc.Decide(context.Background(), DecideRequest{
		SpaceID: gated.Pending.SpaceID, ApprovalID: gated.Pending.ID,
		ActorID: stranger, Decision: DecisionApproved,
	})
	require.ErrorIs(t, err, ErrNotAnApprover)
	require.Empty(t, applier.calls, "a refused decision must not reach the applier at all")

	decided, err := svc.Decide(context.Background(), DecideRequest{
		SpaceID: gated.Pending.SpaceID, ApprovalID: gated.Pending.ID,
		ActorID: approver, Decision: DecisionApproved,
	})
	require.NoError(t, err)
	require.False(t, decided.IsPending())
	require.Equal(t, DecisionApproved, *decided.Decision)

	// The transition travelled WITH the verdict rather than after it. Asserting
	// on a returned effects slice would have proved only that the service
	// planned them; asserting on the applier's recorded input proves they
	// reached the thing that commits them in the same transaction as the
	// decision — which is the whole of D91.
	require.Len(t, applier.calls, 1)
	applied := applier.calls[0].Apply
	require.NotNil(t, applied, "an approval must carry the transition it releases")
	require.Len(t, applied.Effects, 1, "an approved transition runs its post-functions")
	require.Equal(t, "open", applied.ExpectFromStatus,
		"the write must be conditional on the status the approval captured")
	require.Equal(t, "in_progress", applied.ToStatus)
	require.Equal(t, gated.Pending.ID, *applied.ApprovalID)

	// A decided request cannot be decided again, in either direction. The
	// reason is supplied so the already-decided check is what refuses this,
	// not the decline-needs-a-reason check that runs before it.
	_, err = svc.Decide(context.Background(), DecideRequest{
		SpaceID: gated.Pending.SpaceID, ApprovalID: gated.Pending.ID, ActorID: approver,
		Decision: DecisionDeclined, Reason: "changed my mind",
	})
	require.ErrorIs(t, err, ErrApprovalAlreadyDecided)
}

// A decline records the verdict and applies nothing. "Decline returns the item
// to the source status" is satisfied by the item never having left it, which is
// why no effects come back and no status write is implied.
func TestDecide_DeclineAppliesNothing(t *testing.T) {
	t.Parallel()

	approver := uuid.New()
	f, edge := configured()
	f.approvers[edge] = []Approver{{TransitionID: edge, SubjectType: ApproverUser, SubjectID: approver}}
	f.postFns[edge] = []PostFunction{{ID: uuid.New(), Kind: PostAssignTo, AssigneeUserID: ptr(uuid.New())}}

	svc, applier := f.svc()
	gated, err := svc.Gate(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)

	decided, err := svc.Decide(context.Background(), DecideRequest{
		SpaceID: gated.Pending.SpaceID, ApprovalID: gated.Pending.ID, ActorID: approver,
		Decision: DecisionDeclined, Reason: "the release is frozen until Monday",
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeclined, *decided.Decision)
	require.Len(t, applier.calls, 1)
	require.Nil(t, applier.calls[0].Apply,
		"a declined transition writes no status and runs no post-functions")
	require.Equal(t, "open", decided.FromStatus, "the source status the item never left")
	require.NotNil(t, decided.Reason, "a decline must carry the reason it was given")
	require.Equal(t, "the release is frozen until Monday", *decided.Reason)
}

// A decline with no reason is refused before anything is written.
//
// The rule cannot live in a CHECK — migration 050's column is nullable so a
// database that ran 047 can still hold its old declined rows — so this is the
// only thing enforcing it. Deleting the guard clause in Decide makes the first
// two subtests pass, which is what makes them worth having.
func TestDecide_ADeclineMustSayWhy(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (*TierService, *fakeTierStore, uuid.UUID, Approval) {
		t.Helper()
		approver := uuid.New()
		f, edge := configured()
		f.approvers[edge] = []Approver{{TransitionID: edge, SubjectType: ApproverUser, SubjectID: approver}}
		svc := NewTierService(f, &fakeApplier{store: f})
		gated, err := svc.Gate(context.Background(), gateReq(uuid.New()))
		require.NoError(t, err)
		require.NotNil(t, gated.Pending)
		// The whole approval, not just its id: Decide is now space-scoped, and a
		// zero SpaceID would make every subtest below fail as not-found — passing
		// for the wrong reason rather than exercising the reason clause.
		return svc, f, approver, *gated.Pending
	}

	t.Run("an empty reason is refused", func(t *testing.T) {
		t.Parallel()
		svc, f, approver, ap := setup(t)
		_, err := svc.Decide(context.Background(), DecideRequest{
			SpaceID: ap.SpaceID, ApprovalID: ap.ID, ActorID: approver, Decision: DecisionDeclined,
		})
		require.ErrorIs(t, err, ErrDeclineReasonRequired)

		stored, getErr := f.GetApprovalInSpace(context.Background(), ap.SpaceID, ap.ID)
		require.NoError(t, getErr)
		require.True(t, stored.IsPending(),
			"the refusal must happen before the decision is recorded, or a reasonless decline still lands")
	})

	t.Run("whitespace is not a reason", func(t *testing.T) {
		t.Parallel()
		svc, _, approver, ap := setup(t)
		_, err := svc.Decide(context.Background(), DecideRequest{
			SpaceID: ap.SpaceID, ApprovalID: ap.ID, ActorID: approver,
			Decision: DecisionDeclined, Reason: "   	  ",
		})
		require.ErrorIs(t, err, ErrDeclineReasonRequired,
			"spaces render blank, which is the failure the column exists to close")
	})

	t.Run("an approval needs no reason, and stores NULL rather than an empty string", func(t *testing.T) {
		t.Parallel()
		svc, _, approver, ap := setup(t)
		decided, err := svc.Decide(context.Background(), DecideRequest{
			SpaceID: ap.SpaceID, ApprovalID: ap.ID, ActorID: approver, Decision: DecisionApproved,
		})
		require.NoError(t, err, "the transition itself is the record; an approval needs no justification")
		require.Nil(t, decided.Reason,
			`"said nothing" and "said the empty string" must not be two spellings of one thing`)
	})

	t.Run("the reason is refused before authority is even considered", func(t *testing.T) {
		t.Parallel()
		svc, _, _, ap := setup(t)
		_, err := svc.Decide(context.Background(), DecideRequest{
			SpaceID: ap.SpaceID, ApprovalID: ap.ID, ActorID: uuid.New(), Decision: DecisionDeclined,
		})
		require.ErrorIs(t, err, ErrDeclineReasonRequired,
			"a stranger must not be asked for a reason they were never entitled to give")
	})
}

// A team approver decides through the ADR-0007 effective set, so the gate and a
// space grant to the same team can never disagree about membership.
func TestDecide_TeamApproverUsesTheEffectiveSet(t *testing.T) {
	t.Parallel()

	team := uuid.New()
	member := uuid.New()
	f, edge := configured()
	f.approvers[edge] = []Approver{{TransitionID: edge, SubjectType: ApproverTeam, SubjectID: team}}
	f.teams = []uuid.UUID{team}

	svc := NewTierService(f, &fakeApplier{store: f})
	gated, err := svc.Gate(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)

	_, err = svc.Decide(context.Background(), DecideRequest{
		SpaceID: gated.Pending.SpaceID, ApprovalID: gated.Pending.ID,
		ActorID: member, Decision: DecisionApproved,
	})
	require.NoError(t, err)
}

// ─── Post-functions ───────────────────────────────────────────────────────────

func TestGate_PlansPostFunctionsForTheCallerToApply(t *testing.T) {
	t.Parallel()

	f, edge := configured()
	target := uuid.New()
	f.postFns[edge] = []PostFunction{
		{ID: uuid.New(), Kind: PostAssignTo, AssigneeUserID: &target, Position: 0},
		{ID: uuid.New(), Kind: PostSetField, FieldKey: ptr(PostFieldTags), FieldValue: ptr("escalated"), Position: 1},
	}

	got, err := NewTierService(f, &fakeApplier{store: f}).Gate(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)
	require.Len(t, got.Effects, 2)
	require.Equal(t, target, **got.Effects[0].SetAssignee)
	require.Equal(t, []string{"escalated"}, *got.Effects[1].SetTags)
}

// An action this build cannot perform aborts the whole transition. Committing
// the status change with the action silently skipped is the failure this
// prevents.
func TestGate_UnknownPostFunctionAbortsTheTransition(t *testing.T) {
	t.Parallel()

	f, edge := configured()
	f.postFns[edge] = []PostFunction{{ID: uuid.New(), Kind: "send_carrier_pigeon"}}

	_, err := NewTierService(f, &fakeApplier{store: f}).Gate(context.Background(), gateReq(uuid.New()))
	require.ErrorIs(t, err, ErrPostFunctionUnknown)
}

// ─── Conditions filter what is offered ────────────────────────────────────────

// TestOfferedTransitions_ConditionHidesTheTransition covers both directions of
// one condition: the actor who fails it is not offered the move, and the actor
// who satisfies it is.
//
// Both halves matter. Asserting only the first would pass against a function
// that offered nothing to anybody, which is a broken picker rather than a
// working condition.
func TestOfferedTransitions_ConditionHidesTheTransition(t *testing.T) {
	t.Parallel()

	assignee := uuid.New()
	f, edge := configured()
	f.guards[edge] = []Guard{{
		ID: uuid.New(), TransitionID: edge,
		Class: GuardConditionClass, Kind: GuardActorIsAssignee,
	}}
	svc, _ := f.svc()

	// Someone who is not the assignee is not offered it.
	req := gateReq(uuid.New())
	req.Entity.AssigneeID = &assignee
	got, err := svc.OfferedTransitions(context.Background(), req)
	require.NoError(t, err)
	require.Empty(t, got.Transitions)

	// The assignee is.
	req = gateReq(assignee)
	req.Entity.AssigneeID = &assignee
	got, err = svc.OfferedTransitions(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, got.Transitions, 1)
	require.Equal(t, edge, got.Transitions[0].TransitionID)
	require.Equal(t, "in_progress", got.Transitions[0].ToStatus)
}

// An unconfigured workflow offers every edge leaving the entity's state.
func TestOfferedTransitions_NoGuardsOffersEverything(t *testing.T) {
	t.Parallel()

	f, _ := configured()
	svc, _ := f.svc()
	got, err := svc.OfferedTransitions(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)
	require.Len(t, got.Transitions, 1)
	require.False(t, got.Transitions[0].RequiresApproval)
}

// timeFixture is a fixed instant. Tests must not depend on wall-clock time.
func timeFixture() time.Time {
	return time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
}
