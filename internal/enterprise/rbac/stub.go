//go:build !enterprise

package rbac

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotMember is returned when a user is not a member of the given organisation.
var ErrNotMember = errors.New("user is not a member of this organisation")

// stubChecker is the community-edition RBAC checker.
// It falls back to a simple, hard-coded role matrix rather than the full
// attribute-based policy engine available in enterprise builds.
type stubChecker struct {
	// roleFunc is called to resolve a user's role within an org.
	// Callers must inject this via NewCheckerWithRoleFunc; the zero-value stub
	// denies everything (safe default).
	roleFunc func(ctx context.Context, userID, orgID string) (Role, error)
}

// NewChecker returns the community stub RBACChecker using a no-op role resolver.
// Use NewCheckerWithRoleFunc to wire in the real role-lookup once the auth layer exists.
func NewChecker() RBACChecker {
	return &stubChecker{
		roleFunc: func(_ context.Context, _, _ string) (Role, error) {
			return "", ErrNotMember
		},
	}
}

// NewCheckerWithRoleFunc returns a community stub that resolves roles via fn.
// Wire this into the HTTP layer with a function that queries the memberships table.
func NewCheckerWithRoleFunc(fn func(ctx context.Context, userID, orgID string) (Role, error)) RBACChecker {
	return &stubChecker{roleFunc: fn}
}

// CanPerform applies the community role matrix to decide whether the action is allowed.
//
// Community role matrix:
//
//	owner  → all actions on all resources
//	admin  → create, read, update, delete, manage on all resources
//	member → create, read, update on non-admin resources; no manage/delete
//	viewer → read only
func (s *stubChecker) CanPerform(ctx context.Context, userID, orgID, _ string, action Action) (bool, error) {
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
func (s *stubChecker) UserRole(ctx context.Context, userID, orgID string) (Role, error) {
	role, err := s.roleFunc(ctx, userID, orgID)
	if err != nil {
		return "", fmt.Errorf("resolving role for user %s in org %s: %w", userID, orgID, err)
	}
	return role, nil
}

// IsAvailable always returns false in the community edition.
// Basic role checks are still applied; only the advanced policy engine is unavailable.
func (s *stubChecker) IsAvailable() bool {
	return false
}
