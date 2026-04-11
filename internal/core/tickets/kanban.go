package tickets

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// KanbanColumn represents a single column on a kanban board, holding tickets
// grouped by their status.
type KanbanColumn struct {
	Status  Status    `json:"status"`
	Tickets []*Ticket `json:"tickets"`
}

// KanbanBoard returns the full kanban board for a space, with tickets grouped
// into columns by status.
func (s *TicketService) KanbanBoard(ctx context.Context, spaceID uuid.UUID) ([]KanbanColumn, error) {
	statuses := []Status{StatusOpen, StatusInProgress, StatusResolved, StatusClosed}
	columns := make([]KanbanColumn, 0, len(statuses))

	for _, status := range statuses {
		tickets, err := s.repo.ListByStatus(ctx, spaceID, status)
		if err != nil {
			return nil, fmt.Errorf("building kanban board: %w", err)
		}
		columns = append(columns, KanbanColumn{
			Status:  status,
			Tickets: tickets,
		})
	}

	return columns, nil
}

// ListByAssignee returns all tickets in a space assigned to a specific user.
func (s *TicketService) ListByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*Ticket, error) {
	tickets, err := s.repo.ListByAssignee(ctx, spaceID, assigneeID)
	if err != nil {
		return nil, fmt.Errorf("listing tickets by assignee: %w", err)
	}
	return tickets, nil
}

// ListBySpace returns all tickets in a space.
func (s *TicketService) ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*Ticket, error) {
	tickets, err := s.repo.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("listing tickets by space: %w", err)
	}
	return tickets, nil
}
