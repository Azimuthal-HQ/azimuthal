package tickets

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Status represents a ticket lifecycle state.
type Status string

const (
	// StatusOpen is the initial state for new tickets.
	StatusOpen Status = "open"
	// StatusInProgress means work has begun on the ticket.
	StatusInProgress Status = "in_progress"
	// StatusResolved means the issue has been fixed, pending confirmation.
	StatusResolved Status = "resolved"
	// StatusClosed means the ticket is fully completed.
	StatusClosed Status = "closed"
)

// Priority represents ticket urgency.
type Priority string

const (
	// PriorityUrgent is the highest priority.
	PriorityUrgent Priority = "urgent"
	// PriorityHigh is an elevated priority.
	PriorityHigh Priority = "high"
	// PriorityMedium is the default priority.
	PriorityMedium Priority = "medium"
	// PriorityLow is the lowest priority.
	PriorityLow Priority = "low"
)

// validTransitions defines the allowed state machine transitions.
// Key is the current status; value is the set of statuses it can move to.
var validTransitions = map[Status][]Status{
	StatusOpen:       {StatusInProgress, StatusClosed},
	StatusInProgress: {StatusResolved, StatusOpen, StatusClosed},
	// Reverse edges let a ticket step back one state without being forced
	// through open: resolved can resume progress, and closed can reopen to
	// any live state. Forward-only skips (e.g. open -> resolved) stay invalid.
	StatusResolved: {StatusClosed, StatusOpen, StatusInProgress},
	StatusClosed:   {StatusOpen, StatusResolved, StatusInProgress},
}

// allStatuses is the set of recognised statuses.
var allStatuses = map[Status]bool{
	StatusOpen:       true,
	StatusInProgress: true,
	StatusResolved:   true,
	StatusClosed:     true,
}

// allPriorities is the set of recognised priorities.
var allPriorities = map[Priority]bool{
	PriorityUrgent: true,
	PriorityHigh:   true,
	PriorityMedium: true,
	PriorityLow:    true,
}

// IsValid reports whether s is a recognised status.
func (s Status) IsValid() bool {
	return allStatuses[s]
}

// IsValid reports whether p is a recognised priority.
func (p Priority) IsValid() bool {
	return allPriorities[p]
}

// IsDone reports whether s is a terminal ("done") status.
//
// This is the no-workflow discriminator for resolved_at: a space with no
// workflow falls back to this hardcoded state machine, where there is no
// workflow_states.category to consult, so the terminal statuses ARE the done
// set. Resolved and closed are exactly the two states seeded with category
// 'done' in the default ticket workflow (migration 016), which
// TestTicketStateMachine_MatchesTheSeededWorkflow keeps the two in step. The
// workflow-governed path never calls this — there the category is authoritative
// and the write derives done-ness in SQL from the target state.
func (s Status) IsDone() bool {
	return s == StatusResolved || s == StatusClosed
}

// CanTransitionTo reports whether the state machine allows moving from the
// current status to next.
func (s Status) CanTransitionTo(next Status) bool {
	targets, ok := validTransitions[s]
	if !ok {
		return false
	}
	for _, t := range targets {
		if t == next {
			return true
		}
	}
	return false
}

// ValidateTransition checks whether transitioning from current to next is
// allowed. Returns ErrInvalidTransition with context if not.
func ValidateTransition(current, next Status) error {
	if !current.IsValid() {
		return fmt.Errorf("current status %q: %w", current, ErrInvalidStatus)
	}
	if !next.IsValid() {
		return fmt.Errorf("target status %q: %w", next, ErrInvalidStatus)
	}
	if !current.CanTransitionTo(next) {
		return fmt.Errorf("cannot transition from %q to %q: %w", current, next, ErrInvalidTransition)
	}
	return nil
}

// TransitionStatus validates and applies a status transition on the given
// ticket in spaceID, via the repository. Returns the updated ticket or an
// error if the transition is invalid.
//
// spaceID is the same reconciliation Assign makes: the status route authorises
// against the {spaceID} in its URL, and {ticketID} arrives unchecked. The
// route does read the ticket first — but a read that happens to precede the
// write is not the same as a write that cannot address another space, and this
// is the write. Its only caller is that route, so the parameter is threaded
// through rather than added as a second method.
func (s *TicketService) TransitionStatus(ctx context.Context, id, spaceID uuid.UUID, newStatus Status) (*Ticket, error) {
	t, err := s.repo.GetByIDInSpace(ctx, spaceID, id)
	if err != nil {
		return nil, fmt.Errorf("transitioning ticket status: %w", err)
	}

	if err := ValidateTransition(t.Status, newStatus); err != nil {
		return nil, err
	}

	updated, err := s.repo.UpdateStatus(ctx, id, newStatus)
	if err != nil {
		return nil, fmt.Errorf("transitioning ticket status: %w", err)
	}
	return updated, nil
}
