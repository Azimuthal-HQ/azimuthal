package projects

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestBacklogService_RankItemRelative_Before(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	svc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)
	ctx := context.Background()

	spaceID := uuid.New()

	a, _ := svc.CreateItem(ctx, makeItemWithRank(spaceID, "a"))
	b, _ := svc.CreateItem(ctx, makeItemWithRank(spaceID, "b"))
	c, _ := svc.CreateItem(ctx, makeItemWithRank(spaceID, "c"))

	// Move c before b.
	err := backlogSvc.RankItemRelative(ctx, spaceID, c.ID, &b.ID, nil)
	if err != nil {
		t.Fatalf("RankItemRelative before: %v", err)
	}

	// Verify c is now before b.
	updatedC, _ := svc.GetItem(ctx, c.ID)
	updatedB, _ := svc.GetItem(ctx, b.ID)
	if updatedC.Rank >= updatedB.Rank {
		t.Errorf("expected c.Rank < b.Rank after moving before b, got c=%s b=%s", updatedC.Rank, updatedB.Rank)
	}
	_ = a
}

func TestBacklogService_RankItemRelative_After(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	svc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)
	ctx := context.Background()

	spaceID := uuid.New()

	a, _ := svc.CreateItem(ctx, makeItemWithRank(spaceID, "a"))
	b, _ := svc.CreateItem(ctx, makeItemWithRank(spaceID, "b"))
	c, _ := svc.CreateItem(ctx, makeItemWithRank(spaceID, "c"))

	// Move a after b.
	err := backlogSvc.RankItemRelative(ctx, spaceID, a.ID, nil, &b.ID)
	if err != nil {
		t.Fatalf("RankItemRelative after: %v", err)
	}

	updatedA, _ := svc.GetItem(ctx, a.ID)
	updatedB, _ := svc.GetItem(ctx, b.ID)
	if updatedA.Rank <= updatedB.Rank {
		t.Errorf("expected a.Rank > b.Rank after moving after b, got a=%s b=%s", updatedA.Rank, updatedB.Rank)
	}
	_ = c
}

func TestBacklogService_RankItemRelative_EndOfList(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	svc := NewItemService(itemRepo)
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)
	ctx := context.Background()

	spaceID := uuid.New()

	a, _ := svc.CreateItem(ctx, makeItemWithRank(spaceID, "a"))
	_, _ = svc.CreateItem(ctx, makeItemWithRank(spaceID, "b"))
	_, _ = svc.CreateItem(ctx, makeItemWithRank(spaceID, "c"))

	// Move a to end (no before/after).
	err := backlogSvc.RankItemRelative(ctx, spaceID, a.ID, nil, nil)
	if err != nil {
		t.Fatalf("RankItemRelative end of list: %v", err)
	}
}

func TestBacklogService_RankItemRelative_NotFound(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	backlogSvc := NewBacklogService(itemRepo, sprintRepo)
	ctx := context.Background()

	err := backlogSvc.RankItemRelative(ctx, uuid.New(), uuid.New(), nil, nil)
	if err == nil {
		t.Error("expected error for non-existent item")
	}
}

func makeItemWithRank(spaceID uuid.UUID, rank string) *Item {
	item := makeItem(spaceID)
	item.Rank = rank
	return item
}
