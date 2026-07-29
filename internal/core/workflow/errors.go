package workflow

import "errors"

// ErrNotFound is returned when a workflow object cannot be located.
var ErrNotFound = errors.New("not found")

// ErrInvalidTransition is returned when the requested state change is not
// permitted by the workflow definition.
var ErrInvalidTransition = errors.New("invalid workflow transition")

// ErrNoWorkflow is returned when an entity has no workflow assigned.
var ErrNoWorkflow = errors.New("no workflow assigned")

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

// ErrNotAnApprover is returned when the actor is not among the transition's
// configured approvers.
var ErrNotAnApprover = errors.New("you are not an approver for this transition")

// ErrApproverExists is returned when the same subject is added twice as an
// approver on one transition. The unique key on
// (transition_id, subject_type, subject_id) is what enforces it.
var ErrApproverExists = errors.New("this subject is already an approver for this transition")
