package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// AuditReaderAdapter implements audit.ReaderStore: the admin audit viewer's
// read path. One query per page (batches collapsed in SQL), one query per
// batch expansion.
type AuditReaderAdapter struct {
	q *generated.Queries
}

// NewAuditReaderAdapter creates an AuditReaderAdapter.
func NewAuditReaderAdapter(q *generated.Queries) *AuditReaderAdapter {
	return &AuditReaderAdapter{q: q}
}

// ListEntries returns one viewer page.
func (a *AuditReaderAdapter) ListEntries(ctx context.Context, orgID uuid.UUID, f audit.ListFilter) ([]audit.Entry, error) {
	params := generated.ListAuditLogEntriesParams{
		OrgID:           orgID,
		ActorID:         pgUUID(f.ActorID),
		EntityKind:      f.EntityKind,
		Action:          f.Action,
		CreatedFrom:     pgTimestampPtr(f.CreatedFrom),
		CreatedTo:       pgTimestampPtr(f.CreatedTo),
		CursorCreatedAt: pgTimestampPtr(f.CursorCreatedAt),
		CursorID:        pgUUID(f.CursorID),
		PageLimit:       int32(f.Limit), // #nosec G115 -- bounded to <=100 by the reader
	}
	rows, err := a.q.ListAuditLogEntries(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("audit reader list: %w", err)
	}
	out := make([]audit.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, audit.Entry{
			ID:         r.ID,
			ActorID:    goUUIDPtr(r.ActorID),
			ActorName:  r.ActorName,
			Action:     r.Action,
			EntityKind: r.EntityKind,
			EntityID:   r.EntityID,
			Payload:    r.Payload,
			BatchID:    goUUIDPtr(r.BatchID),
			TicketRef:  r.TicketRef,
			CreatedAt:  goTime(r.CreatedAt),
			BatchSize:  int(r.BatchSize),
		})
	}
	return out, nil
}

// ListBatchEvents expands one batch.
func (a *AuditReaderAdapter) ListBatchEvents(ctx context.Context, orgID, batchID uuid.UUID) ([]audit.Entry, error) {
	rows, err := a.q.ListAuditLogBatchEvents(ctx, generated.ListAuditLogBatchEventsParams{
		OrgID:   orgID,
		BatchID: pgtype.UUID{Bytes: batchID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("audit reader batch: %w", err)
	}
	out := make([]audit.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, audit.Entry{
			ID:         r.ID,
			ActorID:    goUUIDPtr(r.ActorID),
			ActorName:  r.ActorName,
			Action:     r.Action,
			EntityKind: r.EntityKind,
			EntityID:   r.EntityID,
			Payload:    r.Payload,
			BatchID:    goUUIDPtr(r.BatchID),
			TicketRef:  r.TicketRef,
			CreatedAt:  goTime(r.CreatedAt),
			BatchSize:  1,
		})
	}
	return out, nil
}
