package tickets

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// AssignmentNotifier is called when a ticket assignment changes.
// Implementations should deliver notifications (e.g. via the job queue).
type AssignmentNotifier interface {
	// NotifyAssignment sends a notification about a ticket assignment.
	NotifyAssignment(ctx context.Context, ticketID uuid.UUID, assigneeID uuid.UUID, title string) error
}

// Assign sets or changes the assignee on a ticket. It validates that the new
// assignee differs from the current one and sends a notification on success.
func (s *TicketService) Assign(ctx context.Context, ticketID uuid.UUID, assigneeID uuid.UUID, notifier AssignmentNotifier) (*Ticket, error) {
	t, err := s.repo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("assigning ticket: %w", err)
	}

	if t.AssigneeID != nil && *t.AssigneeID == assigneeID {
		return nil, ErrAlreadyAssigned
	}

	t.AssigneeID = &assigneeID
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, fmt.Errorf("assigning ticket: %w", err)
	}

	if notifier != nil {
		if err := notifier.NotifyAssignment(ctx, ticketID, assigneeID, t.Title); err != nil {
			// Log but don't fail the assignment if notification fails.
			fmt.Printf("warning: failed to notify assignee: %v\n", err)
		}
	}

	return t, nil
}

// Unassign removes the assignee from a ticket.
func (s *TicketService) Unassign(ctx context.Context, ticketID uuid.UUID) (*Ticket, error) {
	t, err := s.repo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("unassigning ticket: %w", err)
	}

	t.AssigneeID = nil
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, fmt.Errorf("unassigning ticket: %w", err)
	}

	return t, nil
}
