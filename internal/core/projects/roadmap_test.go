package projects

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRoadmapService_GetItemsDueInRange(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	roadmapSvc := NewRoadmapService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	now := time.Now().UTC()

	// Item due tomorrow (in range).
	tomorrow := now.Add(24 * time.Hour)
	inRange := makeItem(spaceID)
	inRange.DueAt = &tomorrow
	if _, err := itemSvc.CreateItem(context.Background(), inRange); err != nil {
		t.Fatal(err)
	}

	// Item due next month (out of range).
	nextMonth := now.Add(30 * 24 * time.Hour)
	outOfRange := makeItem(spaceID)
	outOfRange.DueAt = &nextMonth
	if _, err := itemSvc.CreateItem(context.Background(), outOfRange); err != nil {
		t.Fatal(err)
	}

	// Item with no due date (should be excluded).
	if _, err := itemSvc.CreateItem(context.Background(), makeItem(spaceID)); err != nil {
		t.Fatal(err)
	}

	from := now
	to := now.Add(7 * 24 * time.Hour)

	items, err := roadmapSvc.GetItemsDueInRange(context.Background(), spaceID, from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item in range, got %d", len(items))
	}
}

func TestRoadmapService_GetItemsDueInRange_OverdueFlag(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	roadmapSvc := NewRoadmapService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	yesterday := time.Now().UTC().Add(-24 * time.Hour)

	overdueItem := makeItem(spaceID)
	overdueItem.DueAt = &yesterday
	if _, err := itemSvc.CreateItem(context.Background(), overdueItem); err != nil {
		t.Fatal(err)
	}

	from := time.Now().UTC().Add(-48 * time.Hour)
	to := time.Now().UTC()

	items, err := roadmapSvc.GetItemsDueInRange(context.Background(), spaceID, from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !items[0].Overdue {
		t.Error("item should be marked as overdue")
	}
}

func TestRoadmapService_GetOverdueItems(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	roadmapSvc := NewRoadmapService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	tomorrow := time.Now().UTC().Add(24 * time.Hour)

	// Overdue item.
	overdueItem := makeItem(spaceID)
	overdueItem.DueAt = &yesterday
	if _, err := itemSvc.CreateItem(context.Background(), overdueItem); err != nil {
		t.Fatal(err)
	}

	// Not overdue.
	futureItem := makeItem(spaceID)
	futureItem.DueAt = &tomorrow
	if _, err := itemSvc.CreateItem(context.Background(), futureItem); err != nil {
		t.Fatal(err)
	}

	// No due date.
	if _, err := itemSvc.CreateItem(context.Background(), makeItem(spaceID)); err != nil {
		t.Fatal(err)
	}

	overdue, err := roadmapSvc.GetOverdueItems(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(overdue) != 1 {
		t.Errorf("expected 1 overdue item, got %d", len(overdue))
	}
}

func TestRoadmapService_GetOverdueItems_ResolvedNotOverdue(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	roadmapSvc := NewRoadmapService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	yesterday := time.Now().UTC().Add(-24 * time.Hour)

	item := makeItem(spaceID)
	item.DueAt = &yesterday
	created, _ := itemSvc.CreateItem(context.Background(), item)
	if _, err := itemSvc.UpdateItemStatus(context.Background(), created.ID, "resolved"); err != nil {
		t.Fatal(err)
	}

	overdue, err := roadmapSvc.GetOverdueItems(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(overdue) != 0 {
		t.Errorf("expected 0 overdue items (resolved), got %d", len(overdue))
	}
}

func TestRoadmapService_GetSprintRoadmap(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	sprintSvc := NewSprintService(sprintRepo)
	itemSvc := NewItemService(itemRepo)
	roadmapSvc := NewRoadmapService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	now := time.Now().UTC()
	later := now.Add(14 * 24 * time.Hour)

	sprint := makeSprint(spaceID)
	sprint.StartsAt = &now
	sprint.EndsAt = &later
	created, _ := sprintSvc.CreateSprint(context.Background(), sprint)

	item, _ := itemSvc.CreateItem(context.Background(), makeItem(spaceID))
	if err := itemSvc.AssignToSprint(context.Background(), item.ID, &created.ID); err != nil {
		t.Fatal(err)
	}

	roadmap, err := roadmapSvc.GetSprintRoadmap(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roadmap) != 1 {
		t.Fatalf("expected 1 sprint in roadmap, got %d", len(roadmap))
	}
	if len(roadmap[0].Items) != 1 {
		t.Errorf("expected 1 item in sprint, got %d", len(roadmap[0].Items))
	}
}

func TestRoadmapService_GetSprintRoadmap_SkipsUndatedSprints(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	sprintSvc := NewSprintService(sprintRepo)
	roadmapSvc := NewRoadmapService(itemRepo, sprintRepo)

	spaceID := uuid.New()

	// Sprint without dates should be excluded.
	if _, err := sprintSvc.CreateSprint(context.Background(), makeSprint(spaceID)); err != nil {
		t.Fatal(err)
	}

	roadmap, err := roadmapSvc.GetSprintRoadmap(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roadmap) != 0 {
		t.Errorf("expected 0 sprints in roadmap (no dates), got %d", len(roadmap))
	}
}

func TestRoadmapService_GetItemsWithoutDueDate(t *testing.T) {
	itemRepo := newStubItemRepo()
	sprintRepo := newStubSprintRepo()
	itemSvc := NewItemService(itemRepo)
	roadmapSvc := NewRoadmapService(itemRepo, sprintRepo)

	spaceID := uuid.New()
	tomorrow := time.Now().UTC().Add(24 * time.Hour)

	// Item without due date.
	if _, err := itemSvc.CreateItem(context.Background(), makeItem(spaceID)); err != nil {
		t.Fatal(err)
	}

	// Item with due date.
	dated := makeItem(spaceID)
	dated.DueAt = &tomorrow
	if _, err := itemSvc.CreateItem(context.Background(), dated); err != nil {
		t.Fatal(err)
	}

	unscheduled, err := roadmapSvc.GetItemsWithoutDueDate(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unscheduled) != 1 {
		t.Errorf("expected 1 unscheduled item, got %d", len(unscheduled))
	}
}
