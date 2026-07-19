package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/rbac"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// AccessAdapter implements access.Store, access.GrantStore, and
// access.ExplainStore over sqlc-generated queries.
type AccessAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewAccessAdapter creates an AccessAdapter.
func NewAccessAdapter(pool *pgxpool.Pool) *AccessAdapter {
	return &AccessAdapter{pool: pool, q: generated.New(pool)}
}

// OrgRole returns the caller's membership role. The owner/admin
// classification happens here, once — this is org-level RBAC (rbac package
// roles), not a space role-name check.
func (a *AccessAdapter) OrgRole(ctx context.Context, orgID, userID uuid.UUID) (access.OrgRole, error) {
	m, err := a.q.GetMembership(ctx, generated.GetMembershipParams{OrgID: orgID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return access.OrgRole{}, access.ErrNotOrgMember
	}
	if err != nil {
		return access.OrgRole{}, fmt.Errorf("resolving org role: %w", err)
	}
	role := rbac.Role(m.Role)
	return access.OrgRole{
		Name:  m.Role,
		Admin: role == rbac.RoleOwner || role == rbac.RoleAdmin,
	}, nil
}

// ResolveAccessRows runs the single resolution query of spec §5.
func (a *AccessAdapter) ResolveAccessRows(ctx context.Context, orgID, userID uuid.UUID) ([]access.AccessRow, error) {
	rows, err := a.q.ResolveAccessRows(ctx, generated.ResolveAccessRowsParams{UserID: userID, OrgID: orgID})
	if err != nil {
		return nil, fmt.Errorf("resolving access rows: %w", err)
	}
	out := make([]access.AccessRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, access.AccessRow{SpaceID: r.SpaceID, Role: r.Role})
	}
	return out, nil
}

// ListSpaceIDsByOrg returns every live space id in the org (admin bypass set).
func (a *AccessAdapter) ListSpaceIDsByOrg(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := a.q.ListSpaceIDsByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing org space ids: %w", err)
	}
	return ids, nil
}

// IsOrgMember reports whether the user holds an org membership row.
func (a *AccessAdapter) IsOrgMember(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	_, err := a.q.GetMembership(ctx, generated.GetMembershipParams{OrgID: orgID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking org membership: %w", err)
	}
	return true, nil
}

// TeamExistsInOrg reports whether a live team with this id exists in the org.
func (a *AccessAdapter) TeamExistsInOrg(ctx context.Context, orgID, teamID uuid.UUID) (bool, error) {
	t, err := a.q.GetTeamByID(ctx, teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking team: %w", err)
	}
	return t.OrgID == orgID, nil
}

// CreateGrant inserts a grant row.
func (a *AccessAdapter) CreateGrant(ctx context.Context, g access.Grant) (access.Grant, error) {
	row, err := a.q.CreateSpaceGrant(ctx, generated.CreateSpaceGrantParams{
		ID:          g.ID,
		OrgID:       g.OrgID,
		SpaceID:     g.SpaceID,
		SubjectType: string(g.SubjectType),
		SubjectID:   g.SubjectID,
		Role:        g.Role.String(),
		CreatedBy:   pgUUID(g.CreatedBy),
	})
	if _, ok := uniqueViolation(err); ok {
		return access.Grant{}, access.ErrDuplicateGrant
	}
	if err != nil {
		return access.Grant{}, fmt.Errorf("creating grant: %w", err)
	}
	return dbGrantToDomain(row)
}

// GetGrant returns one grant.
func (a *AccessAdapter) GetGrant(ctx context.Context, id uuid.UUID) (access.Grant, error) {
	row, err := a.q.GetSpaceGrant(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Grant{}, access.ErrGrantNotFound
	}
	if err != nil {
		return access.Grant{}, fmt.Errorf("getting grant: %w", err)
	}
	return dbGrantToDomain(row)
}

// UpdateGrantRole changes a grant's role.
func (a *AccessAdapter) UpdateGrantRole(ctx context.Context, id uuid.UUID, role access.Role) (access.Grant, error) {
	row, err := a.q.UpdateSpaceGrantRole(ctx, generated.UpdateSpaceGrantRoleParams{ID: id, Role: role.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Grant{}, access.ErrGrantNotFound
	}
	if err != nil {
		return access.Grant{}, fmt.Errorf("updating grant role: %w", err)
	}
	return dbGrantToDomain(row)
}

// DeleteGrant removes a grant row.
func (a *AccessAdapter) DeleteGrant(ctx context.Context, id uuid.UUID) error {
	if err := a.q.DeleteSpaceGrant(ctx, id); err != nil {
		return fmt.Errorf("deleting grant: %w", err)
	}
	return nil
}

// ListGrantsBySpace returns every grant on the space with subject identity.
func (a *AccessAdapter) ListGrantsBySpace(ctx context.Context, spaceID uuid.UUID) ([]access.Grant, error) {
	rows, err := a.q.ListGrantsBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("listing grants: %w", err)
	}
	out := make([]access.Grant, 0, len(rows))
	for _, r := range rows {
		role, err := access.ParseRole(r.Role)
		if err != nil {
			return nil, fmt.Errorf("grant %s: %w", r.ID, err)
		}
		out = append(out, access.Grant{
			ID:             r.ID,
			OrgID:          r.OrgID,
			SpaceID:        r.SpaceID,
			SubjectType:    access.SubjectType(r.SubjectType),
			SubjectID:      r.SubjectID,
			Role:           role,
			CreatedAt:      goTime(r.CreatedAt),
			CreatedBy:      goUUIDPtr(r.CreatedBy),
			SubjectName:    r.SubjectName,
			SubjectMissing: r.SubjectMissing,
		})
	}
	return out, nil
}

// ListMatchingGrants returns grants on the space reaching the user, with the
// granted team's path for chain annotation (effective-access).
func (a *AccessAdapter) ListMatchingGrants(ctx context.Context, orgID, spaceID, userID uuid.UUID) ([]access.RawMatch, error) {
	rows, err := a.q.ListMatchingGrantsForSpace(ctx, generated.ListMatchingGrantsForSpaceParams{
		UserID:  userID,
		OrgID:   orgID,
		SpaceID: spaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing matching grants: %w", err)
	}
	out := make([]access.RawMatch, 0, len(rows))
	for _, r := range rows {
		m := access.RawMatch{
			GrantID:     r.ID,
			SubjectType: r.SubjectType,
			SubjectID:   r.SubjectID,
			Role:        r.Role,
			TeamPath:    r.TeamPath,
		}
		if r.TeamName != nil {
			m.TeamName = *r.TeamName
		}
		out = append(out, m)
	}
	return out, nil
}

// ListUserDirectTeams returns the user's direct teams (id + name).
func (a *AccessAdapter) ListUserDirectTeams(ctx context.Context, orgID, userID uuid.UUID) ([]access.TeamRef, error) {
	rows, err := a.q.ListUserTeams(ctx, generated.ListUserTeamsParams{UserID: userID, OrgID: orgID})
	if err != nil {
		return nil, fmt.Errorf("listing user teams: %w", err)
	}
	out := make([]access.TeamRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, access.TeamRef{ID: r.ID, Name: r.Name})
	}
	return out, nil
}

// SpaceVisibility returns the space's visibility value.
func (a *AccessAdapter) SpaceVisibility(ctx context.Context, spaceID uuid.UUID) (string, error) {
	s, err := a.q.GetSpaceByID(ctx, spaceID)
	if err != nil {
		return "", fmt.Errorf("getting space: %w", err)
	}
	return s.Visibility, nil
}

// dbGrantToDomain converts a generated.SpaceGrant.
func dbGrantToDomain(g generated.SpaceGrant) (access.Grant, error) {
	role, err := access.ParseRole(g.Role)
	if err != nil {
		return access.Grant{}, fmt.Errorf("grant %s: %w", g.ID, err)
	}
	return access.Grant{
		ID:          g.ID,
		OrgID:       g.OrgID,
		SpaceID:     g.SpaceID,
		SubjectType: access.SubjectType(g.SubjectType),
		SubjectID:   g.SubjectID,
		Role:        role,
		CreatedAt:   goTime(g.CreatedAt),
		CreatedBy:   goUUIDPtr(g.CreatedBy),
	}, nil
}
