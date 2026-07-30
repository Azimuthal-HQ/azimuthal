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
	ApplyTransition(ctx context.Context, in ApplyInput) error
}
