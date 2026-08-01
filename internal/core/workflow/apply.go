package workflow

import (
	"context"

	"github.com/google/uuid"
)

// ApplyInput is one status change together with every post-function effect it
// triggers.
type ApplyInput struct {
	EntityType ApprovalEntityType
	EntityID   uuid.UUID

	OrgID   uuid.UUID
	SpaceID uuid.UUID
	ActorID uuid.UUID

	// ToStatus is the status text written to the entity.
	ToStatus string
	// ToStateID keeps workflow_state_id in step with ToStatus. Nil when the
	// move resolved to no workflow state, which is the legacy /status route
	// operating in a space whose workflow does not name the status.
	ToStateID *uuid.UUID

	// ExpectFromStatus is the status the entity must still be in for this write
	// to land — the compare-and-swap half of the statement.
	//
	// It is REQUIRED, not optional, and there is deliberately no "write
	// unconditionally" escape. Every caller reached this point by reading the
	// entity and deciding about what it read, so every caller knows the value;
	// an escape would exist only for a caller that had stopped knowing, which is
	// exactly the caller that must not write. A mismatch is zero rows and the
	// transaction rolls back — see ErrTransitionRaced.
	ExpectFromStatus string

	// TransitionID is the edge traversed, for the audit trail. Nil when the
	// tiers did not apply.
	TransitionID *uuid.UUID

	// Effects are the planned post-function mutations. They commit with the
	// status write or not at all.
	Effects []Effect
	// ApprovalID records which approval released this transition, when one did.
	ApprovalID *uuid.UUID
}

// TransitionApplier writes a status change and its effects atomically.
//
// # Why this seam exists at all
//
// ADR-0011's post-functions are the only part of the tier model that WRITES,
// and a post-function that lands when the transition rolls back has invented an
// effect with no cause, while one that is lost when the transition commits has
// silently not run. Both are worse than the feature being absent, so the two
// writes are one transaction.
//
// That makes this shared-surfaces.md Convention B — "adapter layer, inside the
// transaction … used only where the audit trail is part of an atomicity
// contract" — and the audit row goes in the same transaction for the same
// reason: a trail claiming a transition applied its effects must roll back with
// them.
//
// # And why the default path does not use it
//
// A transition with NO post-functions is not an atomicity contract: it is a
// single UPDATE, which is what it has always been. Those keep the existing
// Convention A write (mutate, then audit) untouched, so a space nobody has
// configured executes exactly the statements it executed before migration 046.
// Routing every transition through a transaction "for consistency" would have
// changed the hot path for every user in order to serve a feature almost none
// of them have enabled — and would have been a third audit convention in all
// but name.
type TransitionApplier interface {
	// ApplyTransition writes the status, the effects and the audit row in one
	// transaction. Any failure rolls back all three, and the returned error
	// names what failed.
	//
	// Returns ErrTransitionRaced when the entity is no longer in
	// in.ExpectFromStatus.
	ApplyTransition(ctx context.Context, in ApplyInput) error
}

// ApprovalApplier commits an approver's verdict and the transition that verdict
// releases in ONE transaction.
//
// # Why this is a second seam and not two calls
//
// It replaces a sequence — record the decision, then apply the transition —
// that had no compensation between its halves (D91). The failure it produced is
// specific and unrecoverable by the user: the approval is marked approved, the
// entity never moves, and the request is no longer pending, so nothing can
// decide it again. The route's own error message admitted it, in the words "the
// approval was recorded but the transition could not be applied". That branch
// does not exist any more, because the outcome it described cannot happen.
//
// The second failure the sequence had was quieter. An approval captures the
// status the entity was in when the request was made, and is decided whenever an
// approver gets to it. Applying the captured target unconditionally overwrites
// whatever the entity became in between — a blind write of stale data over
// fresh, with an audit row asserting a transition from a status the entity had
// already left. ApplyInput.ExpectFromStatus is the compare-and-swap that refuses
// it, and it refuses inside the transaction, so the verdict rolls back with the
// write and the approval is still pending for somebody to decide against the
// entity's real state.
type ApprovalApplier interface {
	// DecideAndApply records the verdict and, on an approval, applies the
	// transition it releases. Either both land or neither does.
	//
	// Returns ErrApprovalAlreadyDecided when another approver got there first,
	// and ErrTransitionRaced when the entity left the status the approval
	// captured.
	DecideAndApply(ctx context.Context, in DecideAndApplyInput) (Approval, error)
}

// DecideAndApplyInput is one verdict together with the transition it releases.
type DecideAndApplyInput struct {
	SpaceID    uuid.UUID
	ApprovalID uuid.UUID
	ActorID    uuid.UUID
	Decision   Decision
	// Reason is nil when the approver said nothing, which migration 050 permits
	// alongside a decision but never without one.
	Reason *string

	// Apply is the transition to commit with the verdict, and is nil on a
	// decline — a declined request moves nothing, so there is nothing to apply
	// and the record of the decline is the whole outcome.
	Apply *ApplyInput
}
