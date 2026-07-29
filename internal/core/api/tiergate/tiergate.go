// Package tiergate adapts the workflow tier chokepoint to the HTTP layer.
//
// It exists so the four routes that can change an item's status ask the same
// question in the same way. Before this phase those routes shared nothing —
// two ran a hardcoded Go map, one ran the database engine, and one validated
// nothing at all — and a guard wired into any one of them would have been
// bypassable through the others.
//
// Everything here is thin on purpose. The decisions live in
// internal/core/workflow; this package resolves the space's workflow, snapshots
// the actor's capabilities, and turns a workflow.GateResult into an HTTP
// answer.
package tiergate

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
)

// WorkflowResolver reports which workflow a space uses. It is the one thing
// this package needs from the queries layer.
type WorkflowResolver interface {
	WorkflowIDForSpace(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, error)
}

// Gate holds the tier service and the space→workflow lookup.
//
// It is a REQUIRED constructor argument at every call site rather than an
// optional With* builder. An optional collaborator that answers "feature
// disabled" when absent is exactly the dark-harness shape CLAUDE.md §2 names:
// every tier test would pass, every endpoint would read as covered, and no
// guard would ever have run.
type Gate struct {
	svc       *workflow.TierService
	workflows WorkflowResolver
}

// New creates a Gate.
func New(svc *workflow.TierService, workflows WorkflowResolver) *Gate {
	return &Gate{svc: svc, workflows: workflows}
}

// Request is one status change, in the terms the HTTP layer has.
type Request struct {
	OrgID         uuid.UUID
	SpaceID       uuid.UUID
	EntityType    workflow.ApprovalEntityType
	EntityID      uuid.UUID
	ActorID       uuid.UUID
	CurrentStatus string
	TargetStatus  string
	Entity        workflow.EntitySnapshot
}

// Evaluate runs every configured tier for the request.
//
// A space with no workflow assigned resolves to uuid.Nil rather than an error:
// that is a supported live state — workflow assignment happens outside the
// space-create transaction and is best-effort — and it means "nothing is
// configured", not "refuse".
func (g *Gate) Evaluate(ctx context.Context, req Request) (workflow.GateResult, error) {
	workflowID, err := g.workflows.WorkflowIDForSpace(ctx, req.SpaceID)
	if err != nil {
		if !errors.Is(err, workflow.ErrNotFound) {
			return workflow.GateResult{}, fmt.Errorf("tier gate: resolving the space workflow: %w", err)
		}
		workflowID = uuid.Nil
	}

	res, err := g.svc.Gate(ctx, workflow.GateRequest{
		OrgID:             req.OrgID,
		SpaceID:           req.SpaceID,
		WorkflowID:        workflowID,
		EntityType:        req.EntityType,
		EntityID:          req.EntityID,
		CurrentStatus:     req.CurrentStatus,
		TargetStatus:      req.TargetStatus,
		ActorID:           req.ActorID,
		ActorCapabilities: CapabilitiesIn(ctx, req.SpaceID),
		Entity:            req.Entity,
	})
	if err != nil {
		// Wrapped, not replaced: handleTierError matches the post-function
		// sentinels with errors.Is, and a replaced error would collapse a
		// misconfigured action into a generic 500.
		return workflow.GateResult{}, fmt.Errorf("tier gate: %w", err)
	}
	return res, nil
}

// CapabilitiesIn snapshots the actor's guard-relevant capabilities in a space.
//
// It probes access.Can for exactly the capabilities a guard may name, rather
// than exposing the resolved capability set: the guard vocabulary is a
// deliberate subset, and a snapshot of the whole set would let a future guard
// kind read a capability nobody reviewed as a workflow input.
func CapabilitiesIn(ctx context.Context, spaceID uuid.UUID) map[access.Capability]struct{} {
	caps := map[access.Capability]struct{}{}
	for _, c := range workflow.GuardCapabilities() {
		if access.Can(ctx, c, spaceID) {
			caps[c] = struct{}{}
		}
	}
	return caps
}

// Refused answers the HTTP request when a validator refused, and reports
// whether it did so.
//
// The status is 422 rather than 400 or 409. The request was well formed (not
// 400) and the transition is one the workflow defines (not 409) — what failed
// is a configured precondition on the entity, which is precisely what 422
// describes. The code is VALIDATION_ERROR so friendlyErrorMessage passes the
// server's sentence through to the user unchanged: ADR-0011's case for this
// tier rests on the engine being able to explain itself, and collapsing the
// reason into a generic fallback would throw that away at the last step.
func Refused(w http.ResponseWriter, r *http.Request, res workflow.GateResult) bool {
	if res.Refused == nil {
		return false
	}
	respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation, res.Refused.Reason)
	return true
}

// PendingResponse is the body returned when a transition is gated by an
// approval. It is a 202: the request was accepted and is awaiting a decision,
// and the item has NOT moved.
type PendingResponse struct {
	Status       string     `json:"status"`
	Message      string     `json:"message"`
	ApprovalID   uuid.UUID  `json:"approval_id"`
	FromStatus   string     `json:"from_status"`
	ToStatus     string     `json:"to_status"`
	RequestedAt  string     `json:"requested_at"`
	TransitionID *uuid.UUID `json:"transition_id,omitempty"`
}

// Pending answers the HTTP request when an approval is required, and reports
// whether it did so.
//
// 202 Accepted, not 409 and not 403. The caller's request was understood and
// recorded; what has not happened yet is the decision. A 4xx would tell the
// client it did something wrong, and the board would render a failure where the
// truth is "waiting on somebody".
func Pending(w http.ResponseWriter, res workflow.GateResult) bool {
	if res.Pending == nil {
		return false
	}
	respond.JSON(w, http.StatusAccepted, PendingResponse{
		Status:       "pending_approval",
		Message:      "This transition needs approval. It has been requested and the item has not moved.",
		ApprovalID:   res.Pending.ID,
		FromStatus:   res.Pending.FromStatus,
		ToStatus:     res.Pending.ToStatus,
		RequestedAt:  res.Pending.RequestedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		TransitionID: res.Pending.TransitionID,
	})
	return true
}
