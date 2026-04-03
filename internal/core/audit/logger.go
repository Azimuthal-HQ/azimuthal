// Package audit defines the AuditLogger interface for recording structured,
// append-only audit events. Audit logging is a standard feature available to
// all Azimuthal users.
package audit

import (
	"context"
	"time"
)

// EventType categorises the kind of action captured in an audit event.
type EventType string

const (
	// EventTypeUserLogin records a successful user authentication.
	EventTypeUserLogin EventType = "user.login"
	// EventTypeUserLogout records a user session termination.
	EventTypeUserLogout EventType = "user.logout"
	// EventTypeItemCreated records the creation of any workspace item.
	EventTypeItemCreated EventType = "item.created"
	// EventTypeItemUpdated records a modification to a workspace item.
	EventTypeItemUpdated EventType = "item.updated"
	// EventTypeItemDeleted records a soft-delete of a workspace item.
	EventTypeItemDeleted EventType = "item.deleted"
	// EventTypePermissionChanged records a change in user permissions.
	EventTypePermissionChanged EventType = "permission.changed"
	// EventTypeSettingsChanged records an organisation-level settings change.
	EventTypeSettingsChanged EventType = "settings.changed"
)

// Event is the structured record written to the audit log.
type Event struct {
	// Type is the category of action that occurred.
	Type EventType
	// ActorID is the ID of the user who performed the action.
	ActorID string
	// OrgID is the organisation in whose context the action occurred.
	OrgID string
	// ResourceType is the kind of resource affected (e.g. "ticket", "page").
	ResourceType string
	// ResourceID is the identifier of the affected resource.
	ResourceID string
	// Metadata holds arbitrary key-value pairs for additional context.
	Metadata map[string]string
	// OccurredAt is when the event happened.
	OccurredAt time.Time
}

// Logger writes structured, append-only audit events.
// The default implementation is a no-op that silently discards events until
// a database-backed implementation is wired in.
type Logger interface {
	// Log records an audit event. Implementations must never return an error that
	// would interrupt normal application flow — log and discard on failure.
	Log(ctx context.Context, event Event) error

	// IsAvailable reports whether the audit log is active and accepting events.
	IsAvailable() bool
}

// defaultLogger is a no-op audit logger used until the database-backed
// implementation is wired in.
type defaultLogger struct{}

// NewLogger returns the default Logger.
// Returns a no-op logger until the database-backed audit log is implemented.
func NewLogger() Logger {
	return &defaultLogger{}
}

// Log is a no-op — events are silently discarded until the DB implementation is wired in.
func (s *defaultLogger) Log(_ context.Context, _ Event) error {
	return nil
}

// IsAvailable returns false until the database-backed audit log is active.
func (s *defaultLogger) IsAvailable() bool {
	return false
}
