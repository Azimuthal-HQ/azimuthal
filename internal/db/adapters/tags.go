package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// TagAdapter implements tags.Repository over the tags and page_tags tables
// (migration 040).
//
// It holds a pool as well as the queries because [TagAdapter.ReplacePageTags]
// is two statements that have to be one: a delete of what is no longer wanted
// and an insert of what is new. Between them the page carries neither set, and
// a failure there would leave a page's tags half-applied with nothing to say so.
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

// ForPage implements the repository interface.
//
// The space parameter reaches the query rather than being applied here: the
// association table has no space of its own, so the reconciliation is a join
// through the page and belongs in SQL, where it cannot be forgotten by a caller.
func (a *TagAdapter) ForPage(ctx context.Context, pageID, spaceID uuid.UUID) ([]tags.Tag, error) {
	rows, err := a.q.ListTagsForPage(ctx, generated.ListTagsForPageParams{
		PageID:  pageID,
		SpaceID: spaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("tag adapter list for page: %w", err)
	}
	return dbTagsToTags(rows), nil
}

// ReplacePageTags implements the repository interface, in one transaction.
func (a *TagAdapter) ReplacePageTags(ctx context.Context, pageID uuid.UUID, tagIDs []uuid.UUID) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tag adapter replace page tags: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	// A nil slice would make `= ANY(NULL)` NULL rather than false, and the
	// NOT of that is NULL, so the DELETE would match nothing — the empty case
	// would silently keep every tag. An empty non-nil array is what makes
	// "clear this page's tags" delete them.
	keep := tagIDs
	if keep == nil {
		keep = []uuid.UUID{}
	}
	if err := qtx.DeletePageTagsExcept(ctx, generated.DeletePageTagsExceptParams{
		PageID:  pageID,
		KeepIds: keep,
	}); err != nil {
		return fmt.Errorf("tag adapter replace page tags: delete: %w", err)
	}
	if err := addPageTags(ctx, qtx, pageID, tagIDs); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tag adapter replace page tags: commit: %w", err)
	}
	return nil
}

// AddPageTags implements the repository interface.
func (a *TagAdapter) AddPageTags(ctx context.Context, pageID uuid.UUID, tagIDs []uuid.UUID) error {
	return addPageTags(ctx, a.q, pageID, tagIDs)
}

// pageTagWriter is the slice of the generated queries the association writes
// need, so the same code serves both the pool and a transaction.
type pageTagWriter interface {
	AddPageTag(ctx context.Context, arg generated.AddPageTagParams) error
}

func addPageTags(ctx context.Context, q pageTagWriter, pageID uuid.UUID, tagIDs []uuid.UUID) error {
	for _, tagID := range tagIDs {
		if err := q.AddPageTag(ctx, generated.AddPageTagParams{PageID: pageID, TagID: tagID}); err != nil {
			return fmt.Errorf("tag adapter add page tag: %w", err)
		}
	}
	return nil
}

// PagesWithTag implements the repository interface.
func (a *TagAdapter) PagesWithTag(ctx context.Context, tagID uuid.UUID, readableSpaceIDs []uuid.UUID) ([]tags.TaggedPage, error) {
	// Same NULL trap as above, and here it fails the other way: `= ANY(NULL)`
	// is NULL, so the WHERE never matches and a nil readable set returns
	// nothing. That is the fail-closed direction, but relying on a NULL
	// comparison for an authorisation filter is not something to leave implicit.
	readable := readableSpaceIDs
	if readable == nil {
		readable = []uuid.UUID{}
	}
	rows, err := a.q.ListPagesWithTag(ctx, generated.ListPagesWithTagParams{
		TagID:            tagID,
		ReadableSpaceIds: readable,
	})
	if err != nil {
		return nil, fmt.Errorf("tag adapter pages with tag: %w", err)
	}
	out := make([]tags.TaggedPage, 0, len(rows))
	for _, row := range rows {
		out = append(out, tags.TaggedPage{
			PageID:    row.ID,
			SpaceID:   row.SpaceID,
			SpaceName: row.SpaceName,
			SpaceKey:  row.SpaceKey,
			Title:     row.Title,
			Path:      row.Path,
			UpdatedAt: goTime(row.UpdatedAt),
		})
	}
	return out, nil
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
