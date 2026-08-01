package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
)

// TierService is the single chokepoint every status change passes through.
//
// # Why a chokepoint, and not a check on the engine
//
// This product changes an item's status through four routes, and before this
// phase they did not agree about anything:
//
//	POST .../tickets/{id}/status            a hardcoded Go map, internal/core/tickets/status.go
//	POST .../projects/items/{id}/status     no validation at all
//	POST .../tickets/{id}/workflow-state    the DB engine
//	POST .../projects/items/{id}/workflow-state   the DB engine
//
// The frontend calls only the first two. The engine-backed pair has no client
// at all. So a guard attached to the engine would have been unreachable by
// every real user AND bypassable by the route they actually use — a feature
// that ships dead and insecure at the same time.
//
// Gate is therefore called by all four. It speaks status TEXT as well as state
// ids, because two of the four only know the text.
//
// # A workflow, once assigned, decides
//
// This used to read "absence is not refusal", and it applied that to four
// different absences: no workflow, an unknown current status, an unknown target
// status, and a missing edge. The last three made the tiers unenforceable —
// an administrator could configure an approval on an edge and the move they
// were guarding would resolve to no edge at all and be written unchecked.
//
// So the absences are now split. A space with NO WORKFLOW still means "nothing
// applies", and the caller applies whatever rule it had before workflows
// existed (TransitionDecision.NoWorkflow). Every other absence is a REFUSAL, because in a
// state machine a missing edge is exactly that. See transition.go for the
// resolution and RefusalCheck for the four ways a move is refused.
//
// TestGate_UntouchedWorkflowIsUnaffected still holds and was not weakened: it
// asserts a space with no configured workflow behaves as before, which is the
// case that did not change.
type TierService struct {
	store TierStore
	// applier commits an approver's verdict and the transition it releases in
	// one transaction. It is a REQUIRED constructor argument rather than an
	// optional builder, for the reason tiergate.Gate states about its own three:
	// a collaborator whose absence degrades to "feature off" makes every test
	// pass while no rule ever runs. Decide refuses outright when it is nil.
	applier ApprovalApplier
}

// NewTierService creates a TierService over the given store and applier.
func NewTierService(store TierStore, applier ApprovalApplier) *TierService {
	return &TierService{store: store, applier: applier}
}

// GateRequest is everything Gate needs to decide.
type GateRequest struct {
	OrgID   uuid.UUID
	SpaceID uuid.UUID
	// WorkflowID is the space's workflow. uuid.Nil means the space has none,
	// which is a supported live state: assignment happens outside the
	// space-create transaction and is best-effort.
	WorkflowID uuid.UUID

	EntityType ApprovalEntityType
	EntityID   uuid.UUID

	// CurrentStatus and TargetStatus are the status TEXT, because two of the
	// four callers only know that.
	CurrentStatus string
	TargetStatus  string

	// CurrentStateID is the entity's stored workflow_state_id, when it has one.
	//
	// It is the SECOND of the three ways ResolveFromState places the entity,
	// and the only one that survives a state being renamed. Passing it is not
	// optional for a caller that has it: without it, a renamed state sends the
	// entity down the initial-state fallback, which is a guess where an exact
	// answer was available.
	CurrentStateID *uuid.UUID

	ActorID uuid.UUID
	// ActorCapabilities is the actor's resolved capability set in SpaceID.
	ActorCapabilities map[Capability]struct{}

	Entity EntitySnapshot
}

// Capability is an alias for access.Capability, so a caller assembling a
// GateRequest does not have to import the access package purely to name the
// map's key type. It is the same type, not a copy — a second capability
// vocabulary is exactly what must not exist.
type Capability = access.Capability

// Gate is the canonical decision on one status change, for every route that
// can make one.
//
// It writes in exactly one case — creating the pending approval — and that
// write is deliberately outside the caller's transaction, because the caller's
// transaction is about to be abandoned: a gated transition does not move the
// entity, so there is nothing for the request to commit alongside.
//
// # The order of the checks is load-bearing
//
// Structure first, then conditions, then validators, then approval. A caller
// asking for a move the workflow does not define must be told that, not told
// which guard it would have failed — the guard is configuration they are not
// entitled to enumerate for an edge that does not exist.
//
// Conditions run BEFORE validators for the same reason ADR-0011 separates them:
// a condition decides whether the move is offered at all, so failing one means
// the caller is asking for something they were never shown. Both refuse here.
// The previous build ran validators only, on the stated grounds that "conditions
// were already applied when the transition was offered" — true of the design and
// false of the deployment, because nothing offered transitions over HTTP. This
// phase makes the premise true by shipping OfferedTransitions on a route, and
// enforces both halves regardless, because a server that trusts the client to
// have filtered is not enforcing anything.
func (s *TierService) Gate(ctx context.Context, req GateRequest) (TransitionDecision, error) {
	if req.WorkflowID == uuid.Nil {
		// The one surviving "nothing applies". See transition.go.
		return TransitionDecision{NoWorkflow: true}, nil
	}

	from, err := s.ResolveFromState(ctx, req.WorkflowID, req.CurrentStatus, req.CurrentStateID)
	if err != nil {
		return TransitionDecision{}, err
	}
	if from == nil {
		return TransitionDecision{Refused: refuse(CheckNoCurrentState,
			"this space's workflow declares no starting state, so no transition can be checked against it; an administrator must set one")}, nil
	}
	d := TransitionDecision{FromStateID: &from.ID, FromStatus: from.Name}

	to, err := s.store.StateByName(ctx, req.WorkflowID, req.TargetStatus)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return TransitionDecision{}, fmt.Errorf("gate: resolving target state: %w", err)
	}
	if to == nil {
		d.Refused = refuse(CheckUnknownTargetState, fmt.Sprintf(
			"%q is not a status in this space's workflow", req.TargetStatus))
		return d, nil
	}
	d.ToStateID = &to.ID

	if to.ID == from.ID {
		// Asking for the status the entity already has. No workflow defines an
		// edge from a state to itself, so without this the workflow would refuse
		// a request that changes nothing — and every build before this one
		// accepted it. Nothing is gated because nothing moves.
		d.NoOp = true
		return d, nil
	}

	transition, err := s.store.TransitionBetween(ctx, req.WorkflowID, from.ID, to.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return TransitionDecision{}, fmt.Errorf("gate: resolving transition: %w", err)
	}
	if transition == nil {
		d.Refused = refuse(CheckNoSuchTransition, fmt.Sprintf(
			"this space's workflow defines no move from %q to %q", from.Name, to.Name))
		return d, nil
	}
	d.TransitionID = &transition.ID

	actor, err := s.resolveActor(ctx, req)
	if err != nil {
		return TransitionDecision{}, err
	}

	guards, err := s.store.GuardsForTransition(ctx, transition.ID)
	if err != nil {
		return TransitionDecision{}, fmt.Errorf("gate: loading guards: %w", err)
	}
	if refusal := Evaluate(guards, GuardConditionClass, actor, req.Entity); refusal != nil {
		d.Refused = refusal
		return d, nil
	}
	if refusal := Evaluate(guards, GuardValidatorClass, actor, req.Entity); refusal != nil {
		d.Refused = refusal
		return d, nil
	}

	approvers, err := s.store.ApproversForTransition(ctx, transition.ID)
	if err != nil {
		return TransitionDecision{}, fmt.Errorf("gate: loading approvers: %w", err)
	}
	if RequiresApproval(approvers) {
		pending, isNew, err := s.requestApproval(ctx, req, transition)
		if err != nil {
			return TransitionDecision{}, err
		}
		d.Pending, d.PendingIsNew = pending, isNew
		return d, nil
	}

	effects, err := s.planEffects(ctx, transition.ID)
	if err != nil {
		return TransitionDecision{}, err
	}
	d.Effects = effects
	return d, nil
}

// planEffects loads and plans a transition's post-functions. An action this
// build cannot perform aborts the transition rather than committing with it
// silently skipped.
func (s *TierService) planEffects(ctx context.Context, transitionID uuid.UUID) ([]Effect, error) {
	postFns, err := s.store.PostFunctionsForTransition(ctx, transitionID)
	if err != nil {
		return nil, fmt.Errorf("loading post-functions: %w", err)
	}
	effects, err := PlanPostFunctions(postFns)
	if err != nil {
		return nil, err
	}
	return effects, nil
}

// OfferedTransitions is what the actor may be shown for this entity.
//
// This is where CONDITIONS do their ADR-0011 job — "a condition determines
// whether a transition is offered" — and until this phase there was nothing
// that offered: the filter existed with no HTTP route and no production caller,
// so a condition an administrator configured, schema-validated and saw a badge
// for in the admin UI hid nothing from anybody.
//
// A transition whose condition the actor does not satisfy is omitted SILENTLY,
// which is the point: a condition hides, a validator explains. Validators are
// deliberately NOT applied here — a picker that hid every move whose validator
// currently fails would tell the actor which preconditions are unmet on an
// entity they can see, one guess at a time, and ADR-0011 gives the validator
// the opposite job: offer it, then refuse it with a reason.
//
// It resolves the from-state through the same ResolveFromState the mutation
// route uses. That shared call is the whole of "one canonical service": if the
// two halves placed the entity differently, the picker would offer moves the
// mutation route refuses, which is a worse failure than not offering at all.
func (s *TierService) OfferedTransitions(ctx context.Context, req GateRequest) (Offering, error) {
	if req.WorkflowID == uuid.Nil {
		return Offering{NoWorkflow: true, EntityStatus: req.CurrentStatus, Transitions: []Offer{}}, nil
	}

	out := Offering{EntityStatus: req.CurrentStatus, Transitions: []Offer{}}

	from, err := s.ResolveFromState(ctx, req.WorkflowID, req.CurrentStatus, req.CurrentStateID)
	if err != nil {
		return Offering{}, err
	}
	if from == nil {
		// The workflow declares no initial state. Gate refuses every move in
		// this case, so offering none is the same answer read the other way.
		return out, nil
	}
	out.CurrentStatus = from.Name

	candidates, err := s.store.TransitionsFrom(ctx, req.WorkflowID, from.ID)
	if err != nil {
		return Offering{}, fmt.Errorf("offered transitions: listing edges: %w", err)
	}
	if len(candidates) == 0 {
		return out, nil
	}

	actor, err := s.resolveActor(ctx, req)
	if err != nil {
		return Offering{}, err
	}

	for _, t := range candidates {
		guards, err := s.store.GuardsForTransition(ctx, t.ID)
		if err != nil {
			return Offering{}, fmt.Errorf("offered transitions: loading guards: %w", err)
		}
		if Evaluate(guards, GuardConditionClass, actor, req.Entity) != nil {
			continue
		}

		to, err := s.store.StateByID(ctx, req.WorkflowID, t.ToStateID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Offering{}, fmt.Errorf("offered transitions: resolving target state: %w", err)
		}
		if to == nil {
			// An edge pointing at a state that no longer exists cannot be
			// offered, because the client would have no status to post back.
			// Migration 016 CASCADEs the edge with the state, so this is
			// unreachable in a consistent database and refuses rather than
			// panics if it ever is not.
			continue
		}

		approvers, err := s.store.ApproversForTransition(ctx, t.ID)
		if err != nil {
			return Offering{}, fmt.Errorf("offered transitions: loading approvers: %w", err)
		}
		out.Transitions = append(out.Transitions, Offer{
			TransitionID:     t.ID,
			Name:             t.Name,
			ToStateID:        to.ID,
			ToStatus:         to.Name,
			RequiresApproval: RequiresApproval(approvers),
		})
	}
	return out, nil
}

// resolveActor builds the evaluation snapshot. The effective team set comes
// from the same expansion space grants use, so a guard and a grant can never
// disagree about who is in a team.
func (s *TierService) resolveActor(ctx context.Context, req GateRequest) (Actor, error) {
	teamIDs, err := s.store.EffectiveTeamIDs(ctx, req.OrgID, req.ActorID)
	if err != nil {
		return Actor{}, fmt.Errorf("gate: resolving effective teams: %w", err)
	}

	actor := Actor{
		UserID:       req.ActorID,
		TeamIDs:      make(map[uuid.UUID]struct{}, len(teamIDs)),
		Capabilities: req.ActorCapabilities,
	}
	for _, id := range teamIDs {
		actor.TeamIDs[id] = struct{}{}
	}
	if actor.Capabilities == nil {
		actor.Capabilities = map[Capability]struct{}{}
	}
	return actor, nil
}

// requestApproval records the pending request, or returns the one that already
// exists.
//
// An item that already has a request outstanding does not get a second one:
// migration 047's partial unique index refuses it, and the existing request is
// returned so the caller reports "already awaiting approval" rather than an
// error. Two people pressing the same guarded button is ordinary, not
// exceptional.
// The bool reports whether this call created the request; see
// GateResult.PendingIsNew.
func (s *TierService) requestApproval(ctx context.Context, req GateRequest, t *Transition) (*Approval, bool, error) {
	fromStateID, toStateID := t.FromStateID, t.ToStateID

	created, err := s.store.CreateApproval(ctx, Approval{
		TransitionID: &t.ID,
		EntityType:   req.EntityType,
		EntityID:     req.EntityID,
		SpaceID:      req.SpaceID,
		FromStateID:  &fromStateID,
		ToStateID:    &toStateID,
		FromStatus:   req.CurrentStatus,
		ToStatus:     req.TargetStatus,
		RequestedBy:  req.ActorID,
	})
	if err == nil {
		return &created, true, nil
	}
	if !errors.Is(err, ErrApprovalPending) {
		return nil, false, fmt.Errorf("gate: requesting approval: %w", err)
	}

	existing, getErr := s.store.PendingApprovalForEntity(ctx, req.EntityType, req.EntityID)
	if getErr != nil {
		return nil, false, fmt.Errorf("gate: reading the pending approval: %w", getErr)
	}
	return &existing, false, nil
}

// ─── Deciding ─────────────────────────────────────────────────────────────────

// DecideRequest is one approver's verdict.
type DecideRequest struct {
	OrgID uuid.UUID
	// SpaceID is the space the caller's URL named, and the only thing that ties
	// this verdict to a space at all. OrgID resolves the ACTOR's team
	// memberships; it says nothing about where the approved entity lives. See
	// Decide.
	SpaceID    uuid.UUID
	ApprovalID uuid.UUID
	ActorID    uuid.UUID
	Decision   Decision

	// Reason is what the approver says about the verdict. Required on a
	// decline, optional on an approval; see Decide.
	Reason string
}

// Decide records an approver's verdict and reports what the caller must write.
//
// Authority is DATA: the actor may decide because an administrator named them,
// or a team they are an effective member of, on this transition. No capability
// is consulted and none was added — "who approves change requests" is per-gate,
// not per-role, and adding a Capability constant would have changed the access
// model, which CLAUDE.md §5 makes a stop-and-raise decision.
//
// On approval the caller applies the transition the request captured. On a
// decline it applies nothing: the item never left its source status, so
// "decline returns the item to the source status" is satisfied by the item not
// having moved. The record of the decline is the trail.
//
// # A decline must say why
//
// The reason is required on a decline and optional on an approval, and the
// check runs BEFORE the authority check so a non-approver is told they are not
// an approver rather than being asked for a reason they were never entitled to
// give. Whitespace is not a reason: an approver who submits spaces has said
// nothing, and storing it would produce a decline that renders blank, which is
// the failure the column was added to close rather than a lesser version of it.
//
// # The space is the authorisation
//
// req.SpaceID is not a filter for convenience; it is what makes the rest of
// this function safe, and it is loaded through rather than compared afterwards.
// Approvers hang off a TRANSITION, a transition belongs to a workflow, and a
// workflow is an ORG object every space can assign — so being a configured
// approver is an org-wide fact by construction. Without the space predicate on
// the load, an approver legitimately configured for one space could decide, and
// thereby APPLY, a transition on an entity in another one.
//
// It is also why the load comes first. Every branch below it answers with a
// distinguishable status — 409 for already decided, 409 for a deleted edge, 403
// for not an approver — so a caller outside the space that reached any of them
// would learn the approval exists and what state it is in. Reconciling first
// collapses all of that into the single ErrNotFound the route renders as 404,
// identically to an id that never existed.
// # The verdict and the transition are ONE write
//
// They used to be two, with nothing between them (D91). The service recorded
// the decision and returned; the route then applied the transition through a
// separate call. A failure in the second half left the approval marked approved,
// the entity unmoved, and the request no longer pending for anybody to decide
// again — an unrecoverable state whose only trace was a 500. The route's own
// error message described it: "the approval was recorded but the transition
// could not be applied". That branch is gone, because the state it named cannot
// occur.
//
// The quieter half of the same defect was that the apply was UNCONDITIONAL. An
// approval captures the status the entity held when the request was made, and is
// decided whenever an approver gets to it — minutes or days later. Writing the
// captured target without checking silently reverts everything that happened in
// between and leaves an audit row asserting a move from a status the entity had
// already left. ApplyInput.ExpectFromStatus is the check; it is a predicate on
// the write statement rather than a read before it, so there is no window, and
// it fails the whole transaction rather than just the write.
func (s *TierService) Decide(ctx context.Context, req DecideRequest) (Approval, error) {
	storedReason, err := decisionReason(req.Decision, req.Reason)
	if err != nil {
		return Approval{}, err
	}
	if s.applier == nil {
		// Same fail-closed direction as a missing gate: a deployment that did
		// not wire the applier must not fall back to recording verdicts it has
		// no way to honour.
		return Approval{}, errors.New("decide: no approval applier is configured")
	}

	approval, err := s.store.GetApprovalInSpace(ctx, req.SpaceID, req.ApprovalID)
	if err != nil {
		return Approval{}, fmt.Errorf("decide: loading the approval: %w", err)
	}
	if !approval.IsPending() {
		return Approval{}, ErrApprovalAlreadyDecided
	}
	if approval.TransitionID == nil {
		// The edge was deleted under the request. migration 047 keeps the
		// record rather than destroying it, and there is nothing left to
		// traverse, so the request is unresolvable rather than decidable.
		return Approval{}, ErrInvalidTransition
	}

	if err := s.checkDecideAuthority(ctx, req, *approval.TransitionID); err != nil {
		return Approval{}, err
	}

	in := DecideAndApplyInput{
		SpaceID:    req.SpaceID,
		ApprovalID: req.ApprovalID,
		ActorID:    req.ActorID,
		Decision:   req.Decision,
		Reason:     storedReason,
	}

	// A decline applies nothing: the entity never left its source status, which
	// is what "a decline returns the item to the source status" means when the
	// gate blocks rather than moves. Apply stays nil, and the transaction is the
	// single verdict UPDATE it always was.
	if req.Decision == DecisionApproved {
		effects, err := s.planEffects(ctx, *approval.TransitionID)
		if err != nil {
			return Approval{}, err
		}
		in.Apply = &ApplyInput{
			EntityType:       approval.EntityType,
			EntityID:         approval.EntityID,
			OrgID:            req.OrgID,
			SpaceID:          req.SpaceID,
			ActorID:          req.ActorID,
			ToStatus:         approval.ToStatus,
			ToStateID:        approval.ToStateID,
			ExpectFromStatus: approval.FromStatus,
			TransitionID:     approval.TransitionID,
			ApprovalID:       &approval.ID,
			Effects:          effects,
		}
	}

	return s.applier.DecideAndApply(ctx, in)
}

// ApproverRecipients returns every user who may decide a transition, expanded.
//
// It is the notification fan-out's list, and it is deliberately built from the
// SAME two facts CanDecide consults — the configured approver rows, and
// ADR-0007 effective team membership — rather than from a convenient
// approximation like a team's direct members. If the two disagreed, the
// disagreement would be silent in the worst direction: an approval sitting
// pending on somebody the guard would happily accept but nobody ever told.
//
// Ids are de-duplicated, because one person can be named directly and also sit
// in a named team, and being named twice is not a reason to be alerted twice.
//
// An unknown subject type contributes nobody. That is the same fail-closed
// direction CanDecide takes for the same value, and here it is merely quiet
// rather than unsafe: a subject this build cannot resolve has not been shown to
// include anybody.
func (s *TierService) ApproverRecipients(
	ctx context.Context, orgID, transitionID uuid.UUID,
) ([]uuid.UUID, error) {
	approvers, err := s.store.ApproversForTransition(ctx, transitionID)
	if err != nil {
		return nil, fmt.Errorf("approver recipients: loading approvers: %w", err)
	}

	seen := make(map[uuid.UUID]struct{}, len(approvers))
	out := make([]uuid.UUID, 0, len(approvers))
	add := func(id uuid.UUID) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	for _, ap := range approvers {
		switch ap.SubjectType {
		case ApproverUser:
			add(ap.SubjectID)
		case ApproverTeam:
			members, err := s.store.EffectiveTeamMemberIDs(ctx, orgID, ap.SubjectID)
			if err != nil {
				return nil, fmt.Errorf("approver recipients: expanding team: %w", err)
			}
			for _, m := range members {
				add(m)
			}
		default:
			continue
		}
	}
	return out, nil
}

// MarkDecidable fills CanDecide on each approval for the given actor.
//
// It exists because approval authority is DATA — being named on the transition,
// directly or through an ADR-0007 effective team — so a client has no way to
// work out whether it may offer an Approve button. There is no capability to
// consult and nothing on the approval row that answers it. Without this the
// only honest UI would show the buttons to everybody and let the 403 explain,
// which puts the refusal after the click instead of before it.
//
// The actor is resolved ONCE. Effective team expansion is a recursive query,
// and doing it per row would make a busy space's pending list cost a traversal
// per approval to answer one question that does not vary between them. Approver
// lists are cached per transition for the same reason: a space blocked on one
// guarded edge has many approvals sharing a single approver set.
//
// An approval whose transition has been deleted (TransitionID nil, migration
// 047's ON DELETE SET NULL) is decidable by nobody — there is no approver list
// left to consult — and keeps CanDecide false. It is deliberately still
// returned: the requester needs to see that their request is stuck, which is
// the same reason the row survived the delete at all.
func (s *TierService) MarkDecidable(
	ctx context.Context, orgID, actorID uuid.UUID, approvals []Approval,
) ([]Approval, error) {
	if len(approvals) == 0 {
		return approvals, nil
	}

	actor, err := s.resolveActor(ctx, GateRequest{OrgID: orgID, ActorID: actorID})
	if err != nil {
		return nil, err
	}

	byTransition := make(map[uuid.UUID]bool, len(approvals))
	for i := range approvals {
		if approvals[i].TransitionID == nil {
			continue
		}
		tid := *approvals[i].TransitionID
		decidable, seen := byTransition[tid]
		if !seen {
			approvers, err := s.store.ApproversForTransition(ctx, tid)
			if err != nil {
				return nil, fmt.Errorf("marking decidable: loading approvers: %w", err)
			}
			decidable = CanDecide(approvers, actor)
			byTransition[tid] = decidable
		}
		approvals[i].CanDecide = decidable
	}
	return approvals, nil
}

// decisionReason validates and normalises what the approver said.
//
// A decline must carry one; an approval need not. Whitespace is not a reason —
// an approver who submits spaces has said nothing, and storing it would produce
// a decline that renders blank, which is the failure migration 050 exists to
// close rather than a lesser version of it.
//
// An empty reason on an APPROVAL becomes NULL rather than "", so "said nothing"
// and "said the empty string" are not two representations of one thing.
func decisionReason(d Decision, raw string) (*string, error) {
	reason := strings.TrimSpace(raw)
	if d == DecisionDeclined && reason == "" {
		return nil, ErrDeclineReasonRequired
	}
	if reason == "" {
		return nil, nil
	}
	return &reason, nil
}

// checkDecideAuthority reports whether the actor is one of the transition's
// configured approvers. Authority is data, not a capability; see Decide.
func (s *TierService) checkDecideAuthority(ctx context.Context, req DecideRequest, transitionID uuid.UUID) error {
	approvers, err := s.store.ApproversForTransition(ctx, transitionID)
	if err != nil {
		return fmt.Errorf("decide: loading approvers: %w", err)
	}
	actor, err := s.resolveActor(ctx, GateRequest{OrgID: req.OrgID, ActorID: req.ActorID})
	if err != nil {
		return err
	}
	if !CanDecide(approvers, actor) {
		return ErrNotAnApprover
	}
	return nil
}
