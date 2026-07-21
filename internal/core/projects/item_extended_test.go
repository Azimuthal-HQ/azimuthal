package projects

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestItemService_ListItemsByAssignee(t *testing.T) {
	repo := newStubItemRepo()
	svc := NewItemService(repo, noopShareDeleter{})
	ctx := context.Background()

	spaceID := uuid.New()
	assigneeID := uuid.New()

	item1 := makeItem(spaceID)
	item1.AssigneeID = &assigneeID
	created1, err := svc.CreateItem(ctx, item1)
	if err != nil {
		t.Fatal(err)
	}

	// Unassigned item in same space.
	_, err = svc.CreateItem(ctx, makeItem(spaceID))
	if err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListItemsByAssignee(ctx, spaceID, assigneeID)
	if err != nil {
		t.Fatalf("ListItemsByAssignee: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item, got %d", len(items))
	}
	if items[0].ID != created1.ID {
		t.Errorf("wrong item returned")
	}
}

func TestItemService_ListItemsBySprint(t *testing.T) {
	repo := newStubItemRepo()
	svc := NewItemService(repo, noopShareDeleter{})
	ctx := context.Background()

	spaceID := uuid.New()
	sprintID := uuid.New()

	item, err := svc.CreateItem(ctx, makeItem(spaceID))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AssignToSprint(ctx, item.ID, &sprintID); err != nil {
		t.Fatal(err)
	}

	// Item not in any sprint.
	_, err = svc.CreateItem(ctx, makeItem(spaceID))
	if err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListItemsBySprint(ctx, sprintID)
	if err != nil {
		t.Fatalf("ListItemsBySprint: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item in sprint, got %d", len(items))
	}
	if items[0].ID != item.ID {
		t.Errorf("wrong item returned")
	}
}
