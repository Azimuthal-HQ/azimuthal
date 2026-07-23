// Package people implements administrative user lifecycle within an org
// (P2.5 W2/W4): listing members, deactivation and reactivation, force
// logout, removal from the org, and org role and primary team changes.
//
// Two invariants live in the STORE layer, not the UI, and hold under
// concurrency (the adapter takes row locks):
//
//   - The last active org admin can never be removed, demoted, or
//     deactivated — an org without an admin is unrecoverable from inside.
//   - Deactivation always terminates sessions: the is_active flip and the
//     token_generation bump are one SQL statement, and DB cookie sessions
//     are revoked in the same transaction. There is deliberately no
//     "deactivate but keep signed in" path anywhere in the API.
package people

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/rbac"
)

// Person is one row of the admin People page.
type Person struct {
	UserID          uuid.UUID
	Email           string
	DisplayName     string
	AvatarURL       *string
	OrgRole         string
	IsActive        bool
	LastLoginAt     *time.Time
	JoinedAt        time.Time
	PrimaryTeamID   *uuid.UUID
	PrimaryTeamName *string
}

// PersonRef is a picker search result.
type PersonRef struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	AvatarURL   *string
}

// Store is the persistence contract, implemented by internal/db/adapters.
// Every mutating method is transactional and enforces the last-admin
// invariant under row locks where it applies.
type Store interface {
	// List returns every member of the org in one query.
	List(ctx context.Context, orgID uuid.UUID) ([]Person, error)
	// Search returns active members matching a name-or-email query.
	Search(ctx context.Context, orgID uuid.UUID, query string) ([]PersonRef, error)
	// Deactivate blocks sign-in and kills every session the user holds
	// (token generation bump + DB session revocation, one transaction).
	// ErrLastAdmin when the user is the last active admin of ANY org they
	// administer; ErrNotMember when they are not in this org; ErrNotActive
	// when already deactivated.
	Deactivate(ctx context.Context, orgID, userID uuid.UUID) error
	// Reactivate re-enables sign-in. The generation bump at deactivation
	// already killed the old tokens, so none revive. ErrNotMember /
	// ErrAlreadyActive as appropriate.
	Reactivate(ctx context.Context, orgID, userID uuid.UUID) error
	// ForceLogout bumps token_generation and revokes DB sessions — nothing
	// else. The user stays active and simply signs in again.
	ForceLogout(ctx context.Context, orgID, userID uuid.UUID) error
	// RemoveFromOrg drops the membership, the org's team memberships, and
	// the org's user-subject grants in one transaction. The user record and
	// their authored content survive with attribution intact. ErrLastAdmin
	// when they are the org's last active admin.
	RemoveFromOrg(ctx context.Context, orgID, userID uuid.UUID) error
	// ChangeOrgRole updates the membership role. ErrLastAdmin when demoting
	// the last active admin; ErrCannotChangeOwner when the target currently
	// holds the owner role.
	ChangeOrgRole(ctx context.Context, orgID, userID uuid.UUID, role string) error
	// ChangePrimaryTeam enrols the user in the team if needed and marks it
	// primary. ErrTeamNotFound when the team is not a live team of the org.
	ChangePrimaryTeam(ctx context.Context, orgID, userID, teamID uuid.UUID) error
	// UpdateProfile changes a member's display name. Org-scoped: the adapter
	// verifies the target is a member of orgID before writing (ErrNotMember
	// otherwise). It touches only display_name — never is_active or
	// token_generation, so the deactivate-kills-sessions invariant is
	// unaffected.
	UpdateProfile(ctx context.Context, orgID, userID uuid.UUID, displayName string) error
}

// maxDisplayNameLen bounds a display name (matches the self-serve path).
const maxDisplayNameLen = 255

// Service wraps a Store with input validation.
type Service struct {
	store Store
}

// NewService creates a people Service.
func NewService(store Store) *Service { return &Service{store: store} }

// List returns every member of the org.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Person, error) {
	people, err := s.store.List(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing people: %w", err)
	}
	return people, nil
}

// Search returns active members matching the query, for the picker.
func (s *Service) Search(ctx context.Context, orgID uuid.UUID, query string) ([]PersonRef, error) {
	refs, err := s.store.Search(ctx, orgID, query)
	if err != nil {
		return nil, fmt.Errorf("searching members: %w", err)
	}
	return refs, nil
}

// Deactivate blocks sign-in and always terminates the user's sessions.
func (s *Service) Deactivate(ctx context.Context, orgID, userID uuid.UUID) error {
	if err := s.store.Deactivate(ctx, orgID, userID); err != nil {
		return fmt.Errorf("deactivating user: %w", err)
	}
	return nil
}

// Reactivate re-enables sign-in.
func (s *Service) Reactivate(ctx context.Context, orgID, userID uuid.UUID) error {
	if err := s.store.Reactivate(ctx, orgID, userID); err != nil {
		return fmt.Errorf("reactivating user: %w", err)
	}
	return nil
}

// ForceLogout signs the user out everywhere; they stay active.
func (s *Service) ForceLogout(ctx context.Context, orgID, userID uuid.UUID) error {
	if err := s.store.ForceLogout(ctx, orgID, userID); err != nil {
		return fmt.Errorf("forcing logout: %w", err)
	}
	return nil
}

// RemoveFromOrg drops membership, team rows, and grants; the account and
// authored content survive.
func (s *Service) RemoveFromOrg(ctx context.Context, orgID, userID uuid.UUID) error {
	if err := s.store.RemoveFromOrg(ctx, orgID, userID); err != nil {
		return fmt.Errorf("removing member: %w", err)
	}
	return nil
}

// ChangeOrgRole sets the membership role to member or admin. The owner role
// is assigned at provisioning only and never through this path.
func (s *Service) ChangeOrgRole(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	if role != string(rbac.RoleMember) && role != string(rbac.RoleAdmin) {
		return ErrInvalidOrgRole
	}
	if err := s.store.ChangeOrgRole(ctx, orgID, userID, role); err != nil {
		return fmt.Errorf("changing org role: %w", err)
	}
	return nil
}

// ChangePrimaryTeam enrols the user in the team if needed and marks it primary.
func (s *Service) ChangePrimaryTeam(ctx context.Context, orgID, userID, teamID uuid.UUID) error {
	if err := s.store.ChangePrimaryTeam(ctx, orgID, userID, teamID); err != nil {
		return fmt.Errorf("changing primary team: %w", err)
	}
	return nil
}

// UpdateProfile changes a member's display name after trimming and validating
// it. The store scopes the write to the org.
func (s *Service) UpdateProfile(ctx context.Context, orgID, userID uuid.UUID, displayName string) error {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return ErrInvalidDisplayName
	}
	if len(displayName) > maxDisplayNameLen {
		return ErrInvalidDisplayName
	}
	if err := s.store.UpdateProfile(ctx, orgID, userID, displayName); err != nil {
		return fmt.Errorf("updating profile: %w", err)
	}
	return nil
}
