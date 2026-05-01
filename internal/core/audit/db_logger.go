package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// dbLogger writes audit events to the audit_log table via sqlc-generated queries.
// Per the Logger contract, persistence failures are logged and swallowed so
// they cannot break normal application flow — but unlike the no-op default,
// successful inserts actually persist.
type dbLogger struct {
	queries *generated.Queries
}

// NewDBLogger returns a Logger backed by the audit_log table.
// Audit ref: testing-audit.md §3.3 — replaces the default no-op logger that
// was discarding every event.
func NewDBLogger(queries *generated.Queries) Logger {
	return &dbLogger{queries: queries}
}

// Log inserts an event into the audit_log table. Errors are logged and
// swallowed so caller flow is never interrupted.
func (l *dbLogger) Log(ctx context.Context, event Event) error {
	orgID, err := uuid.Parse(event.OrgID)
	if err != nil {
		slog.Warn("audit: dropping event with invalid org_id", "org_id", event.OrgID, "type", event.Type)
		return nil
	}

	entityID, err := uuid.Parse(event.ResourceID)
	if err != nil {
		entityID = uuid.Nil
	}

	actorID := pgtype.UUID{}
	if parsed, err := uuid.Parse(event.ActorID); err == nil {
		actorID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	payload, err := json.Marshal(event.Metadata)
	if err != nil {
		payload = []byte(`{}`)
	}

	_, err = l.queries.CreateAuditEvent(ctx, generated.CreateAuditEventParams{
		ID:         uuid.New(),
		OrgID:      orgID,
		ActorID:    actorID,
		Action:     string(event.Type),
		EntityKind: event.ResourceType,
		EntityID:   entityID,
		Payload:    payload,
	})
	if err != nil {
		slog.Error("audit: failed to persist event", "type", event.Type, "error", fmt.Sprintf("%v", err))
		return nil
	}
	return nil
}

// IsAvailable returns true — the DB-backed logger is wired and persisting events.
func (l *dbLogger) IsAvailable() bool {
	return true
}
