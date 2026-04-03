package tickets

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

const defaultSearchLimit int32 = 50

// Search performs a full-text search for tickets within a space using the
// postgres search_vector column. Returns up to limit results ranked by
// relevance.
func (s *TicketService) Search(ctx context.Context, spaceID uuid.UUID, query string, limit int32) ([]*Ticket, error) {
	if query == "" {
		return nil, ErrEmptySearchQuery
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	results, err := s.repo.Search(ctx, spaceID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("searching tickets: %w", err)
	}
	return results, nil
}
