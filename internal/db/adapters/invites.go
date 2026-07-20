package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/invites"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// InviteAdapter implements invites.Store. Acceptance is one transaction:
// user creation (when the email is new), membership, team enrolment, and
// consuming the invite commit together or not at all — an invite can never
// be marked accepted without the membership existing.
type InviteAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewInviteAdapter creates an InviteAdapter.
func NewInviteAdapter(pool *pgxpool.Pool) *InviteAdapter {
	return &InviteAdapter{pool: pool, q: generated.New(pool)}
}

// inTx runs fn inside one transaction, mirroring TeamAdapter.inTx.
func (a *InviteAdapter) inTx(ctx context.Context, fn func(q *generated.Queries) error) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("invite adapter: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(a.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("invite adapter: commit: %w", err)
	}
	return nil
}

// Create persists a new invite after friendliness checks (already a member,
// duplicate active invite, dead team). The partial unique index backstops
// the duplicate check against races.
func (a *InviteAdapter) Create(ctx context.Context, inv invites.Invite, tokenHash string) (invites.Invite, error) {
	// The invited email may already hold an account (login-path semantics:
	// the account GetUserByEmail resolves). If that account is already a
	// member, the invite is pointless — reject with a specific error.
	if u, err := a.q.GetUserByEmail(ctx, inv.Email); err == nil {
		if _, mErr := a.q.GetMembership(ctx, generated.GetMembershipParams{OrgID: inv.OrgID, UserID: u.ID}); mErr == nil {
			return invites.Invite{}, invites.ErrAlreadyMember
		} else if !errors.Is(mErr, pgx.ErrNoRows) {
			return invites.Invite{}, fmt.Errorf("invite adapter create: checking membership: %w", mErr)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return invites.Invite{}, fmt.Errorf("invite adapter create: checking email: %w", err)
	}

	if inv.TeamID != nil {
		team, err := a.q.GetTeamByID(ctx, *inv.TeamID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (team.OrgID != inv.OrgID || team.DeletedAt.Valid)) {
			return invites.Invite{}, invites.ErrTeamNotFound
		}
		if err != nil {
			return invites.Invite{}, fmt.Errorf("invite adapter create: checking team: %w", err)
		}
	}

	row, err := a.q.CreateInvite(ctx, generated.CreateInviteParams{
		ID:        uuid.New(),
		OrgID:     inv.OrgID,
		Email:     inv.Email,
		TokenHash: tokenHash,
		OrgRole:   inv.OrgRole,
		TeamID:    pgUUID(inv.TeamID),
		InvitedBy: inv.InvitedBy,
		ExpiresAt: pgTimestamp(inv.ExpiresAt),
	})
	if err != nil {
		if constraint, ok := uniqueViolation(err); ok && constraint == "invites_one_active_per_email" {
			return invites.Invite{}, invites.ErrDuplicateInvite
		}
		return invites.Invite{}, fmt.Errorf("invite adapter create: %w", err)
	}
	return dbInviteToDomain(row), nil
}

// GetByID returns one invite scoped to the org.
func (a *InviteAdapter) GetByID(ctx context.Context, orgID, id uuid.UUID) (invites.Invite, error) {
	row, err := a.q.GetInviteByID(ctx, generated.GetInviteByIDParams{ID: id, OrgID: orgID})
	if errors.Is(err, pgx.ErrNoRows) {
		return invites.Invite{}, invites.ErrNotFound
	}
	if err != nil {
		return invites.Invite{}, fmt.Errorf("invite adapter get: %w", err)
	}
	return dbInviteToDomain(row), nil
}

// ListActive returns the org's pending invites with display joins.
func (a *InviteAdapter) ListActive(ctx context.Context, orgID uuid.UUID) ([]invites.Invite, error) {
	rows, err := a.q.ListActiveInvitesByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("invite adapter list: %w", err)
	}
	out := make([]invites.Invite, 0, len(rows))
	for _, r := range rows {
		out = append(out, invites.Invite{
			ID:            r.ID,
			OrgID:         r.OrgID,
			Email:         r.Email,
			OrgRole:       r.OrgRole,
			TeamID:        goUUIDPtr(r.TeamID),
			InvitedBy:     r.InvitedBy,
			ExpiresAt:     goTime(r.ExpiresAt),
			CreatedAt:     goTime(r.CreatedAt),
			InvitedByName: r.InvitedByName,
			TeamName:      derefStr(r.TeamName),
		})
	}
	return out, nil
}

// Revoke marks an active invite revoked; 0 rows means it was not active.
func (a *InviteAdapter) Revoke(ctx context.Context, orgID, id uuid.UUID) error {
	n, err := a.q.RevokeInvite(ctx, generated.RevokeInviteParams{ID: id, OrgID: orgID})
	if err != nil {
		return fmt.Errorf("invite adapter revoke: %w", err)
	}
	if n == 0 {
		return invites.ErrNotFound
	}
	return nil
}

// RefreshToken rotates an active invite's token and expiry (resend).
func (a *InviteAdapter) RefreshToken(ctx context.Context, orgID, id uuid.UUID, newHash string, newExpiry time.Time) (invites.Invite, error) {
	row, err := a.q.RefreshInviteToken(ctx, generated.RefreshInviteTokenParams{
		ID:        id,
		OrgID:     orgID,
		TokenHash: newHash,
		ExpiresAt: pgTimestamp(newExpiry),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return invites.Invite{}, invites.ErrNotFound
	}
	if err != nil {
		return invites.Invite{}, fmt.Errorf("invite adapter refresh: %w", err)
	}
	return dbInviteToDomain(row), nil
}

// InspectByTokenHash backs the acceptance page's pre-submit lookup.
func (a *InviteAdapter) InspectByTokenHash(ctx context.Context, tokenHash string) (invites.Inspection, error) {
	row, err := a.q.GetInviteByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return invites.Inspection{}, invites.ErrNotFound
	}
	if err != nil {
		return invites.Inspection{}, fmt.Errorf("invite adapter inspect: %w", err)
	}
	org, err := a.q.GetOrganizationByID(ctx, row.OrgID)
	if err != nil {
		return invites.Inspection{}, fmt.Errorf("invite adapter inspect: loading org: %w", err)
	}
	existing := false
	if _, err := a.q.GetUserByEmail(ctx, row.Email); err == nil {
		existing = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return invites.Inspection{}, fmt.Errorf("invite adapter inspect: checking email: %w", err)
	}
	return invites.Inspection{
		Email:           row.Email,
		OrgName:         org.Name,
		State:           inviteState(row),
		ExistingAccount: existing,
	}, nil
}

// Accept consumes the invite transactionally. The MarkInviteAccepted guard
// (0 rows updated when not active) closes the race between the state check
// and the commit.
func (a *InviteAdapter) Accept(ctx context.Context, tokenHash string, newUser *invites.NewUser) (invites.AcceptOutcome, error) {
	var out invites.AcceptOutcome
	err := a.inTx(ctx, func(q *generated.Queries) error {
		row, err := q.GetInviteByTokenHash(ctx, tokenHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return invites.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("loading invite: %w", err)
		}
		switch inviteState(row) {
		case "revoked":
			return invites.ErrRevoked
		case "accepted":
			return invites.ErrAlreadyAccepted
		case "expired":
			return invites.ErrExpired
		}

		org, err := q.GetOrganizationByID(ctx, row.OrgID)
		if err != nil {
			return fmt.Errorf("loading org: %w", err)
		}

		// Resolve the account: the invited email's existing account, or a
		// fresh user created inside this transaction. Never a second user
		// for an email that already has one, never a second org.
		var user generated.User
		existingUser, err := q.GetUserByEmail(ctx, row.Email)
		switch {
		case err == nil:
			if !existingUser.IsActive {
				return invites.ErrAccountInactive
			}
			user = existingUser
			out.ExistingAccount = true
		case errors.Is(err, pgx.ErrNoRows):
			if newUser == nil {
				return invites.ErrDisplayNameAndPasswordRequired
			}
			hash, err := auth.HashPassword(newUser.Password)
			if err != nil {
				return fmt.Errorf("hashing password: %w", err)
			}
			user, err = q.CreateUser(ctx, generated.CreateUserParams{
				ID:           uuid.New(),
				OrgID:        row.OrgID,
				Email:        row.Email,
				DisplayName:  newUser.DisplayName,
				PasswordHash: &hash,
				Role:         "member",
			})
			if err != nil {
				return fmt.Errorf("creating user: %w", err)
			}
		default:
			return fmt.Errorf("checking email: %w", err)
		}

		// Membership: add unless it appeared since the invite was created
		// (acceptance is then an idempotent join).
		_, err = q.GetMembership(ctx, generated.GetMembershipParams{OrgID: row.OrgID, UserID: user.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			invitedBy := row.InvitedBy
			if _, err := q.CreateMembership(ctx, generated.CreateMembershipParams{
				ID:        uuid.New(),
				OrgID:     row.OrgID,
				UserID:    user.ID,
				Role:      row.OrgRole,
				InvitedBy: pgUUID(&invitedBy),
			}); err != nil {
				return fmt.Errorf("creating membership: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("checking membership: %w", err)
		}

		// Team enrolment: the invite's initial team when it is still a live
		// team of the org, else the org default team (ADR-0006: never
		// teamless). Primary only when the user holds no primary here yet.
		teamID, err := resolveInviteTeam(ctx, q, row)
		if err != nil {
			return err
		}
		if _, err := q.AddTeamMember(ctx, generated.AddTeamMemberParams{
			TeamID:    teamID,
			UserID:    user.ID,
			OrgID:     row.OrgID,
			Role:      "member",
			IsPrimary: false,
			Source:    "manual",
		}); err != nil {
			return fmt.Errorf("enrolling in team: %w", err)
		}
		_, err = q.GetPrimaryTeamMember(ctx, generated.GetPrimaryTeamMemberParams{UserID: user.ID, OrgID: row.OrgID})
		if errors.Is(err, pgx.ErrNoRows) {
			if err := q.SetPrimaryFlag(ctx, generated.SetPrimaryFlagParams{TeamID: teamID, UserID: user.ID}); err != nil {
				return fmt.Errorf("marking primary: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("checking primary: %w", err)
		}

		// Consume the invite last; the guarded UPDATE (0 rows when no
		// longer active) makes a concurrent double-accept roll back.
		n, err := q.MarkInviteAccepted(ctx, generated.MarkInviteAcceptedParams{
			ID:             row.ID,
			AcceptedUserID: pgUUID(&user.ID),
		})
		if err != nil {
			return fmt.Errorf("marking accepted: %w", err)
		}
		if n == 0 {
			return invites.ErrNotFound
		}

		out.User = dbUserToDomain(user)
		out.OrgID = row.OrgID
		out.OrgSlug = org.Slug
		out.OrgName = org.Name
		return nil
	})
	if err != nil {
		return invites.AcceptOutcome{}, err
	}
	return out, nil
}

// resolveInviteTeam picks the enrolment team: the invite's team when it is
// still a live team of the org, else the org default team.
func resolveInviteTeam(ctx context.Context, q *generated.Queries, row generated.Invite) (uuid.UUID, error) {
	if id := goUUIDPtr(row.TeamID); id != nil {
		team, err := q.GetTeamByID(ctx, *id)
		if err == nil && team.OrgID == row.OrgID && !team.DeletedAt.Valid {
			return team.ID, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("loading invite team: %w", err)
		}
	}
	def, err := q.GetDefaultTeam(ctx, row.OrgID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolving default team: %w", err)
	}
	return def.ID, nil
}

// inviteState classifies an invite row's lifecycle state.
func inviteState(row generated.Invite) string {
	switch {
	case row.RevokedAt.Valid:
		return "revoked"
	case row.AcceptedAt.Valid:
		return "accepted"
	case goTime(row.ExpiresAt).Before(timeNowUTC()):
		return "expired"
	default:
		return "active"
	}
}

// dbInviteToDomain converts a generated.Invite row.
func dbInviteToDomain(row generated.Invite) invites.Invite {
	return invites.Invite{
		ID:        row.ID,
		OrgID:     row.OrgID,
		Email:     row.Email,
		OrgRole:   row.OrgRole,
		TeamID:    goUUIDPtr(row.TeamID),
		InvitedBy: row.InvitedBy,
		ExpiresAt: goTime(row.ExpiresAt),
		CreatedAt: goTime(row.CreatedAt),
	}
}

// timeNowUTC is a seam kept trivial on purpose; invite expiry is evaluated
// against the database's now() in the guarded UPDATE, this is only for the
// pre-check classification.
func timeNowUTC() time.Time { return time.Now().UTC() }
