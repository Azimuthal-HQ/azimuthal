package access

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SubjectType discriminates polymorphic grant subjects.
type SubjectType string

// Grant subject kinds.
const (
	SubjectUser SubjectType = "user"
	SubjectTeam SubjectType = "team"
)

// Grant is a space grant row in domain form.
type Grant struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	SpaceID     uuid.UUID
	SubjectType SubjectType
	SubjectID   uuid.UUID
	Role        Role
	CreatedAt   time.Time
	CreatedBy   *uuid.UUID
	// SubjectName is the display name of the subject (user display name or
	// team name), populated on list reads for the UI.
	SubjectName string
	// SubjectMissing marks a grant whose subject no longer exists live —
	// e.g. a soft-deleted team. Such grants no longer match at resolution.
	SubjectMissing bool
}

// GrantStore is the persistence contract for grants. subject integrity
// checks live here because subject_id has no foreign key.
type GrantStore interface {
	IsOrgMember(ctx context.Context, orgID, userID uuid.UUID) (bool, error)
	TeamExistsInOrg(ctx context.Context, orgID, teamID uuid.UUID) (bool, error)
	CreateGrant(ctx context.Context, g Grant) (Grant, error) // ErrDuplicateGrant on (space,subject) conflict
	GetGrant(ctx context.Context, id uuid.UUID) (Grant, error)
	UpdateGrantRole(ctx context.Context, id uuid.UUID, role Role) (Grant, error)
	DeleteGrant(ctx context.Context, id uuid.UUID) error
	ListGrantsBySpace(ctx context.Context, spaceID uuid.UUID) ([]Grant, error)
}

// GrantService owns grant lifecycle rules (spec §4 referential obligations).
type GrantService struct {
	store GrantStore
}

// NewGrantService creates a GrantService.
func NewGrantService(store GrantStore) *GrantService {
	return &GrantService{store: store}
}

// Create validates the subject and inserts the grant. A user subject must be
// an org member (ErrSubjectNotOrgMember → 400); a team subject must be a
// live team in the org (ErrSubjectTeamNotFound → 400).
func (s *GrantService) Create(ctx context.Context, orgID, spaceID uuid.UUID, subjectType SubjectType, subjectID uuid.UUID, role Role, createdBy uuid.UUID) (Grant, error) {
	switch subjectType {
	case SubjectUser:
		member, err := s.store.IsOrgMember(ctx, orgID, subjectID)
		if err != nil {
			return Grant{}, fmt.Errorf("checking grant subject membership: %w", err)
		}
		if !member {
			return Grant{}, ErrSubjectNotOrgMember
		}
	case SubjectTeam:
		exists, err := s.store.TeamExistsInOrg(ctx, orgID, subjectID)
		if err != nil {
			return Grant{}, fmt.Errorf("checking grant subject team: %w", err)
		}
		if !exists {
			return Grant{}, ErrSubjectTeamNotFound
		}
	default:
		return Grant{}, fmt.Errorf("unknown grant subject type %q", subjectType)
	}

	return s.store.CreateGrant(ctx, Grant{
		ID:          uuid.New(),
		OrgID:       orgID,
		SpaceID:     spaceID,
		SubjectType: subjectType,
		SubjectID:   subjectID,
		Role:        role,
		CreatedBy:   &createdBy,
	})
}

// Get returns one grant.
func (s *GrantService) Get(ctx context.Context, id uuid.UUID) (Grant, error) {
	return s.store.GetGrant(ctx, id)
}

// UpdateRole changes a grant's role.
func (s *GrantService) UpdateRole(ctx context.Context, id uuid.UUID, role Role) (Grant, error) {
	return s.store.UpdateGrantRole(ctx, id, role)
}

// Revoke deletes a grant. Effective access is computed per request, so the
// revocation takes effect on the next request with no cache to invalidate.
func (s *GrantService) Revoke(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteGrant(ctx, id)
}

// ListBySpace returns every grant on the space.
func (s *GrantService) ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]Grant, error) {
	return s.store.ListGrantsBySpace(ctx, spaceID)
}
