// Package tickets implements the service desk domain: ticket lifecycle,
// email ingestion/egress, and kanban board queries.
package tickets

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Ticket represents a service desk ticket. It maps to items with kind='ticket'.
type Ticket struct {
	ID          uuid.UUID
	SpaceID     uuid.UUID
	Title       string
	Description string
	Status      Status
	Priority    Priority
	ReporterID  uuid.UUID
	AssigneeID  *uuid.UUID
	Labels      []string
	DueAt       *time.Time
	ResolvedAt  *time.Time
	Rank        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TicketRepository defines the data access contract for tickets.
type TicketRepository interface {
	// Create persists a new ticket.
	Create(ctx context.Context, t *Ticket) error
	// GetByID retrieves a ticket by primary key. Returns ErrNotFound if absent.
	GetByID(ctx context.Context, id uuid.UUID) (*Ticket, error)
	// Update persists changes to an existing ticket.
	Update(ctx context.Context, t *Ticket) error
	// UpdateStatus changes only the ticket status. Returns the updated ticket.
	UpdateStatus(ctx context.Context, id uuid.UUID, status Status) (*Ticket, error)
	// Delete soft-deletes a ticket.
	Delete(ctx context.Context, id uuid.UUID) error
	// ListBySpace returns all tickets in a space.
	ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*Ticket, error)
	// ListByStatus returns tickets in a space filtered by status.
	ListByStatus(ctx context.Context, spaceID uuid.UUID, status Status) ([]*Ticket, error)
	// ListByAssignee returns tickets in a space assigned to a specific user.
	ListByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*Ticket, error)
	// Search performs full-text search within a space.
	Search(ctx context.Context, spaceID uuid.UUID, query string, limit int32) ([]*Ticket, error)
}

// CreateTicketParams holds the parameters for creating a new ticket.
type CreateTicketParams struct {
	SpaceID     uuid.UUID
	Title       string
	Description string
	Priority    Priority
	ReporterID  uuid.UUID
	AssigneeID  *uuid.UUID
	Labels      []string
	DueAt       *time.Time
}

// TicketService handles service desk ticket lifecycle operations.
type TicketService struct {
	repo TicketRepository
}

// NewTicketService creates a TicketService backed by the given repository.
func NewTicketService(repo TicketRepository) *TicketService {
	return &TicketService{repo: repo}
}

// Create creates a new ticket with the given parameters.
func (s *TicketService) Create(ctx context.Context, params CreateTicketParams) (*Ticket, error) {
	if params.SpaceID == uuid.Nil {
		return nil, ErrSpaceRequired
	}
	if params.Title == "" {
		return nil, ErrTitleRequired
	}
	if params.ReporterID == uuid.Nil {
		return nil, ErrReporterRequired
	}
	if !params.Priority.IsValid() {
		return nil, fmt.Errorf("creating ticket: %w", ErrInvalidPriority)
	}

	now := time.Now().UTC()
	t := &Ticket{
		ID:          uuid.New(),
		SpaceID:     params.SpaceID,
		Title:       params.Title,
		Description: params.Description,
		Status:      StatusOpen,
		Priority:    params.Priority,
		ReporterID:  params.ReporterID,
		AssigneeID:  params.AssigneeID,
		Labels:      params.Labels,
		DueAt:       params.DueAt,
		Rank:        "",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("creating ticket: %w", err)
	}
	return t, nil
}

// Get retrieves a ticket by ID.
func (s *TicketService) Get(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting ticket: %w", err)
	}
	return t, nil
}

// Update modifies a ticket's mutable fields.
func (s *TicketService) Update(ctx context.Context, t *Ticket) error {
	if t.Title == "" {
		return ErrTitleRequired
	}
	if !t.Priority.IsValid() {
		return fmt.Errorf("updating ticket: %w", ErrInvalidPriority)
	}
	t.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, t); err != nil {
		return fmt.Errorf("updating ticket: %w", err)
	}
	return nil
}

// Delete soft-deletes a ticket.
func (s *TicketService) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting ticket: %w", err)
	}
	return nil
}
