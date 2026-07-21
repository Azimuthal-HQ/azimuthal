package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/people"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/rbac"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// PeopleAdapter implements people.Store. The last-admin invariant is
// enforced here — in the store layer, under FOR UPDATE row locks — so no
// API or UI path can race two operations into an adminless org.
type PeopleAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewPeopleAdapter creates a PeopleAdapter. The pool backs the
// transactional lifecycle operations.
func NewPeopleAdapter(pool *pgxpool.Pool) *PeopleAdapter {
	return &PeopleAdapter{pool: pool, q: generated.New(pool)}
}

// inTx runs fn inside one transaction, mirroring TeamAdapter.inTx.
func (a *PeopleAdapter) inTx(ctx context.Context, fn func(q *generated.Queries) error) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("people adapter: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(a.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("people adapter: commit: %w", err)
	}
	return nil
}

// List returns every member of the org in one query (matrix case 23).
func (a *PeopleAdapter) List(ctx context.Context, orgID uuid.UUID) ([]people.Person, error) {
	rows, err := a.q.ListOrgPeople(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("people adapter list: %w", err)
	}
	out := make([]people.Person, 0, len(rows))
	for _, r := range rows {
		out = append(out, people.Person{
			UserID:          r.UserID,
			Email:           r.Email,
			DisplayName:     r.DisplayName,
			AvatarURL:       r.AvatarUrl,
			OrgRole:         r.OrgRole,
			IsActive:        r.IsActive,
			LastLoginAt:     goTimePtr(r.LastLoginAt),
			JoinedAt:        goTime(r.JoinedAt),
			PrimaryTeamID:   goUUIDPtr(r.PrimaryTeamID),
			PrimaryTeamName: r.PrimaryTeamName,
		})
	}
	return out, nil
}

// Search returns active members matching a name-or-email query.
func (a *PeopleAdapter) Search(ctx context.Context, orgID uuid.UUID, query string) ([]people.PersonRef, error) {
	rows, err := a.q.SearchOrgMembers(ctx, generated.SearchOrgMembersParams{OrgID: orgID, Query: query})
	if err != nil {
		return nil, fmt.Errorf("people adapter search: %w", err)
	}
	out := make([]people.PersonRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, people.PersonRef{
			ID:          r.ID,
			Email:       r.Email,
			DisplayName: r.DisplayName,
			AvatarURL:   r.AvatarUrl,
		})
	}
	return out, nil
}

// requireMembership loads the target's membership in the org or returns
// people.ErrNotMember — the admin surface only acts on members of the org
// the action was taken from.
func requireMembership(ctx context.Context, q *generated.Queries, orgID, userID uuid.UUID) (generated.Membership, error) {
	m, err := q.GetMembership(ctx, generated.GetMembershipParams{OrgID: orgID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return generated.Membership{}, people.ErrNotMember
	}
	if err != nil {
		return generated.Membership{}, fmt.Errorf("loading membership: %w", err)
	}
	return m, nil
}

// Deactivate blocks sign-in and terminates every session, atomically:
// is_active=false and the token_generation bump are one UPDATE, DB cookie
// sessions are revoked in the same transaction, and the last-admin check
// holds admin membership rows FOR UPDATE across every org the target
// administers (deactivation is global, so the guard must be too).
func (a *PeopleAdapter) Deactivate(ctx context.Context, orgID, userID uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		if _, err := requireMembership(ctx, q, orgID, userID); err != nil {
			return err
		}
		if _, err := q.LockAdminMembershipsForUserOrgs(ctx, generated.LockAdminMembershipsForUserOrgsParams{
			TargetUserID: userID,
			AdminRoles:   rbac.AdminRoleNames(),
		}); err != nil {
			return fmt.Errorf("locking admin memberships: %w", err)
		}
		lastAdminOrgs, err := q.ListOrgsWhereUserIsLastAdmin(ctx, generated.ListOrgsWhereUserIsLastAdminParams{
			UserID:     userID,
			AdminRoles: rbac.AdminRoleNames(),
		})
		if err != nil {
			return fmt.Errorf("checking last-admin: %w", err)
		}
		if len(lastAdminOrgs) > 0 {
			return people.ErrLastAdmin
		}
		n, err := q.DeactivateUserAccount(ctx, userID)
		if err != nil {
			return fmt.Errorf("deactivating account: %w", err)
		}
		if n == 0 {
			return people.ErrNotActive
		}
		if err := q.RevokeAllUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("revoking sessions: %w", err)
		}
		return nil
	})
}

// Reactivate re-enables sign-in. No generation bump: the tokens minted
// before deactivation already died at the deactivation bump.
func (a *PeopleAdapter) Reactivate(ctx context.Context, orgID, userID uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		if _, err := requireMembership(ctx, q, orgID, userID); err != nil {
			return err
		}
		n, err := q.ReactivateUserAccount(ctx, userID)
		if err != nil {
			return fmt.Errorf("reactivating account: %w", err)
		}
		if n == 0 {
			return people.ErrAlreadyActive
		}
		return nil
	})
}

// ForceLogout bumps token_generation and revokes DB sessions — nothing
// else. The user stays active.
func (a *PeopleAdapter) ForceLogout(ctx context.Context, orgID, userID uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		if _, err := requireMembership(ctx, q, orgID, userID); err != nil {
			return err
		}
		n, err := q.BumpTokenGeneration(ctx, userID)
		if err != nil {
			return fmt.Errorf("bumping token generation: %w", err)
		}
		if n == 0 {
			return people.ErrNotMember
		}
		if err := q.RevokeAllUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("revoking sessions: %w", err)
		}
		return nil
	})
}

// RemoveFromOrg drops the membership, the org's team memberships, and the
// org's user-subject grants in one transaction. The user record and their
// authored content survive with attribution intact.
func (a *PeopleAdapter) RemoveFromOrg(ctx context.Context, orgID, userID uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		m, err := requireMembership(ctx, q, orgID, userID)
		if err != nil {
			return err
		}
		if isAdminClass(m.Role) {
			if err := lockAndRequireOtherAdmins(ctx, q, orgID, userID); err != nil {
				return err
			}
		}
		if err := q.DeleteMembership(ctx, generated.DeleteMembershipParams{OrgID: orgID, UserID: userID}); err != nil {
			return fmt.Errorf("deleting membership: %w", err)
		}
		if err := q.DeleteTeamMembershipsInOrg(ctx, generated.DeleteTeamMembershipsInOrgParams{UserID: userID, OrgID: orgID}); err != nil {
			return fmt.Errorf("deleting team memberships: %w", err)
		}
		if err := q.DeleteGrantsBySubjectUserInOrg(ctx, generated.DeleteGrantsBySubjectUserInOrgParams{SubjectID: userID, OrgID: orgID}); err != nil {
			return fmt.Errorf("deleting grants: %w", err)
		}
		return nil
	})
}

// ChangeOrgRole updates the membership role. Owners cannot be changed here;
// demoting the last active admin is blocked under the same lock the other
// lifecycle paths take.
func (a *PeopleAdapter) ChangeOrgRole(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		m, err := requireMembership(ctx, q, orgID, userID)
		if err != nil {
			return err
		}
		if m.Role == string(rbac.RoleOwner) {
			return people.ErrCannotChangeOwner
		}
		if m.Role == role {
			return nil
		}
		// Only a demotion out of the admin class can strand the org.
		if isAdminClass(m.Role) && !isAdminClass(role) {
			if err := lockAndRequireOtherAdmins(ctx, q, orgID, userID); err != nil {
				return err
			}
		}
		if err := q.UpdateMembershipRole(ctx, generated.UpdateMembershipRoleParams{OrgID: orgID, UserID: userID, Role: role}); err != nil {
			return fmt.Errorf("updating membership role: %w", err)
		}
		return nil
	})
}

// ChangePrimaryTeam enrols the user in the team when needed (metadata role
// "member") and marks it their primary, in one transaction.
func (a *PeopleAdapter) ChangePrimaryTeam(ctx context.Context, orgID, userID, teamID uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		if _, err := requireMembership(ctx, q, orgID, userID); err != nil {
			return err
		}
		team, err := q.GetTeamByID(ctx, teamID)
		if errors.Is(err, pgx.ErrNoRows) {
			return people.ErrTeamNotFound
		}
		if err != nil {
			return fmt.Errorf("loading team: %w", err)
		}
		if team.OrgID != orgID || team.DeletedAt.Valid {
			return people.ErrTeamNotFound
		}
		if _, err := q.AddTeamMember(ctx, generated.AddTeamMemberParams{
			TeamID:    teamID,
			UserID:    userID,
			OrgID:     orgID,
			Role:      "member",
			IsPrimary: false,
			Source:    "manual",
		}); err != nil {
			return fmt.Errorf("enrolling in team: %w", err)
		}
		if err := q.ClearPrimaryTeam(ctx, generated.ClearPrimaryTeamParams{UserID: userID, OrgID: orgID}); err != nil {
			return fmt.Errorf("clearing primary: %w", err)
		}
		if err := q.SetPrimaryFlag(ctx, generated.SetPrimaryFlagParams{TeamID: teamID, UserID: userID}); err != nil {
			return fmt.Errorf("setting primary: %w", err)
		}
		return nil
	})
}

// isAdminClass reports whether a membership role carries org-admin
// authority, delegating the interpretation to rbac.
func isAdminClass(role string) bool {
	for _, r := range rbac.AdminRoleNames() {
		if role == r {
			return true
		}
	}
	return false
}

// lockAndRequireOtherAdmins serialises against concurrent admin-lifecycle
// operations in the org, then requires at least one OTHER active admin.
func lockAndRequireOtherAdmins(ctx context.Context, q *generated.Queries, orgID, userID uuid.UUID) error {
	if _, err := q.LockAdminMembershipsInOrg(ctx, generated.LockAdminMembershipsInOrgParams{
		OrgID:      orgID,
		AdminRoles: rbac.AdminRoleNames(),
	}); err != nil {
		return fmt.Errorf("locking admin memberships: %w", err)
	}
	n, err := q.CountOtherActiveOrgAdmins(ctx, generated.CountOtherActiveOrgAdminsParams{
		OrgID:      orgID,
		UserID:     userID,
		AdminRoles: rbac.AdminRoleNames(),
	})
	if err != nil {
		return fmt.Errorf("counting other admins: %w", err)
	}
	if n == 0 {
		return people.ErrLastAdmin
	}
	return nil
}
