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
	// EventTypeSpaceCreated records a space being created, with its initial
	// visibility — the create-time counterpart of space.visibility_changed.
	EventTypeSpaceCreated EventType = "space.created"
	// EventTypeSpaceVisibilityChanged records a space visibility change.
	EventTypeSpaceVisibilityChanged EventType = "space.visibility_changed"
	// EventTypeSpaceOwnerTeamChanged records a space owner-team change.
	EventTypeSpaceOwnerTeamChanged EventType = "space.owner_team_changed"
	// EventTypeSpaceDeleted records a space being soft-deleted. Added
	// alongside space.created so both ends of a space's life are accounted
	// for: without it, deleting an entire space left less of a trace than
	// changing its visibility did, and an operator made to supply a ticket
	// reference under required mode had it discarded.
	EventTypeSpaceDeleted EventType = "space.deleted"

	// EventTypeInviteCreated records an org invite being created (P2.5).
	EventTypeInviteCreated EventType = "invite.created"
	// EventTypeInviteRevoked records an invite being revoked.
	EventTypeInviteRevoked EventType = "invite.revoked"
	// EventTypeInviteResent records an invite's token being rotated.
	EventTypeInviteResent EventType = "invite.resent"
	// EventTypeInviteAccepted records an invite being accepted.
	EventTypeInviteAccepted EventType = "invite.accepted"
	// EventTypePortalSignIn records an external requester redeeming a
	// magic link. The ActorID is a requesters.id, NOT a users.id — the only
	// event family where that is true, because a requester holds no account
	// (migration 044). Anything reading actor ids out of the audit log and
	// joining them to users must tolerate a miss on these rows.
	EventTypePortalSignIn EventType = "portal.signed_in"
	// EventTypePortalRequestCreated records a request raised through the
	// customer portal. Its ActorID is likewise a requesters.id.
	EventTypePortalRequestCreated EventType = "portal.request_created"
	// EventTypePortalConfigured records a Beacon space being opted into, or
	// out of, the customer portal. Its actor IS a user.
	EventTypePortalConfigured EventType = "portal.configured"
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

	// EventTypeCredentialLinkIssued records an admin minting a credential link —
	// a sign-in handoff for a newly created account, or a password-reset link for
	// an existing member. The payload's "purpose" says which; the raw token is
	// never recorded (only the admin, the target user, and the purpose).
	EventTypeCredentialLinkIssued EventType = "credential_link.issued"
	// EventTypeCredentialLinkConsumed records a credential link being redeemed —
	// a password set (signin/password_reset) or an email bound (email_change).
	// The actor is the account itself; the payload's "purpose" says what happened.
	EventTypeCredentialLinkConsumed EventType = "credential_link.consumed"

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

// ADR-0011 workflow tier events. Configuration changes are recorded through
// Convention A (mutate, then audit) because a tier row carries no atomicity
// contract; an approval DECISION is recorded alongside the transition it
// releases, which does.
const (
	// EventTypeWorkflowGuardCreated records a condition or validator being
	// attached to a transition.
	EventTypeWorkflowGuardCreated EventType = "workflow.guard_created"
	// EventTypeWorkflowGuardDeleted records one being removed.
	EventTypeWorkflowGuardDeleted EventType = "workflow.guard_deleted"
	// EventTypeWorkflowPostFunctionCreated records a post-function being
	// attached to a transition.
	EventTypeWorkflowPostFunctionCreated EventType = "workflow.post_function_created"
	// EventTypeWorkflowPostFunctionDeleted records one being removed.
	EventTypeWorkflowPostFunctionDeleted EventType = "workflow.post_function_deleted"
	// EventTypeWorkflowApproverCreated records a subject being added to a
	// transition's approver set.
	EventTypeWorkflowApproverCreated EventType = "workflow.approver_created"
	// EventTypeWorkflowApproverDeleted records one being removed.
	EventTypeWorkflowApproverDeleted EventType = "workflow.approver_deleted"
	// EventTypeWorkflowApprovalDecided records an approver's verdict. The
	// append-only log keeps approvals and declines alike; a decline is a new
	// row, never an update.
	EventTypeWorkflowApprovalDecided EventType = "workflow.approval_decided"
)
