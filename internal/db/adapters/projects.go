package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// ItemAdapter implements projects.ItemRepository using sqlc-generated queries.
type ItemAdapter struct {
	q *generated.Queries
}

// NewItemAdapter creates an ItemAdapter backed by the given queries.
func NewItemAdapter(q *generated.Queries) *ItemAdapter {
	return &ItemAdapter{q: q}
}

// Create persists a new project item.
func (a *ItemAdapter) Create(ctx context.Context, item *projects.Item) error {
	_, err := a.q.CreateItem(ctx, itemToCreateParams(item))
	if err != nil {
		return fmt.Errorf("item adapter create: %w", err)
	}
	return nil
}

// GetByID retrieves an item by primary key. Returns an error if absent or soft-deleted.
func (a *ItemAdapter) GetByID(ctx context.Context, id uuid.UUID) (*projects.Item, error) {
	row, err := a.q.GetItemByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("item adapter get by id: %w", err)
	}
	return dbItemToProject(row), nil
}

// Update persists changes to an existing item.
func (a *ItemAdapter) Update(ctx context.Context, item *projects.Item) error {
	_, err := a.q.UpdateItem(ctx, itemToUpdateParams(item))
	if err != nil {
		return fmt.Errorf("item adapter update: %w", err)
	}
	return nil
}

// UpdateStatus changes only the status field.
func (a *ItemAdapter) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (*projects.Item, error) {
	row, err := a.q.UpdateItemStatus(ctx, generated.UpdateItemStatusParams{
		ID:     id,
		Status: status,
	})
	if err != nil {
		return nil, fmt.Errorf("item adapter update status: %w", err)
	}
	return dbItemToProject(row), nil
}

// UpdateSprint assigns an item to a sprint (or removes it if sprintID is nil).
func (a *ItemAdapter) UpdateSprint(ctx context.Context, id uuid.UUID, sprintID *uuid.UUID) error {
	if err := a.q.UpdateItemSprint(ctx, generated.UpdateItemSprintParams{
		ID:       id,
		SprintID: pgUUID(sprintID),
	}); err != nil {
		return fmt.Errorf("item adapter update sprint: %w", err)
	}
	return nil
}

// SoftDelete sets deleted_at on an item.
func (a *ItemAdapter) SoftDelete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.SoftDeleteItem(ctx, id); err != nil {
		return fmt.Errorf("item adapter soft delete: %w", err)
	}
	return nil
}

// ListBySpace returns all non-deleted items in a space, ordered by rank.
func (a *ItemAdapter) ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*projects.Item, error) {
	rows, err := a.q.ListItemsBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("item adapter list by space: %w", err)
	}
	return dbItemsToProjects(rows), nil
}

// ListByStatus returns items filtered by status within a space.
func (a *ItemAdapter) ListByStatus(ctx context.Context, spaceID uuid.UUID, status string) ([]*projects.Item, error) {
	rows, err := a.q.ListItemsByStatus(ctx, generated.ListItemsByStatusParams{
		SpaceID: spaceID,
		Status:  status,
	})
	if err != nil {
		return nil, fmt.Errorf("item adapter list by status: %w", err)
	}
	return dbItemsToProjects(rows), nil
}

// ListByAssignee returns items assigned to a specific user within a space.
func (a *ItemAdapter) ListByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*projects.Item, error) {
	rows, err := a.q.ListItemsByAssignee(ctx, generated.ListItemsByAssigneeParams{
		SpaceID:    spaceID,
		AssigneeID: pgUUID(&assigneeID),
	})
	if err != nil {
		return nil, fmt.Errorf("item adapter list by assignee: %w", err)
	}
	return dbItemsToProjects(rows), nil
}

// ListBySprint returns all items in a given sprint, ordered by rank.
func (a *ItemAdapter) ListBySprint(ctx context.Context, sprintID uuid.UUID) ([]*projects.Item, error) {
	rows, err := a.q.ListItemsBySprint(ctx, pgUUID(&sprintID))
	if err != nil {
		return nil, fmt.Errorf("item adapter list by sprint: %w", err)
	}
	return dbItemsToProjects(rows), nil
}

// Search performs full-text search on items within a space.
func (a *ItemAdapter) Search(ctx context.Context, spaceID uuid.UUID, query string, limit int) ([]*projects.Item, error) {
	searchLimit := int32(limit) //nolint:gosec // limit is validated by the service layer (capped at 50)
	rows, err := a.q.SearchItems(ctx, generated.SearchItemsParams{
		SpaceID:        spaceID,
		PlaintoTsquery: query,
		Limit:          searchLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("item adapter search: %w", err)
	}
	return dbItemsToProjects(rows), nil
}

// itemToCreateParams converts a domain Item to sqlc CreateItemParams.
func itemToCreateParams(item *projects.Item) generated.CreateItemParams {
	return generated.CreateItemParams{
		ID:          item.ID,
		SpaceID:     item.SpaceID,
		ParentID:    pgUUID(item.ParentID),
		Kind:        item.Kind,
		Title:       item.Title,
		Description: strPtr(item.Description),
		Status:      item.Status,
		Priority:    item.Priority,
		ReporterID:  item.ReporterID,
		AssigneeID:  pgUUID(item.AssigneeID),
		Labels:      item.Labels,
		DueAt:       pgTimestampPtr(item.DueAt),
		Rank:        item.Rank,
	}
}

// itemToUpdateParams converts a domain Item to sqlc UpdateItemParams.
func itemToUpdateParams(item *projects.Item) generated.UpdateItemParams {
	return generated.UpdateItemParams{
		ID:          item.ID,
		Title:       item.Title,
		Description: strPtr(item.Description),
		Status:      item.Status,
		Priority:    item.Priority,
		AssigneeID:  pgUUID(item.AssigneeID),
		Labels:      item.Labels,
		DueAt:       pgTimestampPtr(item.DueAt),
		Rank:        item.Rank,
	}
}

// sprintToCreateParams converts a domain Sprint to sqlc CreateSprintParams.
func sprintToCreateParams(sprint *projects.Sprint) generated.CreateSprintParams {
	return generated.CreateSprintParams{
		ID:        sprint.ID,
		SpaceID:   sprint.SpaceID,
		Name:      sprint.Name,
		Goal:      strPtr(sprint.Goal),
		Status:    sprint.Status,
		StartsAt:  pgTimestampPtr(sprint.StartsAt),
		EndsAt:    pgTimestampPtr(sprint.EndsAt),
		CreatedBy: sprint.CreatedBy,
	}
}

// dbItemToProject converts a generated.Item to a projects.Item.
func dbItemToProject(i generated.Item) *projects.Item {
	return &projects.Item{
		ID:          i.ID,
		SpaceID:     i.SpaceID,
		ParentID:    goUUIDPtr(i.ParentID),
		Kind:        i.Kind,
		Title:       i.Title,
		Description: derefStr(i.Description),
		Status:      i.Status,
		Priority:    i.Priority,
		ReporterID:  i.ReporterID,
		AssigneeID:  goUUIDPtr(i.AssigneeID),
		SprintID:    goUUIDPtr(i.SprintID),
		Labels:      i.Labels,
		DueAt:       goTimePtr(i.DueAt),
		ResolvedAt:  goTimePtr(i.ResolvedAt),
		Rank:        i.Rank,
		CreatedAt:   goTime(i.CreatedAt),
		UpdatedAt:   goTime(i.UpdatedAt),
		DeletedAt:   goTimePtr(i.DeletedAt),
	}
}

// dbItemsToProjects converts a slice of generated.Item to domain items.
func dbItemsToProjects(items []generated.Item) []*projects.Item {
	result := make([]*projects.Item, len(items))
	for i, item := range items {
		result[i] = dbItemToProject(item)
	}
	return result
}

// SprintAdapter implements projects.SprintRepository using sqlc-generated queries.
type SprintAdapter struct {
	q *generated.Queries
}

// NewSprintAdapter creates a SprintAdapter backed by the given queries.
func NewSprintAdapter(q *generated.Queries) *SprintAdapter {
	return &SprintAdapter{q: q}
}

// Create persists a new sprint.
func (a *SprintAdapter) Create(ctx context.Context, sprint *projects.Sprint) error {
	_, err := a.q.CreateSprint(ctx, sprintToCreateParams(sprint))
	if err != nil {
		return fmt.Errorf("sprint adapter create: %w", err)
	}
	return nil
}

// GetByID retrieves a sprint by primary key.
func (a *SprintAdapter) GetByID(ctx context.Context, id uuid.UUID) (*projects.Sprint, error) {
	row, err := a.q.GetSprintByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("sprint adapter get by id: %w", err)
	}
	return dbSprintToProject(row), nil
}

// GetActiveBySpace returns the currently active sprint for a space.
func (a *SprintAdapter) GetActiveBySpace(ctx context.Context, spaceID uuid.UUID) (*projects.Sprint, error) {
	row, err := a.q.GetActiveSprintBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("sprint adapter get active by space: %w", err)
	}
	return dbSprintToProject(row), nil
}

// Update persists changes to a sprint (name, goal, dates).
func (a *SprintAdapter) Update(ctx context.Context, sprint *projects.Sprint) error {
	_, err := a.q.UpdateSprint(ctx, generated.UpdateSprintParams{
		ID:       sprint.ID,
		Name:     sprint.Name,
		Goal:     strPtr(sprint.Goal),
		StartsAt: pgTimestampPtr(sprint.StartsAt),
		EndsAt:   pgTimestampPtr(sprint.EndsAt),
	})
	if err != nil {
		return fmt.Errorf("sprint adapter update: %w", err)
	}
	return nil
}

// UpdateStatus changes the sprint status.
func (a *SprintAdapter) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (*projects.Sprint, error) {
	row, err := a.q.UpdateSprintStatus(ctx, generated.UpdateSprintStatusParams{
		ID:     id,
		Status: status,
	})
	if err != nil {
		return nil, fmt.Errorf("sprint adapter update status: %w", err)
	}
	return dbSprintToProject(row), nil
}

// ListBySpace returns all sprints in a space, ordered by creation date descending.
func (a *SprintAdapter) ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*projects.Sprint, error) {
	rows, err := a.q.ListSprintsBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("sprint adapter list by space: %w", err)
	}
	result := make([]*projects.Sprint, len(rows))
	for i, row := range rows {
		result[i] = dbSprintToProject(row)
	}
	return result, nil
}

// dbSprintToProject converts a generated.Sprint to a projects.Sprint.
func dbSprintToProject(s generated.Sprint) *projects.Sprint {
	return &projects.Sprint{
		ID:        s.ID,
		SpaceID:   s.SpaceID,
		Name:      s.Name,
		Goal:      derefStr(s.Goal),
		Status:    s.Status,
		StartsAt:  goTimePtr(s.StartsAt),
		EndsAt:    goTimePtr(s.EndsAt),
		CreatedBy: s.CreatedBy,
		CreatedAt: goTime(s.CreatedAt),
		UpdatedAt: goTime(s.UpdatedAt),
	}
}

// RelationAdapter implements projects.RelationRepository using sqlc-generated queries.
type RelationAdapter struct {
	q *generated.Queries
}

// NewRelationAdapter creates a RelationAdapter backed by the given queries.
func NewRelationAdapter(q *generated.Queries) *RelationAdapter {
	return &RelationAdapter{q: q}
}

// Create persists a new relation.
func (a *RelationAdapter) Create(ctx context.Context, rel *projects.Relation) error {
	_, err := a.q.CreateItemRelation(ctx, generated.CreateItemRelationParams{
		ID:        rel.ID,
		FromID:    rel.FromID,
		ToID:      rel.ToID,
		Kind:      rel.Kind,
		CreatedBy: rel.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("relation adapter create: %w", err)
	}
	return nil
}

// ListByItem returns all relations originating from a given item.
func (a *RelationAdapter) ListByItem(ctx context.Context, fromID uuid.UUID) ([]*projects.Relation, error) {
	rows, err := a.q.ListItemRelations(ctx, fromID)
	if err != nil {
		return nil, fmt.Errorf("relation adapter list by item: %w", err)
	}
	result := make([]*projects.Relation, len(rows))
	for i, row := range rows {
		result[i] = &projects.Relation{
			ID:        row.ID,
			FromID:    row.FromID,
			ToID:      row.ToID,
			Kind:      row.Kind,
			CreatedBy: row.CreatedBy,
			ToTitle:   row.ToTitle,
			ToStatus:  row.ToStatus,
			ToKind:    row.ToKind,
		}
	}
	return result, nil
}

// Delete removes a relation by ID.
func (a *RelationAdapter) Delete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.DeleteItemRelation(ctx, id); err != nil {
		return fmt.Errorf("relation adapter delete: %w", err)
	}
	return nil
}

// LabelAdapter implements projects.LabelRepository using sqlc-generated queries.
type LabelAdapter struct {
	q *generated.Queries
}

// NewLabelAdapter creates a LabelAdapter backed by the given queries.
func NewLabelAdapter(q *generated.Queries) *LabelAdapter {
	return &LabelAdapter{q: q}
}

// Create persists a new label.
func (a *LabelAdapter) Create(ctx context.Context, label *projects.Label) error {
	_, err := a.q.CreateLabel(ctx, generated.CreateLabelParams{
		ID:    label.ID,
		OrgID: label.OrgID,
		Name:  label.Name,
		Color: label.Color,
	})
	if err != nil {
		return fmt.Errorf("label adapter create: %w", err)
	}
	return nil
}

// ListByOrg returns all labels for an organization, ordered by name.
func (a *LabelAdapter) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*projects.Label, error) {
	rows, err := a.q.ListLabelsByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("label adapter list by org: %w", err)
	}
	result := make([]*projects.Label, len(rows))
	for i, row := range rows {
		result[i] = &projects.Label{
			ID:    row.ID,
			OrgID: row.OrgID,
			Name:  row.Name,
			Color: row.Color,
		}
	}
	return result, nil
}

// Delete removes a label by ID.
func (a *LabelAdapter) Delete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.DeleteLabel(ctx, id); err != nil {
		return fmt.Errorf("label adapter delete: %w", err)
	}
	return nil
}
