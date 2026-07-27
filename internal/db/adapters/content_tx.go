package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// ContentTxAdapter owns the entity mutations that carry share invariants
// (ADR-0008 rules 9 and 10): deleting an entity revokes its shares in the
// same transaction; moving a page across spaces revokes the whole moved
// subtree's shares in the same transaction. The share.revoked audit rows
// are written through those transactions too — the P2.5 bulk-grant
// precedent: when the trail is part of the atomicity contract, it commits
// or rolls back with the change, never best-effort.
type ContentTxAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewContentTxAdapter creates a ContentTxAdapter.
func NewContentTxAdapter(pool *pgxpool.Pool) *ContentTxAdapter {
	return &ContentTxAdapter{pool: pool, q: generated.New(pool)}
}

// MovePageTx moves a page (and its whole subtree) transactionally. In-space
// moves reparent and rewrite descendant paths; cross-space moves also flip
// the subtree's space membership and revoke every active share on any moved
// page. The share revocation runs FIRST, while the subtree is still
// addressable by its old space and paths — revoking after the rewrite would
// match nothing and silently leave the shares alive.
func (a *ContentTxAdapter) MovePageTx(ctx context.Context, in wiki.MovePageInput) (wiki.MovePageTxResult, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return wiki.MovePageTxResult{}, fmt.Errorf("move page: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	page, err := qtx.GetPageForUpdate(ctx, in.PageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return wiki.MovePageTxResult{}, wiki.ErrPageNotFound
	}
	if err != nil {
		return wiki.MovePageTxResult{}, fmt.Errorf("move page: locking page: %w", err)
	}
	if err := validateMoveSpaces(ctx, qtx, in, page.SpaceID); err != nil {
		return wiki.MovePageTxResult{}, err
	}

	parentID, newPath, err := resolveMoveParent(ctx, qtx, in, page)
	if err != nil {
		return wiki.MovePageTxResult{}, err
	}

	res := wiki.MovePageTxResult{CrossSpace: page.SpaceID != in.TargetSpaceID}
	pathPattern := access.SubtreeLikePattern(page.Path)

	// ADR-0008 rule 9, before any rewrite: a page shared org-wide must not
	// be draggable into a sensitive space with its shares intact — and any
	// DESCENDANT'S own share is just as dangerous after the move.
	if res.CrossSpace {
		revoked, err := revokeSubtreeSharesTx(ctx, qtx, in.ActorID, page.SpaceID, page.ID, pathPattern)
		if err != nil {
			return wiki.MovePageTxResult{}, err
		}
		res.RevokedShares = revoked
	}

	if err := applyPageMove(ctx, qtx, in, page, parentID, newPath, pathPattern); err != nil {
		return wiki.MovePageTxResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return wiki.MovePageTxResult{}, fmt.Errorf("move page: commit: %w", err)
	}
	return res, nil
}

// applyPageMove rewrites the root row and then every descendant's space and
// path prefix — the two statements that together relocate the subtree.
func applyPageMove(ctx context.Context, qtx *generated.Queries, in wiki.MovePageInput, page generated.Page, parentID pgtype.UUID, newPath, pathPattern string) error {
	if err := qtx.MovePageToSpace(ctx, generated.MovePageToSpaceParams{
		ID:       page.ID,
		SpaceID:  in.TargetSpaceID,
		ParentID: parentID,
		Position: in.Position,
		Path:     newPath,
	}); err != nil {
		return fmt.Errorf("move page: updating root: %w", err)
	}
	if err := qtx.MovePageDescendantsToSpace(ctx, generated.MovePageDescendantsToSpaceParams{
		NewSpaceID:  in.TargetSpaceID,
		NewPrefix:   newPath,
		OldPrefix:   page.Path,
		OldSpaceID:  page.SpaceID,
		PathPattern: pathPattern,
	}); err != nil {
		return fmt.Errorf("move page: updating descendants: %w", err)
	}
	return nil
}

// validateMoveSpaces enforces tenancy: the page's own space and the target
// space must both belong to the caller's org (and the target must be live).
// An out-of-org page reads as ErrPageNotFound; a bad target as
// ErrTargetSpaceNotFound — neither leaks existence.
func validateMoveSpaces(ctx context.Context, qtx *generated.Queries, in wiki.MovePageInput, sourceSpaceID uuid.UUID) error {
	sourceSpace, err := qtx.GetSpaceByID(ctx, sourceSpaceID)
	if err != nil || sourceSpace.OrgID != in.OrgID {
		return wiki.ErrPageNotFound
	}
	targetSpace, err := qtx.GetSpaceByID(ctx, in.TargetSpaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return wiki.ErrTargetSpaceNotFound
	}
	if err != nil {
		return fmt.Errorf("move page: loading target space: %w", err)
	}
	if targetSpace.OrgID != in.OrgID || targetSpace.DeletedAt.Valid {
		return wiki.ErrTargetSpaceNotFound
	}
	return nil
}

// resolveMoveParent validates the requested parent (if any) and computes the
// moved page's new parent id and path. A parent in another space, or one
// inside the moved subtree, is rejected.
func resolveMoveParent(ctx context.Context, qtx *generated.Queries, in wiki.MovePageInput, page generated.Page) (pgtype.UUID, string, error) {
	if in.ParentID == nil {
		return pgtype.UUID{}, in.PageID.String(), nil
	}
	parent, err := qtx.GetPageByID(ctx, *in.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, "", wiki.ErrParentPageNotFound
	}
	if err != nil {
		return pgtype.UUID{}, "", fmt.Errorf("move page: loading parent: %w", err)
	}
	if parent.SpaceID != in.TargetSpaceID {
		return pgtype.UUID{}, "", wiki.ErrParentNotInTargetSpace
	}
	// A parent inside the moved subtree (the page itself included) would
	// graft the subtree onto one of its own descendants.
	if parent.SpaceID == page.SpaceID && access.PathWithinSubtree(parent.Path, page.Path) {
		return pgtype.UUID{}, "", wiki.ErrPageMoveCycle
	}
	return pgtype.UUID{Bytes: *in.ParentID, Valid: true}, parent.Path + "." + in.PageID.String(), nil
}

// revokeSubtreeSharesTx revokes every active share on the page and its
// descendants and writes their share.revoked audit rows, all through the
// move's transaction. Returns the number revoked.
func revokeSubtreeSharesTx(ctx context.Context, qtx *generated.Queries, actorID, spaceID, pageID uuid.UUID, pathPattern string) (int64, error) {
	revoked, err := qtx.RevokeSharesByPageSubtree(ctx, generated.RevokeSharesByPageSubtreeParams{
		SpaceID:     spaceID,
		PageID:      pageID,
		PathPattern: pathPattern,
	})
	if err != nil {
		return 0, fmt.Errorf("move page: revoking subtree shares: %w", err)
	}
	for _, share := range revoked {
		if err := writeShareRevokedTx(ctx, qtx, actorID, share, "entity_moved"); err != nil {
			return 0, fmt.Errorf("move page: %w", err)
		}
	}
	return int64(len(revoked)), nil
}

// UpdatePageContentTx is the markdown save path, as one transaction.
//
// Two writes that have to be one: the page row (title, content, version+1)
// and the history row for the version just created. Run separately — as they
// were until this — a failure between them leaves a page whose history skips
// a version, permanently, because nothing retries a half-finished save. That
// is the same failure PublishPageTx exists to prevent on the document path,
// and it gets the same answer (shared-surfaces convention B).
//
// The row is locked before anything is decided, which is what makes the three
// refusals distinguishable instead of one undifferentiated "zero rows
// affected":
//
//   - the page is gone           → ErrPageNotFound
//   - the page holds a document  → ErrPageIsDocumentBacked
//   - the version moved on       → ErrVersionConflict
//
// The document refusal is the load-bearing one. The markdown UPDATE writes
// `content` and never touches `doc`, and `doc` is authoritative once it
// exists (ADR-0012) — so a markdown save against a document-backed page
// reports success, bumps the version, writes a history row, and is then
// invisible to every reader that goes through the document. The write is
// refused rather than reconciled: converting markdown back into a document
// here would silently drop whatever the document holds that markdown cannot
// express, which is the loss ADR-0012 exists to prevent.
//
// The test is strictly `doc IS NOT NULL`. A page that has only ever held
// markdown — including one being edited in the new editor but not yet
// published — has a NULL doc and keeps taking markdown saves.
func (a *ContentTxAdapter) UpdatePageContentTx(ctx context.Context, in wiki.UpdatePageInput) (generated.Page, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return generated.Page{}, fmt.Errorf("update page content: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	current, err := qtx.GetPageForUpdate(ctx, in.PageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return generated.Page{}, wiki.ErrPageNotFound
	}
	if err != nil {
		return generated.Page{}, fmt.Errorf("update page content: locking page: %w", err)
	}
	if current.Doc != nil {
		return generated.Page{}, wiki.ErrPageIsDocumentBacked
	}
	if current.Version != in.ExpectedVersion {
		return generated.Page{}, wiki.ErrVersionConflict
	}

	page, err := qtx.UpdatePageContent(ctx, generated.UpdatePageContentParams{
		ID:      in.PageID,
		Version: in.ExpectedVersion,
		Title:   in.Title,
		Content: in.Content,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Unreachable while the row lock above is held — kept so a future
		// change that drops the lock degrades to a conflict rather than to a
		// 500 with a pgx error in it.
		return generated.Page{}, wiki.ErrVersionConflict
	}
	if err != nil {
		return generated.Page{}, fmt.Errorf("update page content: %w", err)
	}

	if _, err := qtx.CreatePageRevision(ctx, generated.CreatePageRevisionParams{
		ID:       uuid.New(),
		PageID:   page.ID,
		Version:  page.Version,
		Title:    page.Title,
		Content:  page.Content,
		AuthorID: in.AuthorID,
	}); err != nil {
		return generated.Page{}, fmt.Errorf("update page content: recording revision: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return generated.Page{}, fmt.Errorf("update page content: commit: %w", err)
	}
	return page, nil
}

// DeletePageAndRevokeShares soft-deletes the page and revokes its shares in
// one transaction (ADR-0008 rule 10).
func (a *ContentTxAdapter) DeletePageAndRevokeShares(ctx context.Context, pageID, actorID uuid.UUID) (int64, error) {
	return a.deleteEntityAndRevokeShares(ctx, access.ShareEntityPage, pageID, actorID,
		func(ctx context.Context, qtx *generated.Queries) error {
			return qtx.SoftDeletePage(ctx, pageID)
		})
}

// DeleteTicketAndRevokeShares soft-deletes the ticket and revokes its
// shares in one transaction.
func (a *ContentTxAdapter) DeleteTicketAndRevokeShares(ctx context.Context, ticketID, actorID uuid.UUID) error {
	_, err := a.deleteEntityAndRevokeShares(ctx, access.ShareEntityTicket, ticketID, actorID,
		func(ctx context.Context, qtx *generated.Queries) error {
			return qtx.SoftDeleteTicket(ctx, ticketID)
		})
	return err
}

// DeleteItemAndRevokeShares soft-deletes the project item and revokes its
// shares in one transaction.
func (a *ContentTxAdapter) DeleteItemAndRevokeShares(ctx context.Context, itemID, actorID uuid.UUID) error {
	_, err := a.deleteEntityAndRevokeShares(ctx, access.ShareEntityProjectItem, itemID, actorID,
		func(ctx context.Context, qtx *generated.Queries) error {
			return qtx.SoftDeleteProjectItem(ctx, itemID)
		})
	return err
}

// deleteEntityAndRevokeShares is the shared delete transaction: soft-delete
// via del, revoke the entity's shares, write their audit rows, commit.
func (a *ContentTxAdapter) deleteEntityAndRevokeShares(ctx context.Context, entityType string, entityID, actorID uuid.UUID, del func(context.Context, *generated.Queries) error) (int64, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete %s: begin: %w", entityType, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	if err := del(ctx, qtx); err != nil {
		return 0, fmt.Errorf("delete %s: %w", entityType, err)
	}

	revoked, err := qtx.RevokeSharesByEntity(ctx, generated.RevokeSharesByEntityParams{
		EntityType: entityType,
		EntityID:   entityID,
	})
	if err != nil {
		return 0, fmt.Errorf("delete %s: revoking shares: %w", entityType, err)
	}
	for _, share := range revoked {
		if err := writeShareRevokedTx(ctx, qtx, actorID, share, "entity_deleted"); err != nil {
			return 0, fmt.Errorf("delete %s: %w", entityType, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("delete %s: commit: %w", entityType, err)
	}
	return int64(len(revoked)), nil
}

// writeShareRevokedTx records one share.revoked event through the mutation's
// own transaction. Failing to record it fails the mutation.
func writeShareRevokedTx(ctx context.Context, qtx *generated.Queries, actorID uuid.UUID, share generated.EntityShare, reason string) error {
	meta := map[string]string{
		"entity_type": share.EntityType,
		"entity_id":   share.EntityID.String(),
		"space_id":    share.SpaceID.String(),
		"audience":    share.Audience,
		"cascade":     fmt.Sprintf("%t", share.Cascade),
		"reason":      reason,
	}
	if share.AudienceID.Valid {
		meta["audience_id"] = uuid.UUID(share.AudienceID.Bytes).String()
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshalling share audit payload: %w", err)
	}
	if _, err := qtx.CreateAuditEvent(ctx, generated.CreateAuditEventParams{
		ID:         uuid.New(),
		OrgID:      share.OrgID,
		ActorID:    pgtype.UUID{Bytes: actorID, Valid: true},
		Action:     string(audit.EventTypeShareRevoked),
		EntityKind: "share",
		EntityID:   share.ID,
		Payload:    payload,
	}); err != nil {
		return fmt.Errorf("writing share.revoked audit event: %w", err)
	}
	return nil
}
