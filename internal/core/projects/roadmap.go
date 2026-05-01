package projects

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RoadmapService provides date-based roadmap queries over project items and sprints.
type RoadmapService struct {
	itemRepo   ItemRepository
	sprintRepo SprintRepository
}

// NewRoadmapService creates a RoadmapService backed by the given repositories.
func NewRoadmapService(itemRepo ItemRepository, sprintRepo SprintRepository) *RoadmapService {
	return &RoadmapService{itemRepo: itemRepo, sprintRepo: sprintRepo}
}

// RoadmapItem represents an item with its due date for roadmap display.
type RoadmapItem struct {
	Item    *Item     `json:"item"`
	DueAt   time.Time `json:"due_at"`
	Overdue bool      `json:"overdue"`
}

// RoadmapSprint represents a sprint with its date range for roadmap display.
type RoadmapSprint struct {
	Sprint *Sprint `json:"sprint"`
	Items  []*Item `json:"items"`
}

// GetItemsDueInRange returns items with a due date within the given time range.
func (s *RoadmapService) GetItemsDueInRange(ctx context.Context, spaceID uuid.UUID, from, to time.Time) ([]*RoadmapItem, error) {
	allItems, err := s.itemRepo.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("getting roadmap items: %w", err)
	}

	now := time.Now().UTC()
	roadmap := make([]*RoadmapItem, 0)
	for _, item := range allItems {
		if item.DueAt == nil {
			continue
		}
		due := *item.DueAt
		if (due.Equal(from) || due.After(from)) && (due.Equal(to) || due.Before(to)) {
			roadmap = append(roadmap, &RoadmapItem{
				Item:    item,
				DueAt:   due,
				Overdue: due.Before(now) && item.Status != "closed" && item.Status != "resolved",
			})
		}
	}
	return roadmap, nil
}

// GetOverdueItems returns all items whose due date has passed and are not resolved or closed.
func (s *RoadmapService) GetOverdueItems(ctx context.Context, spaceID uuid.UUID) ([]*RoadmapItem, error) {
	allItems, err := s.itemRepo.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("getting overdue items: %w", err)
	}

	now := time.Now().UTC()
	overdue := make([]*RoadmapItem, 0)
	for _, item := range allItems {
		if item.DueAt == nil {
			continue
		}
		if item.DueAt.Before(now) && item.Status != "closed" && item.Status != "resolved" {
			overdue = append(overdue, &RoadmapItem{
				Item:    item,
				DueAt:   *item.DueAt,
				Overdue: true,
			})
		}
	}
	return overdue, nil
}

// GetSprintRoadmap returns all sprints with their date ranges and assigned items
// for display on a timeline/roadmap view.
func (s *RoadmapService) GetSprintRoadmap(ctx context.Context, spaceID uuid.UUID) ([]*RoadmapSprint, error) {
	sprints, err := s.sprintRepo.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("getting sprint roadmap: %w", err)
	}

	roadmap := make([]*RoadmapSprint, 0, len(sprints))
	for _, sprint := range sprints {
		if sprint.StartsAt == nil && sprint.EndsAt == nil {
			continue
		}
		items, err := s.itemRepo.ListBySprint(ctx, sprint.ID)
		if err != nil {
			return nil, fmt.Errorf("getting sprint roadmap items: %w", err)
		}
		roadmap = append(roadmap, &RoadmapSprint{
			Sprint: sprint,
			Items:  items,
		})
	}
	return roadmap, nil
}

// GetItemsWithoutDueDate returns all items that have no due date set,
// useful for identifying work that needs scheduling.
func (s *RoadmapService) GetItemsWithoutDueDate(ctx context.Context, spaceID uuid.UUID) ([]*Item, error) {
	allItems, err := s.itemRepo.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("getting items without due date: %w", err)
	}

	unscheduled := make([]*Item, 0)
	for _, item := range allItems {
		if item.DueAt == nil {
			unscheduled = append(unscheduled, item)
		}
	}
	return unscheduled, nil
}
