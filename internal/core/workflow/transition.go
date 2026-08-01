package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// This file is the canonical answer to "may this entity move to that status?".
//
// # What changed, and why the previous answer was not one
//
// Before this phase the resolution step returned "the tiers do not apply" for
// every way of failing to find an edge — an unknown current status, an unknown
// target status, a missing transition — and the caller then wrote the status
// anyway. The justification was that the legacy routes owned legality and the
// tiers only added restrictions on top. That was coherent while nothing else
// adjudicated, and it produced a system in which an administrator could
// configure an approval on an edge and watch a `201` sail past it, because the
// move they were guarding resolved to no edge at all.
//
// So the rule is now the other way round: WHEN A SPACE HAS A WORKFLOW, THE
// WORKFLOW DECIDES. A status the workflow does not name is refused, and a pair
// of states it defines no edge between is refused. Absence of an edge is a
// refusal, because in a state machine that is exactly what absence means.
//
// # Absence of a WORKFLOW is still not refusal
//
// The one surviving "nothing applies" is a space with no workflow at all
// (`spaces.workflow_id IS NULL`). That is a supported live state — assignment
// happens outside the space-create transaction and is best-effort, and codex
// spaces are never assigned one — so those spaces keep the legality rule they
// have always had: the hardcoded map for tickets, nothing for project items.
// TransitionDecision.NoWorkflow is how the caller is told to apply it. That is what keeps
// TestGate_UntouchedWorkflowIsUnaffected honest rather than weakened.
//
// # Why the from-state is resolved three ways
//
// An entity's position in its state machine is recorded twice — `status` text
// and `workflow_state_id` — and until this phase only the legacy routes wrote,
// and they wrote only the first. So the two disagree on essentially every row
// written before migration 051. ResolveFromState prefers the status text (it is
// what the user sees and what the picker posts), falls back to the stored state
// id (a row whose status was renamed out from under it), and falls back again
// to the workflow's initial state (a row that has never been placed in the
// machine at all — every project item created before this phase, which is D72).
//
// The third fallback is what makes a legacy item's FIRST transition gated
// instead of invisible. It is sound here in a way it would not have been before
// migration 051, because it is reached only when neither recorded position
// resolves — not when the status merely fails to match, which is the conflation
// known-issues #30 rejects.

// RefusalCheck names which check refused a transition.
//
// A caller branches on this, never on Reason. The structural checks below are
// properties of the workflow graph; CheckGuard is the only one that carries a
// guard identity.
type RefusalCheck string

// The refusal checks. Every one of them means nothing was written.
const (
	// CheckGuard is a configured ADR-0011 tier-1 guard — a condition or a
	// validator — that the actor or the entity did not satisfy.
	CheckGuard RefusalCheck = "guard"

	// CheckUnknownTargetState is a target status that names no state in the
	// space's workflow. The workflow cannot accept the value at all, which is
	// why it is reported as a validation failure rather than as an illegal move.
	CheckUnknownTargetState RefusalCheck = "unknown_target_state"

	// CheckNoSuchTransition is a pair of states the workflow defines no edge
	// between. This is the state machine's own refusal, and it is deliberately
	// the same class of answer the hardcoded ticket map has always given for the
	// same situation — see TransitionDecision.
	CheckNoSuchTransition RefusalCheck = "no_such_transition"

	// CheckNoCurrentState is a workflow that could not place the entity
	// anywhere: its status names no state, its stored state id resolves to
	// none, and the workflow declares no initial state.
	//
	// Migration 016's partial unique index allows at most one initial state but
	// does not require one, so a workflow with zero is representable. It is a
	// misconfiguration rather than a user error, and it refuses rather than
	// falling through — a workflow nobody can leave is safer than one nobody is
	// held to.
	CheckNoCurrentState RefusalCheck = "no_current_state"
)

// TransitionDecision is the canonical verdict on one proposed status change.
//
// Exactly one of NoWorkflow, Refused, Pending or "proceed" holds:
//
//   - NoWorkflow — the space has no workflow. The caller applies whatever
//     legality rule it had before workflows existed, and writes `status` alone.
//   - Refused — stop, answer 4xx with Refused. Nothing was written.
//   - Pending — stop, report the request as awaiting approval. The entity has
//     NOT moved.
//   - all nil/false — apply, writing ToStatus and ToStateID together, with
//     Effects inside the same transaction.
type TransitionDecision struct {
	// NoWorkflow reports that the space has no workflow assigned, so this
	// package has no opinion and the caller's own rule governs.
	NoWorkflow bool

	// FromStateID is where the workflow considers the entity to be right now,
	// after the three-way resolution ResolveFromState performs. Nil only when
	// NoWorkflow.
	//
	// It is the expected-current value a caller compares against before writing
	// — see ApplyInput.ExpectFromStatus.
	FromStateID *uuid.UUID
	// FromStatus is the status text of FromStateID.
	FromStatus string

	// TransitionID is the matched edge. Nil when NoWorkflow, when Refused, or
	// when the move is a no-op (see NoOp).
	TransitionID *uuid.UUID
	// ToStateID is the state the target status names, so a caller writing the
	// status keeps workflow_state_id in step with it. This is the D71 repair:
	// the two columns are written together or not at all.
	ToStateID *uuid.UUID

	// NoOp reports that the target status is the state the entity is already
	// in. There is no edge from a state to itself in any workflow, so without
	// this the workflow would refuse a request that asks for nothing. Writing
	// the same status again is harmless and is what every previous build did.
	NoOp bool

	// Refused names the check that refused.
	Refused *Refusal

	// Pending is the approval request gating this move. Non-nil means the
	// status change must NOT be written.
	Pending *Approval
	// PendingIsNew distinguishes "this call created the request" from "a request
	// was already outstanding and this is it"; see the notification fan-out.
	PendingIsNew bool

	// Effects are the post-function mutations to apply in the same transaction
	// as the status change.
	Effects []Effect
}

// Offer is one transition the actor is allowed to be shown.
//
// It is deliberately expressed in the vocabulary the mutation route speaks —
// a target STATUS, not a state id — because the client that renders it posts a
// status back, and a picker built from ids would have to translate twice.
type Offer struct {
	TransitionID uuid.UUID `json:"transition_id"`
	Name         string    `json:"name"`
	ToStateID    uuid.UUID `json:"to_state_id"`
	ToStatus     string    `json:"to_status"`
	// RequiresApproval reports that taking this transition creates an approval
	// request rather than moving the entity. The client says so before the
	// click; without it the only honest UI is a button that sometimes answers
	// 202 for reasons the user cannot see.
	RequiresApproval bool `json:"requires_approval"`
}

// Offering is everything a client needs to render a status picker.
type Offering struct {
	// NoWorkflow reports that the space has no workflow, so this package can
	// offer nothing and the client falls back to its own vocabulary. It is the
	// read-side twin of Decision.NoWorkflow, and it exists so "no workflow" and
	// "a workflow that offers nothing" are distinguishable — they are very
	// different answers and collapsing them would strand the client.
	NoWorkflow bool `json:"no_workflow"`

	// CurrentStatus is where the workflow considers the entity to be, which is
	// not always the entity's status text — see ResolveFromState.
	CurrentStatus string `json:"current_status"`
	// EntityStatus is the entity's stored status text, so a client can tell
	// that the two have drifted rather than silently rendering the resolved one.
	EntityStatus string `json:"entity_status"`

	// Transitions are the moves the actor may be offered, conditions applied.
	// Never nil; an empty list means the actor is offered nothing, which is a
	// real answer.
	Transitions []Offer `json:"transitions"`
}

// ResolveFromState places the entity in its workflow.
//
// Three sources are tried in order, and the order is the point:
//
//  1. the status TEXT, which is what the user sees and what the picker posts;
//  2. the stored workflow_state_id, which survives a state being RENAMED —
//     a rename rewrites the state row, never the status text on entities;
//  3. the workflow's initial state, for an entity that has never been placed
//     in the machine at all.
//
// Only the third is a guess, and it is reached only when the first two both
// fail. That is what separates it from the fallback known-issues #30 rejects:
// that one fired whenever the status failed to match, which conflates "never
// transitioned" with "renamed out from under it" and would run the initial
// edge's post-functions — which MUTATE — for a move that has nothing to do with
// them. Here the stored state id answers the rename case before the fallback is
// consulted, so reaching it genuinely means the entity has no recorded position.
//
// Every entity created after this phase is born in its initial state (both
// columns written), so case 3 is history's path, not the ordinary one.
func (s *TierService) ResolveFromState(
	ctx context.Context, workflowID uuid.UUID, status string, stateID *uuid.UUID,
) (*State, error) {
	byName, err := s.store.StateByName(ctx, workflowID, status)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("resolving the current state by name: %w", err)
	}
	if byName != nil {
		return byName, nil
	}

	if stateID != nil {
		byID, err := s.store.StateByID(ctx, workflowID, *stateID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("resolving the current state by id: %w", err)
		}
		if byID != nil {
			return byID, nil
		}
	}

	initial, err := s.store.InitialState(ctx, workflowID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolving the workflow's initial state: %w", err)
	}
	return initial, nil
}

// InitialState is the workflow's starting state, or nil when it declares none.
//
// Exported because CREATION needs it and creation is not a transition: a new
// entity is placed in the machine rather than moved through it, so it has no
// from-state to resolve and no edge to check. See tiergate.Gate.InitialPosition
// for why an entity that starts outside its machine is D72.
func (s *TierService) InitialState(ctx context.Context, workflowID uuid.UUID) (*State, error) {
	initial, err := s.store.InitialState(ctx, workflowID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolving the workflow's initial state: %w", err)
	}
	return initial, nil
}

// refuse builds a structural refusal. The sentences name configuration and the
// move that was attempted, never another user's data, so they are safe to send
// to the caller verbatim — which is the whole of ADR-0011's inspectability
// claim at the last step.
func refuse(check RefusalCheck, reason string) *Refusal {
	return &Refusal{Check: check, Reason: reason}
}
