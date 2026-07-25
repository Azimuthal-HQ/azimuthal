package tickets

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
)

// Suggestion is one ticket offered to the ticket_ref typeahead.
//
// Ref is the whole point of the type: tickets carry no key column of their
// own (unlike project_items.item_key), so the human-readable reference is
// composed from the owning space's key and the ticket number. It is what the
// picker writes into the ticket_ref field, and it stays free text from there
// on — nothing downstream resolves it back to this row.
type Suggestion struct {
	Ref          string
	TicketID     uuid.UUID
	Number       int32
	Title        string
	SpaceID      uuid.UUID
	SpaceKey     string
	Status       Status
	AssignedToMe bool
}

// SuggestParams are the inputs to one typeahead query.
type SuggestParams struct {
	// ReadableSpaceIDs is the caller's resolved readable set. It is the
	// access control, not a hint: the store filters on it and nothing else
	// bounds which tickets can be seen.
	ReadableSpaceIDs []uuid.UUID
	// CallerID orders the caller's own assignments first.
	CallerID uuid.UUID
	// Query is free text; empty means "the default ordering, unfiltered".
	Query string
}

// SuggestionStore is the read seam for the typeahead.
//
// Deliberately separate from TicketRepository. The typeahead reads across
// many spaces at once, which no other ticket read does — every method on
// TicketRepository is scoped to a single space id — and widening that
// interface would push a cross-space method onto every implementation of it.
type SuggestionStore interface {
	// SuggestRefs returns at most a bounded page of matches, already ordered
	// assigned-to-caller first then most recently updated. Implementations
	// must never widen the space filter.
	SuggestRefs(ctx context.Context, params SuggestParams) ([]Suggestion, error)
}

// SuggestionService serves the ticket_ref typeahead.
type SuggestionService struct {
	store SuggestionStore
}

// NewSuggestionService creates a SuggestionService over the given store.
func NewSuggestionService(store SuggestionStore) *SuggestionService {
	return &SuggestionService{store: store}
}

// Suggest returns ticket suggestions visible to the caller.
//
// An empty readable set short-circuits: a caller who can read nothing is
// answered with nothing, without a round trip. `space_id = ANY('{}')` would
// return no rows anyway, so this is not what makes the endpoint safe — it
// makes the intent explicit at the layer where "no read access" is a
// meaningful state rather than an empty array literal in SQL.
func (s *SuggestionService) Suggest(ctx context.Context, params SuggestParams) ([]Suggestion, error) {
	if len(params.ReadableSpaceIDs) == 0 {
		return []Suggestion{}, nil
	}

	out, err := s.store.SuggestRefs(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("suggesting ticket refs: %w", err)
	}
	return out, nil
}

// ComposeRef builds the human-readable reference for a ticket: the space key
// and the ticket number, e.g. "BEA-42". One function so the API layer, the
// store and any future caller cannot drift into two spellings of the same
// string.
func ComposeRef(spaceKey string, number int32) string {
	return spaceKey + "-" + strconv.FormatInt(int64(number), 10)
}
