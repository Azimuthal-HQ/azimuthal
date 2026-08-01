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
	itemSvc := NewItemService(itemRepo, noopShareDeleter{})
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
	if err := itemSvc.AssignToSprint(context.Background(), sprinted.ID, spaceID, &sprintID); err != nil {
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
	itemSvc := NewItemService(itemRepo, noopShareDeleter{})
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	sprintID := uuid.New()

	item1, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))
	item2, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))
	if err := itemSvc.AssignToSprint(context.Background(), item1.ID, spaceID, &sprintID); err != nil {
		t.Fatal(err)
	}
	if err := itemSvc.AssignToSprint(context.Background(), item2.ID, spaceID, &sprintID); err != nil {
		t.Fatal(err)
	}

	items, err := backlogSvc.GetSprintBacklog(context.Background(), spaceID, sprintID)
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
	itemSvc := NewItemService(itemRepo, noopShareDeleter{})
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	sprint, _ := sprintSvc.CreateSprint(context.Background(), makeSprint(spaceID))
	item, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))

	if err := backlogSvc.MoveToSprint(context.Background(), item.ID, sprint.ID, spaceID); err != nil {
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
	itemSvc := NewItemService(itemRepo, noopShareDeleter{})
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	item, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))

	err := backlogSvc.MoveToSprint(context.Background(), item.ID, uuid.New(), spaceID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// A sprint in another space answers exactly as a sprint that does not exist.
//
// The lookup used to be unscoped, so a real foreign sprint id returned 200 and
// an unreal one returned 404 — an existence oracle over every organisation's
// sprints, readable by anyone holding the write floor on any single space. Both
// halves are asserted here because either alone would pass on a broken build:
// the 404 alone passes if the route refuses everything, and the success case
// alone passes if it refuses nothing.
func TestBacklogService_MoveToSprint_OtherSpaceSprintIsIndistinguishable(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	sprintSvc := NewSprintService(sprintRepo)
	itemSvc := NewItemService(itemRepo, noopShareDeleter{})
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	callerSpace, otherSpace := uuid.New(), uuid.New()
	foreign, _ := sprintSvc.CreateSprint(context.Background(), makeSprint(otherSpace))
	item, _ := itemSvc.CreateItem(context.Background(), makeItem(callerSpace))

	foreignErr := backlogSvc.MoveToSprint(context.Background(), item.ID, foreign.ID, callerSpace)
	if !errors.Is(foreignErr, ErrNotFound) {
		t.Errorf("another space's sprint must be ErrNotFound, got %v", foreignErr)
	}
	absentErr := backlogSvc.MoveToSprint(context.Background(), item.ID, uuid.New(), callerSpace)
	if !errors.Is(absentErr, ErrNotFound) {
		t.Errorf("a nonexistent sprint must be ErrNotFound, got %v", absentErr)
	}

	if got, _ := itemSvc.GetItem(context.Background(), item.ID); got.SprintID != nil {
		t.Error("a refused move must not have written the sprint")
	}

	// The caller's own sprint still moves the item, or a service that refused
	// everything would satisfy both assertions above.
	own, _ := sprintSvc.CreateSprint(context.Background(), makeSprint(callerSpace))
	if err := backlogSvc.MoveToSprint(context.Background(), item.ID, own.ID, callerSpace); err != nil {
		t.Fatalf("the caller's own sprint must still work: %v", err)
	}
	got, _ := itemSvc.GetItem(context.Background(), item.ID)
	if got.SprintID == nil || *got.SprintID != own.ID {
		t.Error("the item should be in the caller's own sprint")
	}
}

func TestBacklogService_MoveToBacklog(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	sprintSvc := NewSprintService(sprintRepo)
	itemSvc := NewItemService(itemRepo, noopShareDeleter{})
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	sprint, _ := sprintSvc.CreateSprint(context.Background(), makeSprint(spaceID))
	item, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))

	if err := backlogSvc.MoveToSprint(context.Background(), item.ID, sprint.ID, spaceID); err != nil {
		t.Fatal(err)
	}

	// Another space cannot knock the item off its sprint. The item id arrives
	// in the request BODY here, so nothing upstream has reconciled it at all.
	if err := backlogSvc.MoveToBacklog(context.Background(), item.ID, uuid.New()); err != nil {
		t.Fatalf("a foreign space must not error: %v", err)
	}
	if still, _ := itemSvc.GetItem(context.Background(), item.ID); still.SprintID == nil {
		t.Error("a foreign space must not have cleared the sprint")
	}

	if err := backlogSvc.MoveToBacklog(context.Background(), item.ID, spaceID); err != nil {
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
	itemSvc := NewItemService(itemRepo, noopShareDeleter{})
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
	itemSvc := NewItemService(itemRepo, noopShareDeleter{})
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
