package workflow

import "errors"

// ErrNotFound is returned when a workflow object cannot be located.
var ErrNotFound = errors.New("not found")

// ErrInvalidTransition is returned when the requested state change is not
// permitted by the workflow definition.
var ErrInvalidTransition = errors.New("invalid workflow transition")

// ErrNoWorkflow is returned when an entity has no workflow assigned.
var ErrNoWorkflow = errors.New("no workflow assigned")

// ErrWorkflowInUse is returned when a workflow cannot be deleted because
// something still references it. The handler answers it as 409. The common
// case — a live space assigned the workflow — is caught by a count before the
// delete is attempted, so it can name the number of spaces; this sentinel
// covers the residual case the count cannot see, where no live space is
// assigned but a ticket or project item still carries a workflow_state_id
// pointing into one of the workflow's states (a space reassigned to another
// workflow, or a soft-deleted space's items). The workflow_state_id foreign
// keys are ON DELETE NO ACTION, so the database refuses such a delete; mapping
// that refusal here turns a raw constraint 500 into the same honest 409 rather
// than letting the delete strand a state id.
var ErrWorkflowInUse = errors.New("workflow is still in use")

// ErrStateNotInWorkflow is returned when a transition names an endpoint state
// that is not a state of the workflow the transition is being added to.
//
// It deliberately does not say WHICH endpoint was wrong, and does not
// distinguish a state that exists in another workflow from one that exists
// nowhere at all. The predicate that raises it cannot tell those apart either —
// that is the point of it. A caller that maps this to anything other than the
// answer it gives for a state that does not exist turns the route back into an
// existence oracle over every workflow state in the installation.
var ErrStateNotInWorkflow = errors.New("state not found")

// ErrTransitionNotInWorkflow is returned when a delete names a transition that
// is not an edge of the workflow in the URL.
//
// Like ErrStateNotInWorkflow, it does not distinguish a transition that belongs
// to another workflow from one that exists nowhere: the workflow-scoped DELETE
// matches no rows in either case, so the two are one answer. A caller that maps
// this to anything other than a plain not-found turns the route into an existence
// oracle over every workflow transition in the installation.
var ErrTransitionNotInWorkflow = errors.New("transition not found")

// ─── Tier errors (ADR-0011) ───────────────────────────────────────────────────

// ErrPostFunctionUnknown is returned when a stored post-function names an
// action this build cannot perform. The transition fails rather than committing
// with the action silently skipped; see PlanPostFunctions.
var ErrPostFunctionUnknown = errors.New("unknown workflow post-function")

// ErrPostFunctionMalformed is returned when a stored post-function is missing a
// parameter its kind requires, or carries one this build cannot parse.
var ErrPostFunctionMalformed = errors.New("malformed workflow post-function")

// ErrApprovalRequired is returned when a transition is gated by an approval and
// no decision has been made. It is not a failure: the caller turns it into the
// "requested, pending approval" answer.
var ErrApprovalRequired = errors.New("workflow transition requires approval")

// ErrApprovalPending is returned when a transition already has an approval
// awaiting a decision. The partial unique index in migration 047 is what
// actually enforces one-pending-per-item; this is the mapped form of that
// violation.
var ErrApprovalPending = errors.New("an approval is already pending for this item")

// ErrApprovalAlreadyDecided is returned when a decision is made on an approval
// that another approver has already decided.
var ErrApprovalAlreadyDecided = errors.New("this approval has already been decided")

// ErrTransitionRaced is returned when the entity is no longer in the status the
// caller decided about, so the compare-and-swap on the status write matched no
// rows and nothing was written.
//
// It is a CONFLICT, not a not-found and not an internal error. The entity
// exists; what expired is the caller's belief about where it was. Two callers
// can reach it: a direct transition whose gate read is overtaken by a
// concurrent one, and an approval decided after the entity has moved on from
// the status the request captured (D91). Both want the same answer — re-read
// and decide again — which is why they share a sentinel.
var ErrTransitionRaced = errors.New("this item changed while the request was in flight, so nothing was written")

// ErrNotAnApprover is returned when the actor is not among the transition's
// configured approvers.
var ErrNotAnApprover = errors.New("you are not an approver for this transition")

// ErrApproverExists is returned when the same subject is added twice as an
// approver on one transition. The unique key on
// (transition_id, subject_type, subject_id) is what enforces it.
var ErrApproverExists = errors.New("this subject is already an approver for this transition")

// ErrDeclineReasonRequired is returned when an approver declines a transition
// without saying why.
//
// The rule lives here rather than in a CHECK because migration 050's column is
// nullable on purpose: a database that already ran 047 can hold declined rows
// written before the column existed, and a constraint refusing them would fail
// at boot — where D73 has already shown this project has no safety net. See
// that migration's header.
//
// It is required on a DECLINE only. An approval needs no justification, because
// the transition itself is the record; a decline leaves the requester holding
// an item that did not move, and without a sentence they have no way to learn
// what would make it move. That is the silent no-op this tier exists to
// prevent, arriving one layer later than the guards.
var ErrDeclineReasonRequired = errors.New("a decline must say why")
