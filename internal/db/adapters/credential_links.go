package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/credlink"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// CredentialLinkAdapter implements credlink.Store. Every apply-on-consume effect
// commits in the SAME transaction as the guarded consume, so a burned link and
// its effect are all-or-nothing — a consumed link whose password write failed
// would strand the user, which is exactly what this atomicity prevents. It takes
// the pool (not just *generated.Queries) for Begin.
type CredentialLinkAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewCredentialLinkAdapter creates a CredentialLinkAdapter.
func NewCredentialLinkAdapter(pool *pgxpool.Pool) *CredentialLinkAdapter {
	return &CredentialLinkAdapter{pool: pool, q: generated.New(pool)}
}

// inTx runs fn inside one transaction, mirroring InviteAdapter.inTx.
func (a *CredentialLinkAdapter) inTx(ctx context.Context, fn func(q *generated.Queries) error) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("credential link adapter: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(a.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("credential link adapter: commit: %w", err)
	}
	return nil
}

// Issue supersedes outstanding links for (userID, purpose) and inserts a new
// one, in one transaction — mirroring PortalAdapter.CreateMagicLink.
func (a *CredentialLinkAdapter) Issue(ctx context.Context, userID uuid.UUID, purpose credlink.Purpose, tokenHash string, newEmail *string, expiresAt time.Time, createdBy *uuid.UUID) error {
	return a.inTx(ctx, func(q *generated.Queries) error {
		if _, err := q.InvalidateOutstandingCredentialLinks(ctx, generated.InvalidateOutstandingCredentialLinksParams{
			UserID:  userID,
			Purpose: string(purpose),
		}); err != nil {
			return fmt.Errorf("credential link adapter: invalidate outstanding: %w", err)
		}
		if _, err := q.CreateCredentialLink(ctx, generated.CreateCredentialLinkParams{
			UserID:    userID,
			Purpose:   string(purpose),
			TokenHash: tokenHash,
			NewEmail:  newEmail,
			ExpiresAt: pgTimestamp(expiresAt),
			CreatedBy: pgUUID(createdBy),
		}); err != nil {
			return fmt.Errorf("credential link adapter: create: %w", err)
		}
		return nil
	})
}

// Inspect returns the link's purpose without consuming it, collapsing every
// not-redeemable state to credlink.ErrInvalidLink.
func (a *CredentialLinkAdapter) Inspect(ctx context.Context, tokenHash string) (credlink.Inspection, error) {
	row, err := a.q.InspectCredentialLink(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return credlink.Inspection{}, credlink.ErrInvalidLink
	}
	if err != nil {
		return credlink.Inspection{}, fmt.Errorf("credential link adapter: inspect: %w", err)
	}
	insp := credlink.Inspection{Purpose: credlink.Purpose(row.Purpose)}
	if row.NewEmail != nil {
		insp.NewEmail = *row.NewEmail
	}
	return insp, nil
}

// Consume redeems the link and applies its effect in one transaction. The
// guarded UPDATE (ConsumeCredentialLink) is the single-use terminus; a dead or
// deactivated account is refused with the same ErrInvalidLink so consume never
// discriminates.
func (a *CredentialLinkAdapter) Consume(ctx context.Context, tokenHash string, passwordHash *string) (credlink.Consumed, error) {
	var out credlink.Consumed
	err := a.inTx(ctx, func(q *generated.Queries) error {
		row, err := q.ConsumeCredentialLink(ctx, tokenHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return credlink.ErrInvalidLink
		}
		if err != nil {
			return fmt.Errorf("credential link adapter: consume: %w", err)
		}

		// The account must still be live. A soft-deleted user is not returned by
		// GetUserByID (WHERE deleted_at IS NULL); a deactivated one is. Both are
		// refused, indistinguishably, so a redeemer learns nothing from the state
		// of an account they do not control.
		user, err := q.GetUserByID(ctx, row.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return credlink.ErrInvalidLink
		}
		if err != nil {
			return fmt.Errorf("credential link adapter: loading account: %w", err)
		}
		if !user.IsActive {
			return credlink.ErrInvalidLink
		}

		purpose := credlink.Purpose(row.Purpose)
		switch purpose {
		case credlink.PurposeSignIn, credlink.PurposePasswordReset:
			if passwordHash == nil {
				return credlink.ErrPasswordRequired
			}
			if err := q.UpdateUserPasswordHash(ctx, generated.UpdateUserPasswordHashParams{
				ID:           row.UserID,
				PasswordHash: passwordHash,
			}); err != nil {
				return fmt.Errorf("credential link adapter: setting password: %w", err)
			}
			if purpose == credlink.PurposePasswordReset {
				// A reset is a break-glass event: every existing session dies. The
				// password write already bumped token_generation (killing every
				// JWT); revoking the session rows closes the other axis.
				if err := q.RevokeAllUserSessions(ctx, row.UserID); err != nil {
					return fmt.Errorf("credential link adapter: revoking sessions: %w", err)
				}
			}
		case credlink.PurposeEmailChange:
			if row.NewEmail == nil {
				// The payload CHECK makes this impossible, but fail closed rather
				// than bind an empty address.
				return credlink.ErrInvalidLink
			}
			out.NewEmail = *row.NewEmail
			if _, err := q.UpdateUserEmail(ctx, generated.UpdateUserEmailParams{
				ID:    row.UserID,
				Email: *row.NewEmail,
			}); err != nil {
				if _, ok := uniqueViolation(err); ok {
					return credlink.ErrEmailTaken
				}
				return fmt.Errorf("credential link adapter: applying email: %w", err)
			}
		default:
			return credlink.ErrInvalidLink
		}

		out.UserID = row.UserID
		out.Purpose = purpose
		return nil
	})
	if err != nil {
		return credlink.Consumed{}, err
	}
	return out, nil
}

// FindUserInOrg resolves a user by email within orgID (never globally).
func (a *CredentialLinkAdapter) FindUserInOrg(ctx context.Context, orgID uuid.UUID, email string) (uuid.UUID, bool, error) {
	user, err := a.q.GetUserByEmailAndOrg(ctx, generated.GetUserByEmailAndOrgParams{OrgID: orgID, Email: email})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("credential link adapter: find user in org: %w", err)
	}
	return user.ID, true, nil
}

// CreateUserWithSignInLink provisions a passwordless member (user row, org
// membership, default-team enrolment) and mints its sign-in link, all in one
// transaction — modelled on InviteAdapter.Accept. ADR-0006: never teamless.
func (a *CredentialLinkAdapter) CreateUserWithSignInLink(ctx context.Context, p credlink.NewUser, tokenHash string, expiresAt time.Time) (uuid.UUID, error) {
	userID := uuid.New()
	err := a.inTx(ctx, func(q *generated.Queries) error {
		// Refuse an address that is already a member of THIS org (per-org unique
		// email); an address that exists only in another org is a stranger here
		// and gets a fresh account, exactly like an invite.
		if _, err := q.GetUserByEmailAndOrg(ctx, generated.GetUserByEmailAndOrgParams{OrgID: p.OrgID, Email: p.Email}); err == nil {
			return credlink.ErrEmailTaken
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("credential link adapter: checking email: %w", err)
		}

		// No password: the account cannot be signed into until the user sets one
		// through the link (a nil password_hash makes bcrypt verification fail).
		if _, err := q.CreateUser(ctx, generated.CreateUserParams{
			ID:           userID,
			OrgID:        p.OrgID,
			Email:        p.Email,
			DisplayName:  p.DisplayName,
			PasswordHash: nil,
			Role:         "member",
		}); err != nil {
			if _, ok := uniqueViolation(err); ok {
				return credlink.ErrEmailTaken
			}
			return fmt.Errorf("credential link adapter: creating user: %w", err)
		}

		invitedBy := p.CreatedBy
		if _, err := q.CreateMembership(ctx, generated.CreateMembershipParams{
			ID:        uuid.New(),
			OrgID:     p.OrgID,
			UserID:    userID,
			Role:      p.Role,
			InvitedBy: pgUUID(&invitedBy),
		}); err != nil {
			return fmt.Errorf("credential link adapter: creating membership: %w", err)
		}

		if err := enrolDefaultTeam(ctx, q, p.OrgID, userID); err != nil {
			return err
		}

		if _, err := q.CreateCredentialLink(ctx, generated.CreateCredentialLinkParams{
			UserID:    userID,
			Purpose:   string(credlink.PurposeSignIn),
			TokenHash: tokenHash,
			NewEmail:  nil,
			ExpiresAt: pgTimestamp(expiresAt),
			CreatedBy: pgUUID(&invitedBy),
		}); err != nil {
			return fmt.Errorf("credential link adapter: creating link: %w", err)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return userID, nil
}

// enrolDefaultTeam enrols userID in the org's default team, mirroring the invite
// path's enrolAcceptTeam (primary only when the user holds no primary yet).
func enrolDefaultTeam(ctx context.Context, q *generated.Queries, orgID, userID uuid.UUID) error {
	team, err := q.GetDefaultTeam(ctx, orgID)
	if err != nil {
		return fmt.Errorf("credential link adapter: default team: %w", err)
	}
	if _, err := q.AddTeamMember(ctx, generated.AddTeamMemberParams{
		TeamID:    team.ID,
		UserID:    userID,
		OrgID:     orgID,
		Role:      "member",
		IsPrimary: false,
		Source:    "manual",
	}); err != nil {
		return fmt.Errorf("credential link adapter: enrolling in team: %w", err)
	}
	if _, err := q.GetPrimaryTeamMember(ctx, generated.GetPrimaryTeamMemberParams{UserID: userID, OrgID: orgID}); errors.Is(err, pgx.ErrNoRows) {
		if err := q.SetPrimaryFlag(ctx, generated.SetPrimaryFlagParams{TeamID: team.ID, UserID: userID}); err != nil {
			return fmt.Errorf("credential link adapter: marking primary: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("credential link adapter: checking primary: %w", err)
	}
	return nil
}
