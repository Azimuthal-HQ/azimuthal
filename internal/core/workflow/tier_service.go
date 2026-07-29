package workflow

import (
	"context"
	"errors"
	"fmt"

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
// # Absence is not refusal
//
// The tiers must be invisible until an administrator opts in, so every way of
// NOT being configured answers "nothing applies" rather than "no":
//
//   - the space has no workflow            → nothing applies
//   - the status names no state            → nothing applies
//   - the workflow has no such edge        → nothing applies
//   - the edge has no guards or approvers  → nothing applies
//
// Only a CONFIGURED guard refuses. That is what makes an untouched space
// byte-identical to the build before migration 046, and it is asserted directly
// by TestGate_UntouchedWorkflowIsUnaffected.
//
// The third case deserves a note: "the workflow has no such edge" does NOT mean
// the transition is illegal. The legacy /status routes have their own opinion
// about legality — one of them a hardcoded map, the other nothing — and this
// phase does not adjudicate between them. Gate adds restrictions; it never
// removes one and never adds a new legality rule. Reconciling the two state
// machines is a separate, larger change, recorded in the phase report.
type TierService struct {
	store TierStore
}

// NewTierService creates a TierService over the given store.
func NewTierService(store TierStore) *TierService {
	return &TierService{store: store}
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

// GateResult is what the caller must do about the transition.
//
// Exactly one of Refused, Pending or "proceed" holds: Refused non-nil means
// stop with a 4xx naming the guard, Pending non-nil means stop and report the
// request as awaiting approval, and both nil means apply Effects inside the
// transaction that writes the status.
type GateResult struct {
	// TransitionID is the matched edge, nil when no edge was found and the
	// tiers therefore do not apply.
	TransitionID *uuid.UUID

	// Refused names the guard that refused. The caller answers 4xx with
	// Refused.Reason, which is written for a person.
	Refused *Refusal

	// Pending is the approval request this call created. Non-nil means the
	// status change must NOT be written: the item stays where it is until an
	// approver decides.
	Pending *Approval

	// Effects are the post-function mutations to apply inside the same
	// transaction as the status change. Empty when nothing is configured.
	Effects []Effect
}

// Gate evaluates every configured tier for one status change.
//
// It writes only in one case — creating the pending approval — and that write
// is deliberately outside the caller's transaction, because the caller's
// transaction is about to be abandoned: a gated transition does not move the
// item, so there is nothing for the request to commit alongside.
func (s *TierService) Gate(ctx context.Context, req GateRequest) (GateResult, error) {
	transition, err := s.resolveTransition(ctx, req)
	if err != nil {
		return GateResult{}, err
	}
	if transition == nil {
		// Nothing is configured for this move, in any of the several ways that
		// can be true. Absence is not refusal.
		return GateResult{}, nil
	}

	actor, err := s.resolveActor(ctx, req)
	if err != nil {
		return GateResult{}, err
	}

	// Validators decide whether the transition succeeds. Conditions were
	// already applied when the transition was offered; re-running them here
	// would refuse a transition the user was never shown, with a message about
	// something they cannot see.
	guards, err := s.store.GuardsForTransition(ctx, transition.ID)
	if err != nil {
		return GateResult{}, fmt.Errorf("gate: loading guards: %w", err)
	}
	if refusal := Evaluate(guards, GuardValidatorClass, actor, req.Entity); refusal != nil {
		return GateResult{TransitionID: &transition.ID, Refused: refusal}, nil
	}

	approvers, err := s.store.ApproversForTransition(ctx, transition.ID)
	if err != nil {
		return GateResult{}, fmt.Errorf("gate: loading approvers: %w", err)
	}
	if RequiresApproval(approvers) {
		pending, err := s.requestApproval(ctx, req, transition)
		if err != nil {
			return GateResult{}, err
		}
		return GateResult{TransitionID: &transition.ID, Pending: pending}, nil
	}

	effects, err := s.planEffects(ctx, transition.ID)
	if err != nil {
		return GateResult{}, err
	}
	return GateResult{TransitionID: &transition.ID, Effects: effects}, nil
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

// AvailableTransitions returns the transitions reachable from the item's
// current status that the actor is actually offered.
//
// This is where CONDITIONS run. ADR-0011 defines a condition as deciding
// "whether a transition is offered", which needs something that offers — and
// before this phase nothing did: AvailableTransitions existed on the engine
// with no production caller and no HTTP route.
//
// A transition whose condition the actor does not satisfy is omitted silently,
// which is the point: a condition hides, a validator explains. An actor who
// needs to know why asks an administrator, because the answer is configuration
// they are not entitled to enumerate.
func (s *TierService) AvailableTransitions(
	ctx context.Context, req GateRequest, candidates []*Transition,
) ([]*Transition, error) {
	if len(candidates) == 0 {
		return []*Transition{}, nil
	}

	actor, err := s.resolveActor(ctx, req)
	if err != nil {
		return nil, err
	}

	offered := make([]*Transition, 0, len(candidates))
	for _, t := range candidates {
		guards, err := s.store.GuardsForTransition(ctx, t.ID)
		if err != nil {
			return nil, fmt.Errorf("available transitions: loading guards: %w", err)
		}
		if Evaluate(guards, GuardConditionClass, actor, req.Entity) == nil {
			offered = append(offered, t)
		}
	}
	return offered, nil
}

// resolveTransition maps the request's status text onto the edge it names, or
// nil when the tiers do not apply. Each nil return is a way of not being
// configured, never a refusal.
func (s *TierService) resolveTransition(ctx context.Context, req GateRequest) (*Transition, error) {
	if req.WorkflowID == uuid.Nil {
		return nil, nil
	}

	from, err := s.store.StateByName(ctx, req.WorkflowID, req.CurrentStatus)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// The item's status names no state — an ordinary situation, because
			// status is free text and a state can be renamed out from under it.
			return nil, nil
		}
		return nil, fmt.Errorf("gate: resolving current state: %w", err)
	}

	to, err := s.store.StateByName(ctx, req.WorkflowID, req.TargetStatus)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("gate: resolving target state: %w", err)
	}

	transition, err := s.store.TransitionBetween(ctx, req.WorkflowID, from.ID, to.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// The workflow defines no such edge. This is NOT a refusal: the
			// legacy routes have their own legality rules and this phase does
			// not adjudicate between them. See the type comment.
			return nil, nil
		}
		return nil, fmt.Errorf("gate: resolving transition: %w", err)
	}
	return transition, nil
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
func (s *TierService) requestApproval(ctx context.Context, req GateRequest, t *Transition) (*Approval, error) {
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
		return &created, nil
	}
	if !errors.Is(err, ErrApprovalPending) {
		return nil, fmt.Errorf("gate: requesting approval: %w", err)
	}

	existing, getErr := s.store.PendingApprovalForEntity(ctx, req.EntityType, req.EntityID)
	if getErr != nil {
		return nil, fmt.Errorf("gate: reading the pending approval: %w", getErr)
	}
	return &existing, nil
}

// ─── Deciding ─────────────────────────────────────────────────────────────────

// DecideRequest is one approver's verdict.
type DecideRequest struct {
	OrgID      uuid.UUID
	ApprovalID uuid.UUID
	ActorID    uuid.UUID
	Decision   Decision
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
func (s *TierService) Decide(ctx context.Context, req DecideRequest) (Approval, []Effect, error) {
	approval, err := s.store.GetApproval(ctx, req.ApprovalID)
	if err != nil {
		return Approval{}, nil, fmt.Errorf("decide: loading the approval: %w", err)
	}
	if !approval.IsPending() {
		return Approval{}, nil, ErrApprovalAlreadyDecided
	}
	if approval.TransitionID == nil {
		// The edge was deleted under the request. migration 047 keeps the
		// record rather than destroying it, and there is nothing left to
		// traverse, so the request is unresolvable rather than decidable.
		return Approval{}, nil, ErrInvalidTransition
	}

	if err := s.checkDecideAuthority(ctx, req, *approval.TransitionID); err != nil {
		return Approval{}, nil, err
	}

	decided, err := s.store.DecideApproval(ctx, req.ApprovalID, req.ActorID, req.Decision)
	if err != nil {
		return Approval{}, nil, fmt.Errorf("decide: recording the decision: %w", err)
	}
	if req.Decision != DecisionApproved {
		return decided, nil, nil
	}

	// The transition now proceeds, so its post-functions run with it.
	effects, err := s.planEffects(ctx, *approval.TransitionID)
	if err != nil {
		return Approval{}, nil, err
	}
	return decided, effects, nil
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
