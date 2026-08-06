package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

// Create persists a new project item. The number, item_key and org_id are
// assigned atomically by the CreateProjectItem query (concurrency-safe per-space
// counter, see internal/db/queries/project_items.sql); the returned row's
// generated values are written back onto item so the caller — and the create
// response — carry the assigned key.
func (a *ItemAdapter) Create(ctx context.Context, item *projects.Item) error {
	row, err := a.q.CreateProjectItem(ctx, generated.CreateProjectItemParams{
		ID:          item.ID,
		SpaceID:     item.SpaceID,
		ParentID:    pgUUID(item.ParentID),
		Kind:        item.Kind,
		Title:       item.Title,
		Description: item.Description,
		Status:      item.Status,
		Priority:    item.Priority,
		ReporterID:  item.ReporterID,
		AssigneeID:  pgUUID(item.AssigneeID),
		SprintID:    pgUUID(item.SprintID),
		DueAt:       pgTimestampPtr(item.DueAt),
		Rank:        item.Rank,
		// See the ticket twin: written at creation so the item starts inside its
		// state machine, and omitting the field would silently mean NULL.
		WorkflowStateID: pgUUID(item.WorkflowStateID),
	})
	if err != nil {
		return fmt.Errorf("item adapter create: %w", err)
	}
	item.Number = int(row.Number)
	item.ItemKey = row.ItemKey
	return nil
}

// GetByOrgKey resolves a human-readable key (e.g. VEC-123) to an item within an
// org. Returns projects.ErrNotFound if absent or soft-deleted.
func (a *ItemAdapter) GetByOrgKey(ctx context.Context, orgID uuid.UUID, key string) (*projects.Item, error) {
	row, err := a.q.GetProjectItemByOrgKey(ctx, generated.GetProjectItemByOrgKeyParams{
		OrgID:   orgID,
		ItemKey: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, projects.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("item adapter get by org key: %w", err)
	}
	return dbProjectItemToItem(row), nil
}

// GetByID retrieves an item by primary key. Returns projects.ErrNotFound if
// absent or soft-deleted.
func (a *ItemAdapter) GetByID(ctx context.Context, id uuid.UUID) (*projects.Item, error) {
	row, err := a.q.GetProjectItemByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, projects.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("item adapter get by id: %w", err)
	}
	return dbProjectItemToItem(row), nil
}

// GetByIDInSpace retrieves an item reconciled against the given space.
//
// A wrong space produces pgx.ErrNoRows and therefore the same ErrNotFound an
// absent item produces. That collapse is the point: the caller cannot use this
// endpoint to learn whether an id it guessed names something real.
func (a *ItemAdapter) GetByIDInSpace(ctx context.Context, spaceID, id uuid.UUID) (*projects.Item, error) {
	row, err := a.q.GetProjectItemInSpace(ctx, generated.GetProjectItemInSpaceParams{
		ItemID:  id,
		SpaceID: spaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, projects.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("item adapter get by id in space: %w", err)
	}
	return dbProjectItemToItem(row), nil
}

// Update persists changes to an existing item. An item that is gone by the time
// the write lands is ErrNotFound, not an internal error: every handler on this
// path pre-loads the item, so this is the TOCTOU window between the read and
// the write rather than a plain unknown id — but the caller's answer for "the
// thing you were editing has been deleted" is 404 either way (known-issues #24).
func (a *ItemAdapter) Update(ctx context.Context, item *projects.Item) error {
	_, err := a.q.UpdateProjectItem(ctx, generated.UpdateProjectItemParams{
		ID:          item.ID,
		Title:       item.Title,
		Description: item.Description,
		Status:      item.Status,
		Priority:    item.Priority,
		AssigneeID:  pgUUID(item.AssigneeID),
		DueAt:       pgTimestampPtr(item.DueAt),
		Rank:        item.Rank,
		// Safe for a PATCH that omitted "kind": applyItemPatch leaves the
		// stored slug on the item it loaded, so this rewrites the same value
		// rather than blanking it.
		Kind: item.Kind,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("item adapter update: %w", err)
	}
	return nil
}

// UpdateStatus changes only the status field. See Update on why a missing row
// is ErrNotFound rather than an internal error.
func (a *ItemAdapter) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (*projects.Item, error) {
	row, err := a.q.UpdateProjectItemStatus(ctx, generated.UpdateProjectItemStatusParams{
		ID:     id,
		Status: status,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, projects.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("item adapter update status: %w", err)
	}
	return dbProjectItemToItem(row), nil
}

// UpdateSprintInSpace assigns an item in spaceID to a sprint in the same
// space, or removes it from one when sprintID is nil.
//
// An item outside spaceID, or a sprint outside it, updates nothing and reports
// success — see the query for why both ids are reconciled there rather than by
// a pair of lookups here.
// A nil sprintID takes the clearing statement, which names no sprint and so
// has none to reconcile. The two are separate queries rather than one with a
// nullable parameter; see the query headers.
func (a *ItemAdapter) UpdateSprintInSpace(
	ctx context.Context, id, spaceID uuid.UUID, sprintID *uuid.UUID,
) error {
	if sprintID == nil {
		if err := a.q.ClearProjectItemSprintInSpace(ctx, generated.ClearProjectItemSprintInSpaceParams{
			ItemID:  id,
			SpaceID: spaceID,
		}); err != nil {
			return fmt.Errorf("item adapter clear sprint: %w", err)
		}
		return nil
	}
	if err := a.q.AssignProjectItemToSprintInSpace(ctx, generated.AssignProjectItemToSprintInSpaceParams{
		ItemID:   id,
		SpaceID:  spaceID,
		SprintID: pgUUID(sprintID),
	}); err != nil {
		return fmt.Errorf("item adapter update sprint: %w", err)
	}
	return nil
}

// SoftDeleteInSpace sets deleted_at on an item in spaceID.
func (a *ItemAdapter) SoftDeleteInSpace(ctx context.Context, id, spaceID uuid.UUID) error {
	if err := a.q.SoftDeleteProjectItemInSpace(ctx, generated.SoftDeleteProjectItemInSpaceParams{
		ItemID: id, SpaceID: spaceID,
	}); err != nil {
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
func (a *ItemAdapter) ListBySprint(ctx context.Context, spaceID, sprintID uuid.UUID) ([]*projects.Item, error) {
	rows, err := a.q.ListProjectItemsBySprint(ctx, generated.ListProjectItemsBySprintParams{
		SprintID: pgUUID(&sprintID),
		SpaceID:  spaceID,
	})
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
		Number:      int(i.Number),
		ItemKey:     i.ItemKey,
		ParentID:    goUUIDPtr(i.ParentID),
		Kind:        i.Kind,
		Title:       i.Title,
		Description: i.Description,
		Status:      i.Status,
		Priority:    i.Priority,
		ReporterID:  i.ReporterID,
		AssigneeID:  goUUIDPtr(i.AssigneeID),
		SprintID:    goUUIDPtr(i.SprintID),
		DueAt:       goTimePtr(i.DueAt),
		ResolvedAt:  goTimePtr(i.ResolvedAt),
		Rank:        i.Rank,
		// See the ticket twin in tickets.go: populated on every read, because a
		// gate that sometimes receives a zero value decides against one.
		WorkflowStateID: goUUIDPtr(i.WorkflowStateID),
		CreatedAt:       goTime(i.CreatedAt),
		UpdatedAt:       goTime(i.UpdatedAt),
		DeletedAt:       goTimePtr(i.DeletedAt),
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
// It holds the pool as well as the queries because sprint completion is a
// two-statement transaction (flip status, reassign incomplete items) that must
// commit or roll back atomically.
type SprintAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewSprintAdapter creates a SprintAdapter backed by the given pool. The
// queries handle is derived from the pool.
func NewSprintAdapter(pool *pgxpool.Pool) *SprintAdapter {
	return &SprintAdapter{pool: pool, q: generated.New(pool)}
}

// Create persists a new sprint.
func (a *SprintAdapter) Create(ctx context.Context, sprint *projects.Sprint) error {
	_, err := a.q.CreateSprint(ctx, sprintToCreateParams(sprint))
	if err != nil {
		return fmt.Errorf("sprint adapter create: %w", err)
	}
	return nil
}

// GetByID retrieves a sprint by primary key. Returns projects.ErrNotFound if
// absent.
func (a *SprintAdapter) GetByID(ctx context.Context, id uuid.UUID) (*projects.Sprint, error) {
	row, err := a.q.GetSprintByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, projects.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sprint adapter get by id: %w", err)
	}
	return dbSprintToProject(row), nil
}

// GetByIDInSpace retrieves a sprint reconciled against the given space. A
// sprint in another space is ErrNotFound, exactly as an absent one is.
func (a *SprintAdapter) GetByIDInSpace(ctx context.Context, spaceID, id uuid.UUID) (*projects.Sprint, error) {
	row, err := a.q.GetSprintInSpace(ctx, generated.GetSprintInSpaceParams{
		SprintID: id,
		SpaceID:  spaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, projects.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sprint adapter get by id in space: %w", err)
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

// Update persists changes to a sprint (name, goal, dates). A sprint that is
// gone by the time the write lands is ErrNotFound, for the reason given on
// ItemAdapter.Update.
func (a *SprintAdapter) Update(ctx context.Context, sprint *projects.Sprint) error {
	_, err := a.q.UpdateSprint(ctx, generated.UpdateSprintParams{
		ID:       sprint.ID,
		Name:     sprint.Name,
		Goal:     strPtr(sprint.Goal),
		StartsAt: pgTimestampPtr(sprint.StartsAt),
		EndsAt:   pgTimestampPtr(sprint.EndsAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("sprint adapter update: %w", err)
	}
	return nil
}

// UpdateStatus changes the sprint status. A unique-violation on the
// one-active-per-space partial index (migration 034) is mapped to
// ErrSprintActive so a lost race to activate surfaces as 409, not 500 —
// the same outcome as the service-level GetActiveBySpace guard. A sprint that
// is gone is ErrNotFound, for the reason given on ItemAdapter.Update.
func (a *SprintAdapter) UpdateStatus(ctx context.Context, id uuid.UUID, status string) (*projects.Sprint, error) {
	row, err := a.q.UpdateSprintStatus(ctx, generated.UpdateSprintStatusParams{
		ID:     id,
		Status: status,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, projects.ErrNotFound
		}
		if name, ok := uniqueViolation(err); ok && name == "idx_sprints_one_active_per_space" {
			return nil, projects.ErrSprintActive
		}
		return nil, fmt.Errorf("sprint adapter update status: %w", err)
	}
	return dbSprintToProject(row), nil
}

// CompleteWithDisposition marks the sprint completed and, in the same
// transaction, moves every not-yet-done item off it — to nextSprintID when
// non-nil, otherwise back to the backlog (sprint_id = NULL). Items whose
// status is in doneStatuses stay on the completed sprint. Returns the updated
// sprint. The two writes are atomic: a crash between them can never leave a
// completed sprint still holding unfinished work.
func (a *SprintAdapter) CompleteWithDisposition(ctx context.Context, id uuid.UUID, nextSprintID *uuid.UUID, doneStatuses []string) (*projects.Sprint, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sprint adapter complete: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	if _, err := qtx.ReassignIncompleteSprintItems(ctx, generated.ReassignIncompleteSprintItemsParams{
		NextSprintID: pgUUID(nextSprintID),
		SprintID:     pgUUID(&id),
		DoneStatuses: doneStatuses,
	}); err != nil {
		return nil, fmt.Errorf("sprint adapter complete: reassign items: %w", err)
	}

	row, err := qtx.UpdateSprintStatus(ctx, generated.UpdateSprintStatusParams{
		ID:     id,
		Status: projects.SprintStatusCompleted,
	})
	// The same mapping UpdateStatus makes, because it is the same query: a
	// sprint that vanished between the handler's read and this write is 404,
	// not 500. The rollback is already deferred.
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, projects.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sprint adapter complete: update status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sprint adapter complete: commit: %w", err)
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
func (a *RelationAdapter) Create(ctx context.Context, id uuid.UUID, rel *projects.NewRelation) error {
	_, err := a.q.CreateEntityRelation(ctx, generated.CreateEntityRelationParams{
		ID:        id,
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

// TargetIsReadable reports whether a relation target both exists and sits in a
// space the caller may read. One bool, so the two failure modes cannot be told
// apart by anything downstream.
func (a *RelationAdapter) TargetIsReadable(ctx context.Context, targetID uuid.UUID, targetType string, readableSpaceIDs []uuid.UUID) (bool, error) {
	ok, err := a.q.EntityRelationTargetIsReadable(ctx, generated.EntityRelationTargetIsReadableParams{
		TargetID:         targetID,
		TargetType:       targetType,
		ReadableSpaceIds: nonNilUUIDs(readableSpaceIDs),
	})
	if err != nil {
		return false, fmt.Errorf("relation adapter target readable: %w", err)
	}
	return ok, nil
}

// ListForEntity returns every relation touching the entity, in both directions,
// with far sides resolved only where the caller may read them.
//
// The type-probing shim this replaced ran the query as 'project_item' and, on
// an empty result, ran it again as 'ticket' — so a caller that did not know the
// entity's type got an answer anyway, by guessing. The type is now required,
// because the route always knows it.
func (a *RelationAdapter) ListForEntity(ctx context.Context, entityID uuid.UUID, entityType string, readableSpaceIDs []uuid.UUID) ([]*projects.Relation, error) {
	rows, err := a.q.ListEntityRelationsForEntity(ctx, generated.ListEntityRelationsForEntityParams{
		EntityID:         entityID,
		EntityType:       entityType,
		ReadableSpaceIds: nonNilUUIDs(readableSpaceIDs),
	})
	if err != nil {
		return nil, fmt.Errorf("relation adapter list for entity: %w", err)
	}
	return dbEntityRelationRowsToRelations(rows), nil
}

// DeleteInSpace removes a relation the given space touches, and nothing else.
//
// A relation neither of whose endpoints lives in spaceID is left alone and
// reported as success, which is the same answer an id that never existed gets.
// See the query for why silence rather than a 404 is the right shape here.
func (a *RelationAdapter) DeleteInSpace(ctx context.Context, id, spaceID uuid.UUID) error {
	if err := a.q.DeleteEntityRelationInSpace(ctx, generated.DeleteEntityRelationInSpaceParams{
		RelationID: id,
		SpaceID:    spaceID,
	}); err != nil {
		return fmt.Errorf("relation adapter delete: %w", err)
	}
	return nil
}

// dbEntityRelationRowsToRelations maps gated rows onto the viewer-facing type.
//
// FarID.Valid is the single readability signal, and it is the query's answer
// rather than this function's: the far side joined, or it did not. Every other
// far field is copied only inside that branch, so an unreadable relation cannot
// acquire an identity through a mapping mistake here.
func dbEntityRelationRowsToRelations(rows []generated.ListEntityRelationsForEntityRow) []*projects.Relation {
	result := make([]*projects.Relation, len(rows))
	for i, row := range rows {
		direction := projects.DirectionIncoming
		if row.IsOutgoing {
			direction = projects.DirectionOutgoing
		}
		rel := &projects.Relation{
			ID:        row.ID,
			Kind:      row.Kind,
			Direction: direction,
		}
		if row.FarID.Valid {
			farID := uuid.UUID(row.FarID.Bytes)
			rel.FarReadable = true
			rel.FarID = &farID
			rel.FarType = row.FarType
			rel.FarTitle = row.FarTitle
			rel.FarStatus = row.FarStatus
			if row.FarSpaceID.Valid {
				farSpaceID := uuid.UUID(row.FarSpaceID.Bytes)
				rel.FarSpaceID = &farSpaceID
			}
		}
		result[i] = rel
	}
	return result
}
