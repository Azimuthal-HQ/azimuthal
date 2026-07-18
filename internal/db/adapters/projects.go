package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// ItemAdapter implements projects.ItemRepository using the project_items table.
type ItemAdapter struct {
	q *generated.Queries
}

// NewItemAdapter creates an ItemAdapter backed by the given queries.
func NewItemAdapter(q *generated.Queries) *ItemAdapter {
	return &ItemAdapter{q: q}
}

// Create persists a new project item, auto-assigning the next available number.
func (a *ItemAdapter) Create(ctx context.Context, item *projects.Item) error {
	maxNum, err := a.q.GetProjectItemMaxNumber(ctx, item.SpaceID)
	if err != nil {
		return fmt.Errorf("item adapter get max number: %w", err)
	}
	number := int32(maxNum) + 1 //nolint:gosec // G115 — item numbers are sequential and will never approach int32 max

	_, err = a.q.CreateProjectItem(ctx, generated.CreateProjectItemParams{
		ID:          item.ID,
		SpaceID:     item.SpaceID,
		ParentID:    pgUUID(item.ParentID),
		Number:      number,
		Kind:        item.Kind,
		Title:       item.Title,
		Description: item.Description,
		Status:      item.Status,
		Priority:    item.Priority,
		ReporterID:  item.ReporterID,
		AssigneeID:  pgUUID(item.AssigneeID),
		SprintID:    pgUUID(item.SprintID),
		Labels:      coalesceLabels(item.Labels),
		DueAt:       pgTimestampPtr(item.DueAt),
		Rank:        item.Rank,
	})
	if err != nil {
		return fmt.Errorf("item adapter create: %w", err)
	}
	return nil
}

// GetByID retrieves an item by primary key. Returns an error if absent or soft-deleted.
func (a *ItemAdapter) GetByID(ctx context.Context, id uuid.UUID) (*projects.Item, error) {
	row, err := a.q.GetProjectItemByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("item adapter get by id: %w", err)
	}
	return dbProjectItemToItem(row), nil
}

// Update persists changes to an existing item.
func (a *ItemAdapter) Update(ctx context.Context, item *projects.Item) error {
	_, err := a.q.UpdateProjectItem(ctx, generated.UpdateProjectItemParams{
		ID:          item.ID,
		Title:       item.Title,
		Description: item.Description,
		Status:      item.Status,
		Priority:    item.Priority,
		AssigneeID:  pgUUID(item.AssigneeID),
		Labels:      coalesceLabels(item.Labels),
		DueAt:       pgTimestampPtr(item.DueAt),
		Rank:        item.Rank,
	})
	if err != nil {
		return fmt.Errorf("item adapter update: %w", err)
	}
	return nil
}

// UpdateStatus changes only the status field.
func (a *ItemAdapter) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (*projects.Item, error) {
	row, err := a.q.UpdateProjectItemStatus(ctx, generated.UpdateProjectItemStatusParams{
		ID:     id,
		Status: status,
	})
	if err != nil {
		return nil, fmt.Errorf("item adapter update status: %w", err)
	}
	return dbProjectItemToItem(row), nil
}

// UpdateSprint assigns an item to a sprint (or removes it if sprintID is nil).
func (a *ItemAdapter) UpdateSprint(ctx context.Context, id uuid.UUID, sprintID *uuid.UUID) error {
	if err := a.q.UpdateProjectItemSprint(ctx, generated.UpdateProjectItemSprintParams{
		ID:       id,
		SprintID: pgUUID(sprintID),
	}); err != nil {
		return fmt.Errorf("item adapter update sprint: %w", err)
	}
	return nil
}

// SoftDelete sets deleted_at on an item.
func (a *ItemAdapter) SoftDelete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.SoftDeleteProjectItem(ctx, id); err != nil {
		return fmt.Errorf("item adapter soft delete: %w", err)
	}
	return nil
}

// ListBySpace returns all non-deleted items in a space, ordered by rank.
func (a *ItemAdapter) ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*projects.Item, error) {
	rows, err := a.q.ListProjectItemsBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("item adapter list by space: %w", err)
	}
	return dbProjectItemsToItems(rows), nil
}

// ListByStatus returns items filtered by status within a space.
func (a *ItemAdapter) ListByStatus(ctx context.Context, spaceID uuid.UUID, status string) ([]*projects.Item, error) {
	rows, err := a.q.ListProjectItemsByStatus(ctx, generated.ListProjectItemsByStatusParams{
		SpaceID: spaceID,
		Status:  status,
	})
	if err != nil {
		return nil, fmt.Errorf("item adapter list by status: %w", err)
	}
	return dbProjectItemsToItems(rows), nil
}

// ListByAssignee returns items assigned to a specific user within a space.
func (a *ItemAdapter) ListByAssignee(ctx context.Context, spaceID uuid.UUID, assigneeID uuid.UUID) ([]*projects.Item, error) {
	rows, err := a.q.ListProjectItemsByAssignee(ctx, generated.ListProjectItemsByAssigneeParams{
		SpaceID:    spaceID,
		AssigneeID: pgUUID(&assigneeID),
	})
	if err != nil {
		return nil, fmt.Errorf("item adapter list by assignee: %w", err)
	}
	return dbProjectItemsToItems(rows), nil
}

// ListBySprint returns all items in a given sprint, ordered by rank.
func (a *ItemAdapter) ListBySprint(ctx context.Context, sprintID uuid.UUID) ([]*projects.Item, error) {
	rows, err := a.q.ListProjectItemsBySprint(ctx, pgUUID(&sprintID))
	if err != nil {
		return nil, fmt.Errorf("item adapter list by sprint: %w", err)
	}
	return dbProjectItemsToItems(rows), nil
}

// Search performs full-text search on items within a space.
func (a *ItemAdapter) Search(ctx context.Context, spaceID uuid.UUID, query string, limit int) ([]*projects.Item, error) {
	searchLimit := int32(limit) //nolint:gosec // limit is validated by the service layer (capped at 50)
	rows, err := a.q.SearchProjectItems(ctx, generated.SearchProjectItemsParams{
		SpaceID:        spaceID,
		PlaintoTsquery: query,
		Limit:          searchLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("item adapter search: %w", err)
	}
	return dbProjectItemsToItems(rows), nil
}

func dbProjectItemToItem(i generated.ProjectItem) *projects.Item {
	return &projects.Item{
		ID:          i.ID,
		SpaceID:     i.SpaceID,
		ParentID:    goUUIDPtr(i.ParentID),
		Kind:        i.Kind,
		Title:       i.Title,
		Description: i.Description,
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

func dbProjectItemsToItems(rows []generated.ProjectItem) []*projects.Item {
	result := make([]*projects.Item, len(rows))
	for i, row := range rows {
		result[i] = dbProjectItemToItem(row)
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

// GetActiveBySpace returns the currently active sprint for a space. Having no
// active sprint is the repository contract's ErrNotFound, not an internal
// error — the API maps it to 404 and the frontend treats that as "no sprint".
func (a *SprintAdapter) GetActiveBySpace(ctx context.Context, spaceID uuid.UUID) (*projects.Sprint, error) {
	row, err := a.q.GetActiveSprintBySpace(ctx, spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, projects.ErrNotFound
	}
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

// RelationAdapter implements projects.RelationRepository using the entity_relations table.
type RelationAdapter struct {
	q *generated.Queries
}

// NewRelationAdapter creates a RelationAdapter backed by the given queries.
func NewRelationAdapter(q *generated.Queries) *RelationAdapter {
	return &RelationAdapter{q: q}
}

// Create persists a new polymorphic entity relation.
func (a *RelationAdapter) Create(ctx context.Context, rel *projects.Relation) error {
	_, err := a.q.CreateEntityRelation(ctx, generated.CreateEntityRelationParams{
		ID:        rel.ID,
		FromID:    rel.FromID,
		FromType:  rel.FromType,
		ToID:      rel.ToID,
		ToType:    rel.ToType,
		Kind:      rel.Kind,
		CreatedBy: rel.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("relation adapter create: %w", err)
	}
	return nil
}

// ListByItem returns all relations from a given entity (identified by fromID and fromType).
func (a *RelationAdapter) ListByItem(ctx context.Context, fromID uuid.UUID) ([]*projects.Relation, error) {
	// Attempt to list from both entity types — try project_item first, then ticket.
	// In practice, callers pass a from_type too; this shim queries without type constraint.
	// For full polymorphism, use ListByEntity which accepts a from_type.
	rows, err := a.q.ListEntityRelationsByEntity(ctx, generated.ListEntityRelationsByEntityParams{
		FromID:   fromID,
		FromType: "project_item",
	})
	if err != nil {
		return nil, fmt.Errorf("relation adapter list by item: %w", err)
	}
	if len(rows) == 0 {
		// Fall back to ticket type.
		rows, err = a.q.ListEntityRelationsByEntity(ctx, generated.ListEntityRelationsByEntityParams{
			FromID:   fromID,
			FromType: "ticket",
		})
		if err != nil {
			return nil, fmt.Errorf("relation adapter list by item (ticket): %w", err)
		}
	}
	return dbEntityRelationRowsToRelations(rows), nil
}

// ListByEntity returns all relations from a specific typed entity.
func (a *RelationAdapter) ListByEntity(ctx context.Context, fromID uuid.UUID, fromType string) ([]*projects.Relation, error) {
	rows, err := a.q.ListEntityRelationsByEntity(ctx, generated.ListEntityRelationsByEntityParams{
		FromID:   fromID,
		FromType: fromType,
	})
	if err != nil {
		return nil, fmt.Errorf("relation adapter list by entity: %w", err)
	}
	return dbEntityRelationRowsToRelations(rows), nil
}

// Delete removes a relation by ID.
func (a *RelationAdapter) Delete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.DeleteEntityRelation(ctx, id); err != nil {
		return fmt.Errorf("relation adapter delete: %w", err)
	}
	return nil
}

func dbEntityRelationRowsToRelations(rows []generated.ListEntityRelationsByEntityRow) []*projects.Relation {
	result := make([]*projects.Relation, len(rows))
	for i, row := range rows {
		result[i] = &projects.Relation{
			ID:        row.ID,
			FromID:    row.FromID,
			FromType:  row.FromType,
			ToID:      row.ToID,
			ToType:    row.ToType,
			Kind:      row.Kind,
			CreatedBy: row.CreatedBy,
			ToTitle:   row.ToTitle,
			ToStatus:  row.ToStatus,
		}
	}
	return result
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
