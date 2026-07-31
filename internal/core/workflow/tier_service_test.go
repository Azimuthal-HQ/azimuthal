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
	teams      []uuid.UUID

	approvals map[uuid.UUID]Approval
	pending   map[uuid.UUID]uuid.UUID // entityID -> approvalID

	createApprovalCalls int
}

func newFakeStore() *fakeTierStore {
	return &fakeTierStore{
		guards:    map[uuid.UUID][]Guard{},
		approvers: map[uuid.UUID][]Approver{},
		postFns:   map[uuid.UUID][]PostFunction{},
		states:    map[string]*State{},
		approvals: map[uuid.UUID]Approval{},
		pending:   map[uuid.UUID]uuid.UUID{},
	}
}

func (f *fakeTierStore) GuardsForTransition(_ context.Context, id uuid.UUID) ([]Guard, error) {
	return f.guards[id], nil
}
func (f *fakeTierStore) GuardsForWorkflow(context.Context, uuid.UUID) ([]Guard, error) {
	return nil, nil
}
func (f *fakeTierStore) CreateGuard(_ context.Context, g Guard) (Guard, error) { return g, nil }
func (f *fakeTierStore) DeleteGuard(context.Context, uuid.UUID) error          { return nil }

func (f *fakeTierStore) PostFunctionsForTransition(_ context.Context, id uuid.UUID) ([]PostFunction, error) {
	return f.postFns[id], nil
}
func (f *fakeTierStore) PostFunctionsForWorkflow(context.Context, uuid.UUID) ([]PostFunction, error) {
	return nil, nil
}
func (f *fakeTierStore) CreatePostFunction(_ context.Context, p PostFunction) (PostFunction, error) {
	return p, nil
}
func (f *fakeTierStore) DeletePostFunction(context.Context, uuid.UUID) error { return nil }

func (f *fakeTierStore) ApproversForTransition(_ context.Context, id uuid.UUID) ([]Approver, error) {
	return f.approvers[id], nil
}
func (f *fakeTierStore) ApproversForWorkflow(context.Context, uuid.UUID) ([]Approver, error) {
	return nil, nil
}
func (f *fakeTierStore) CreateApprover(_ context.Context, a Approver) (Approver, error) {
	return a, nil
}
func (f *fakeTierStore) DeleteApprover(context.Context, uuid.UUID) error { return nil }

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

func (f *fakeTierStore) GetApproval(_ context.Context, id uuid.UUID) (Approval, error) {
	a, ok := f.approvals[id]
	if !ok {
		return Approval{}, ErrNotFound
	}
	return a, nil
}

func (f *fakeTierStore) DecideApproval(
	_ context.Context, id, by uuid.UUID, d Decision, reason *string,
) (Approval, error) {
	a, ok := f.approvals[id]
	if !ok {
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

func (f *fakeTierStore) ApprovalsForEntity(context.Context, ApprovalEntityType, uuid.UUID) ([]Approval, error) {
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

func (f *fakeTierStore) EffectiveTeamIDs(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	return f.teams, nil
}

// configured returns a store with one real edge, open -> in_progress.
func configured() (*fakeTierStore, uuid.UUID) {
	f := newFakeStore()
	open := &State{ID: uuid.New(), Name: "open"}
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

// ─── Absence is not refusal ───────────────────────────────────────────────────

// TestGate_UntouchedWorkflowIsUnaffected is the byte-identical guarantee, and
// the single most important test in this phase: an administrator who has
// configured nothing must see exactly the behaviour of the build before
// migration 046, through every one of the several ways "not configured" can be
// true.
func TestGate_UntouchedWorkflowIsUnaffected(t *testing.T) {
	t.Parallel()

	actor := uuid.New()

	t.Run("the space has no workflow", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		req := gateReq(actor)
		req.WorkflowID = uuid.Nil

		got, err := NewTierService(f).Gate(context.Background(), req)
		require.NoError(t, err)
		require.Nil(t, got.Refused)
		require.Nil(t, got.Pending)
		require.Empty(t, got.Effects)
		require.Nil(t, got.TransitionID)
	})

	t.Run("the status names no state", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		req := gateReq(actor)
		req.CurrentStatus = "renamed_out_from_under_it"

		got, err := NewTierService(f).Gate(context.Background(), req)
		require.NoError(t, err)
		require.Nil(t, got.Refused)
		require.Nil(t, got.TransitionID)
	})

	// A move the workflow does not define is NOT refused here. The legacy
	// routes have their own legality rules and this phase does not adjudicate
	// between them: Gate adds restrictions and never adds a legality rule.
	t.Run("the workflow defines no such edge", func(t *testing.T) {
		t.Parallel()
		f, _ := configured()
		f.states["closed"] = &State{ID: uuid.New(), Name: "closed"}
		req := gateReq(actor)
		req.TargetStatus = "closed"

		got, err := NewTierService(f).Gate(context.Background(), req)
		require.NoError(t, err)
		require.Nil(t, got.Refused, "an undefined edge must not become a new refusal")
		require.Nil(t, got.TransitionID)
	})

	t.Run("the edge carries no configuration", func(t *testing.T) {
		t.Parallel()
		f, edge := configured()

		got, err := NewTierService(f).Gate(context.Background(), gateReq(actor))
		require.NoError(t, err)
		require.Nil(t, got.Refused)
		require.Nil(t, got.Pending)
		require.Empty(t, got.Effects)
		require.Equal(t, edge, *got.TransitionID)
	})
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
	got, err := NewTierService(f).Gate(context.Background(), req)

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

	got, err := NewTierService(f).Gate(context.Background(), req)
	require.NoError(t, err)
	require.Nil(t, got.Refused)
}

// A CONDITION must not refuse at commit time. It hides the transition when it
// is offered; refusing here would reject a transition the user was never shown,
// with a message about something they cannot see.
func TestGate_ConditionsDoNotRefuseAtCommitTime(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	f, edge := configured()
	f.guards[edge] = []Guard{{
		ID: uuid.New(), TransitionID: edge,
		Class: GuardConditionClass, Kind: GuardActorIsAssignee,
	}}

	got, err := NewTierService(f).Gate(context.Background(), gateReq(actor))
	require.NoError(t, err)
	require.Nil(t, got.Refused, "a condition is evaluated when transitions are offered, not when one commits")
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
	svc := NewTierService(f)

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
	got, err := NewTierService(f).Gate(context.Background(), req)

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
	svc := NewTierService(f)
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

	svc := NewTierService(f)
	gated, err := svc.Gate(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)
	require.NotNil(t, gated.Pending)

	// A stranger cannot decide. Authority is data, not a role.
	_, _, err = svc.Decide(context.Background(), DecideRequest{
		ApprovalID: gated.Pending.ID, ActorID: stranger, Decision: DecisionApproved,
	})
	require.ErrorIs(t, err, ErrNotAnApprover)

	decided, effects, err := svc.Decide(context.Background(), DecideRequest{
		ApprovalID: gated.Pending.ID, ActorID: approver, Decision: DecisionApproved,
	})
	require.NoError(t, err)
	require.False(t, decided.IsPending())
	require.Equal(t, DecisionApproved, *decided.Decision)
	require.Len(t, effects, 1, "an approved transition runs its post-functions")

	// A decided request cannot be decided again, in either direction.
	_, _, err = svc.Decide(context.Background(), DecideRequest{
		ApprovalID: gated.Pending.ID, ActorID: approver, Decision: DecisionDeclined,
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

	svc := NewTierService(f)
	gated, err := svc.Gate(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)

	decided, effects, err := svc.Decide(context.Background(), DecideRequest{
		ApprovalID: gated.Pending.ID, ActorID: approver, Decision: DecisionDeclined,
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeclined, *decided.Decision)
	require.Empty(t, effects, "a declined transition must run no post-functions")
	require.Equal(t, "open", decided.FromStatus, "the source status the item never left")
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

	svc := NewTierService(f)
	gated, err := svc.Gate(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)

	_, _, err = svc.Decide(context.Background(), DecideRequest{
		ApprovalID: gated.Pending.ID, ActorID: member, Decision: DecisionApproved,
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
		{ID: uuid.New(), Kind: PostSetField, FieldKey: ptr(PostFieldLabels), FieldValue: ptr("escalated"), Position: 1},
	}

	got, err := NewTierService(f).Gate(context.Background(), gateReq(uuid.New()))
	require.NoError(t, err)
	require.Len(t, got.Effects, 2)
	require.Equal(t, target, **got.Effects[0].SetAssignee)
	require.Equal(t, []string{"escalated"}, *got.Effects[1].SetLabels)
}

// An action this build cannot perform aborts the whole transition. Committing
// the status change with the action silently skipped is the failure this
// prevents.
func TestGate_UnknownPostFunctionAbortsTheTransition(t *testing.T) {
	t.Parallel()

	f, edge := configured()
	f.postFns[edge] = []PostFunction{{ID: uuid.New(), Kind: "send_carrier_pigeon"}}

	_, err := NewTierService(f).Gate(context.Background(), gateReq(uuid.New()))
	require.ErrorIs(t, err, ErrPostFunctionUnknown)
}

// ─── Conditions filter what is offered ────────────────────────────────────────

func TestAvailableTransitions_ConditionHidesTheTransition(t *testing.T) {
	t.Parallel()

	assignee := uuid.New()
	f, edge := configured()
	f.guards[edge] = []Guard{{
		ID: uuid.New(), TransitionID: edge,
		Class: GuardConditionClass, Kind: GuardActorIsAssignee,
	}}
	svc := NewTierService(f)
	candidates := []*Transition{f.transition}

	// Someone who is not the assignee is not offered it.
	req := gateReq(uuid.New())
	req.Entity.AssigneeID = &assignee
	offered, err := svc.AvailableTransitions(context.Background(), req, candidates)
	require.NoError(t, err)
	require.Empty(t, offered)

	// The assignee is.
	req = gateReq(assignee)
	req.Entity.AssigneeID = &assignee
	offered, err = svc.AvailableTransitions(context.Background(), req, candidates)
	require.NoError(t, err)
	require.Len(t, offered, 1)
	require.Equal(t, edge, offered[0].ID)
}

// A validator must not hide a transition — it must let it be offered and then
// explain the refusal. Without this, a validator and a condition would be the
// same thing.
func TestAvailableTransitions_ValidatorsDoNotHide(t *testing.T) {
	t.Parallel()

	f, edge := configured()
	f.guards[edge] = []Guard{{
		ID: uuid.New(), TransitionID: edge,
		Class: GuardValidatorClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldDueAt),
	}}

	offered, err := NewTierService(f).AvailableTransitions(
		context.Background(), gateReq(uuid.New()), []*Transition{f.transition})
	require.NoError(t, err)
	require.Len(t, offered, 1, "a validator explains at commit time; it does not hide the action")
}

// An unconfigured workflow offers everything it did before.
func TestAvailableTransitions_NoGuardsOffersEverything(t *testing.T) {
	t.Parallel()

	f, _ := configured()
	offered, err := NewTierService(f).AvailableTransitions(
		context.Background(), gateReq(uuid.New()), []*Transition{f.transition})
	require.NoError(t, err)
	require.Len(t, offered, 1)
}

// timeFixture is a fixed instant. Tests must not depend on wall-clock time.
func timeFixture() time.Time {
	return time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
}
