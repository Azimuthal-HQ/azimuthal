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
	// Auth events
	EventTypeUserLogin    EventType = "user.login"
	EventTypeLoginFailed  EventType = "user.login_failed"
	EventTypeUserLogout   EventType = "user.logout"
	EventTypeTokenIssued  EventType = "user.token_issued"

	// Ticket events
	EventTypeTicketCreated      EventType = "ticket.created"
	EventTypeTicketUpdated      EventType = "ticket.updated"
	EventTypeTicketStatusChange EventType = "ticket.status_changed"
	EventTypeTicketAssigned     EventType = "ticket.assigned"
	EventTypeTicketUnassigned   EventType = "ticket.unassigned"
	EventTypeTicketDeleted      EventType = "ticket.deleted"

	// Wiki events
	EventTypePageCreated EventType = "page.created"
	EventTypePageUpdated EventType = "page.updated"
	EventTypePageMoved   EventType = "page.moved"
	EventTypePageDeleted EventType = "page.deleted"

	// Project item events
	EventTypeItemCreated      EventType = "item.created"
	EventTypeItemUpdated      EventType = "item.updated"
	EventTypeItemStatusChange EventType = "item.status_changed"
	EventTypeItemSprintMove   EventType = "item.sprint_moved"
	EventTypeItemDeleted      EventType = "item.deleted"

	// Sprint events
	EventTypeSprintCreated   EventType = "sprint.created"
	EventTypeSprintStarted   EventType = "sprint.started"
	EventTypeSprintCompleted EventType = "sprint.completed"

	// Comment events
	EventTypeCommentCreated EventType = "comment.created"
	EventTypeCommentDeleted EventType = "comment.deleted"

	// Legacy / misc
	EventTypePermissionChanged EventType = "permission.changed"
	EventTypeSettingsChanged   EventType = "settings.changed"
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
