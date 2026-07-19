package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// TeamAdapter implements teams.Store. The multi-step operations (create,
// reparent, delete, membership moves) run inside transactions with their
// validations under row locks, so the path obligations of spec §4 hold under
// concurrency, not just in single-threaded tests.
type TeamAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewTeamAdapter creates a TeamAdapter.
func NewTeamAdapter(pool *pgxpool.Pool) *TeamAdapter {
	return &TeamAdapter{pool: pool, q: generated.New(pool)}
}

// inTx runs fn inside a transaction, committing on nil error.
func (a *TeamAdapter) inTx(ctx context.Context, fn func(q *generated.Queries) error) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(a.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// Create inserts a team with path = parent.path || id. The parent row is
// locked for the duration so a concurrent reparent cannot leave the new
// child's path prefix stale.
func (a *TeamAdapter) Create(ctx context.Context, t teams.Team) (teams.Team, error) {
	var created generated.Team
	err := a.inTx(ctx, func(q *generated.Queries) error {
		if t.ParentID != nil {
			parent, err := q.GetTeamForUpdate(ctx, *t.ParentID)
			if errors.Is(err, pgx.ErrNoRows) {
				return teams.ErrParentNotFound
			}
			if err != nil {
				return fmt.Errorf("locking parent team: %w", err)
			}
			if parent.OrgID != t.OrgID {
				return teams.ErrParentNotFound
			}
			if len(parent.Path)+1 > teams.MaxDepth {
				return teams.ErrDepthExceeded
			}
		}
		row, err := q.CreateTeam(ctx, generated.CreateTeamParams{
			ID:          t.ID,
			OrgID:       t.OrgID,
			ParentID:    pgUUID(t.ParentID),
			Slug:        t.Slug,
			Name:        t.Name,
			Description: t.Description,
			IsDefault:   t.IsDefault,
			Source:      t.Source,
		})
		if constraint, ok := uniqueViolation(err); ok && constraint == "teams_org_id_slug_key" {
			return teams.ErrSlugTaken
		}
		if err != nil {
			return fmt.Errorf("creating team: %w", err)
		}
		created = row
		return nil
	})
	if err != nil {
		return teams.Team{}, err
	}
	return dbTeamToDomain(created), nil
}

// Get returns one live team.
func (a *TeamAdapter) Get(ctx context.Context, id uuid.UUID) (teams.Team, error) {
	row, err := a.q.GetTeamByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return teams.Team{}, teams.ErrNotFound
	}
	if err != nil {
		return teams.Team{}, fmt.Errorf("getting team: %w", err)
	}
	return dbTeamToDomain(row), nil
}

// GetDefault returns the org's default team.
func (a *TeamAdapter) GetDefault(ctx context.Context, orgID uuid.UUID) (teams.Team, error) {
	row, err := a.q.GetDefaultTeam(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return teams.Team{}, teams.ErrNotFound
	}
	if err != nil {
		return teams.Team{}, fmt.Errorf("getting default team: %w", err)
	}
	return dbTeamToDomain(row), nil
}

// ListByOrg returns every live team in the org.
func (a *TeamAdapter) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]teams.Team, error) {
	rows, err := a.q.ListTeamsByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing teams: %w", err)
	}
	out := make([]teams.Team, 0, len(rows))
	for _, row := range rows {
		out = append(out, dbTeamToDomain(row))
	}
	return out, nil
}

// Update renames a team.
func (a *TeamAdapter) Update(ctx context.Context, id uuid.UUID, name, description string) (teams.Team, error) {
	row, err := a.q.UpdateTeam(ctx, generated.UpdateTeamParams{ID: id, Name: name, Description: description})
	if errors.Is(err, pgx.ErrNoRows) {
		return teams.Team{}, teams.ErrNotFound
	}
	if err != nil {
		return teams.Team{}, fmt.Errorf("updating team: %w", err)
	}
	return dbTeamToDomain(row), nil
}

// Reparent moves the team and its whole subtree under newParent (nil = root)
// in one transaction. Inside the transaction, under locks:
//   - cycle check: the team must not appear in the prospective parent's path
//     (a parent inside the moved subtree would orphan the tree);
//   - depth check: depth(new_parent) + height(moved_subtree) <= MaxDepth —
//     the subtree's height, not just the moved node's own depth.
func (a *TeamAdapter) Reparent(ctx context.Context, orgID, teamID uuid.UUID, newParent *uuid.UUID) (teams.Team, error) {
	var moved generated.Team
	err := a.inTx(ctx, func(q *generated.Queries) error {
		subtree, err := q.ListSubtreeForUpdate(ctx, generated.ListSubtreeForUpdateParams{OrgID: orgID, TeamID: teamID})
		if err != nil {
			return fmt.Errorf("locking subtree: %w", err)
		}
		if len(subtree) == 0 || subtree[0].ID != teamID {
			return teams.ErrNotFound
		}
		root := subtree[0]
		if root.IsDefault {
			return teams.ErrDefaultTeam
		}

		newParentPath := []uuid.UUID{}
		if newParent != nil {
			parent, err := q.GetTeamForUpdate(ctx, *newParent)
			if errors.Is(err, pgx.ErrNoRows) {
				return teams.ErrParentNotFound
			}
			if err != nil {
				return fmt.Errorf("locking new parent: %w", err)
			}
			if parent.OrgID != orgID {
				return teams.ErrParentNotFound
			}
			for _, ancestor := range parent.Path {
				if ancestor == teamID {
					return teams.ErrCycle
				}
			}
			newParentPath = parent.Path
		}

		height := 0
		for _, node := range subtree {
			if h := len(node.Path) - len(root.Path) + 1; h > height {
				height = h
			}
		}
		if len(newParentPath)+height > teams.MaxDepth {
			return teams.ErrDepthExceeded
		}

		if err := q.ReparentSubtree(ctx, generated.ReparentSubtreeParams{
			NewParentPath: newParentPath,
			TeamID:        teamID,
			NewParentID:   pgUUID(newParent),
			OrgID:         orgID,
		}); err != nil {
			return fmt.Errorf("rewriting subtree paths: %w", err)
		}

		refreshed, err := q.GetTeamByID(ctx, teamID)
		if err != nil {
			return fmt.Errorf("reloading moved team: %w", err)
		}
		moved = refreshed
		return nil
	})
	if err != nil {
		return teams.Team{}, err
	}
	return dbTeamToDomain(moved), nil
}

// Delete soft-deletes a team per ADR-0006: RESTRICT when it has children or
// owns spaces; members move to the org default team; anyone whose primary it
// was gets the default as primary; the team's grants are removed in the same
// transaction (symmetric with user deletion — subject_id has no FK).
func (a *TeamAdapter) Delete(ctx context.Context, orgID, teamID uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		subtree, err := q.ListSubtreeForUpdate(ctx, generated.ListSubtreeForUpdateParams{OrgID: orgID, TeamID: teamID})
		if err != nil {
			return fmt.Errorf("locking team: %w", err)
		}
		if len(subtree) == 0 || subtree[0].ID != teamID {
			return teams.ErrNotFound
		}
		if subtree[0].IsDefault {
			return teams.ErrDefaultTeam
		}
		if len(subtree) > 1 {
			return teams.ErrHasChildren
		}
		owned, err := q.CountTeamOwnedSpaces(ctx, teamID)
		if err != nil {
			return fmt.Errorf("counting owned spaces: %w", err)
		}
		if owned > 0 {
			return teams.ErrOwnsSpaces
		}

		def, err := q.GetDefaultTeam(ctx, orgID)
		if err != nil {
			return fmt.Errorf("resolving default team: %w", err)
		}

		primaries, err := q.ListPrimaryUserIDsOfTeam(ctx, teamID)
		if err != nil {
			return fmt.Errorf("listing primary members: %w", err)
		}

		if err := q.BulkEnrollInTeam(ctx, generated.BulkEnrollInTeamParams{
			DestTeamID: def.ID,
			SrcTeamID:  teamID,
		}); err != nil {
			return fmt.Errorf("moving members to default team: %w", err)
		}
		if err := q.DeleteTeamMembers(ctx, teamID); err != nil {
			return fmt.Errorf("removing old memberships: %w", err)
		}
		if len(primaries) > 0 {
			if err := q.SetPrimaryForUsers(ctx, generated.SetPrimaryForUsersParams{
				TeamID:  def.ID,
				UserIds: primaries,
			}); err != nil {
				return fmt.Errorf("reassigning primary team: %w", err)
			}
		}
		if err := q.DeleteGrantsBySubjectTeam(ctx, teamID); err != nil {
			return fmt.Errorf("removing team grants: %w", err)
		}
		if err := q.SoftDeleteTeam(ctx, teamID); err != nil {
			return fmt.Errorf("deleting team: %w", err)
		}
		return nil
	})
}

// AddMember enrols an org member into a team. Adding an existing member only
// updates the metadata role; is_primary is never touched here.
func (a *TeamAdapter) AddMember(ctx context.Context, teamID, userID, orgID uuid.UUID, role string) (teams.Member, error) {
	team, err := a.Get(ctx, teamID)
	if err != nil {
		return teams.Member{}, err
	}
	if team.OrgID != orgID {
		return teams.Member{}, teams.ErrNotFound
	}
	row, err := a.q.AddTeamMember(ctx, generated.AddTeamMemberParams{
		TeamID:    teamID,
		UserID:    userID,
		OrgID:     orgID,
		Role:      role,
		IsPrimary: false,
		Source:    "manual",
	})
	if err != nil {
		return teams.Member{}, fmt.Errorf("adding team member: %w", err)
	}
	return dbMemberToDomain(row), nil
}

// RemoveMember removes the user from the team. A user removed from their
// last team is re-added to the org default team as primary — never teamless
// (ADR-0006 point 4). When the removed membership was primary, primary falls
// to the default team when they belong to it, else their oldest membership.
func (a *TeamAdapter) RemoveMember(ctx context.Context, teamID, userID, orgID uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		existing, err := q.GetTeamMember(ctx, generated.GetTeamMemberParams{TeamID: teamID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return teams.ErrMemberNotFound
		}
		if err != nil {
			return fmt.Errorf("loading membership: %w", err)
		}

		if err := q.RemoveTeamMember(ctx, generated.RemoveTeamMemberParams{TeamID: teamID, UserID: userID}); err != nil {
			return fmt.Errorf("removing membership: %w", err)
		}

		remaining, err := q.CountUserTeamsInOrg(ctx, generated.CountUserTeamsInOrgParams{UserID: userID, OrgID: orgID})
		if err != nil {
			return fmt.Errorf("counting remaining teams: %w", err)
		}
		if remaining == 0 {
			def, err := q.GetDefaultTeam(ctx, orgID)
			if err != nil {
				return fmt.Errorf("resolving default team: %w", err)
			}
			if _, err := q.AddTeamMember(ctx, generated.AddTeamMemberParams{
				TeamID:    def.ID,
				UserID:    userID,
				OrgID:     orgID,
				Role:      "member",
				IsPrimary: true,
				Source:    "manual",
			}); err != nil {
				return fmt.Errorf("re-adding to default team: %w", err)
			}
			return nil
		}
		if existing.IsPrimary {
			fallback, err := q.GetFallbackPrimaryTeam(ctx, generated.GetFallbackPrimaryTeamParams{UserID: userID, OrgID: orgID})
			if err != nil {
				return fmt.Errorf("resolving fallback primary: %w", err)
			}
			if err := q.SetPrimaryFlag(ctx, generated.SetPrimaryFlagParams{TeamID: fallback, UserID: userID}); err != nil {
				return fmt.Errorf("reassigning primary: %w", err)
			}
		}
		return nil
	})
}

// ListMembers returns team members joined with live user identity.
func (a *TeamAdapter) ListMembers(ctx context.Context, teamID uuid.UUID) ([]teams.Member, error) {
	rows, err := a.q.ListTeamMembers(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("listing team members: %w", err)
	}
	out := make([]teams.Member, 0, len(rows))
	for _, row := range rows {
		out = append(out, teams.Member{
			TeamID:      row.TeamID,
			UserID:      row.UserID,
			OrgID:       row.OrgID,
			Role:        row.Role,
			IsPrimary:   row.IsPrimary,
			CreatedAt:   goTime(row.CreatedAt),
			Email:       row.Email,
			DisplayName: row.DisplayName,
			AvatarURL:   row.AvatarUrl,
		})
	}
	return out, nil
}

// GetMember returns one membership row.
func (a *TeamAdapter) GetMember(ctx context.Context, teamID, userID uuid.UUID) (teams.Member, error) {
	row, err := a.q.GetTeamMember(ctx, generated.GetTeamMemberParams{TeamID: teamID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return teams.Member{}, teams.ErrMemberNotFound
	}
	if err != nil {
		return teams.Member{}, fmt.Errorf("getting team member: %w", err)
	}
	return dbMemberToDomain(row), nil
}

// SetPrimary makes teamID the user's primary team in the org.
func (a *TeamAdapter) SetPrimary(ctx context.Context, teamID, userID, orgID uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		if _, err := q.GetTeamMember(ctx, generated.GetTeamMemberParams{TeamID: teamID, UserID: userID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return teams.ErrMemberNotFound
			}
			return fmt.Errorf("loading membership: %w", err)
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

// EnsureDefaultMembership enrols the user in the org default team, primary
// when they hold no primary membership yet. New-user provisioning hook.
func (a *TeamAdapter) EnsureDefaultMembership(ctx context.Context, orgID, userID uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		def, err := q.GetDefaultTeam(ctx, orgID)
		if err != nil {
			return fmt.Errorf("resolving default team: %w", err)
		}
		_, err = q.GetPrimaryTeamMember(ctx, generated.GetPrimaryTeamMemberParams{UserID: userID, OrgID: orgID})
		hasPrimary := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("checking primary membership: %w", err)
		}
		if _, err := q.AddTeamMember(ctx, generated.AddTeamMemberParams{
			TeamID:    def.ID,
			UserID:    userID,
			OrgID:     orgID,
			Role:      "member",
			IsPrimary: false,
			Source:    "manual",
		}); err != nil {
			return fmt.Errorf("enrolling in default team: %w", err)
		}
		if !hasPrimary {
			if err := q.SetPrimaryFlag(ctx, generated.SetPrimaryFlagParams{TeamID: def.ID, UserID: userID}); err != nil {
				return fmt.Errorf("marking default primary: %w", err)
			}
		}
		return nil
	})
}

// IsOrgMember reports whether the user holds an org membership row.
func (a *TeamAdapter) IsOrgMember(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	_, err := a.q.GetMembership(ctx, generated.GetMembershipParams{OrgID: orgID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking org membership: %w", err)
	}
	return true, nil
}

// SeedDefaultTeam creates the org's default team if absent. Called by every
// org-creation path (register provisioning and the admin CLI).
func (a *TeamAdapter) SeedDefaultTeam(ctx context.Context, orgID uuid.UUID) error {
	_, err := a.q.GetDefaultTeam(ctx, orgID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("checking default team: %w", err)
	}
	id := uuid.New()
	_, err = a.q.CreateTeam(ctx, generated.CreateTeamParams{
		ID:          id,
		OrgID:       orgID,
		ParentID:    pgUUID(nil),
		Slug:        "default",
		Name:        "Default",
		Description: "Org default team. Every member belongs here until assigned elsewhere.",
		IsDefault:   true,
		Source:      "manual",
	})
	if err != nil {
		return fmt.Errorf("seeding default team: %w", err)
	}
	return nil
}

// dbTeamToDomain converts a generated.Team.
func dbTeamToDomain(t generated.Team) teams.Team {
	return teams.Team{
		ID:          t.ID,
		OrgID:       t.OrgID,
		ParentID:    goUUIDPtr(t.ParentID),
		Path:        t.Path,
		Slug:        t.Slug,
		Name:        t.Name,
		Description: t.Description,
		IsDefault:   t.IsDefault,
		Source:      t.Source,
		CreatedAt:   goTime(t.CreatedAt),
		UpdatedAt:   goTime(t.UpdatedAt),
	}
}

// dbMemberToDomain converts a generated.TeamMember.
func dbMemberToDomain(m generated.TeamMember) teams.Member {
	return teams.Member{
		TeamID:    m.TeamID,
		UserID:    m.UserID,
		OrgID:     m.OrgID,
		Role:      m.Role,
		IsPrimary: m.IsPrimary,
		CreatedAt: goTime(m.CreatedAt),
	}
}
