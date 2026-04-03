package wiki

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// DefaultSearchLimit is the maximum number of results returned when no limit is specified.
const DefaultSearchLimit int32 = 50

// SearchInput holds parameters for full-text search.
type SearchInput struct {
	SpaceID uuid.UUID
	Query   string
	Limit   int32
}

// SearchPages performs a full-text search over pages in a space using
// PostgreSQL's tsvector/tsquery. Results are ranked by relevance.
func (s *Service) SearchPages(ctx context.Context, input SearchInput) ([]generated.SearchPagesRow, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return []generated.SearchPagesRow{}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	results, err := s.store.SearchPages(ctx, generated.SearchPagesParams{
		SpaceID:        input.SpaceID,
		PlaintoTsquery: query,
		Limit:          limit,
	})
	if err != nil {
		return nil, fmt.Errorf("searching pages: %w", err)
	}
	return results, nil
}
