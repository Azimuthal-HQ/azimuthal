//go:build enterprise

// Package rbac defines the RBACChecker interface and its community stub.
// This file is a compile-time placeholder; the azimuthal-ee private repo
// replaces NewChecker with the real ABAC policy engine.
package rbac

import (
	"context"
	"fmt"
)

// enterpriseChecker is a compile-time placeholder that satisfies Checker
// when building with the enterprise tag in the community repository.
type enterpriseChecker struct{}

// NewChecker returns a placeholder RBACChecker for enterprise builds in the
// community repository. The azimuthal-ee private repo provides the real implementation.
func NewChecker() Checker {
	return &enterpriseChecker{}
}

// NewCheckerWithRoleFunc returns a placeholder RBACChecker. The azimuthal-ee
// private repo replaces this with the attribute-based policy engine.
func NewCheckerWithRoleFunc(_ func(ctx context.Context, userID, orgID string) (Role, error)) Checker {
	return &enterpriseChecker{}
}

// CanPerform returns false — the real implementation is in azimuthal-ee.
func (e *enterpriseChecker) CanPerform(_ context.Context, userID, orgID, _ string, _ Action) (bool, error) {
	return false, fmt.Errorf("enterprise RBAC engine not available in community repository build")
}

// UserRole returns an error — the real implementation is in azimuthal-ee.
func (e *enterpriseChecker) UserRole(_ context.Context, userID, orgID string) (Role, error) {
	return "", fmt.Errorf("enterprise RBAC engine not available in community repository build")
}

// IsAvailable returns false — the real implementation is in azimuthal-ee.
func (e *enterpriseChecker) IsAvailable() bool {
	return false
}
