package access

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// Row is one (space, role) pair produced by the resolution query: a
// grant matching the user directly or via their effective teams, or the
// implicit viewer row of an org-visible space.
type Row struct {
	SpaceID uuid.UUID
	Role    string
}

// Store is the persistence contract the resolver needs. Implemented by
// internal/db/adapters against real PostgreSQL.
type Store interface {
	// OrgRole returns the caller's membership role in the org, or
	// ErrNotOrgMember when no membership exists.
	OrgRole(ctx context.Context, orgID, userID uuid.UUID) (OrgRole, error)
	// ResolveAccessRows runs the single resolution query of spec §5.
	ResolveAccessRows(ctx context.Context, orgID, userID uuid.UUID) ([]Row, error)
	// ListSpaceIDsByOrg returns every non-deleted space id in the org —
	// the org-admin bypass set.
	ListSpaceIDsByOrg(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error)
}

// OrgRole is the org-membership role, already classified: the resolver is
// the one place org role names are interpreted.
type OrgRole struct {
	// Name is the raw membership role, kept for effective-access output.
	Name string
	// Admin is true for org owners and admins — the middleware bypass.
	Admin bool
}

// Resolver computes per-request access resolutions.
type Resolver struct {
	store Store
	// shareStore backs ResolveShares (P3). Attached via WithShareStore;
	// share resolution errors when it is missing rather than silently
	// denying, so a wiring mistake cannot masquerade as "no shares".
	shareStore ShareResolutionStore
}

// NewResolver creates a Resolver over the given store.
func NewResolver(store Store) *Resolver {
	return &Resolver{store: store}
}

// Resolution is the per-request access cache (ADR-0007 consequence 4): the
// caller's readable space set and highest role per space, resolved once in
// middleware and never shared across requests — which is what makes grant
// revocation immediate.
type Resolution struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
	// OrgRoleName is the raw membership role (for effective-access output).
	OrgRoleName string
	// IsOrgAdmin marks the middleware bypass: full access, zero grant rows.
	IsOrgAdmin bool

	// roles maps space id → highest granted role (non-admin callers).
	roles map[uuid.UUID]Role
	// adminSpaces is the org-admin bypass set (every live space id).
	adminSpaces map[uuid.UUID]struct{}
	// readable is the flat readable set; for org admins it is every live
	// space id in the org.
	readable []uuid.UUID
}

// Resolve computes the Resolution for one request. Cost is constant: two
// queries (org role + either the resolution query or the admin id list),
// independent of how many rows any later list endpoint returns.
func (r *Resolver) Resolve(ctx context.Context, orgID, userID uuid.UUID) (*Resolution, error) {
	orgRole, err := r.store.OrgRole(ctx, orgID, userID)
	if err != nil {
		// %w keeps errors.Is(err, ErrNotOrgMember) true for the middleware.
		return nil, fmt.Errorf("resolving caller org role: %w", err)
	}

	res := &Resolution{
		OrgID:       orgID,
		UserID:      userID,
		OrgRoleName: orgRole.Name,
		IsOrgAdmin:  orgRole.Admin,
	}

	if orgRole.Admin {
		ids, err := r.store.ListSpaceIDsByOrg(ctx, orgID)
		if err != nil {
			return nil, fmt.Errorf("resolving admin space set: %w", err)
		}
		res.readable = ids
		res.adminSpaces = make(map[uuid.UUID]struct{}, len(ids))
		for _, id := range ids {
			res.adminSpaces[id] = struct{}{}
		}
		return res, nil
	}

	rows, err := r.store.ResolveAccessRows(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("resolving access rows: %w", err)
	}

	res.roles = make(map[uuid.UUID]Role, len(rows))
	for _, row := range rows {
		role, err := ParseRole(row.Role)
		if err != nil {
			// A grant row with an unknown role cannot happen through the
			// CHECK constraint; if it somehow does, fail closed on that row.
			slog.Error("access: dropping grant row with unknown role",
				"space_id", row.SpaceID, "role", row.Role)
			continue
		}
		if role > res.roles[row.SpaceID] {
			res.roles[row.SpaceID] = role // highest role wins
		}
	}
	res.readable = make([]uuid.UUID, 0, len(res.roles))
	for id := range res.roles {
		res.readable = append(res.readable, id)
	}
	return res, nil
}

// CanReadSpace reports whether the space is in the caller's readable set.
func (res *Resolution) CanReadSpace(spaceID uuid.UUID) bool {
	if res.IsOrgAdmin {
		_, ok := res.adminSpaces[spaceID]
		return ok
	}
	_, ok := res.roles[spaceID]
	return ok
}

// RoleOn returns the caller's effective role on the space (highest across
// all matching grants; RoleSpaceAdmin for org admins on any org space;
// RoleNone when unreadable).
func (res *Resolution) RoleOn(spaceID uuid.UUID) Role {
	if res.IsOrgAdmin {
		if res.CanReadSpace(spaceID) {
			return RoleSpaceAdmin
		}
		return RoleNone
	}
	return res.roles[spaceID]
}

// Can reports whether the caller holds the capability on the space. Org
// admins hold every capability on every org space via the bypass — including
// org-only capabilities (set_visibility) that no space role grants, which is
// why the bypass answers directly instead of routing through RoleSpaceAdmin.
func (res *Resolution) Can(c Capability, spaceID uuid.UUID) bool {
	if res.IsOrgAdmin {
		return res.CanReadSpace(spaceID)
	}
	return res.RoleOn(spaceID).Grants(c)
}

// ReadableSpaceIDs returns the resolved readable set. Callers must treat it
// as read-only.
func (res *Resolution) ReadableSpaceIDs() []uuid.UUID {
	return res.readable
}
