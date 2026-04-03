package projects

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// stubItemRepo is an in-memory ItemRepository for testing.
type stubItemRepo struct {
	items map[uuid.UUID]*Item
}

func newStubItemRepo() *stubItemRepo {
	return &stubItemRepo{items: make(map[uuid.UUID]*Item)}
}

func (r *stubItemRepo) Create(_ context.Context, item *Item) error {
	r.items[item.ID] = item
	return nil
}

func (r *stubItemRepo) GetByID(_ context.Context, id uuid.UUID) (*Item, error) {
	item, ok := r.items[id]
	if !ok || item.DeletedAt != nil {
		return nil, ErrNotFound
	}
	return item, nil
}

func (r *stubItemRepo) Update(_ context.Context, item *Item) error {
	if _, ok := r.items[item.ID]; !ok {
		return ErrNotFound
	}
	r.items[item.ID] = item
	return nil
}

func (r *stubItemRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string) (*Item, error) {
	item, ok := r.items[id]
	if !ok || item.DeletedAt != nil {
		return nil, ErrNotFound
	}
	item.Status = status
	return item, nil
}

func (r *stubItemRepo) UpdateSprint(_ context.Context, id uuid.UUID, sprintID *uuid.UUID) error {
	item, ok := r.items[id]
	if !ok || item.DeletedAt != nil {
		return ErrNotFound
	}
	item.SprintID = sprintID
	return nil
}

func (r *stubItemRepo) SoftDelete(_ context.Context, id uuid.UUID) error {
	item, ok := r.items[id]
	if !ok {
		return ErrNotFound
	}
	now := timeNowUTC()
	item.DeletedAt = &now
	return nil
}

func (r *stubItemRepo) ListBySpace(_ context.Context, spaceID uuid.UUID) ([]*Item, error) {
	result := make([]*Item, 0)
	for _, item := range r.items {
		if item.SpaceID == spaceID && item.DeletedAt == nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *stubItemRepo) ListByStatus(_ context.Context, spaceID uuid.UUID, status string) ([]*Item, error) {
	result := make([]*Item, 0)
	for _, item := range r.items {
		if item.SpaceID == spaceID && item.Status == status && item.DeletedAt == nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *stubItemRepo) ListByAssignee(_ context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*Item, error) {
	result := make([]*Item, 0)
	for _, item := range r.items {
		if item.SpaceID == spaceID && item.AssigneeID != nil && *item.AssigneeID == assigneeID && item.DeletedAt == nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *stubItemRepo) ListBySprint(_ context.Context, sprintID uuid.UUID) ([]*Item, error) {
	result := make([]*Item, 0)
	for _, item := range r.items {
		if item.SprintID != nil && *item.SprintID == sprintID && item.DeletedAt == nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *stubItemRepo) Search(_ context.Context, _ uuid.UUID, _ string, _ int) ([]*Item, error) {
	// Stub: return all items (full-text search is a DB concern).
	result := make([]*Item, 0)
	for _, item := range r.items {
		if item.DeletedAt == nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func makeItem(spaceID uuid.UUID) *Item {
	return &Item{
		SpaceID:    spaceID,
		Kind:       "task",
		Title:      "Test item",
		Priority:   "medium",
		ReporterID: uuid.New(),
	}
}

func TestItemService_CreateItem(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	spaceID := uuid.New()

	item, err := svc.CreateItem(context.Background(), makeItem(spaceID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.ID == (uuid.UUID{}) {
		t.Error("item must have a non-zero UUID")
	}
	if item.Status != "open" {
		t.Errorf("expected status open, got %s", item.Status)
	}
	if item.CreatedAt.IsZero() {
		t.Error("created_at must be set")
	}
}

func TestItemService_CreateItem_TitleRequired(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	item := makeItem(uuid.New())
	item.Title = ""

	_, err := svc.CreateItem(context.Background(), item)
	if !errors.Is(err, ErrTitleRequired) {
		t.Errorf("expected ErrTitleRequired, got %v", err)
	}
}

func TestItemService_CreateItem_InvalidKind(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	item := makeItem(uuid.New())
	item.Kind = "invalid"

	_, err := svc.CreateItem(context.Background(), item)
	if !errors.Is(err, ErrInvalidKind) {
		t.Errorf("expected ErrInvalidKind, got %v", err)
	}
}

func TestItemService_CreateItem_InvalidPriority(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	item := makeItem(uuid.New())
	item.Priority = "critical"

	_, err := svc.CreateItem(context.Background(), item)
	if !errors.Is(err, ErrInvalidPriority) {
		t.Errorf("expected ErrInvalidPriority, got %v", err)
	}
}

func TestItemService_CreateItem_AllKinds(t *testing.T) {
	for kind := range ValidKinds {
		t.Run(kind, func(t *testing.T) {
			svc := NewItemService(newStubItemRepo())
			item := makeItem(uuid.New())
			item.Kind = kind
			if _, err := svc.CreateItem(context.Background(), item); err != nil {
				t.Fatalf("unexpected error for kind %s: %v", kind, err)
			}
		})
	}
}

func TestItemService_CreateItem_AllPriorities(t *testing.T) {
	for priority := range ValidPriorities {
		t.Run(priority, func(t *testing.T) {
			svc := NewItemService(newStubItemRepo())
			item := makeItem(uuid.New())
			item.Priority = priority
			if _, err := svc.CreateItem(context.Background(), item); err != nil {
				t.Fatalf("unexpected error for priority %s: %v", priority, err)
			}
		})
	}
}

func TestItemService_GetItem(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	created, _ := svc.CreateItem(context.Background(), makeItem(uuid.New()))

	got, err := svc.GetItem(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Title != "Test item" {
		t.Errorf("wrong title: %s", got.Title)
	}
}

func TestItemService_GetItem_NotFound(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	_, err := svc.GetItem(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestItemService_UpdateItem(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	created, _ := svc.CreateItem(context.Background(), makeItem(uuid.New()))

	created.Title = "Updated title"
	updated, err := svc.UpdateItem(context.Background(), created)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Title != "Updated title" {
		t.Errorf("expected updated title, got %s", updated.Title)
	}
}

func TestItemService_UpdateItemStatus(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	created, _ := svc.CreateItem(context.Background(), makeItem(uuid.New()))

	updated, err := svc.UpdateItemStatus(context.Background(), created.ID, "in_progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Status != "in_progress" {
		t.Errorf("expected in_progress, got %s", updated.Status)
	}
}

func TestItemService_DeleteItem(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	created, _ := svc.CreateItem(context.Background(), makeItem(uuid.New()))

	if err := svc.DeleteItem(context.Background(), created.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err := svc.GetItem(context.Background(), created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestItemService_ListItemsBySpace(t *testing.T) {
	repo := newStubItemRepo()
	svc := NewItemService(repo)
	spaceID := uuid.New()

	for i := 0; i < 3; i++ {
		if _, err := svc.CreateItem(context.Background(), makeItem(spaceID)); err != nil {
			t.Fatal(err)
		}
	}
	// Item in different space should not appear.
	if _, err := svc.CreateItem(context.Background(), makeItem(uuid.New())); err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListItemsBySpace(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}
}

func TestItemService_ListItemsByStatus(t *testing.T) {
	repo := newStubItemRepo()
	svc := NewItemService(repo)
	spaceID := uuid.New()

	created, _ := svc.CreateItem(context.Background(), makeItem(spaceID))
	if _, err := svc.UpdateItemStatus(context.Background(), created.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateItem(context.Background(), makeItem(spaceID)); err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListItemsByStatus(context.Background(), spaceID, "open")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 open item, got %d", len(items))
	}
}

func TestItemService_AssignToSprint(t *testing.T) {
	repo := newStubItemRepo()
	svc := NewItemService(repo)
	spaceID := uuid.New()
	sprintID := uuid.New()

	created, _ := svc.CreateItem(context.Background(), makeItem(spaceID))
	if err := svc.AssignToSprint(context.Background(), created.ID, &sprintID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := svc.GetItem(context.Background(), created.ID)
	if got.SprintID == nil || *got.SprintID != sprintID {
		t.Error("item should be assigned to sprint")
	}
}

func TestItemService_SearchItems(t *testing.T) {
	repo := newStubItemRepo()
	svc := NewItemService(repo)
	spaceID := uuid.New()

	if _, err := svc.CreateItem(context.Background(), makeItem(spaceID)); err != nil {
		t.Fatal(err)
	}

	items, err := svc.SearchItems(context.Background(), spaceID, "test", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) == 0 {
		t.Error("expected at least one search result")
	}
}

func TestItemService_SearchItems_EmptyQuery(t *testing.T) {
	svc := NewItemService(newStubItemRepo())
	_, err := svc.SearchItems(context.Background(), uuid.New(), "", 10)
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestItemService_SearchItems_DefaultLimit(t *testing.T) {
	repo := newStubItemRepo()
	svc := NewItemService(repo)
	spaceID := uuid.New()

	if _, err := svc.CreateItem(context.Background(), makeItem(spaceID)); err != nil {
		t.Fatal(err)
	}

	// Limit <= 0 should default to 50.
	items, err := svc.SearchItems(context.Background(), spaceID, "test", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items == nil {
		t.Error("expected non-nil results")
	}
}
