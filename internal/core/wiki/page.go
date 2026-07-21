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
)

// PageStore defines the database operations required by the wiki service.
type PageStore interface {
	CreatePage(ctx context.Context, arg generated.CreatePageParams) (generated.Page, error)
	GetPageByID(ctx context.Context, id uuid.UUID) (generated.Page, error)
	UpdatePageContent(ctx context.Context, arg generated.UpdatePageContentParams) (generated.Page, error)
	ListPagesBySpace(ctx context.Context, spaceID uuid.UUID) ([]generated.ListPagesBySpaceRow, error)
	ListRootPagesBySpace(ctx context.Context, spaceID uuid.UUID) ([]generated.ListRootPagesBySpaceRow, error)
	ListChildPages(ctx context.Context, parentID pgtype.UUID) ([]generated.ListChildPagesRow, error)
	CreatePageRevision(ctx context.Context, arg generated.CreatePageRevisionParams) (generated.PageRevision, error)
	GetPageRevision(ctx context.Context, arg generated.GetPageRevisionParams) (generated.PageRevision, error)
	ListPageRevisions(ctx context.Context, pageID uuid.UUID) ([]generated.ListPageRevisionsRow, error)
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
type ContentTxStore interface {
	MovePageTx(ctx context.Context, in MovePageInput) (MovePageTxResult, error)
	DeletePageAndRevokeShares(ctx context.Context, pageID, actorID uuid.UUID) (int64, error)
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
		parent, err := s.store.GetPageByID(ctx, *input.ParentID)
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

// GetPage retrieves a page by ID.
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

// UpdatePageInput holds parameters for updating page content.
type UpdatePageInput struct {
	PageID          uuid.UUID
	ExpectedVersion int32
	Title           string
	Content         string
	AuthorID        uuid.UUID
}

// UpdatePage updates a page's title and content using optimistic locking.
// Returns ErrVersionConflict if the expected version does not match.
func (s *Service) UpdatePage(ctx context.Context, input UpdatePageInput) (generated.Page, error) {
	if strings.TrimSpace(input.Title) == "" {
		return generated.Page{}, ErrEmptyTitle
	}

	page, err := s.store.UpdatePageContent(ctx, generated.UpdatePageContentParams{
		ID:      input.PageID,
		Version: input.ExpectedVersion,
		Title:   input.Title,
		Content: input.Content,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, getErr := s.store.GetPageByID(ctx, input.PageID)
			if getErr != nil {
				if errors.Is(getErr, pgx.ErrNoRows) {
					return generated.Page{}, ErrPageNotFound
				}
				return generated.Page{}, fmt.Errorf("checking page existence: %w", getErr)
			}
			return generated.Page{}, ErrVersionConflict
		}
		return generated.Page{}, fmt.Errorf("updating page content: %w", err)
	}

	_, err = s.store.CreatePageRevision(ctx, generated.CreatePageRevisionParams{
		ID:       uuid.New(),
		PageID:   page.ID,
		Version:  page.Version,
		Title:    page.Title,
		Content:  page.Content,
		AuthorID: input.AuthorID,
	})
	if err != nil {
		return generated.Page{}, fmt.Errorf("creating revision: %w", err)
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
		return MovePageTxResult{}, err
	}
	return res, nil
}

// DeletePage soft-deletes a page and revokes its shares in the same
// transaction (ADR-0008 rule 10). actorID attributes the share.revoked
// audit rows.
func (s *Service) DeletePage(ctx context.Context, id, actorID uuid.UUID) error {
	if _, err := s.tx.DeletePageAndRevokeShares(ctx, id, actorID); err != nil {
		return err
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
