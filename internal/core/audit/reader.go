package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Entry is one row of the admin audit viewer: a single event, or the
// representative of a batch (its newest event) carrying the batch size.
type Entry struct {
	ID         uuid.UUID
	ActorID    *uuid.UUID
	ActorName  string
	Action     string
	EntityKind string
	EntityID   uuid.UUID
	Payload    []byte
	BatchID    *uuid.UUID
	TicketRef  *string
	CreatedAt  time.Time
	// BatchSize is 1 for singleton events, the event count for a batch row.
	BatchSize int
}

// ListFilter narrows the viewer list. Nil fields mean no filter.
type ListFilter struct {
	ActorID     *uuid.UUID
	EntityKind  *string
	Action      *string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	// Cursor is keyset pagination over (created_at, id) of the previous
	// page's last row; both nil for the first page.
	CursorCreatedAt *time.Time
	CursorID        *uuid.UUID
	Limit           int
}

// ReaderStore is the persistence contract for the viewer, implemented by
// internal/db/adapters in a constant number of queries.
type ReaderStore interface {
	ListEntries(ctx context.Context, orgID uuid.UUID, f ListFilter) ([]Entry, error)
	ListBatchEvents(ctx context.Context, orgID, batchID uuid.UUID) ([]Entry, error)
}

// Reader serves the admin audit viewer.
type Reader struct {
	store ReaderStore
}

// NewReader creates a Reader.
func NewReader(store ReaderStore) *Reader { return &Reader{store: store} }

// List returns one page of viewer entries, batches collapsed to one row.
func (r *Reader) List(ctx context.Context, orgID uuid.UUID, f ListFilter) ([]Entry, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	entries, err := r.store.ListEntries(ctx, orgID, f)
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	return entries, nil
}

// BatchEvents expands one batch into its constituent events.
func (r *Reader) BatchEvents(ctx context.Context, orgID, batchID uuid.UUID) ([]Entry, error) {
	entries, err := r.store.ListBatchEvents(ctx, orgID, batchID)
	if err != nil {
		return nil, fmt.Errorf("listing batch events: %w", err)
	}
	return entries, nil
}
