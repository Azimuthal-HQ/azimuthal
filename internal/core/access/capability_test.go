package access

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// CanOrgWide answers the create-time question: does the caller hold an
// org-level capability when there is no space to check against yet? These
// cases pin the three ways it could be got wrong — routing through Can (which
// would deny org admins), granting it to space roles, and answering anything
// at all without a resolution.

func TestCanOrgWideOrgAdminHoldsSetVisibilityWithNoSpaces(t *testing.T) {
	// adminSpaces deliberately empty: this is the state at space creation,
	// before the row exists. Can(CapSetVisibility, uuid.Nil) would answer
	// false here, which is exactly why CanOrgWide exists.
	res := &Resolution{IsOrgAdmin: true}
	if !res.CanOrgWide(CapSetVisibility) {
		t.Error("org admin was refused set_visibility org-wide; create-time visibility would 403 for the one role that holds it")
	}
	if res.Can(CapSetVisibility, uuid.Nil) {
		t.Error("Can answered true for a space that does not exist — the premise of CanOrgWide no longer holds, re-check both")
	}
}

func TestCanOrgWideRefusesSpaceAdmin(t *testing.T) {
	spaceID := uuid.New()
	res := &Resolution{roles: map[uuid.UUID]Role{spaceID: RoleSpaceAdmin}}
	if res.CanOrgWide(CapSetVisibility) {
		t.Error("a space_admin held set_visibility org-wide; no space role holds it (ADR-0007)")
	}
}

// A space-scoped capability is never held without a space — not even by an org
// admin, whose bypass is a per-space answer. This is the case that fails if
// CanOrgWide stops consulting minRoleFor and just returns IsOrgAdmin.
func TestCanOrgWideRefusesSpaceScopedCapabilities(t *testing.T) {
	res := &Resolution{IsOrgAdmin: true}
	for c := range minRoleFor {
		if res.CanOrgWide(c) {
			t.Errorf("%s answered true org-wide; it is space-scoped and has no space here", c)
		}
	}
}

func TestCanOrgWideFailsClosedWithoutResolution(t *testing.T) {
	if CanOrgWide(context.Background(), CapSetVisibility) {
		t.Error("CanOrgWide granted a capability on a context carrying no resolution")
	}
}

func TestCanOrgWideReadsTheContextResolution(t *testing.T) {
	ctx := WithResolution(context.Background(), &Resolution{IsOrgAdmin: true})
	if !CanOrgWide(ctx, CapSetVisibility) {
		t.Error("the context helper did not reach the resolution stored on the context")
	}
}
