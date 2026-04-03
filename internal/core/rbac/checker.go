// Package rbac provides role-based access control for Azimuthal.
// RBAC is a standard feature available to all users.
package rbac

import (
	"context"
	"errors"
	"fmt"
)

// Role represents a built-in role.
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

// ErrNotMember is returned when a user is not a member of the given organisation.
var ErrNotMember = errors.New("user is not a member of this organisation")

// Checker evaluates whether a user may perform an action on a resource.
type Checker interface {
	// CanPerform returns true when the user identified by userID is allowed to
	// perform action on resourceType within the given organisation.
	CanPerform(ctx context.Context, userID, orgID, resourceType string, action Action) (bool, error)

	// UserRole returns the Role assigned to userID within orgID.
	// Returns an error if the user is not a member of the organisation.
	UserRole(ctx context.Context, userID, orgID string) (Role, error)

	// IsAvailable reports whether the RBAC engine is active.
	IsAvailable() bool
}

// roleChecker applies a role-based permission matrix.
type roleChecker struct {
	roleFunc func(ctx context.Context, userID, orgID string) (Role, error)
}

// NewChecker returns a Checker using a no-op role resolver.
// Use NewCheckerWithRoleFunc to wire in the real role-lookup once the auth layer exists.
func NewChecker() Checker {
	return &roleChecker{
		roleFunc: func(_ context.Context, _, _ string) (Role, error) {
			return "", ErrNotMember
		},
	}
}

// NewCheckerWithRoleFunc returns a Checker that resolves roles via fn.
// Wire this into the HTTP layer with a function that queries the memberships table.
func NewCheckerWithRoleFunc(fn func(ctx context.Context, userID, orgID string) (Role, error)) Checker {
	return &roleChecker{roleFunc: fn}
}

// CanPerform applies the role matrix to decide whether the action is allowed.
//
// Role matrix:
//
//	owner  → all actions on all resources
//	admin  → create, read, update, delete, manage on all resources
//	member → create, read, update on non-admin resources; no manage/delete
//	viewer → read only
func (s *roleChecker) CanPerform(ctx context.Context, userID, orgID, _ string, action Action) (bool, error) {
	role, err := s.roleFunc(ctx, userID, orgID)
	if err != nil {
		return false, fmt.Errorf("resolving role for user %s in org %s: %w", userID, orgID, err)
	}

	switch role {
	case RoleOwner, RoleAdmin:
		return true, nil
	case RoleMember:
		return action == ActionCreate || action == ActionRead || action == ActionUpdate, nil
	case RoleViewer:
		return action == ActionRead, nil
	default:
		return false, nil
	}
}

// UserRole delegates to the injected role resolver.
func (s *roleChecker) UserRole(ctx context.Context, userID, orgID string) (Role, error) {
	role, err := s.roleFunc(ctx, userID, orgID)
	if err != nil {
		return "", fmt.Errorf("resolving role for user %s in org %s: %w", userID, orgID, err)
	}
	return role, nil
}

// IsAvailable reports whether the RBAC engine is active.
func (s *roleChecker) IsAvailable() bool {
	return true
}
