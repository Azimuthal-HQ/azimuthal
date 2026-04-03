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

// ValidKinds contains all allowed item kind values.
var ValidKinds = map[string]bool{
	"ticket": true,
	"task":   true,
	"story":  true,
	"epic":   true,
	"bug":    true,
}

// Item represents a project work item (task, story, epic, bug, or ticket).
type Item struct {
	ID          uuid.UUID
	SpaceID     uuid.UUID
	ParentID    *uuid.UUID
	Kind        string
	Title       string
	Description string
	Status      string
	Priority    string
	ReporterID  uuid.UUID
	AssigneeID  *uuid.UUID
	SprintID    *uuid.UUID
	Labels      []string
	DueAt       *time.Time
	ResolvedAt  *time.Time
	Rank        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

// ItemRepository defines the data access contract for project items.
type ItemRepository interface {
	// Create persists a new item.
	Create(ctx context.Context, item *Item) error
	// GetByID retrieves an item by primary key. Returns ErrNotFound if absent or soft-deleted.
	GetByID(ctx context.Context, id uuid.UUID) (*Item, error)
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

// ItemService handles project item management.
type ItemService struct {
	repo ItemRepository
}

// NewItemService creates an ItemService backed by the given repository.
func NewItemService(repo ItemRepository) *ItemService {
	return &ItemService{repo: repo}
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

// DeleteItem soft-deletes a project item.
func (s *ItemService) DeleteItem(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.SoftDelete(ctx, id); err != nil {
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

// validateItem checks that an item has valid required fields.
func validateItem(item *Item) error {
	if item.Title == "" {
		return ErrTitleRequired
	}
	if !ValidKinds[item.Kind] {
		return ErrInvalidKind
	}
	if !ValidPriorities[item.Priority] {
		return ErrInvalidPriority
	}
	return nil
}
