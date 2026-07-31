package projects

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
)

// BacklogService handles backlog ordering and prioritisation for a project space.
type BacklogService struct {
	itemRepo   ItemRepository
	sprintRepo SprintRepository
}

// NewBacklogService creates a BacklogService backed by the given repositories.
func NewBacklogService(itemRepo ItemRepository, sprintRepo SprintRepository) *BacklogService {
	return &BacklogService{itemRepo: itemRepo, sprintRepo: sprintRepo}
}

// GetBacklog returns all items in a space that are not assigned to any sprint,
// ordered by rank (priority ordering).
func (s *BacklogService) GetBacklog(ctx context.Context, spaceID uuid.UUID) ([]*Item, error) {
	allItems, err := s.itemRepo.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("getting backlog: %w", err)
	}

	backlog := make([]*Item, 0, len(allItems))
	for _, item := range allItems {
		if item.SprintID == nil {
			backlog = append(backlog, item)
		}
	}
	return backlog, nil
}

// GetSprintBacklog returns all items assigned to a specific sprint, ordered by
// rank, reconciled against the space the request named.
func (s *BacklogService) GetSprintBacklog(ctx context.Context, spaceID, sprintID uuid.UUID) ([]*Item, error) {
	items, err := s.itemRepo.ListBySprint(ctx, spaceID, sprintID)
	if err != nil {
		return nil, fmt.Errorf("getting sprint backlog: %w", err)
	}
	return items, nil
}

// MoveToSprint assigns an item to a sprint, removing it from the unassigned backlog.
func (s *BacklogService) MoveToSprint(ctx context.Context, itemID, sprintID uuid.UUID) error {
	// Verify the sprint exists.
	if _, err := s.sprintRepo.GetByID(ctx, sprintID); err != nil {
		return fmt.Errorf("moving item to sprint: %w", err)
	}

	if err := s.itemRepo.UpdateSprint(ctx, itemID, &sprintID); err != nil {
		return fmt.Errorf("moving item to sprint: %w", err)
	}
	return nil
}

// MoveToBacklog removes an item from its sprint, returning it to the unassigned backlog.
func (s *BacklogService) MoveToBacklog(ctx context.Context, itemID uuid.UUID) error {
	if err := s.itemRepo.UpdateSprint(ctx, itemID, nil); err != nil {
		return fmt.Errorf("moving item to backlog: %w", err)
	}
	return nil
}

// ReorderItem changes the rank of an item to reposition it in the backlog or sprint view.
func (s *BacklogService) ReorderItem(ctx context.Context, itemID uuid.UUID, newRank string) error {
	item, err := s.itemRepo.GetByID(ctx, itemID)
	if err != nil {
		return fmt.Errorf("reordering item: %w", err)
	}

	item.Rank = newRank
	if err := s.itemRepo.Update(ctx, item); err != nil {
		return fmt.Errorf("reordering item: %w", err)
	}
	return nil
}

// RankItemRelative repositions an item relative to its neighbours in the space.
// Pass beforeID to place the item immediately before that item,
// or afterID to place it immediately after. Both are optional.
func (s *BacklogService) RankItemRelative(ctx context.Context, spaceID, itemID uuid.UUID, beforeID, afterID *uuid.UUID) error { //nolint:cyclop // lexorank positioning logic requires branching on before/after/both/neither cases
	all, err := s.itemRepo.ListBySpace(ctx, spaceID)
	if err != nil {
		return fmt.Errorf("ranking item: %w", err)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Rank < all[j].Rank })

	var moving *Item
	remaining := make([]*Item, 0, len(all))
	for _, it := range all {
		if it.ID == itemID {
			moving = it
		} else {
			remaining = append(remaining, it)
		}
	}
	if moving == nil {
		return ErrNotFound
	}

	insertIdx := len(remaining)
	if beforeID != nil {
		for i, it := range remaining {
			if it.ID == *beforeID {
				insertIdx = i
				break
			}
		}
	} else if afterID != nil {
		for i, it := range remaining {
			if it.ID == *afterID {
				insertIdx = i + 1
				break
			}
		}
	}

	newOrder := make([]*Item, 0, len(all))
	newOrder = append(newOrder, remaining[:insertIdx]...)
	newOrder = append(newOrder, moving)
	newOrder = append(newOrder, remaining[insertIdx:]...)

	for i, it := range newOrder {
		it.Rank = fmt.Sprintf("0|%06d:", i+1)
		if err := s.itemRepo.Update(ctx, it); err != nil {
			return fmt.Errorf("ranking item: %w", err)
		}
	}
	return nil
}

// GetBacklogByPriority returns backlog items filtered by priority level.
func (s *BacklogService) GetBacklogByPriority(ctx context.Context, spaceID uuid.UUID, priority string) ([]*Item, error) {
	if !ValidPriorities[priority] {
		return nil, fmt.Errorf("filtering backlog: %w", ErrInvalidPriority)
	}

	allItems, err := s.itemRepo.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("filtering backlog by priority: %w", err)
	}

	filtered := make([]*Item, 0)
	for _, item := range allItems {
		if item.SprintID == nil && item.Priority == priority {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}
