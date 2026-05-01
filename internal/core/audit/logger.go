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
	// EventTypeUserLogin records a successful user login.
	EventTypeUserLogin EventType = "user.login"
	// EventTypeLoginFailed records a failed login attempt.
	EventTypeLoginFailed EventType = "user.login_failed"
	// EventTypeUserLogout records a user logout.
	EventTypeUserLogout EventType = "user.logout"
	// EventTypeTokenIssued records a JWT token being issued.
	EventTypeTokenIssued EventType = "user.token_issued"

	// EventTypeTicketCreated records a new service-desk ticket being created.
	EventTypeTicketCreated EventType = "ticket.created"
	// EventTypeTicketUpdated records a ticket being updated.
	EventTypeTicketUpdated EventType = "ticket.updated"
	// EventTypeTicketStatusChange records a ticket status transition.
	EventTypeTicketStatusChange EventType = "ticket.status_changed"
	// EventTypeTicketAssigned records a ticket being assigned to a user.
	EventTypeTicketAssigned EventType = "ticket.assigned"
	// EventTypeTicketUnassigned records a ticket being unassigned.
	EventTypeTicketUnassigned EventType = "ticket.unassigned"
	// EventTypeTicketDeleted records a ticket being deleted.
	EventTypeTicketDeleted EventType = "ticket.deleted"

	// EventTypePageCreated records a wiki page being created.
	EventTypePageCreated EventType = "page.created"
	// EventTypePageUpdated records a wiki page being updated.
	EventTypePageUpdated EventType = "page.updated"
	// EventTypePageMoved records a wiki page being moved.
	EventTypePageMoved EventType = "page.moved"
	// EventTypePageDeleted records a wiki page being deleted.
	EventTypePageDeleted EventType = "page.deleted"

	// EventTypeItemCreated records a project item being created.
	EventTypeItemCreated EventType = "item.created"
	// EventTypeItemUpdated records a project item being updated.
	EventTypeItemUpdated EventType = "item.updated"
	// EventTypeItemStatusChange records a project item status transition.
	EventTypeItemStatusChange EventType = "item.status_changed"
	// EventTypeItemSprintMove records a project item being moved between sprints.
	EventTypeItemSprintMove EventType = "item.sprint_moved"
	// EventTypeItemDeleted records a project item being deleted.
	EventTypeItemDeleted EventType = "item.deleted"

	// EventTypeSprintCreated records a sprint being created.
	EventTypeSprintCreated EventType = "sprint.created"
	// EventTypeSprintStarted records a sprint being started.
	EventTypeSprintStarted EventType = "sprint.started"
	// EventTypeSprintCompleted records a sprint being completed.
	EventTypeSprintCompleted EventType = "sprint.completed"

	// EventTypeCommentCreated records a comment being created.
	EventTypeCommentCreated EventType = "comment.created"
	// EventTypeCommentDeleted records a comment being deleted.
	EventTypeCommentDeleted EventType = "comment.deleted"

	// EventTypePermissionChanged records a permission change.
	EventTypePermissionChanged EventType = "permission.changed"
	// EventTypeSettingsChanged records a settings change.
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
