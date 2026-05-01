package wiki

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// ConflictDetail provides information about a version conflict so the
// caller can present merge guidance to the user.
type ConflictDetail struct {
	// PageID is the page that has the conflict.
	PageID uuid.UUID `json:"page_id"`

	// ExpectedVersion is what the caller thought the current version was.
	ExpectedVersion int32 `json:"expected_version"`

	// CurrentPage is the page's current state in the database.
	CurrentPage generated.Page `json:"current_page"`

	// Message is a human-readable explanation of the conflict.
	Message string `json:"message"`
}

// UpdatePageOrConflict attempts to update a page. On version mismatch it
// returns a ConflictDetail with the current page state instead of a bare error,
// giving the caller everything needed to present a merge UI (HTTP 409).
func (s *Service) UpdatePageOrConflict(ctx context.Context, input UpdatePageInput) (generated.Page, *ConflictDetail, error) {
	page, err := s.UpdatePage(ctx, input)
	if err == nil {
		return page, nil, nil
	}

	if !errors.Is(err, ErrVersionConflict) {
		return generated.Page{}, nil, err
	}

	// Fetch the current state so the caller can show both versions.
	current, getErr := s.store.GetPageByID(ctx, input.PageID)
	if getErr != nil {
		return generated.Page{}, nil, fmt.Errorf("fetching current page after conflict: %w", getErr)
	}

	conflict := &ConflictDetail{
		PageID:          input.PageID,
		ExpectedVersion: input.ExpectedVersion,
		CurrentPage:     current,
		Message: fmt.Sprintf(
			"page was modified: expected version %d but current version is %d — reload and re-apply your changes",
			input.ExpectedVersion, current.Version,
		),
	}

	return generated.Page{}, conflict, ErrVersionConflict
}
