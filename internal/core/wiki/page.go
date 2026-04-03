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
)

// PageStore defines the database operations required by the wiki service.
// This interface allows for mocking in tests and decouples the service
// from the concrete sqlc-generated implementation.
type PageStore interface {
	CreatePage(ctx context.Context, arg generated.CreatePageParams) (generated.Page, error)
	GetPageByID(ctx context.Context, id uuid.UUID) (generated.Page, error)
	UpdatePageContent(ctx context.Context, arg generated.UpdatePageContentParams) (generated.Page, error)
	UpdatePagePosition(ctx context.Context, arg generated.UpdatePagePositionParams) error
	SoftDeletePage(ctx context.Context, id uuid.UUID) error
	ListPagesBySpace(ctx context.Context, spaceID uuid.UUID) ([]generated.ListPagesBySpaceRow, error)
	ListRootPagesBySpace(ctx context.Context, spaceID uuid.UUID) ([]generated.ListRootPagesBySpaceRow, error)
	ListChildPages(ctx context.Context, parentID pgtype.UUID) ([]generated.ListChildPagesRow, error)
	CreatePageRevision(ctx context.Context, arg generated.CreatePageRevisionParams) (generated.PageRevision, error)
	GetPageRevision(ctx context.Context, arg generated.GetPageRevisionParams) (generated.PageRevision, error)
	ListPageRevisions(ctx context.Context, pageID uuid.UUID) ([]generated.ListPageRevisionsRow, error)
	SearchPages(ctx context.Context, arg generated.SearchPagesParams) ([]generated.SearchPagesRow, error)
}

// Service provides wiki operations: page CRUD, tree navigation, versioning,
// search, and markdown rendering.
type Service struct {
	store    PageStore
	renderer *Renderer
}

// NewService creates a new wiki Service with the given store.
func NewService(store PageStore) *Service {
	return &Service{
		store:    store,
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

// CreatePage creates a new wiki page, validates inputs, and stores
// the initial revision for version history.
func (s *Service) CreatePage(ctx context.Context, input CreatePageInput) (generated.Page, error) {
	if err := validateCreateInput(input); err != nil {
		return generated.Page{}, fmt.Errorf("validating create input: %w", err)
	}

	pageID := uuid.New()

	var parentID pgtype.UUID
	if input.ParentID != nil {
		parentID = pgtype.UUID{Bytes: *input.ParentID, Valid: true}
	}

	page, err := s.store.CreatePage(ctx, generated.CreatePageParams{
		ID:       pageID,
		SpaceID:  input.SpaceID,
		ParentID: parentID,
		Title:    input.Title,
		Content:  input.Content,
		AuthorID: input.AuthorID,
		Position: input.Position,
	})
	if err != nil {
		return generated.Page{}, fmt.Errorf("creating page: %w", err)
	}

	// Store the initial revision for history.
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
// Returns ErrVersionConflict if the expected version does not match (409).
// A new revision is created on every successful update.
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
			// Either the page doesn't exist or the version didn't match.
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

	// Store revision for the updated version.
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

// MovePageInput holds parameters for moving a page in the tree.
type MovePageInput struct {
	PageID   uuid.UUID
	ParentID *uuid.UUID
	Position int32
}

// MovePage changes a page's parent and/or position in the tree.
func (s *Service) MovePage(ctx context.Context, input MovePageInput) error {
	var parentID pgtype.UUID
	if input.ParentID != nil {
		parentID = pgtype.UUID{Bytes: *input.ParentID, Valid: true}
	}

	if err := s.store.UpdatePagePosition(ctx, generated.UpdatePagePositionParams{
		ID:       input.PageID,
		ParentID: parentID,
		Position: input.Position,
	}); err != nil {
		return fmt.Errorf("moving page: %w", err)
	}
	return nil
}

// DeletePage performs a soft delete on a page.
func (s *Service) DeletePage(ctx context.Context, id uuid.UUID) error {
	if err := s.store.SoftDeletePage(ctx, id); err != nil {
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
