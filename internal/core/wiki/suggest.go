package wiki

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// PageSuggestion is one page offered to the page-picker typeahead.
//
// SpaceKey and SpaceName are context for the row the operator is choosing
// between — two spaces can each have a page titled "Runbook", and the space
// is how a human tells them apart. SpaceID is what the picker actually
// submits alongside nothing: a relation target is (type, id), and the space
// is only ever display context.
type PageSuggestion struct {
	PageID    uuid.UUID
	Title     string
	SpaceID   uuid.UUID
	SpaceKey  string
	SpaceName string
}

// PageSuggestionStore is the read seam for the page typeahead.
//
// Deliberately separate from PageStore, for the same reason tickets keep
// SuggestionStore separate from TicketRepository: the typeahead reads across
// many spaces at once, which no other page read does — every method on
// PageStore is scoped to a single space or a single page — and widening that
// interface would push a cross-space method onto every implementation of it.
// *generated.Queries satisfies this directly.
type PageSuggestionStore interface {
	// SuggestPages returns at most a bounded page of matches, most recently
	// updated first. Implementations must never widen the space filter.
	SuggestPages(ctx context.Context, arg generated.SuggestPagesParams) ([]generated.SuggestPagesRow, error)
}

// PageSuggestionService serves the page-picker typeahead.
type PageSuggestionService struct {
	store PageSuggestionStore
}

// NewPageSuggestionService creates a PageSuggestionService over the store.
func NewPageSuggestionService(store PageSuggestionStore) *PageSuggestionService {
	return &PageSuggestionService{store: store}
}

// Suggest returns page suggestions visible to the caller.
//
// An empty readable set short-circuits: a caller who can read nothing is
// answered with nothing, without a round trip. `space_id = ANY('{}')` would
// return no rows anyway, so this is not what makes the endpoint safe — it
// makes the intent explicit at the layer where "no read access" is a
// meaningful state rather than an empty array literal in SQL.
func (s *PageSuggestionService) Suggest(ctx context.Context, readableSpaceIDs []uuid.UUID, query string) ([]PageSuggestion, error) {
	if len(readableSpaceIDs) == 0 {
		return []PageSuggestion{}, nil
	}

	rows, err := s.store.SuggestPages(ctx, generated.SuggestPagesParams{
		ReadableSpaceIds: readableSpaceIDs,
		Query:            query,
	})
	if err != nil {
		return nil, fmt.Errorf("suggesting pages: %w", err)
	}

	out := make([]PageSuggestion, 0, len(rows))
	for _, row := range rows {
		out = append(out, PageSuggestion{
			PageID:    row.ID,
			Title:     row.Title,
			SpaceID:   row.SpaceID,
			SpaceKey:  row.SpaceKey,
			SpaceName: row.SpaceName,
		})
	}
	return out, nil
}
