package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// TicketAdapter implements tickets.TicketRepository using the tickets table.
type TicketAdapter struct {
	q *generated.Queries
}

// NewTicketAdapter creates a TicketAdapter backed by the given queries.
func NewTicketAdapter(q *generated.Queries) *TicketAdapter {
	return &TicketAdapter{q: q}
}

// Create persists a new ticket, auto-assigning the next available number.
func (a *TicketAdapter) Create(ctx context.Context, t *tickets.Ticket) error {
	maxNum, err := a.q.GetTicketMaxNumber(ctx, t.SpaceID)
	if err != nil {
		return fmt.Errorf("ticket adapter get max number: %w", err)
	}
	number := int32(maxNum) + 1 //nolint:gosec // G115 — ticket numbers are sequential and will never approach int32 max

	row, err := a.q.CreateTicket(ctx, generated.CreateTicketParams{
		ID:          t.ID,
		SpaceID:     t.SpaceID,
		Number:      number,
		Title:       t.Title,
		Description: t.Description,
		Status:      string(t.Status),
		Priority:    string(t.Priority),
		ReporterID:  t.ReporterID,
		AssigneeID:  pgUUID(t.AssigneeID),
		Labels:      coalesceLabels(t.Labels),
		DueAt:       pgTimestampPtr(t.DueAt),
		Rank:        t.Rank,
	})
	if err != nil {
		return fmt.Errorf("ticket adapter create: %w", err)
	}
	t.Number = row.Number
	return nil
}

// GetByID retrieves a ticket by primary key. Returns an error if absent.
func (a *TicketAdapter) GetByID(ctx context.Context, id uuid.UUID) (*tickets.Ticket, error) {
	row, err := a.q.GetTicketByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("ticket adapter get by id: %w", err)
	}
	return dbTicketToTicket(row), nil
}

// Update persists changes to an existing ticket.
func (a *TicketAdapter) Update(ctx context.Context, t *tickets.Ticket) error {
	_, err := a.q.UpdateTicket(ctx, generated.UpdateTicketParams{
		ID:          t.ID,
		Title:       t.Title,
		Description: t.Description,
		Status:      string(t.Status),
		Priority:    string(t.Priority),
		AssigneeID:  pgUUID(t.AssigneeID),
		Labels:      coalesceLabels(t.Labels),
		DueAt:       pgTimestampPtr(t.DueAt),
		Rank:        t.Rank,
	})
	if err != nil {
		return fmt.Errorf("ticket adapter update: %w", err)
	}
	return nil
}

// UpdateStatus changes only the ticket status. Returns the updated ticket.
func (a *TicketAdapter) UpdateStatus(ctx context.Context, id uuid.UUID, status tickets.Status) (*tickets.Ticket, error) {
	row, err := a.q.UpdateTicketStatus(ctx, generated.UpdateTicketStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("ticket adapter update status: %w", err)
	}
	return dbTicketToTicket(row), nil
}

// Delete soft-deletes a ticket.
func (a *TicketAdapter) Delete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.SoftDeleteTicket(ctx, id); err != nil {
		return fmt.Errorf("ticket adapter delete: %w", err)
	}
	return nil
}

// ListBySpace returns all tickets in a space.
func (a *TicketAdapter) ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*tickets.Ticket, error) {
	rows, err := a.q.ListTicketsBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("ticket adapter list by space: %w", err)
	}
	return dbTicketsToTickets(rows), nil
}

// ListByStatus returns tickets filtered by status.
func (a *TicketAdapter) ListByStatus(ctx context.Context, spaceID uuid.UUID, status tickets.Status) ([]*tickets.Ticket, error) {
	rows, err := a.q.ListTicketsByStatus(ctx, generated.ListTicketsByStatusParams{
		SpaceID: spaceID,
		Status:  string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("ticket adapter list by status: %w", err)
	}
	return dbTicketsToTickets(rows), nil
}

// ListByAssignee returns tickets assigned to a specific user.
func (a *TicketAdapter) ListByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*tickets.Ticket, error) {
	rows, err := a.q.ListTicketsByAssignee(ctx, generated.ListTicketsByAssigneeParams{
		SpaceID:    spaceID,
		AssigneeID: pgUUID(&assigneeID),
	})
	if err != nil {
		return nil, fmt.Errorf("ticket adapter list by assignee: %w", err)
	}
	return dbTicketsToTickets(rows), nil
}

// Search performs full-text search within a space.
func (a *TicketAdapter) Search(ctx context.Context, spaceID uuid.UUID, query string, limit int32) ([]*tickets.Ticket, error) {
	rows, err := a.q.SearchTickets(ctx, generated.SearchTicketsParams{
		SpaceID:        spaceID,
		PlaintoTsquery: query,
		Limit:          limit,
	})
	if err != nil {
		return nil, fmt.Errorf("ticket adapter search: %w", err)
	}
	return dbTicketsToTickets(rows), nil
}

func dbTicketToTicket(t generated.Ticket) *tickets.Ticket {
	return &tickets.Ticket{
		ID:          t.ID,
		SpaceID:     t.SpaceID,
		Number:      t.Number,
		Title:       t.Title,
		Description: t.Description,
		Status:      tickets.Status(t.Status),
		Priority:    tickets.Priority(t.Priority),
		ReporterID:  t.ReporterID,
		AssigneeID:  goUUIDPtr(t.AssigneeID),
		Labels:      t.Labels,
		DueAt:       goTimePtr(t.DueAt),
		ResolvedAt:  goTimePtr(t.ResolvedAt),
		Rank:        t.Rank,
		CreatedAt:   goTime(t.CreatedAt),
		UpdatedAt:   goTime(t.UpdatedAt),
	}
}

func dbTicketsToTickets(rows []generated.Ticket) []*tickets.Ticket {
	result := make([]*tickets.Ticket, 0, len(rows))
	for _, r := range rows {
		result = append(result, dbTicketToTicket(r))
	}
	return result
}

// coalesceLabels ensures nil slices become empty slices for DB compatibility.
func coalesceLabels(labels []string) []string {
	if labels == nil {
		return []string{}
	}
	return labels
}
