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
	StatusResolved:   {StatusClosed, StatusOpen},
	StatusClosed:     {StatusOpen},
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

// TransitionTicket validates and applies a status transition on the given ticket
// via the repository. Returns the updated ticket or an error if the transition
// is invalid.
func (s *TicketService) TransitionStatus(ctx context.Context, id uuid.UUID, newStatus Status) (*Ticket, error) {
	t, err := s.repo.GetByID(ctx, id)
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
