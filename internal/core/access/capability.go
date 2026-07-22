// Package access implements the v0.3 access-control core (ADR-0007, spec §5):
// capability-based permission checks over explicit space grants with
// subject-side team expansion, resolved once per request and cached on the
// request context.
//
// This file is the single place role names exist as strings. Everywhere else
// roles are the ordered Role type and permission checks are capability
// checks — `Can(ctx, CapReadItems, spaceID)`, never a role-name comparison.
package access

import "fmt"

// Role is a space role, ordered by authority so "highest role wins" is a
// plain integer comparison. The zero value RoleNone grants nothing.
type Role int

// Space roles in ascending order of authority.
const (
	RoleNone Role = iota
	RoleViewer
	RoleContributor
	RoleAgent
	RoleSpaceAdmin
)

// roleNames is the wire/database representation, index-aligned with the
// Role constants. RoleNone has no wire form — it never leaves the process.
var roleNames = [...]string{
	RoleNone:        "",
	RoleViewer:      "viewer",
	RoleContributor: "contributor",
	RoleAgent:       "agent",
	RoleSpaceAdmin:  "space_admin",
}

// String returns the wire form of the role ("" for RoleNone).
func (r Role) String() string {
	if r < RoleNone || int(r) >= len(roleNames) {
		return ""
	}
	return roleNames[r]
}

// ParseRole converts a wire/database role string into a Role. It is the one
// permitted string→role boundary in the codebase; a string that is not a
// role is an error, never silently RoleNone.
func ParseRole(s string) (Role, error) {
	for r, name := range roleNames {
		if Role(r) != RoleNone && name == s {
			return Role(r), nil
		}
	}
	return RoleNone, fmt.Errorf("unknown space role %q", s)
}

// Capability is a single permission that can be checked against a space.
type Capability string

// Capabilities per the ADR-0007 model.
const (
	CapReadItems         Capability = "read_items"
	CapReadAggregates    Capability = "read_aggregates"
	CapCreateItems       Capability = "create_items"
	CapEditOwnItems      Capability = "edit_own_items"
	CapComment           Capability = "comment"
	CapEditAnyItem       Capability = "edit_any_item"
	CapTransitionAnyItem Capability = "transition_any_item"
	CapManageQueue       Capability = "manage_queue"
	CapManageSpace       Capability = "manage_space"
	CapManageGrants      Capability = "manage_grants"
	CapManageShares      Capability = "manage_shares"
	CapManageWorkflow    Capability = "manage_workflow"

	// CapSetVisibility governs changing a space's visibility. Deliberately
	// absent from minRoleFor: no space role holds it — not even space_admin —
	// because visibility changes what the whole organisation sees, an
	// org-level concern. Only the org-admin bypass grants it.
	CapSetVisibility Capability = "set_visibility"
)

// minRoleFor is the ADR-0007 capability table: the lowest role holding each
// capability. Roles are cumulative, so Grants is a >= check against this.
var minRoleFor = map[Capability]Role{
	CapReadItems:         RoleViewer,
	CapReadAggregates:    RoleViewer,
	CapCreateItems:       RoleContributor,
	CapEditOwnItems:      RoleContributor,
	CapComment:           RoleContributor,
	CapEditAnyItem:       RoleAgent,
	CapTransitionAnyItem: RoleAgent,
	CapManageQueue:       RoleAgent,
	CapManageSpace:       RoleSpaceAdmin,
	CapManageGrants:      RoleSpaceAdmin,
	CapManageShares:      RoleSpaceAdmin,
	CapManageWorkflow:    RoleSpaceAdmin,
}

// Grants reports whether the role holds the capability. Unknown capabilities
// are never granted — fail closed.
func (r Role) Grants(c Capability) bool {
	minimum, known := minRoleFor[c]
	if !known || r == RoleNone {
		return false
	}
	return r >= minimum
}
