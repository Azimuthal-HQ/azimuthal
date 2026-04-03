// Package rbac defines the RBACChecker interface and its community stub.
// The real advanced permissions engine lives in the enterprise repository.
package rbac

import "context"

// Role represents a built-in role in the community edition.
type Role string

const (
	// RoleOwner has full administrative control of an organisation.
	RoleOwner Role = "owner"
	// RoleAdmin can manage spaces, members, and most settings.
	RoleAdmin Role = "admin"
	// RoleMember can create and edit items within their assigned spaces.
	RoleMember Role = "member"
	// RoleViewer has read-only access to assigned spaces.
	RoleViewer Role = "viewer"
)

// Action represents a permission that can be checked.
type Action string

const (
	// ActionCreate covers creating new resources.
	ActionCreate Action = "create"
	// ActionRead covers reading existing resources.
	ActionRead Action = "read"
	// ActionUpdate covers modifying existing resources.
	ActionUpdate Action = "update"
	// ActionDelete covers soft-deleting resources.
	ActionDelete Action = "delete"
	// ActionManage covers administrative actions (member management, settings).
	ActionManage Action = "manage"
)

// Checker evaluates whether a user may perform an action on a resource.
// The community stub applies simple, hard-coded role rules. The enterprise
// implementation supports custom roles, attribute-based policies, and space-level
// overrides — all configured via the admin UI.
type Checker interface {
	// CanPerform returns true when the user identified by userID is allowed to
	// perform action on resourceType within the given organisation.
	// The orgID scopes the check to a specific organisation.
	CanPerform(ctx context.Context, userID, orgID, resourceType string, action Action) (bool, error)

	// UserRole returns the Role assigned to userID within orgID.
	// Returns an error if the user is not a member of the organisation.
	UserRole(ctx context.Context, userID, orgID string) (Role, error)

	// IsAvailable reports whether the advanced RBAC engine is active.
	// Returns false in the community edition; basic role checks are always applied.
	IsAvailable() bool
}
