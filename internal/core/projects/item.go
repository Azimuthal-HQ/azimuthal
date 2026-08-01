package projects

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ValidPriorities contains all allowed priority values for items.
var ValidPriorities = map[string]bool{
	"urgent": true,
	"high":   true,
	"medium": true,
	"low":    true,
}

// ValidKinds is the default seeded item-type/kind vocabulary. Item types are
// org-editable at runtime (see the itemtypes package), so this is the seed set
// and the canonical list used by tests, not an exhaustive allow-list.
var ValidKinds = map[string]bool{
	"ticket": true,
	"task":   true,
	"story":  true,
	"epic":   true,
	"bug":    true,
}

// Item represents a project work item (task, story, epic, bug, or ticket).
type Item struct {
	ID      uuid.UUID `json:"id"`
	SpaceID uuid.UUID `json:"space_id"`
	// Number is the per-space monotonic sequence value assigned at creation.
	Number int `json:"number"`
	// ItemKey is the permanent, org-unique human-readable key (<SPACE_KEY>-<n>),
	// assigned at creation and immutable thereafter. Empty until persisted.
	ItemKey     string     `json:"item_key"`
	ParentID    *uuid.UUID `json:"parent_id"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	ReporterID  uuid.UUID  `json:"reporter_id"`
	AssigneeID  *uuid.UUID `json:"assignee_id"`
	SprintID    *uuid.UUID `json:"sprint_id"`
	Labels      []string   `json:"labels"`
	DueAt       *time.Time `json:"due_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	Rank        string     `json:"rank"`
	// WorkflowStateID is where the item sits in its space's workflow. See the
	// ticket twin in internal/core/tickets/ticket.go for why the column is read
	// alongside the status text rather than instead of it.
	WorkflowStateID *uuid.UUID `json:"workflow_state_id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}

// ItemRepository defines the data access contract for project items.
type ItemRepository interface {
	// Create persists a new item.
	Create(ctx context.Context, item *Item) error
	// GetByIDInSpace retrieves an item by primary key, reconciled against the
	// space the request named. Returns ErrNotFound if absent, soft-deleted, OR
	// in a different space — the three are indistinguishable on purpose.
	//
	// This is the read every space-scoped route must use. Those routes prove
	// the caller may read {spaceID} and prove nothing whatever about {itemID},
	// so reading by id alone let an authorised member of any one space reach
	// every item in the installation, across organizations included.
	GetByIDInSpace(ctx context.Context, spaceID, id uuid.UUID) (*Item, error)
	// GetByID retrieves an item by primary key with NO space reconciliation.
	// Returns ErrNotFound if absent or soft-deleted.
	//
	// Only for callers whose authorisation is established some other way — the
	// entity-share read path (ADR-0008), where share coverage deliberately
	// grants access without space access. Everything else wants GetByIDInSpace.
	GetByID(ctx context.Context, id uuid.UUID) (*Item, error)
	// GetByOrgKey resolves a human-readable key (e.g. VEC-123) to an item
	// within an org. Returns ErrNotFound if absent or soft-deleted.
	GetByOrgKey(ctx context.Context, orgID uuid.UUID, key string) (*Item, error)
	// Update persists changes to an existing item.
	Update(ctx context.Context, item *Item) error
	// UpdateStatus changes only the status field.
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (*Item, error)
	// UpdateSprintInSpace assigns an item in spaceID to a sprint in the same
	// space, or removes it from one when sprintID is nil. There is no unscoped
	// variant: this write is reached from three routes, two of which take the
	// item id from the request body.
	UpdateSprintInSpace(ctx context.Context, id, spaceID uuid.UUID, sprintID *uuid.UUID) error
	// SoftDeleteInSpace sets deleted_at on an item in spaceID. There is no
	// unscoped variant: an id alone reaches every item in the installation.
	SoftDeleteInSpace(ctx context.Context, id, spaceID uuid.UUID) error
	// ListBySpace returns all non-deleted items in a space, ordered by rank.
	ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*Item, error)
	// ListByStatus returns items filtered by status within a space.
	ListByStatus(ctx context.Context, spaceID uuid.UUID, status string) ([]*Item, error)
	// ListByAssignee returns items assigned to a specific user within a space.
	ListByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*Item, error)
	// ListBySprint returns all items in a given sprint, ordered by rank, with
	// both the sprint and its items reconciled against the given space. A
	// sprint id on its own used to be enough, which made this a bulk read of
	// another space's sprint.
	ListBySprint(ctx context.Context, spaceID, sprintID uuid.UUID) ([]*Item, error)
	// Search performs full-text search on items within a space.
	Search(ctx context.Context, spaceID uuid.UUID, query string, limit int) ([]*Item, error)
}

// ShareRevokingDeleter is the transactional seam for item deletion: the
// soft delete and the revocation of the item's entity shares commit or
// roll back together (ADR-0008 rule 10), with the share.revoked audit rows
// in the same transaction.
type ShareRevokingDeleter interface {
	DeleteItemAndRevokeShares(ctx context.Context, itemID, spaceID, actorID uuid.UUID) error
}

// ItemService handles project item management.
type ItemService struct {
	repo ItemRepository
	tx   ShareRevokingDeleter
}

// NewItemService creates an ItemService backed by the given repository.
// The ShareRevokingDeleter is required — deletion runs through it so the
// share invariant cannot be skipped by a wiring mistake.
func NewItemService(repo ItemRepository, tx ShareRevokingDeleter) *ItemService {
	return &ItemService{repo: repo, tx: tx}
}

// DefaultStatus is where an item starts when its space has no workflow to
// start it anywhere.
//
// It is the value this service wrote unconditionally, and it stays the value for
// a space with no workflow — a codex space, or one whose best-effort workflow
// assignment failed. Where a workflow DOES govern, the caller resolves that
// workflow's initial state and sets Status and WorkflowStateID before calling;
// see tiergate.Gate.InitialPosition and D72.
const DefaultStatus = "open"

// CreateItem validates and persists a new project item.
//
// Status is honoured when the caller set one and defaults otherwise. It used to
// be overwritten unconditionally, which is what made an item's own workflow
// unable to place it at birth. The caller cannot use this to smuggle a
// user-supplied status: the create handler builds the Item itself and never
// copies status off the request body — CreateItemRequest has no status field.
func (s *ItemService) CreateItem(ctx context.Context, item *Item) (*Item, error) {
	if err := validateItem(item); err != nil {
		return nil, fmt.Errorf("creating item: %w", err)
	}

	item.ID = uuid.New()
	if item.Status == "" {
		item.Status = DefaultStatus
	}
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now

	if item.Rank == "" {
		item.Rank = "0|aaaaaa:"
	}

	if err := s.repo.Create(ctx, item); err != nil {
		return nil, fmt.Errorf("creating item: %w", err)
	}
	return item, nil
}

// GetItemInSpace retrieves a project item by ID, reconciled against the space
// the request named. This is the read every space-scoped route must use; see
// ItemRepository.GetByIDInSpace for why.
func (s *ItemService) GetItemInSpace(ctx context.Context, spaceID, id uuid.UUID) (*Item, error) {
	item, err := s.repo.GetByIDInSpace(ctx, spaceID, id)
	if err != nil {
		return nil, fmt.Errorf("getting item: %w", err)
	}
	return item, nil
}

// GetItem retrieves a project item by ID with NO space reconciliation.
//
// Reserved for callers authorised some other way — the entity-share reader,
// where coverage grants access without space access (ADR-0008). A space-scoped
// route reaching for this is a defect; it wants GetItemInSpace.
func (s *ItemService) GetItem(ctx context.Context, id uuid.UUID) (*Item, error) {
	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting item: %w", err)
	}
	return item, nil
}

// ResolveKey resolves a human-readable item key (e.g. VEC-123) to an item
// within an org. This is the callable service path the future Jira importer
// uses to map external keys onto items — key resolution lives here, not in a
// handler. The key is matched exactly (case-sensitive, as stored).
func (s *ItemService) ResolveKey(ctx context.Context, orgID uuid.UUID, key string) (*Item, error) {
	if key == "" {
		return nil, ErrKeyRequired
	}
	item, err := s.repo.GetByOrgKey(ctx, orgID, key)
	if err != nil {
		return nil, fmt.Errorf("resolving item key: %w", err)
	}
	return item, nil
}

// UpdateItem validates and persists changes to a project item.
func (s *ItemService) UpdateItem(ctx context.Context, item *Item) (*Item, error) {
	if err := validateItem(item); err != nil {
		return nil, fmt.Errorf("updating item: %w", err)
	}

	item.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, fmt.Errorf("updating item: %w", err)
	}
	return item, nil
}

// UpdateItemStatus changes the status of a project item.
func (s *ItemService) UpdateItemStatus(ctx context.Context, id uuid.UUID, status string) (*Item, error) {
	updated, err := s.repo.UpdateStatus(ctx, id, status)
	if err != nil {
		return nil, fmt.Errorf("updating item status: %w", err)
	}
	return updated, nil
}

// AssignToSprint moves an item in spaceID into a sprint in the same space, or
// out of one when sprintID is nil.
//
// spaceID is here because the route checks CapEditAnyItem against the {spaceID}
// in its URL and reconciled it with neither the {itemID} beside it nor the
// sprint id in the body. Like the ticket assign pair, this route writes without
// reading the item first, so the predicate in the query is the only thing
// standing between three ids that have nothing to do with each other.
func (s *ItemService) AssignToSprint(
	ctx context.Context, itemID, spaceID uuid.UUID, sprintID *uuid.UUID,
) error {
	if err := s.repo.UpdateSprintInSpace(ctx, itemID, spaceID, sprintID); err != nil {
		return fmt.Errorf("assigning item to sprint: %w", err)
	}
	return nil
}

// DeleteItem soft-deletes a project item and revokes its entity shares in
// the same transaction. actorID attributes the share.revoked audit rows.
//
// spaceID reaches the transaction rather than stopping at the route. The
// handler above reconciles the entity before calling this, but that refusal
// lived in a handler and the deleter took an id alone — so the guarantee was a
// convention the next caller inherits nothing of. It is now in the statement.
func (s *ItemService) DeleteItem(ctx context.Context, id, spaceID, actorID uuid.UUID) error {
	if err := s.tx.DeleteItemAndRevokeShares(ctx, id, spaceID, actorID); err != nil {
		return fmt.Errorf("deleting item: %w", err)
	}
	return nil
}

// ListItemsBySpace returns all items in a space.
func (s *ItemService) ListItemsBySpace(ctx context.Context, spaceID uuid.UUID) ([]*Item, error) {
	items, err := s.repo.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("listing items by space: %w", err)
	}
	return items, nil
}

// ListItemsByStatus returns items filtered by status.
func (s *ItemService) ListItemsByStatus(ctx context.Context, spaceID uuid.UUID, status string) ([]*Item, error) {
	items, err := s.repo.ListByStatus(ctx, spaceID, status)
	if err != nil {
		return nil, fmt.Errorf("listing items by status: %w", err)
	}
	return items, nil
}

// ListItemsByAssignee returns items assigned to a user.
func (s *ItemService) ListItemsByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*Item, error) {
	items, err := s.repo.ListByAssignee(ctx, spaceID, assigneeID)
	if err != nil {
		return nil, fmt.Errorf("listing items by assignee: %w", err)
	}
	return items, nil
}

// ListItemsBySprint returns items in a sprint, reconciled against the space the
// request named. A sprint id on its own was enough before, which made this a
// bulk read of another space's sprint.
func (s *ItemService) ListItemsBySprint(ctx context.Context, spaceID, sprintID uuid.UUID) ([]*Item, error) {
	items, err := s.repo.ListBySprint(ctx, spaceID, sprintID)
	if err != nil {
		return nil, fmt.Errorf("listing items by sprint: %w", err)
	}
	return items, nil
}

// SearchItems performs full-text search within a space.
func (s *ItemService) SearchItems(ctx context.Context, spaceID uuid.UUID, query string, limit int) ([]*Item, error) {
	if query == "" {
		return nil, fmt.Errorf("searching items: query is required")
	}
	if limit <= 0 {
		limit = 50
	}
	items, err := s.repo.Search(ctx, spaceID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("searching items: %w", err)
	}
	return items, nil
}

// validateItem checks that an item has valid required fields. Note the item
// TYPE (Kind) vocabulary is org-defined (item_types) and validated by the API
// handler, which has the org context; the service only requires a non-empty
// kind so it cannot silently persist a typeless item.
func validateItem(item *Item) error {
	if item.Title == "" {
		return ErrTitleRequired
	}
	if item.Kind == "" {
		return ErrInvalidKind
	}
	if !ValidPriorities[item.Priority] {
		return ErrInvalidPriority
	}
	return nil
}
