// Package teams implements the team grouping concept (ADR-0006): nested to
// five levels via a materialised path by ID, with an org default team that
// guarantees no user is ever teamless. Teams are grouping and filtering
// only — team_members.role is metadata and never affects permissions.
package teams

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// MaxDepth is the maximum team nesting depth (database CHECK teams_depth_max).
const MaxDepth = 5

// Team is a team row in domain form.
type Team struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"org_id"`
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	// Path is the materialised ancestor chain ending in the team's own id;
	// its length is the team's depth.
	Path        []uuid.UUID `json:"path"`
	Slug        string      `json:"slug"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	IsDefault   bool        `json:"is_default"`
	Source      string      `json:"source"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Member is a team membership row joined with user identity.
type Member struct {
	TeamID      uuid.UUID `json:"team_id"`
	UserID      uuid.UUID `json:"user_id"`
	OrgID       uuid.UUID `json:"org_id"`
	Role        string    `json:"role"` // metadata only — never a permission input
	IsPrimary   bool      `json:"is_primary"`
	CreatedAt   time.Time `json:"created_at"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	AvatarURL   *string   `json:"avatar_url,omitempty"`
}

// Team metadata roles. Metadata only — the capability model never reads
// them. The one sanctioned administrative use is the space-creation
// authority check (ADR-0007: "org admin or a lead of the owning team"),
// which goes through Member.IsLead.
const (
	MemberRoleMember = "member"
	MemberRoleLead   = "lead"
)

// MemberRoles are the valid team_members.role values (metadata only).
var MemberRoles = map[string]bool{MemberRoleMember: true, MemberRoleLead: true}

// IsLead reports whether the membership carries the lead metadata role.
func (m Member) IsLead() bool {
	return m.Role == MemberRoleLead
}

var validSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// Store is the persistence contract. The transactional operations (Create,
// Reparent, Delete, membership moves) run their validations inside the
// transaction under row locks — the service performs input validation only.
type Store interface {
	Create(ctx context.Context, t Team) (Team, error)
	Get(ctx context.Context, id uuid.UUID) (Team, error)
	GetDefault(ctx context.Context, orgID uuid.UUID) (Team, error)
	ListByOrg(ctx context.Context, orgID uuid.UUID) ([]Team, error)
	Update(ctx context.Context, id uuid.UUID, name, description string) (Team, error)
	// Reparent moves the team (and its whole subtree) under newParent
	// (nil = root), rewriting every descendant path in one transaction.
	Reparent(ctx context.Context, orgID, teamID uuid.UUID, newParent *uuid.UUID) (Team, error)
	// Delete soft-deletes the team after moving its members to the org
	// default team (primary reassigned there when needed). Fails with
	// ErrHasChildren / ErrOwnsSpaces / ErrDefaultTeam.
	Delete(ctx context.Context, orgID, teamID uuid.UUID) error

	AddMember(ctx context.Context, teamID, userID, orgID uuid.UUID, role string) (Member, error)
	// RemoveMember removes the user, re-adding them to the org default team
	// (as primary) when it was their last team, and reassigning primary
	// when the removed membership was primary.
	RemoveMember(ctx context.Context, teamID, userID, orgID uuid.UUID) error
	ListMembers(ctx context.Context, teamID uuid.UUID) ([]Member, error)
	GetMember(ctx context.Context, teamID, userID uuid.UUID) (Member, error)
	// SetPrimary makes teamID the user's primary team in the org.
	SetPrimary(ctx context.Context, teamID, userID, orgID uuid.UUID) error
	// EnsureDefaultMembership adds the user to the org default team; primary
	// when the user has no primary team yet. Used at user provisioning.
	EnsureDefaultMembership(ctx context.Context, orgID, userID uuid.UUID) error
	// IsOrgMember reports whether the user holds an org membership.
	IsOrgMember(ctx context.Context, orgID, userID uuid.UUID) (bool, error)
}

// Service owns team lifecycle rules.
type Service struct {
	store Store
}

// NewService creates a team Service.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Create validates inputs and creates a team under the given parent
// (nil = root). Depth and parent-org checks run inside the store transaction.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, parentID *uuid.UUID, slug, name, description string) (Team, error) {
	if name == "" {
		return Team{}, ErrNameRequired
	}
	if !validSlug.MatchString(slug) {
		return Team{}, ErrInvalidSlug
	}
	return s.store.Create(ctx, Team{
		ID:          uuid.New(),
		OrgID:       orgID,
		ParentID:    parentID,
		Slug:        slug,
		Name:        name,
		Description: description,
		Source:      "manual",
	})
}

// Get returns one team.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Team, error) {
	return s.store.Get(ctx, id)
}

// GetDefault returns the org's default team.
func (s *Service) GetDefault(ctx context.Context, orgID uuid.UUID) (Team, error) {
	return s.store.GetDefault(ctx, orgID)
}

// List returns every live team in the org.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Team, error) {
	return s.store.ListByOrg(ctx, orgID)
}

// Rename updates name and description.
func (s *Service) Rename(ctx context.Context, id uuid.UUID, name, description string) (Team, error) {
	if name == "" {
		return Team{}, ErrNameRequired
	}
	return s.store.Update(ctx, id, name, description)
}

// Reparent moves a team (with its subtree) under a new parent. The store
// enforces, inside one transaction: no cycles (the team must not appear in
// the prospective parent's path), and depth(new_parent) + height(subtree)
// <= MaxDepth — checking only the moved node's own depth is insufficient.
func (s *Service) Reparent(ctx context.Context, orgID, teamID uuid.UUID, newParent *uuid.UUID) (Team, error) {
	if newParent != nil && *newParent == teamID {
		return Team{}, ErrCycle
	}
	return s.store.Reparent(ctx, orgID, teamID, newParent)
}

// Delete removes a team per the ADR-0006 rules.
func (s *Service) Delete(ctx context.Context, orgID, teamID uuid.UUID) error {
	return s.store.Delete(ctx, orgID, teamID)
}

// AddMember validates the metadata role and org membership, then enrols the
// user. team_members.role is metadata only and never a permission input.
func (s *Service) AddMember(ctx context.Context, teamID, userID, orgID uuid.UUID, role string) (Member, error) {
	if role == "" {
		role = "member"
	}
	if !MemberRoles[role] {
		return Member{}, ErrInvalidMemberRole
	}
	member, err := s.store.IsOrgMember(ctx, orgID, userID)
	if err != nil {
		return Member{}, fmt.Errorf("checking org membership: %w", err)
	}
	if !member {
		return Member{}, ErrNotOrgMember
	}
	return s.store.AddMember(ctx, teamID, userID, orgID, role)
}

// RemoveMember removes the user from the team (default-team fallback rules
// in the store).
func (s *Service) RemoveMember(ctx context.Context, teamID, userID, orgID uuid.UUID) error {
	return s.store.RemoveMember(ctx, teamID, userID, orgID)
}

// ListMembers returns the team's members with user identity.
func (s *Service) ListMembers(ctx context.Context, teamID uuid.UUID) ([]Member, error) {
	return s.store.ListMembers(ctx, teamID)
}

// GetMember returns one membership row (ErrMemberNotFound when absent).
func (s *Service) GetMember(ctx context.Context, teamID, userID uuid.UUID) (Member, error) {
	return s.store.GetMember(ctx, teamID, userID)
}

// SetPrimary makes the team the user's primary team.
func (s *Service) SetPrimary(ctx context.Context, teamID, userID, orgID uuid.UUID) error {
	return s.store.SetPrimary(ctx, teamID, userID, orgID)
}
