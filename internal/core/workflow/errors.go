package workflow

import "errors"

// ErrNotFound is returned when a workflow object cannot be located.
var ErrNotFound = errors.New("not found")

// ErrInvalidTransition is returned when the requested state change is not
// permitted by the workflow definition.
var ErrInvalidTransition = errors.New("invalid workflow transition")

// ErrNoWorkflow is returned when an entity has no workflow assigned.
var ErrNoWorkflow = errors.New("no workflow assigned")

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
