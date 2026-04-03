//go:build !enterprise

package rbac_test

import (
	"context"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/rbac"
)

// makeCheckerWithRole returns a checker that always resolves to the given role.
func makeCheckerWithRole(role rbac.Role) rbac.RBACChecker {
	return rbac.NewCheckerWithRoleFunc(func(_ context.Context, _, _ string) (rbac.Role, error) {
		return role, nil
	})
}

func TestStubChecker_IsAvailable(t *testing.T) {
	c := rbac.NewChecker()
	if c.IsAvailable() {
		t.Error("community stub should report IsAvailable() == false")
	}
}

func TestStubChecker_OwnerCanDoEverything(t *testing.T) {
	c := makeCheckerWithRole(rbac.RoleOwner)
	actions := []rbac.Action{rbac.ActionCreate, rbac.ActionRead, rbac.ActionUpdate, rbac.ActionDelete, rbac.ActionManage}
	for _, action := range actions {
		ok, err := c.CanPerform(context.Background(), "u1", "org1", "ticket", action)
		if err != nil {
			t.Errorf("owner action %s: unexpected error: %v", action, err)
		}
		if !ok {
			t.Errorf("owner should be allowed to %s", action)
		}
	}
}

func TestStubChecker_AdminCanDoEverything(t *testing.T) {
	c := makeCheckerWithRole(rbac.RoleAdmin)
	actions := []rbac.Action{rbac.ActionCreate, rbac.ActionRead, rbac.ActionUpdate, rbac.ActionDelete, rbac.ActionManage}
	for _, action := range actions {
		ok, err := c.CanPerform(context.Background(), "u1", "org1", "ticket", action)
		if err != nil {
			t.Errorf("admin action %s: unexpected error: %v", action, err)
		}
		if !ok {
			t.Errorf("admin should be allowed to %s", action)
		}
	}
}

func TestStubChecker_MemberPermissions(t *testing.T) {
	c := makeCheckerWithRole(rbac.RoleMember)
	allowed := []rbac.Action{rbac.ActionCreate, rbac.ActionRead, rbac.ActionUpdate}
	denied := []rbac.Action{rbac.ActionDelete, rbac.ActionManage}

	for _, action := range allowed {
		ok, err := c.CanPerform(context.Background(), "u1", "org1", "ticket", action)
		if err != nil {
			t.Errorf("member action %s: unexpected error: %v", action, err)
		}
		if !ok {
			t.Errorf("member should be allowed to %s", action)
		}
	}
	for _, action := range denied {
		ok, err := c.CanPerform(context.Background(), "u1", "org1", "ticket", action)
		if err != nil {
			t.Errorf("member action %s: unexpected error: %v", action, err)
		}
		if ok {
			t.Errorf("member should NOT be allowed to %s", action)
		}
	}
}

func TestStubChecker_ViewerCanOnlyRead(t *testing.T) {
	c := makeCheckerWithRole(rbac.RoleViewer)
	allowed := []rbac.Action{rbac.ActionRead}
	denied := []rbac.Action{rbac.ActionCreate, rbac.ActionUpdate, rbac.ActionDelete, rbac.ActionManage}

	for _, action := range allowed {
		ok, err := c.CanPerform(context.Background(), "u1", "org1", "page", action)
		if err != nil {
			t.Errorf("viewer action %s: unexpected error: %v", action, err)
		}
		if !ok {
			t.Errorf("viewer should be allowed to %s", action)
		}
	}
	for _, action := range denied {
		ok, err := c.CanPerform(context.Background(), "u1", "org1", "page", action)
		if err != nil {
			t.Errorf("viewer action %s: unexpected error: %v", action, err)
		}
		if ok {
			t.Errorf("viewer should NOT be allowed to %s", action)
		}
	}
}

func TestStubChecker_NonMemberIsDenied(t *testing.T) {
	c := rbac.NewChecker() // zero-value resolver always returns ErrNotMember
	ok, err := c.CanPerform(context.Background(), "stranger", "org1", "ticket", rbac.ActionRead)
	if err == nil {
		t.Error("expected an error for a non-member, got nil")
	}
	if ok {
		t.Error("non-member should not be permitted")
	}
}

func TestStubChecker_UserRole(t *testing.T) {
	c := makeCheckerWithRole(rbac.RoleAdmin)
	role, err := c.UserRole(context.Background(), "u1", "org1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != rbac.RoleAdmin {
		t.Errorf("expected RoleAdmin, got %v", role)
	}
}

func TestStubChecker_ImplementsInterface(t *testing.T) {
	var _ rbac.RBACChecker = rbac.NewChecker()
}
