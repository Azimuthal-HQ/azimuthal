package projects

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestBacklogService_GetBacklog(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	sprintID := uuid.New()

	// Create items: 2 in backlog, 1 in sprint.
	if _, err := itemSvc.CreateItem(context.Background(), makeItem(spaceID)); err != nil {
		t.Fatal(err)
	}
	if _, err := itemSvc.CreateItem(context.Background(), makeItem(spaceID)); err != nil {
		t.Fatal(err)
	}
	sprinted, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))
	if err := itemSvc.AssignToSprint(context.Background(), sprinted.ID, &sprintID); err != nil {
		t.Fatal(err)
	}

	backlog, err := backlogSvc.GetBacklog(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backlog) != 2 {
		t.Errorf("expected 2 backlog items, got %d", len(backlog))
	}
}

func TestBacklogService_GetSprintBacklog(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	sprintID := uuid.New()

	item1, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))
	item2, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))
	if err := itemSvc.AssignToSprint(context.Background(), item1.ID, &sprintID); err != nil {
		t.Fatal(err)
	}
	if err := itemSvc.AssignToSprint(context.Background(), item2.ID, &sprintID); err != nil {
		t.Fatal(err)
	}

	items, err := backlogSvc.GetSprintBacklog(context.Background(), sprintID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 sprint items, got %d", len(items))
	}
}

func TestBacklogService_MoveToSprint(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	sprintSvc := NewSprintService(sprintRepo)
	itemSvc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	sprint, _ := sprintSvc.CreateSprint(context.Background(), makeSprint(spaceID))
	item, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))

	if err := backlogSvc.MoveToSprint(context.Background(), item.ID, sprint.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := itemSvc.GetItem(context.Background(), item.ID)
	if got.SprintID == nil || *got.SprintID != sprint.ID {
		t.Error("item should be in sprint")
	}
}

func TestBacklogService_MoveToSprint_SprintNotFound(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	item, _ := itemSvc.CreateItem(context.Background(), makeItem(uuid.New()))

	err := backlogSvc.MoveToSprint(context.Background(), item.ID, uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestBacklogService_MoveToBacklog(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	sprintSvc := NewSprintService(sprintRepo)
	itemSvc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	sprint, _ := sprintSvc.CreateSprint(context.Background(), makeSprint(spaceID))
	item, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))

	if err := backlogSvc.MoveToSprint(context.Background(), item.ID, sprint.ID); err != nil {
		t.Fatal(err)
	}
	if err := backlogSvc.MoveToBacklog(context.Background(), item.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := itemSvc.GetItem(context.Background(), item.ID)
	if got.SprintID != nil {
		t.Error("item should not be in any sprint")
	}
}

func TestBacklogService_ReorderItem(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	item, _ := itemSvc.CreateItem(context.Background(), makeItem(uuid.New()))

	if err := backlogSvc.ReorderItem(context.Background(), item.ID, "0|zzzzzz:"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := itemSvc.GetItem(context.Background(), item.ID)
	if got.Rank != "0|zzzzzz:" {
		t.Errorf("expected updated rank, got %s", got.Rank)
	}
}

func TestBacklogService_GetBacklogByPriority(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	spaceID := uuid.New()

	urgentItem := makeItem(spaceID)
	urgentItem.Priority = "urgent"
	if _, err := itemSvc.CreateItem(context.Background(), urgentItem); err != nil {
		t.Fatal(err)
	}

	lowItem := makeItem(spaceID)
	lowItem.Priority = "low"
	if _, err := itemSvc.CreateItem(context.Background(), lowItem); err != nil {
		t.Fatal(err)
	}

	items, err := backlogSvc.GetBacklogByPriority(context.Background(), spaceID, "urgent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 urgent item, got %d", len(items))
	}
}

func TestBacklogService_GetBacklogByPriority_InvalidPriority(t *testing.T) {
	backlogSvc := NewBacklogService(newStubItemRepo(), newStubSprintRepo())

	_, err := backlogSvc.GetBacklogByPriority(context.Background(), uuid.New(), "invalid")
	if !errors.Is(err, ErrInvalidPriority) {
		t.Errorf("expected ErrInvalidPriority, got %v", err)
	}
}
