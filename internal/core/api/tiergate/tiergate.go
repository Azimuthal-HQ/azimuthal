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
// the actor's capabilities, and turns a workflow.TransitionDecision into an HTTP
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
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
)

// WorkflowResolver reports which workflow a space uses. It is the one thing
// this package needs from the queries layer.
type WorkflowResolver interface {
	WorkflowIDForSpace(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, error)
}

// NotificationEnqueuer is the subset of jobs.Queue this package needs. It is
// the same interface the comments and tickets handlers hold, named locally so
// this package does not depend on the jobs package for one method.
type NotificationEnqueuer interface {
	EnqueueNotification(ctx context.Context, args jobs.NotificationArgs) error
}

// Gate holds the tier service, the space→workflow lookup, and the notifier.
//
// All three are REQUIRED constructor arguments rather than optional With*
// builders. An optional collaborator that answers "feature disabled" when
// absent is exactly the dark-harness shape CLAUDE.md §2 names: every tier test
// would pass, every endpoint would read as covered, and no guard would ever
// have run.
type Gate struct {
	svc       *workflow.TierService
	workflows WorkflowResolver
	notifs    NotificationEnqueuer
}

// New creates a Gate.
func New(svc *workflow.TierService, workflows WorkflowResolver, notifs NotificationEnqueuer) *Gate {
	return &Gate{svc: svc, workflows: workflows, notifs: notifs}
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
	// CurrentStateID is the entity's stored workflow_state_id. It is the only
	// thing that places an entity whose state has been RENAMED, so a caller
	// holding the entity passes it; see workflow.TierService.ResolveFromState.
	CurrentStateID *uuid.UUID
	Entity         workflow.EntitySnapshot
}

// Evaluate runs every configured tier for the request.
//
// A space with no workflow assigned resolves to uuid.Nil rather than an error:
// that is a supported live state — workflow assignment happens outside the
// space-create transaction and is best-effort — and it means "nothing is
// configured", not "refuse".
func (g *Gate) Evaluate(ctx context.Context, req Request) (workflow.TransitionDecision, error) {
	workflowID, err := g.resolveWorkflow(ctx, req.SpaceID)
	if err != nil {
		return workflow.TransitionDecision{}, err
	}

	res, err := g.svc.Gate(ctx, g.gateRequest(ctx, req, workflowID))
	if err != nil {
		// Wrapped, not replaced: handleTierError matches the post-function
		// sentinels with errors.Is, and a replaced error would collapse a
		// misconfigured action into a generic 500.
		return workflow.TransitionDecision{}, fmt.Errorf("tier gate: %w", err)
	}
	g.notifyApprovers(ctx, req, res)
	return res, nil
}

// Offer reports which transitions the actor may be shown for this entity.
//
// It is the READ half of the chokepoint and it shares the mutation half's
// resolution, so the picker cannot offer a move Evaluate would refuse. It is
// deliberately NOT built on Evaluate: that call WRITES — it creates the pending
// approval row and notifies the approvers — and a page load must not request an
// approval on the user's behalf.
func (g *Gate) Offer(ctx context.Context, req Request) (workflow.Offering, error) {
	workflowID, err := g.resolveWorkflow(ctx, req.SpaceID)
	if err != nil {
		return workflow.Offering{}, err
	}
	offering, err := g.svc.OfferedTransitions(ctx, g.gateRequest(ctx, req, workflowID))
	if err != nil {
		return workflow.Offering{}, fmt.Errorf("tier gate: %w", err)
	}
	return offering, nil
}

// InitialPosition is where a NEW entity in this space starts.
//
// # Why creation has to care about the workflow at all
//
// Before this, every project item was born at the literal "open" and every
// ticket at "open" too. For tickets that happened to be right, because the
// seeded ticket workflow has a state called "open" — a NAME COINCIDENCE, not a
// design, and one that breaks the moment an administrator's workflow starts
// somewhere else. For project items it was simply wrong: the seeded project
// workflow's states are backlog/todo/in_progress/in_review/done, so a new item's
// status named no state, its first transition resolved no edge, and every guard,
// approval and post-function an administrator attached to the initial edge
// applied to every move EXCEPT the first one (D72, known-issues #30).
//
// An entity born inside its state machine has neither problem: both position
// columns are written, they agree, and the first move is adjudicated like every
// other.
//
// # ok=false is not a failure
//
// A space with no workflow has no initial state and the caller keeps its own
// literal default, exactly as before. That is a codex space, a space whose
// best-effort assignment at create time failed, or one whose workflow was
// deleted. Creation must not fail because of any of them — an entity that cannot
// be created is a worse outcome than one outside a state machine.
//
// A workflow that declares NO initial state answers ok=false for the same
// reason. It is a misconfiguration, and the honest response at creation time is
// the old default rather than a refusal to create; Gate is where that workflow
// becomes visible, by refusing every move with CheckNoCurrentState.
func (g *Gate) InitialPosition(ctx context.Context, spaceID uuid.UUID) (status string, stateID *uuid.UUID, ok bool) {
	workflowID, err := g.resolveWorkflow(ctx, spaceID)
	if err != nil || workflowID == uuid.Nil {
		return "", nil, false
	}
	initial, err := g.svc.InitialState(ctx, workflowID)
	if err != nil || initial == nil {
		return "", nil, false
	}
	id := initial.ID
	return initial.Name, &id, true
}

// resolveWorkflow maps the space onto its workflow, or uuid.Nil when it has
// none. See Evaluate for why "none" is an answer rather than an error.
func (g *Gate) resolveWorkflow(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, error) {
	workflowID, err := g.workflows.WorkflowIDForSpace(ctx, spaceID)
	if err != nil {
		if !errors.Is(err, workflow.ErrNotFound) {
			return uuid.Nil, fmt.Errorf("tier gate: resolving the space workflow: %w", err)
		}
		return uuid.Nil, nil
	}
	return workflowID, nil
}

// gateRequest builds the service-level request both halves share.
//
// One builder rather than two literals: the capability snapshot and the
// three-way position inputs must be identical on the read and write paths, and
// a second literal is how they come to differ by one field.
func (g *Gate) gateRequest(ctx context.Context, req Request, workflowID uuid.UUID) workflow.GateRequest {
	return workflow.GateRequest{
		OrgID:             req.OrgID,
		SpaceID:           req.SpaceID,
		WorkflowID:        workflowID,
		EntityType:        req.EntityType,
		EntityID:          req.EntityID,
		CurrentStatus:     req.CurrentStatus,
		TargetStatus:      req.TargetStatus,
		CurrentStateID:    req.CurrentStateID,
		ActorID:           req.ActorID,
		ActorCapabilities: CapabilitiesIn(ctx, req.SpaceID),
		Entity:            req.Entity,
	}
}

// notifyApprovers tells the transition's approvers that something is waiting.
//
// It lives HERE, at the chokepoint, for the same reason Gate itself does: four
// routes can change an item's status, and an emission written into any one of
// them would leave the other three silently un-notified. That is the failure
// mode PR-A's own header describes for guards — "a guard attached to the engine
// would have been unreachable by every real user AND bypassable by the route
// they actually use" — and a notification has the same shape, just quieter.
//
// # Only for a request this call created
//
// Two people pressing the same guarded button is ordinary, and the second press
// returns the FIRST person's still-pending approval (see
// TierService.requestApproval). Re-notifying on every retry turns one decision
// into a stream of identical alerts, which is how people learn to ignore the
// real one. PendingIsNew is the discriminator.
//
// # Failure is logged by absence, never propagated
//
// The approval row is already committed by the time this runs — Gate writes it
// deliberately outside the caller's transaction, because the caller's
// transaction is about to be abandoned. Returning an error here would turn "the
// approval was recorded but we could not send the alert" into "your transition
// failed", which is false and would invite the user to retry into the
// already-pending branch. The enqueue is best-effort, exactly as the ticket and
// comment handlers treat theirs.
func (g *Gate) notifyApprovers(ctx context.Context, req Request, res workflow.TransitionDecision) {
	if res.Pending == nil || !res.PendingIsNew || res.TransitionID == nil || g.notifs == nil {
		return
	}

	recipients, err := g.svc.ApproverRecipients(ctx, req.OrgID, *res.TransitionID)
	if err != nil {
		return
	}

	message := fmt.Sprintf(
		"Approval needed: a move from %q to %q is waiting for your decision.",
		res.Pending.FromStatus, res.Pending.ToStatus,
	)
	for _, userID := range recipients {
		// The requester is not told they are waiting on themselves. Somebody
		// can legitimately be both — an approver who moves an item through an
		// edge they also police — and the useful notification in that case is
		// the decision one, which they will get.
		if userID == req.ActorID {
			continue
		}
		_ = g.notifs.EnqueueNotification(ctx, jobs.NotificationArgs{
			UserID:    userID.String(),
			EventKind: KindApprovalRequested,
			Message:   message,
			// The recipient wants the ITEM, not the approval record — there is
			// no approval page to land on, and the decision is made from the
			// item's own surface. EntityKind is the approval's entity type,
			// whose vocabulary migration 047 deliberately matched to the audit
			// log's words for the same two things.
			ResourceID: res.Pending.EntityID.String(),
			EntityKind: string(res.Pending.EntityType),
			SpaceID:    req.SpaceID.String(),
		})
	}
}

// The two approval notification kinds.
//
// Dotted lowercase, matching the existing vocabulary ("ticket.assigned",
// "comment.added"). They are declared here rather than inline so the decision
// side and the request side cannot drift into two spellings of one family.
const (
	// KindApprovalRequested goes to the named approvers when a transition is
	// gated and a request is created.
	KindApprovalRequested = "workflow.approval_requested"
	// KindApprovalDecided goes to the person whose transition was decided.
	KindApprovalDecided = "workflow.approval_decided"
)

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

// Refused answers the HTTP request when the workflow refused the move, and
// reports whether it did so.
//
// # Two status codes, because there are two kinds of refusal
//
// A GUARD refusal is 422 VALIDATION_ERROR. The request was well formed (not
// 400) and the transition is one the workflow defines (not 409) — what failed
// is a configured precondition on the entity, which is precisely what 422
// describes. The code is VALIDATION_ERROR so friendlyErrorMessage passes the
// server's sentence through unchanged: ADR-0011's case for this tier rests on
// the engine being able to explain itself, and collapsing the reason into a
// generic fallback would throw that away at the last step. A target status the
// workflow does not name is the same class — a value the workflow cannot accept
// — and answers the same way.
//
// A MISSING EDGE is 409 INVALID_TRANSITION, which is what this product has
// always called that refusal. `tickets.ValidateTransition` has answered 409 for
// "cannot transition from x to y" since before workflows existed, the board
// rolls a card back on it, and now that the workflow adjudicates the same
// question it must not answer it under a different number. Reusing the code is
// the unification being real rather than cosmetic: the hardcoded map and the
// configured workflow give the same answer to the same question.
//
// This is a deliberate departure from the phase brief, which asked for 422 on
// both. The brief's own fence — do not regress the reverse-edge behaviour, keep
// the untouched path identical — points here, and answering 409 keeps
// TestKanbanDrag_InvalidTransition_Returns409 a genuine pass rather than a test
// rewritten to match new behaviour.
func Refused(w http.ResponseWriter, r *http.Request, res workflow.TransitionDecision) bool {
	if res.Refused == nil {
		return false
	}
	status, code := http.StatusUnprocessableEntity, respond.CodeValidation
	if res.Refused.Check == workflow.CheckNoSuchTransition {
		status, code = http.StatusConflict, respond.CodeInvalidTransition
	}
	respond.Error(w, r, status, code, res.Refused.Reason)
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
func Pending(w http.ResponseWriter, res workflow.TransitionDecision) bool {
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
