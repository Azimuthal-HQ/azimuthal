package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// TagAdapter implements tags.Repository over the tags and entity_tags tables
// (migrations 040, 055).
//
// It holds a pool as well as the queries because
// [TagAdapter.ReplaceEntityTags] is two statements that have to be one: a
// delete of what is no longer wanted and an insert of what is new. Between
// them the entity carries neither set, and a failure there would leave its
// tags half-applied with nothing to say so.
type TagAdapter struct {
	q    *generated.Queries
	pool *pgxpool.Pool
}

// NewTagAdapter creates a TagAdapter.
func NewTagAdapter(q *generated.Queries, pool *pgxpool.Pool) *TagAdapter {
	return &TagAdapter{q: q, pool: pool}
}

// ListByOrg implements the repository interface.
func (a *TagAdapter) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]tags.Tag, error) {
	rows, err := a.q.ListTagsByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("tag adapter list by org: %w", err)
	}
	return dbTagsToTags(rows), nil
}

// GetByOrgSlug implements the repository interface.
func (a *TagAdapter) GetByOrgSlug(ctx context.Context, orgID uuid.UUID, slug string) (tags.Tag, error) {
	row, err := a.q.GetTagByOrgSlug(ctx, generated.GetTagByOrgSlugParams{OrgID: orgID, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return tags.Tag{}, tags.ErrNotFound
	}
	if err != nil {
		return tags.Tag{}, fmt.Errorf("tag adapter get by slug: %w", err)
	}
	return dbTagToTag(row), nil
}

// Upsert implements the repository interface.
func (a *TagAdapter) Upsert(ctx context.Context, orgID uuid.UUID, slug, name string) (tags.Tag, error) {
	row, err := a.q.UpsertTag(ctx, generated.UpsertTagParams{OrgID: orgID, Slug: slug, Name: name})
	if err != nil {
		return tags.Tag{}, fmt.Errorf("tag adapter upsert: %w", err)
	}
	return dbTagToTag(row), nil
}

// ForEntity implements the repository interface.
//
// The space reaches the query rather than being applied here: the association
// table has no space of its own, so the reconciliation is a three-arm EXISTS
// against the entity's own table and belongs in SQL, where it cannot be
// forgotten by a caller.
func (a *TagAdapter) ForEntity(ctx context.Context, ref tags.EntityRef) ([]tags.Tag, error) {
	rows, err := a.q.ListTagsForEntity(ctx, generated.ListTagsForEntityParams{
		EntityType: string(ref.Type),
		EntityID:   ref.ID,
		SpaceID:    ref.SpaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("tag adapter list for entity: %w", err)
	}
	return dbTagsToTags(rows), nil
}

// EntityInSpace implements the repository interface.
func (a *TagAdapter) EntityInSpace(ctx context.Context, ref tags.EntityRef) (bool, error) {
	ok, err := a.q.EntityTagTargetInSpace(ctx, generated.EntityTagTargetInSpaceParams{
		EntityType: string(ref.Type),
		EntityID:   ref.ID,
		SpaceID:    ref.SpaceID,
	})
	if err != nil {
		return false, fmt.Errorf("tag adapter entity in space: %w", err)
	}
	return ok, nil
}

// ReplaceEntityTags implements the repository interface, in one transaction.
func (a *TagAdapter) ReplaceEntityTags(ctx context.Context, ref tags.EntityRef, tagIDs []uuid.UUID) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tag adapter replace entity tags: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	// A nil slice would make `= ANY(NULL)` NULL rather than false, and the
	// NOT of that is NULL, so the DELETE would match nothing — the empty case
	// would silently keep every tag. An empty non-nil array is what makes
	// "clear this entity's tags" delete them.
	keep := tagIDs
	if keep == nil {
		keep = []uuid.UUID{}
	}
	if err := qtx.DeleteEntityTagsExcept(ctx, generated.DeleteEntityTagsExceptParams{
		EntityType: string(ref.Type),
		EntityID:   ref.ID,
		SpaceID:    ref.SpaceID,
		KeepIds:    keep,
	}); err != nil {
		return fmt.Errorf("tag adapter replace entity tags: delete: %w", err)
	}
	if err := addEntityTags(ctx, qtx, ref, tagIDs); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tag adapter replace entity tags: commit: %w", err)
	}
	return nil
}

// AddEntityTags implements the repository interface.
func (a *TagAdapter) AddEntityTags(ctx context.Context, ref tags.EntityRef, tagIDs []uuid.UUID) error {
	return addEntityTags(ctx, a.q, ref, tagIDs)
}

// entityTagWriter is the slice of the generated queries the association writes
// need, so the same code serves both the pool and a transaction.
type entityTagWriter interface {
	AddEntityTag(ctx context.Context, arg generated.AddEntityTagParams) error
}

func addEntityTags(ctx context.Context, q entityTagWriter, ref tags.EntityRef, tagIDs []uuid.UUID) error {
	for _, tagID := range tagIDs {
		if err := q.AddEntityTag(ctx, generated.AddEntityTagParams{
			EntityType: string(ref.Type),
			EntityID:   ref.ID,
			SpaceID:    ref.SpaceID,
			TagID:      tagID,
		}); err != nil {
			return fmt.Errorf("tag adapter add entity tag: %w", err)
		}
	}
	return nil
}

// EntitiesWithTag implements the repository interface.
func (a *TagAdapter) EntitiesWithTag(ctx context.Context, tagID uuid.UUID, readableSpaceIDs []uuid.UUID) ([]tags.TaggedEntity, error) {
	// Same NULL trap as ReplaceEntityTags, and here it fails the other way:
	// `= ANY(NULL)` is NULL, so the WHERE never matches and a nil readable set
	// returns nothing. That is the fail-closed direction, but relying on a NULL
	// comparison for an authorisation filter is not something to leave implicit.
	readable := readableSpaceIDs
	if readable == nil {
		readable = []uuid.UUID{}
	}
	rows, err := a.q.ListEntitiesWithTag(ctx, generated.ListEntitiesWithTagParams{
		TagID:            tagID,
		ReadableSpaceIds: readable,
	})
	if err != nil {
		return nil, fmt.Errorf("tag adapter entities with tag: %w", err)
	}
	out := make([]tags.TaggedEntity, 0, len(rows))
	for _, row := range rows {
		out = append(out, tags.TaggedEntity{
			EntityType: tags.EntityType(row.EntityType),
			EntityID:   row.ID,
			SpaceID:    row.SpaceID,
			SpaceName:  row.SpaceName,
			SpaceKey:   row.SpaceKey,
			Title:      row.Title,
			Ref:        composeEntityRef(row),
			UpdatedAt:  goTime(row.UpdatedAt),
		})
	}
	return out, nil
}

// composeEntityRef turns a browse row's raw ref parts into the kind's one
// human-readable reference. Each arm is the kind's single existing composition
// site: tickets.ComposeRef for tickets, the stored item_key for project items
// (saved_views.sql records why it is selected rather than re-derived), and the
// materialised path for pages.
func composeEntityRef(row generated.ListEntitiesWithTagRow) string {
	switch tags.EntityType(row.EntityType) {
	case tags.EntityTicket:
		if row.Number == nil {
			// Unreachable: the ticket arm always selects its number. An empty
			// ref beats a panic on a shape this code does not control.
			return ""
		}
		return tickets.ComposeRef(row.SpaceKey, *row.Number)
	case tags.EntityProjectItem:
		return row.ItemKey
	default:
		return row.Path
	}
}

func dbTagToTag(row generated.Tag) tags.Tag {
	return tags.Tag{
		ID:        row.ID,
		OrgID:     row.OrgID,
		Slug:      row.Slug,
		Name:      row.Name,
		CreatedAt: goTime(row.CreatedAt),
	}
}

func dbTagsToTags(rows []generated.Tag) []tags.Tag {
	out := make([]tags.Tag, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbTagToTag(row))
	}
	return out
}
