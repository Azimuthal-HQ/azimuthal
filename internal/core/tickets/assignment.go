package tickets

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// AssignmentNotifier is called when a ticket assignment changes.
// Implementations should deliver notifications (e.g. via the job queue).
type AssignmentNotifier interface {
	// NotifyAssignment sends a notification about a ticket assignment. spaceID
	// is carried through so the recipient's bell can build a route to the
	// ticket.
	NotifyAssignment(ctx context.Context, ticketID uuid.UUID, spaceID uuid.UUID, assigneeID uuid.UUID, title string) error
}

// Assign sets or changes the assignee on a ticket in spaceID. It validates
// that the new assignee differs from the current one and sends a notification
// on success.
//
// spaceID is here because the assign route checks CapEditAnyItem against the
// {spaceID} in its URL and never reconciled it with {ticketID}: read the
// ticket unscoped and an editor in one space could reassign a ticket in any
// other. Unlike the read routes this one takes no prior look at the ticket, so
// the scoped read here is the only thing standing between the two ids. Every
// caller is the space-scoped HTTP route, so the parameter is threaded through
// rather than added as a second method.
func (s *TicketService) Assign(ctx context.Context, ticketID, spaceID uuid.UUID, assigneeID uuid.UUID, notifier AssignmentNotifier) (*Ticket, error) {
	t, err := s.repo.GetByIDInSpace(ctx, spaceID, ticketID)
	if err != nil {
		return nil, fmt.Errorf("assigning ticket: %w", err)
	}

	// The assignee is a THIRD id, and until now nothing looked at it at all.
	// assignee_id references the global users table, so any user in the
	// installation satisfied the foreign key: the write landed 200, the ticket
	// named somebody with no membership in the org and no access to the space,
	// and the notification below carried the ticket's title to them.
	//
	// The check runs before ErrAlreadyAssigned so that a foreign assignee is
	// refused as a foreign assignee, rather than being told the ticket is
	// already theirs when it happens to be.
	member, err := s.repo.UserIsMemberOfSpaceOrg(ctx, spaceID, assigneeID)
	if err != nil {
		return nil, fmt.Errorf("assigning ticket: %w", err)
	}
	if !member {
		return nil, ErrAssigneeNotOrgMember
	}

	if t.AssigneeID != nil && *t.AssigneeID == assigneeID {
		return nil, ErrAlreadyAssigned
	}

	t.AssigneeID = &assigneeID
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, fmt.Errorf("assigning ticket: %w", err)
	}

	if notifier != nil {
		if err := notifier.NotifyAssignment(ctx, ticketID, t.SpaceID, assigneeID, t.Title); err != nil {
			// Log but don't fail the assignment if notification fails.
			fmt.Printf("warning: failed to notify assignee: %v\n", err)
		}
	}

	return t, nil
}

// Unassign removes the assignee from a ticket in spaceID. spaceID carries the
// same weight it does in Assign — the route proved {spaceID} readable and
// {ticketID} nothing, and it reaches this write without reading first.
func (s *TicketService) Unassign(ctx context.Context, ticketID, spaceID uuid.UUID) (*Ticket, error) {
	t, err := s.repo.GetByIDInSpace(ctx, spaceID, ticketID)
	if err != nil {
		return nil, fmt.Errorf("unassigning ticket: %w", err)
	}

	t.AssigneeID = nil
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, fmt.Errorf("unassigning ticket: %w", err)
	}

	return t, nil
}
