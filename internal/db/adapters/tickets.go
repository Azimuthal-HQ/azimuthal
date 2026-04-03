package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// TicketAdapter implements tickets.TicketRepository using sqlc-generated item
// queries. Tickets are stored in the items table with kind='ticket'.
type TicketAdapter struct {
	q *generated.Queries
}

// NewTicketAdapter creates a TicketAdapter backed by the given queries.
func NewTicketAdapter(q *generated.Queries) *TicketAdapter {
	return &TicketAdapter{q: q}
}

// Create persists a new ticket as an item with kind='ticket'.
func (a *TicketAdapter) Create(ctx context.Context, t *tickets.Ticket) error {
	_, err := a.q.CreateItem(ctx, generated.CreateItemParams{
		ID:          t.ID,
		SpaceID:     t.SpaceID,
		Kind:        "ticket",
		Title:       t.Title,
		Description: strPtr(t.Description),
		Status:      string(t.Status),
		Priority:    string(t.Priority),
		ReporterID:  t.ReporterID,
		AssigneeID:  pgUUID(t.AssigneeID),
		Labels:      t.Labels,
		DueAt:       pgTimestampPtr(t.DueAt),
		Rank:        t.Rank,
	})
	if err != nil {
		return fmt.Errorf("ticket adapter create: %w", err)
	}
	return nil
}

// GetByID retrieves a ticket by primary key. Returns an error if absent.
func (a *TicketAdapter) GetByID(ctx context.Context, id uuid.UUID) (*tickets.Ticket, error) {
	row, err := a.q.GetItemByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("ticket adapter get by id: %w", err)
	}
	return dbItemToTicket(row), nil
}

// Update persists changes to an existing ticket.
func (a *TicketAdapter) Update(ctx context.Context, t *tickets.Ticket) error {
	_, err := a.q.UpdateItem(ctx, generated.UpdateItemParams{
		ID:          t.ID,
		Title:       t.Title,
		Description: strPtr(t.Description),
		Status:      string(t.Status),
		Priority:    string(t.Priority),
		AssigneeID:  pgUUID(t.AssigneeID),
		Labels:      t.Labels,
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
	row, err := a.q.UpdateItemStatus(ctx, generated.UpdateItemStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("ticket adapter update status: %w", err)
	}
	return dbItemToTicket(row), nil
}

// Delete soft-deletes a ticket.
func (a *TicketAdapter) Delete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.SoftDeleteItem(ctx, id); err != nil {
		return fmt.Errorf("ticket adapter delete: %w", err)
	}
	return nil
}

// ListBySpace returns all tickets in a space.
func (a *TicketAdapter) ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*tickets.Ticket, error) {
	rows, err := a.q.ListItemsBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("ticket adapter list by space: %w", err)
	}
	return filterTickets(rows), nil
}

// ListByStatus returns tickets in a space filtered by status.
func (a *TicketAdapter) ListByStatus(ctx context.Context, spaceID uuid.UUID, status tickets.Status) ([]*tickets.Ticket, error) {
	rows, err := a.q.ListItemsByStatus(ctx, generated.ListItemsByStatusParams{
		SpaceID: spaceID,
		Status:  string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("ticket adapter list by status: %w", err)
	}
	return filterTickets(rows), nil
}

// ListByAssignee returns tickets in a space assigned to a specific user.
func (a *TicketAdapter) ListByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*tickets.Ticket, error) {
	rows, err := a.q.ListItemsByAssignee(ctx, generated.ListItemsByAssigneeParams{
		SpaceID:    spaceID,
		AssigneeID: pgUUID(&assigneeID),
	})
	if err != nil {
		return nil, fmt.Errorf("ticket adapter list by assignee: %w", err)
	}
	return filterTickets(rows), nil
}

// Search performs full-text search within a space.
func (a *TicketAdapter) Search(ctx context.Context, spaceID uuid.UUID, query string, limit int32) ([]*tickets.Ticket, error) {
	rows, err := a.q.SearchItems(ctx, generated.SearchItemsParams{
		SpaceID:        spaceID,
		PlaintoTsquery: query,
		Limit:          limit,
	})
	if err != nil {
		return nil, fmt.Errorf("ticket adapter search: %w", err)
	}
	return filterTickets(rows), nil
}

// filterTickets converts generated items and keeps only those with kind='ticket'.
func filterTickets(items []generated.Item) []*tickets.Ticket {
	result := make([]*tickets.Ticket, 0, len(items))
	for _, item := range items {
		if item.Kind == "ticket" {
			result = append(result, dbItemToTicket(item))
		}
	}
	return result
}

// dbItemToTicket converts a generated.Item to a tickets.Ticket.
func dbItemToTicket(i generated.Item) *tickets.Ticket {
	return &tickets.Ticket{
		ID:          i.ID,
		SpaceID:     i.SpaceID,
		Title:       i.Title,
		Description: derefStr(i.Description),
		Status:      tickets.Status(i.Status),
		Priority:    tickets.Priority(i.Priority),
		ReporterID:  i.ReporterID,
		AssigneeID:  goUUIDPtr(i.AssigneeID),
		Labels:      i.Labels,
		DueAt:       goTimePtr(i.DueAt),
		ResolvedAt:  goTimePtr(i.ResolvedAt),
		Rank:        i.Rank,
		CreatedAt:   goTime(i.CreatedAt),
		UpdatedAt:   goTime(i.UpdatedAt),
	}
}
