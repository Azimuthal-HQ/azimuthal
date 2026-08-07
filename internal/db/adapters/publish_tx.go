package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// PublishPageTx publishes a document version atomically (issue #15).
//
// Three writes that have to be one:
//
//  1. the page row — new title, new document, new markdown projection, version+1
//  2. the history row for the version just created
//  3. the removal of the author's draft, which has now been published
//
// Doing them separately is how the existing markdown save path works, and it has
// two failure modes nobody would notice until they mattered: a page whose history
// skips a version, and a draft that reappears afterwards as unpublished work the
// author already published. Neither is fixable by retrying. So this follows
// shared-surfaces convention B — the atomicity is the contract.
//
// The version guard lives in the UPDATE rather than in a preceding SELECT, so two
// simultaneous publishes cannot both pass a check and then both write. Zero rows
// affected means the page moved on, which the caller turns into the
// reload-or-overwrite conflict.
func (a *ContentTxAdapter) PublishPageTx(ctx context.Context, in wiki.PublishPageTxInput) (generated.Page, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return generated.Page{}, fmt.Errorf("publish page: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	page, err := publishPageRow(ctx, qtx, in)
	if err != nil {
		return generated.Page{}, err
	}

	if _, err := qtx.CreatePageRevisionWithDoc(ctx, generated.CreatePageRevisionWithDocParams{
		ID:       uuid.New(),
		PageID:   page.ID,
		Version:  page.Version,
		Title:    page.Title,
		Content:  page.Content,
		Doc:      page.Doc,
		AuthorID: in.AuthorID,
	}); err != nil {
		return generated.Page{}, fmt.Errorf("publish page: recording revision: %w", err)
	}

	// The draft has been published, so it is no longer unpublished work. Absence
	// is fine: publishing straight from the editor without an autosave having
	// landed is an ordinary sequence.
	if _, err := qtx.DeletePageDraft(ctx, generated.DeletePageDraftParams{
		PageID:   in.PageID,
		AuthorID: in.AuthorID,
	}); err != nil {
		return generated.Page{}, fmt.Errorf("publish page: clearing draft: %w", err)
	}

	// The inline tags the body carries (migration 040). ADD, never replace: the
	// page-level tag list is the authority, and a body that no longer mentions
	// `#foo` is not a request to untag the page. The space is the page row's
	// own, just written above in this transaction, so AddEntityTag's
	// reconciliation predicate is satisfied by construction.
	for _, tagID := range in.TagIDs {
		if err := qtx.AddEntityTag(ctx, generated.AddEntityTagParams{
			EntityType: string(tags.EntityPage),
			EntityID:   in.PageID,
			SpaceID:    page.SpaceID,
			TagID:      tagID,
		}); err != nil {
			return generated.Page{}, fmt.Errorf("publish page: tagging: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return generated.Page{}, fmt.Errorf("publish page: commit: %w", err)
	}
	return page, nil
}

// publishPageRow writes the page row, guarded by the base version unless the
// caller has explicitly chosen to overwrite.
func publishPageRow(ctx context.Context, qtx *generated.Queries, in wiki.PublishPageTxInput) (generated.Page, error) {
	if in.Overwrite {
		page, err := qtx.OverwritePageDocument(ctx, generated.OverwritePageDocumentParams{
			ID:      in.PageID,
			Title:   in.Title,
			Content: in.Content,
			Doc:     in.Doc,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return generated.Page{}, wiki.ErrPageNotFound
		}
		if err != nil {
			return generated.Page{}, fmt.Errorf("publish page: overwriting: %w", err)
		}
		return page, nil
	}

	page, err := qtx.PublishPageDocument(ctx, generated.PublishPageDocumentParams{
		ID:      in.PageID,
		Version: in.BaseVersion,
		Title:   in.Title,
		Content: in.Content,
		Doc:     in.Doc,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Either the page is gone or its version moved on. The caller
		// distinguishes them by re-reading; ErrVersionConflict is the more
		// informative default because the page existed a moment ago.
		return generated.Page{}, wiki.ErrVersionConflict
	}
	if err != nil {
		return generated.Page{}, fmt.Errorf("publish page: %w", err)
	}
	return page, nil
}
