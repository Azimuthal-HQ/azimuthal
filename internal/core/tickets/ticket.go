// Package tickets implements the service desk domain: ticket lifecycle,
// email ingestion/egress, and kanban board queries.
package tickets

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Ticket represents a service desk ticket.
//
// ReporterID and RequesterID are exclusive: exactly one is set, enforced by
// migration 044's tickets_origin_identity. A ticket raised inside the product
// has a reporter (a users row); one raised through the customer portal has a
// requester (an account-less external identity, internal/core/portal). That
// XOR is why there is no separate `origin` field — "this came from the
// portal" is RequesterID != nil, derived from the identity itself, so a
// provenance badge cannot disagree with the data behind it.
type Ticket struct {
	ID          uuid.UUID  `json:"id"`
	SpaceID     uuid.UUID  `json:"space_id"`
	Number      int32      `json:"number"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      Status     `json:"status"`
	Priority    Priority   `json:"priority"`
	ReporterID  *uuid.UUID `json:"reporter_id"`
	RequesterID *uuid.UUID `json:"requester_id"`
	AssigneeID  *uuid.UUID `json:"assignee_id"`
	Labels      []string   `json:"labels"`
	DueAt       *time.Time `json:"due_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	Rank        string     `json:"rank"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TicketRepository defines the data access contract for tickets.
type TicketRepository interface {
	// Create persists a new ticket.
	Create(ctx context.Context, t *Ticket) error
	// GetByID retrieves a ticket by primary key. Returns ErrNotFound if absent.
	//
	// UNSCOPED, deliberately. Its callers are the ones that authorise without a
	// space: the entity-share reader (ADR-0008, where share coverage is the
	// authorisation) and the customer portal, whose requester holds no space
	// membership at all. A space-scoped route wants GetByIDInSpace.
	GetByID(ctx context.Context, id uuid.UUID) (*Ticket, error)
	// GetByIDInSpace retrieves a ticket by primary key, but only when it lives
	// in spaceID. Returns ErrNotFound both when the ticket is absent and when
	// it belongs to another space.
	//
	// A route under /spaces/{spaceID}/tickets/{ticketID} has its caller
	// authorised against {spaceID} and proves nothing whatever about
	// {ticketID}; reconciling the two is what this method is for. The two
	// misses answer identically because a distinguishable "exists but
	// forbidden" discloses the same thing in a different shape.
	GetByIDInSpace(ctx context.Context, spaceID, id uuid.UUID) (*Ticket, error)
	// Update persists changes to an existing ticket.
	Update(ctx context.Context, t *Ticket) error
	// UpdateStatus changes only the ticket status. Returns the updated ticket.
	UpdateStatus(ctx context.Context, id uuid.UUID, status Status) (*Ticket, error)
	// Delete soft-deletes a ticket.
	// DeleteInSpace soft-deletes a ticket in spaceID. There is no unscoped
	// variant: an id alone reaches every ticket in the installation.
	DeleteInSpace(ctx context.Context, id, spaceID uuid.UUID) error
	// ListBySpace returns all tickets in a space.
	ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*Ticket, error)
	// ListByStatus returns tickets in a space filtered by status.
	ListByStatus(ctx context.Context, spaceID uuid.UUID, status Status) ([]*Ticket, error)
	// ListByAssignee returns tickets in a space assigned to a specific user.
	ListByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*Ticket, error)
	// Search performs full-text search within a space.
	Search(ctx context.Context, spaceID uuid.UUID, query string, limit int32) ([]*Ticket, error)
	// UserIsMemberOfSpaceOrg reports whether a user belongs to the organisation
	// that owns spaceID.
	//
	// One bool, so "no such user" and "a user in another organisation" cannot
	// become two answers a caller could tell apart. Membership is resolved
	// through the SPACE rather than read from the actor's token, so the check is
	// about the entity being written rather than about who is asking.
	UserIsMemberOfSpaceOrg(ctx context.Context, spaceID, userID uuid.UUID) (bool, error)
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

// ShareRevokingDeleter is the transactional seam for ticket deletion: the
// soft delete and the revocation of the ticket's entity shares commit or
// roll back together (ADR-0008 rule 10), with the share.revoked audit rows
// in the same transaction.
type ShareRevokingDeleter interface {
	DeleteTicketAndRevokeShares(ctx context.Context, ticketID, spaceID, actorID uuid.UUID) error
}

// TicketService handles service desk ticket lifecycle operations.
type TicketService struct {
	repo TicketRepository
	tx   ShareRevokingDeleter
}

// NewTicketService creates a TicketService backed by the given repository.
// The ShareRevokingDeleter is required — deletion runs through it so the
// share invariant cannot be skipped by a wiring mistake.
func NewTicketService(repo TicketRepository, tx ShareRevokingDeleter) *TicketService {
	return &TicketService{repo: repo, tx: tx}
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
		// The agent create path still REQUIRES a reporter — ErrReporterRequired
		// above is unchanged. Only the read model widened, so that a ticket
		// raised through the portal (which does not come through here) can
		// carry a requester instead. See the Ticket doc comment.
		ReporterID: &params.ReporterID,
		AssigneeID: params.AssigneeID,
		Labels:     params.Labels,
		DueAt:      params.DueAt,
		Rank:       "",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.repo.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("creating ticket: %w", err)
	}
	return t, nil
}

// Get retrieves a ticket by ID, without regard to which space it is in.
//
// The remaining callers are the space-less ones: the entity-share reader
// (api/shares), which satisfies its TicketReader interface with this method,
// and the customer portal. Every route that names a space uses GetInSpace.
func (s *TicketService) Get(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting ticket: %w", err)
	}
	return t, nil
}

// GetInSpace retrieves a ticket by ID, but only when it lives in spaceID.
//
// The space-scoped ticket routes read through here because their middleware
// proved only that the caller may read {spaceID} — a ticket id in the URL is
// caller-supplied and reconciled nowhere else. The error is wrapped exactly as
// Get wraps it, so a ticket in another space is reported in the same words as
// a ticket that does not exist.
func (s *TicketService) GetInSpace(ctx context.Context, spaceID, id uuid.UUID) (*Ticket, error) {
	t, err := s.repo.GetByIDInSpace(ctx, spaceID, id)
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

// Delete soft-deletes a ticket and revokes its entity shares in the same
// transaction. actorID attributes the share.revoked audit rows.
//
// spaceID reaches the transaction rather than stopping at the route. The
// handler above reconciles the entity before calling this, but that refusal
// lived in a handler and the deleter took an id alone — so the guarantee was a
// convention the next caller inherits nothing of. It is now in the statement.
func (s *TicketService) Delete(ctx context.Context, id, spaceID, actorID uuid.UUID) error {
	if err := s.tx.DeleteTicketAndRevokeShares(ctx, id, spaceID, actorID); err != nil {
		return fmt.Errorf("deleting ticket: %w", err)
	}
	return nil
}
