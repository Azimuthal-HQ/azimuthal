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
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// ItemRepository defines the data access contract for project items.
type ItemRepository interface {
	// Create persists a new item.
	Create(ctx context.Context, item *Item) error
	// GetByID retrieves an item by primary key. Returns ErrNotFound if absent or soft-deleted.
	GetByID(ctx context.Context, id uuid.UUID) (*Item, error)
	// GetByOrgKey resolves a human-readable key (e.g. VEC-123) to an item
	// within an org. Returns ErrNotFound if absent or soft-deleted.
	GetByOrgKey(ctx context.Context, orgID uuid.UUID, key string) (*Item, error)
	// Update persists changes to an existing item.
	Update(ctx context.Context, item *Item) error
	// UpdateStatus changes only the status field.
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (*Item, error)
	// UpdateSprint assigns an item to a sprint (or removes it if sprintID is nil).
	UpdateSprint(ctx context.Context, id uuid.UUID, sprintID *uuid.UUID) error
	// SoftDelete sets deleted_at on an item.
	SoftDelete(ctx context.Context, id uuid.UUID) error
	// ListBySpace returns all non-deleted items in a space, ordered by rank.
	ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*Item, error)
	// ListByStatus returns items filtered by status within a space.
	ListByStatus(ctx context.Context, spaceID uuid.UUID, status string) ([]*Item, error)
	// ListByAssignee returns items assigned to a specific user within a space.
	ListByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*Item, error)
	// ListBySprint returns all items in a given sprint, ordered by rank.
	ListBySprint(ctx context.Context, sprintID uuid.UUID) ([]*Item, error)
	// Search performs full-text search on items within a space.
	Search(ctx context.Context, spaceID uuid.UUID, query string, limit int) ([]*Item, error)
}

// ShareRevokingDeleter is the transactional seam for item deletion: the
// soft delete and the revocation of the item's entity shares commit or
// roll back together (ADR-0008 rule 10), with the share.revoked audit rows
// in the same transaction.
type ShareRevokingDeleter interface {
	DeleteItemAndRevokeShares(ctx context.Context, itemID, actorID uuid.UUID) error
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

// CreateItem validates and persists a new project item.
func (s *ItemService) CreateItem(ctx context.Context, item *Item) (*Item, error) {
	if err := validateItem(item); err != nil {
		return nil, fmt.Errorf("creating item: %w", err)
	}

	item.ID = uuid.New()
	item.Status = "open"
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

// GetItem retrieves a project item by ID.
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

// AssignToSprint moves an item into a sprint.
func (s *ItemService) AssignToSprint(ctx context.Context, itemID uuid.UUID, sprintID *uuid.UUID) error {
	if err := s.repo.UpdateSprint(ctx, itemID, sprintID); err != nil {
		return fmt.Errorf("assigning item to sprint: %w", err)
	}
	return nil
}

// DeleteItem soft-deletes a project item and revokes its entity shares in
// the same transaction. actorID attributes the share.revoked audit rows.
func (s *ItemService) DeleteItem(ctx context.Context, id, actorID uuid.UUID) error {
	if err := s.tx.DeleteItemAndRevokeShares(ctx, id, actorID); err != nil {
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

// ListItemsBySprint returns items in a sprint.
func (s *ItemService) ListItemsBySprint(ctx context.Context, sprintID uuid.UUID) ([]*Item, error) {
	items, err := s.repo.ListBySprint(ctx, sprintID)
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
