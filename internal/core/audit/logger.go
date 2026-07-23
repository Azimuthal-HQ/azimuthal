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

	// EventTypeTeamCreated records a team being created (v0.3 spec §6).
	EventTypeTeamCreated EventType = "team.created"
	// EventTypeTeamUpdated records a team rename or description change.
	EventTypeTeamUpdated EventType = "team.updated"
	// EventTypeTeamReparented records a team moving in the tree.
	EventTypeTeamReparented EventType = "team.reparented"
	// EventTypeTeamDeleted records a team being deleted.
	EventTypeTeamDeleted EventType = "team.deleted"
	// EventTypeTeamMemberAdded records a user joining a team.
	EventTypeTeamMemberAdded EventType = "team_member.added"
	// EventTypeTeamMemberRemoved records a user leaving a team.
	EventTypeTeamMemberRemoved EventType = "team_member.removed"
	// EventTypeGrantCreated records a space grant being created.
	EventTypeGrantCreated EventType = "grant.created"
	// EventTypeGrantUpdated records a space grant's role changing.
	EventTypeGrantUpdated EventType = "grant.updated"
	// EventTypeGrantRevoked records a space grant being revoked.
	EventTypeGrantRevoked EventType = "grant.revoked"
	// EventTypeSpaceVisibilityChanged records a space visibility change.
	EventTypeSpaceVisibilityChanged EventType = "space.visibility_changed"
	// EventTypeSpaceOwnerTeamChanged records a space owner-team change.
	EventTypeSpaceOwnerTeamChanged EventType = "space.owner_team_changed"

	// EventTypeInviteCreated records an org invite being created (P2.5).
	EventTypeInviteCreated EventType = "invite.created"
	// EventTypeInviteRevoked records an invite being revoked.
	EventTypeInviteRevoked EventType = "invite.revoked"
	// EventTypeInviteResent records an invite's token being rotated.
	EventTypeInviteResent EventType = "invite.resent"
	// EventTypeInviteAccepted records an invite being accepted.
	EventTypeInviteAccepted EventType = "invite.accepted"
	// EventTypeUserDeactivated records an account being deactivated (which
	// always terminates its sessions).
	EventTypeUserDeactivated EventType = "user.deactivated"
	// EventTypeUserReactivated records an account being reactivated.
	EventTypeUserReactivated EventType = "user.reactivated"
	// EventTypeUserForceLogout records an administrative sign-out-everywhere.
	EventTypeUserForceLogout EventType = "user.force_logout"
	// EventTypeUserRemovedFromOrg records a membership removal.
	EventTypeUserRemovedFromOrg EventType = "user.removed_from_org"
	// EventTypeUserOrgRoleChanged records an org role change.
	EventTypeUserOrgRoleChanged EventType = "user.org_role_changed"
	// EventTypeUserPrimaryTeamChanged records a primary team change.
	EventTypeUserPrimaryTeamChanged EventType = "user.primary_team_changed"
	// EventTypeUserProfileChanged records a profile field change (e.g. display
	// name), whether by the user or an admin on their behalf.
	EventTypeUserProfileChanged EventType = "user.profile_changed"
	// EventTypeUserAvatarChanged records an avatar image being set.
	EventTypeUserAvatarChanged EventType = "user.avatar_changed"

	// EventTypeShareCreated records an entity share being created (P3,
	// ADR-0008).
	EventTypeShareCreated EventType = "share.created"
	// EventTypeShareRevoked records an entity share being revoked — by an
	// administrator, or by the same-transaction revoke-on-delete /
	// revoke-on-move invariants (the payload's "reason" says which).
	EventTypeShareRevoked EventType = "share.revoked"
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
	// BatchID groups the events of one atomic bulk change (migration 025).
	// Empty for ordinary single events.
	BatchID string
	// TicketRef is an operator-supplied free-text reference recorded with
	// the event. Stored without a foreign key — the audit log is
	// self-contained and survives deletion of whatever it references.
	TicketRef string
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
