package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
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
		out, err = applyConsumedEffect(ctx, q, row, passwordHash)
		return err
	})
	if err != nil {
		return credlink.Consumed{}, err
	}
	return out, nil
}

// applyConsumedEffect performs the per-purpose write for a just-consumed link,
// inside the same transaction. The account must still be live: a soft-deleted
// user is not returned by GetUserByID (WHERE deleted_at IS NULL), a deactivated
// one is, and both are refused with the same ErrInvalidLink so a redeemer learns
// nothing from the state of an account they do not control.
func applyConsumedEffect(ctx context.Context, q *generated.Queries, row generated.ConsumeCredentialLinkRow, passwordHash *string) (credlink.Consumed, error) {
	user, err := q.GetUserByID(ctx, row.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return credlink.Consumed{}, credlink.ErrInvalidLink
	}
	if err != nil {
		return credlink.Consumed{}, fmt.Errorf("credential link adapter: loading account: %w", err)
	}
	if !user.IsActive {
		return credlink.Consumed{}, credlink.ErrInvalidLink
	}

	purpose := credlink.Purpose(row.Purpose)
	switch purpose {
	case credlink.PurposeSignIn, credlink.PurposePasswordReset:
		if err := applyPasswordEffect(ctx, q, row.UserID, purpose, passwordHash); err != nil {
			return credlink.Consumed{}, err
		}
	case credlink.PurposeEmailChange:
		if err := applyEmailEffect(ctx, q, row); err != nil {
			return credlink.Consumed{}, err
		}
	default:
		return credlink.Consumed{}, credlink.ErrInvalidLink
	}

	out := credlink.Consumed{UserID: row.UserID, Purpose: purpose}
	if row.NewEmail != nil {
		out.NewEmail = *row.NewEmail
	}
	return out, nil
}

// applyPasswordEffect sets the new password (bumping token_generation) and, for a
// reset, revokes every session row — the other revocation axis.
func applyPasswordEffect(ctx context.Context, q *generated.Queries, userID uuid.UUID, purpose credlink.Purpose, passwordHash *string) error {
	if passwordHash == nil {
		return credlink.ErrPasswordRequired
	}
	if err := q.UpdateUserPasswordHash(ctx, generated.UpdateUserPasswordHashParams{
		ID:           userID,
		PasswordHash: passwordHash,
	}); err != nil {
		return fmt.Errorf("credential link adapter: setting password: %w", err)
	}
	if purpose == credlink.PurposePasswordReset {
		// A reset is a break-glass event: every existing session dies. The
		// password write already bumped token_generation (killing every JWT);
		// revoking the session rows closes the other axis.
		if err := q.RevokeAllUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("credential link adapter: revoking sessions: %w", err)
		}
	}
	return nil
}

// applyEmailEffect binds the pending address (bumping token_generation), mapping
// a uniqueness collision to ErrEmailTaken.
func applyEmailEffect(ctx context.Context, q *generated.Queries, row generated.ConsumeCredentialLinkRow) error {
	if row.NewEmail == nil {
		// The payload CHECK makes this impossible, but fail closed rather than
		// bind an empty address.
		return credlink.ErrInvalidLink
	}
	if _, err := q.UpdateUserEmail(ctx, generated.UpdateUserEmailParams{
		ID:    row.UserID,
		Email: *row.NewEmail,
	}); err != nil {
		if _, ok := uniqueViolation(err); ok {
			return credlink.ErrEmailTaken
		}
		return fmt.Errorf("credential link adapter: applying email: %w", err)
	}
	return nil
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
		if err := ensureEmailFreeInOrg(ctx, q, p.OrgID, p.Email); err != nil {
			return err
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

		if _, err := q.CreateMembership(ctx, generated.CreateMembershipParams{
			ID:        uuid.New(),
			OrgID:     p.OrgID,
			UserID:    userID,
			Role:      p.Role,
			InvitedBy: pgUUID(p.CreatedBy),
		}); err != nil {
			return fmt.Errorf("credential link adapter: creating membership: %w", err)
		}

		if err := enrolDefaultTeam(ctx, q, p.OrgID, userID); err != nil {
			return err
		}

		// Optional default-space grant, in the same transaction: the account and
		// its first readable space commit together, so a link-created user never
		// lands in an empty product behind zero grants.
		if err := maybeGrantDefaultSpace(ctx, q, p, userID); err != nil {
			return err
		}

		if _, err := q.CreateCredentialLink(ctx, generated.CreateCredentialLinkParams{
			UserID:    userID,
			Purpose:   string(credlink.PurposeSignIn),
			TokenHash: tokenHash,
			NewEmail:  nil,
			ExpiresAt: pgTimestamp(expiresAt),
			CreatedBy: pgUUID(p.CreatedBy),
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

// ensureEmailFreeInOrg refuses an address already a member of orgID (per-org
// unique email). An address that exists only in ANOTHER org is a stranger here
// and gets a fresh account, exactly like an invite — so this is org-scoped, not
// the global lookup.
func ensureEmailFreeInOrg(ctx context.Context, q *generated.Queries, orgID uuid.UUID, email string) error {
	if _, err := q.GetUserByEmailAndOrg(ctx, generated.GetUserByEmailAndOrgParams{OrgID: orgID, Email: email}); err == nil {
		return credlink.ErrEmailTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("credential link adapter: checking email: %w", err)
	}
	return nil
}

// maybeGrantDefaultSpace grants the optional default space when one is requested,
// and is a no-op otherwise — keeping the nil-check out of the provisioning
// transaction body.
func maybeGrantDefaultSpace(ctx context.Context, q *generated.Queries, p credlink.NewUser, userID uuid.UUID) error {
	if p.SpaceID == nil {
		return nil
	}
	return grantDefaultSpace(ctx, q, p.OrgID, *p.SpaceID, userID, p.SpaceRole, p.CreatedBy)
}

// grantDefaultSpace validates that spaceID is a live space of orgID and inserts a
// user grant on it, inside the caller's transaction — the same CreateSpaceGrant
// insert AccessAdapter.CreateGrant uses, so this reuses the grants machinery
// rather than reimplementing it. A space that is unknown, soft-deleted, or
// another org's is refused with credlink.ErrSpaceNotFound — the same answer an
// unknown id gets, so an admin cannot probe another org's spaces through it.
// role is already validated (access.ParseRole) and defaulted by the service.
func grantDefaultSpace(ctx context.Context, q *generated.Queries, orgID, spaceID, userID uuid.UUID, role string, createdBy *uuid.UUID) error {
	space, err := q.GetSpaceByID(ctx, spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return credlink.ErrSpaceNotFound
	}
	if err != nil {
		return fmt.Errorf("credential link adapter: checking space: %w", err)
	}
	if space.OrgID != orgID {
		return credlink.ErrSpaceNotFound
	}
	if _, err := q.CreateSpaceGrant(ctx, generated.CreateSpaceGrantParams{
		ID:          uuid.New(),
		OrgID:       orgID,
		SpaceID:     spaceID,
		SubjectType: string(access.SubjectUser),
		SubjectID:   userID,
		Role:        role,
		CreatedBy:   pgUUID(createdBy),
	}); err != nil {
		return fmt.Errorf("credential link adapter: creating space grant: %w", err)
	}
	return nil
}
