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

// Auth events.
const (
	// EventTypeUserLogin records a successful user authentication.
	EventTypeUserLogin EventType = "user.login"
	// EventTypeUserLoginFailed records a failed user authentication.
	EventTypeUserLoginFailed EventType = "user.login_failed"
	// EventTypeUserLogout records a user session termination.
	EventTypeUserLogout EventType = "user.logout"
	// EventTypeUserTokenIssued records a fresh token pair issued to a user.
	EventTypeUserTokenIssued EventType = "user.token_issued"
)

// Generic item events (used by older callers).
const (
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

// Service-desk ticket events.
const (
	EventTypeTicketCreated  EventType = "ticket.created"
	EventTypeTicketUpdated  EventType = "ticket.updated"
	EventTypeTicketStatus   EventType = "ticket.status_changed"
	EventTypeTicketAssigned EventType = "ticket.assigned"
	EventTypeTicketUnassign EventType = "ticket.unassigned"
	EventTypeTicketDeleted  EventType = "ticket.deleted"
)

// Wiki page events.
const (
	EventTypePageCreated EventType = "page.created"
	EventTypePageUpdated EventType = "page.updated"
	EventTypePageMoved   EventType = "page.moved"
	EventTypePageDeleted EventType = "page.deleted"
)

// Project item events.
const (
	EventTypeProjectItemCreated     EventType = "project_item.created"
	EventTypeProjectItemUpdated     EventType = "project_item.updated"
	EventTypeProjectItemStatus      EventType = "project_item.status_changed"
	EventTypeProjectItemSprintMoved EventType = "project_item.sprint_moved"
	EventTypeProjectItemDeleted     EventType = "project_item.deleted"
)

// Sprint events.
const (
	EventTypeSprintCreated   EventType = "sprint.created"
	EventTypeSprintStarted   EventType = "sprint.started"
	EventTypeSprintCompleted EventType = "sprint.completed"
)

// Comment events.
const (
	EventTypeCommentCreated EventType = "comment.created"
	EventTypeCommentDeleted EventType = "comment.deleted"
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
	// Phase 1 callers MUST keep this small (a transition delta, an assignee
	// id, etc.) — never log full row contents.
	Metadata map[string]string
	// OccurredAt is when the event happened. Defaults to time.Now() in the
	// recorder if zero.
	OccurredAt time.Time
}

// Logger writes structured, append-only audit events.
//
// Implementations MUST never return an error that could interrupt normal
// application flow at the call site — log and swallow on failure. The
// boolean returned by IsAvailable is informational; the default no-op
// implementation reports false so callers can choose to skip emission
// when there is no real backend.
type Logger interface {
	Log(ctx context.Context, event Event) error
	IsAvailable() bool
}

// Recorder is an alias for Logger that better names the producer side
// when injected into services. Audit emission points (handlers / services)
// receive a Recorder; the same value satisfies the Logger contract for
// diagnostic callers.
type Recorder = Logger

// defaultLogger is a no-op audit logger used when the database-backed
// implementation is not configured (e.g. during startup before the DB pool
// is open, or in unit tests).
type defaultLogger struct{}

// NewLogger returns the default Logger.
// Returns a no-op logger; pass a database-backed Logger via NewDBLogger
// to actually persist events.
func NewLogger() Logger {
	return &defaultLogger{}
}

// Log is a no-op — events are silently discarded.
func (s *defaultLogger) Log(_ context.Context, _ Event) error {
	return nil
}

// IsAvailable returns false — the default logger does not persist events.
func (s *defaultLogger) IsAvailable() bool {
	return false
}
