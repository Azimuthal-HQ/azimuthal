// Package wiki implements wiki/docs: page tree, markdown rendering,
// version history, and conflict detection.
package wiki

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// Common errors returned by wiki operations.
var (
	// ErrPageNotFound is returned when a page cannot be found.
	ErrPageNotFound = errors.New("page not found")

	// ErrVersionConflict is returned when an update fails due to a version mismatch.
	ErrVersionConflict = errors.New("version conflict: page was modified by another user")

	// ErrEmptyTitle is returned when a page title is blank.
	ErrEmptyTitle = errors.New("page title must not be empty")

	// ErrInvalidSpaceID is returned when a nil space ID is provided.
	ErrInvalidSpaceID = errors.New("space ID must not be empty")

	// ErrInvalidAuthorID is returned when a nil author ID is provided.
	ErrInvalidAuthorID = errors.New("author ID must not be empty")

	// ErrTargetSpaceNotFound is returned when a move names a target space
	// that does not exist live in the org.
	ErrTargetSpaceNotFound = errors.New("target space not found")

	// ErrParentPageNotFound is returned when a move names a parent page
	// that does not exist live.
	ErrParentPageNotFound = errors.New("parent page not found")

	// ErrParentNotInTargetSpace is returned when a move's parent page lives
	// in a different space than the move's target.
	ErrParentNotInTargetSpace = errors.New("parent page is not in the target space")

	// ErrPageMoveCycle is returned when a move would place a page beneath
	// itself or one of its own descendants.
	ErrPageMoveCycle = errors.New("cannot move a page beneath itself or its descendants")

	// ErrPageIsDocumentBacked is returned when the markdown update path is
	// aimed at a page that already holds a stored document. The markdown
	// path writes `content` and leaves `doc` untouched, so accepting the
	// write would leave the two representations describing different pages
	// — and `doc` is the authoritative one (ADR-0012), so the author's
	// markdown would be silently discarded on the next document read.
	ErrPageIsDocumentBacked = errors.New("page is edited as a document: publish through the document editor")
)

// PageStore defines the database operations required by the wiki service.
type PageStore interface {
	CreatePage(ctx context.Context, arg generated.CreatePageParams) (generated.Page, error)
	// GetPageByID is UNSCOPED. It stays for the entity-share read path
	// (ADR-0008), where share coverage is what authorises the read and the
	// caller holds no access to the page's space at all.
	GetPageByID(ctx context.Context, id uuid.UUID) (generated.Page, error)
	// GetPageInSpace reconciles the page against a space. A route of the shape
	// /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID} proves {spaceID} readable and
	// proves nothing whatever about {pageID}, so a page id on its own must not be
	// enough to load a page — every space-scoped route uses this one.
	GetPageInSpace(ctx context.Context, arg generated.GetPageInSpaceParams) (generated.Page, error)
	ListPagesBySpace(ctx context.Context, spaceID uuid.UUID) ([]generated.ListPagesBySpaceRow, error)
	ListRootPagesBySpace(ctx context.Context, spaceID uuid.UUID) ([]generated.ListRootPagesBySpaceRow, error)
	ListChildPages(ctx context.Context, parentID pgtype.UUID) ([]generated.ListChildPagesRow, error)
	CreatePageRevision(ctx context.Context, arg generated.CreatePageRevisionParams) (generated.PageRevision, error)
	GetPageRevision(ctx context.Context, arg generated.GetPageRevisionParams) (generated.PageRevision, error)
	// ListPageRevisions carries the space because page_revisions has no space of
	// its own: the ledger is readable exactly when its page is, and the query
	// joins through the page to say so.
	ListPageRevisions(ctx context.Context, arg generated.ListPageRevisionsParams) ([]generated.ListPageRevisionsRow, error)
	SearchPages(ctx context.Context, arg generated.SearchPagesParams) ([]generated.SearchPagesRow, error)
}

// MovePageTxResult reports what a move did.
type MovePageTxResult struct {
	// CrossSpace is true when the page changed spaces.
	CrossSpace bool
	// RevokedShares is how many active shares the move revoked (always 0
	// for in-space moves).
	RevokedShares int64
}

// ContentTxStore is the transactional seam for the mutations that carry
// share invariants (ADR-0008 rules 9 and 10): moving a page updates the
// whole subtree and — across spaces — revokes the subtree's shares in the
// SAME transaction; deleting a page revokes its shares in the SAME
// transaction. The share.revoked audit rows ride in those transactions too,
// following the P2.5 bulk-grant precedent: the trail is part of the
// atomicity contract, not best-effort.
//
// UpdatePageContentTx joined it for the same reason: the markdown save is a
// page row and a history row that have to be one write, and the guard that
// decides whether the write is allowed at all has to see the row it is
// guarding.
type ContentTxStore interface {
	MovePageTx(ctx context.Context, in MovePageInput) (MovePageTxResult, error)
	DeletePageAndRevokeShares(ctx context.Context, pageID, actorID uuid.UUID) (int64, error)
	UpdatePageContentTx(ctx context.Context, in UpdatePageInput) (generated.Page, error)
}

// Service provides wiki operations: page CRUD, tree navigation, versioning,
// search, and markdown rendering.
type Service struct {
	store    PageStore
	tx       ContentTxStore
	renderer *Renderer
}

// NewService creates a new wiki Service. The ContentTxStore is required —
// move and delete run through it so the ADR-0008 share invariants cannot be
// skipped by forgetting a wiring step.
func NewService(store PageStore, tx ContentTxStore) *Service {
	return &Service{
		store:    store,
		tx:       tx,
		renderer: NewRenderer(),
	}
}

// CreatePageInput holds parameters for creating a new page.
type CreatePageInput struct {
	SpaceID  uuid.UUID
	ParentID *uuid.UUID
	Title    string
	Content  string
	AuthorID uuid.UUID
	Position int32
}

// CreatePage creates a new wiki page and stores the initial revision.
// The materialized path is computed from the parent's path before insert.
func (s *Service) CreatePage(ctx context.Context, input CreatePageInput) (generated.Page, error) {
	if err := validateCreateInput(input); err != nil {
		return generated.Page{}, fmt.Errorf("validating create input: %w", err)
	}

	pageID := uuid.New()

	var parentID pgtype.UUID
	var path string
	if input.ParentID != nil {
		parentID = pgtype.UUID{Bytes: *input.ParentID, Valid: true}
		// Reconciled against the space the page is being created in. The route
		// proved {spaceID} writable and proved nothing about the parent id in the
		// body, and the parent's materialised path becomes this page's prefix — so
		// an unscoped read here both confirmed that a foreign page exists and
		// returned its path inside the created page's own.
		parent, err := s.store.GetPageInSpace(ctx, generated.GetPageInSpaceParams{
			PageID:  *input.ParentID,
			SpaceID: input.SpaceID,
		})
		// A parent_id naming nothing is the caller's mistake, not the server's.
		// Unmapped, this returned a bare pgx.ErrNoRows that no arm of
		// handleWikiError matched, so the request answered 500 and echoed
		// "fetching parent page: no rows in result set" (known-issues #24). The
		// sentinel already existed for the move path; the create path is now
		// the second producer of it, so the two routes answer the same status
		// for the same condition.
		if errors.Is(err, pgx.ErrNoRows) {
			return generated.Page{}, ErrParentPageNotFound
		}
		if err != nil {
			return generated.Page{}, fmt.Errorf("fetching parent page: %w", err)
		}
		path = parent.Path + "." + pageID.String()
	} else {
		path = pageID.String()
	}

	page, err := s.store.CreatePage(ctx, generated.CreatePageParams{
		ID:       pageID,
		SpaceID:  input.SpaceID,
		ParentID: parentID,
		Title:    input.Title,
		Content:  input.Content,
		AuthorID: input.AuthorID,
		Position: input.Position,
		Path:     path,
	})
	if err != nil {
		return generated.Page{}, fmt.Errorf("creating page: %w", err)
	}

	_, err = s.store.CreatePageRevision(ctx, generated.CreatePageRevisionParams{
		ID:       uuid.New(),
		PageID:   page.ID,
		Version:  page.Version,
		Title:    page.Title,
		Content:  page.Content,
		AuthorID: page.AuthorID,
	})
	if err != nil {
		return generated.Page{}, fmt.Errorf("creating initial revision: %w", err)
	}

	return page, nil
}

// GetPage retrieves a page by ID, without reference to any space.
//
// UNSCOPED, and deliberately so: the caller is the entity-share read path
// (internal/core/api/shares/reader.go), where an ADR-0008 share is what
// authorises the read and the reader holds no access to the page's space.
// A space-scoped route must use [Service.GetPageInSpace] instead.
func (s *Service) GetPage(ctx context.Context, id uuid.UUID) (generated.Page, error) {
	page, err := s.store.GetPageByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return generated.Page{}, ErrPageNotFound
		}
		return generated.Page{}, fmt.Errorf("getting page: %w", err)
	}
	return page, nil
}

// GetPageInSpace retrieves a page that lives in the given space.
//
// The space parameter is the authorisation, not a filter. A route shaped
// /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID} runs its space-read guard against
// {spaceID} and establishes nothing at all about {pageID}; until the two were
// reconciled, a member of any one space could read every page in every other
// space and every other organisation by id.
//
// A page in another space reports ErrPageNotFound, which is exactly what a page
// that does not exist reports. Answering "it exists but you may not have it"
// would be the same disclosure wearing a different status code.
func (s *Service) GetPageInSpace(ctx context.Context, spaceID, id uuid.UUID) (generated.Page, error) {
	page, err := s.store.GetPageInSpace(ctx, generated.GetPageInSpaceParams{
		PageID:  id,
		SpaceID: spaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return generated.Page{}, ErrPageNotFound
		}
		return generated.Page{}, fmt.Errorf("getting page: %w", err)
	}
	return page, nil
}

// UpdatePageInput holds parameters for updating page content.
type UpdatePageInput struct {
	PageID uuid.UUID
	// SpaceID is the space the route named, and it scopes the page re-read that
	// builds the 409 conflict body — see [Service.UpdatePageOrConflict]. That body
	// carries the whole current page, content included, so it is a read path in
	// its own right and cannot be keyed on the page id alone.
	SpaceID         uuid.UUID
	ExpectedVersion int32
	Title           string
	Content         string
	AuthorID        uuid.UUID
}

// UpdatePage updates a page's title and content using optimistic locking.
// Returns ErrVersionConflict if the expected version does not match,
// ErrPageNotFound if the page is gone, and ErrPageIsDocumentBacked if the
// page has moved to the document model and can no longer take a markdown
// write.
//
// The page row and its history row commit together — see
// [ContentTxStore.UpdatePageContentTx]. This service method holds only the
// validation that needs no database.
func (s *Service) UpdatePage(ctx context.Context, input UpdatePageInput) (generated.Page, error) {
	if strings.TrimSpace(input.Title) == "" {
		return generated.Page{}, ErrEmptyTitle
	}
	page, err := s.tx.UpdatePageContentTx(ctx, input)
	if err != nil {
		return generated.Page{}, fmt.Errorf("updating page: %w", err)
	}
	return page, nil
}

// MovePageInput holds parameters for moving a page: within its space
// (reparent/reposition) or — when TargetSpaceID differs from the page's
// current space — across spaces, which revokes every active share on the
// moved subtree in the same transaction (ADR-0008 rule 9).
type MovePageInput struct {
	// OrgID scopes every lookup: entities of other orgs do not exist.
	OrgID uuid.UUID
	// TargetSpaceID is where the page lands. Same as the page's current
	// space for an in-space move.
	TargetSpaceID uuid.UUID
	PageID        uuid.UUID
	ParentID      *uuid.UUID
	Position      int32
	// ActorID attributes the move's share.revoked audit rows.
	ActorID uuid.UUID
}

// MovePage moves a page and its subtree, transactionally: root row,
// descendant paths (and space membership, when crossing spaces), and the
// cross-space share revocation all commit or roll back together.
func (s *Service) MovePage(ctx context.Context, input MovePageInput) (MovePageTxResult, error) {
	res, err := s.tx.MovePageTx(ctx, input)
	if err != nil {
		return MovePageTxResult{}, fmt.Errorf("moving page: %w", err)
	}
	return res, nil
}

// DeletePage soft-deletes a page and revokes its shares in the same
// transaction (ADR-0008 rule 10). actorID attributes the share.revoked
// audit rows.
func (s *Service) DeletePage(ctx context.Context, id, actorID uuid.UUID) error {
	if _, err := s.tx.DeletePageAndRevokeShares(ctx, id, actorID); err != nil {
		return fmt.Errorf("deleting page: %w", err)
	}
	return nil
}

func validateCreateInput(input CreatePageInput) error {
	if strings.TrimSpace(input.Title) == "" {
		return ErrEmptyTitle
	}
	if input.SpaceID == uuid.Nil {
		return ErrInvalidSpaceID
	}
	if input.AuthorID == uuid.Nil {
		return ErrInvalidAuthorID
	}
	return nil
}
