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

// SubjectChecker validates that a polymorphic (user|team) subject exists in an
// org. It is the subset of GrantStore the subject-existence rule needs, exposed
// on its own so a second caller — the workflow approver surface (migration 047)
// — can SHARE this exact check rather than re-deriving membership. Two hand-copied
// membership checks are how two callers come to disagree about who is a member,
// and the direction they drift is "one of them permits more". AccessAdapter
// satisfies it, and so does GrantStore.
type SubjectChecker interface {
	IsOrgMember(ctx context.Context, orgID, userID uuid.UUID) (bool, error)
	TeamExistsInOrg(ctx context.Context, orgID, teamID uuid.UUID) (bool, error)
}

// ValidateSubjectInOrg checks that a polymorphic grant/approver subject exists
// in the org: a user subject must be an org member (ErrSubjectNotOrgMember), a
// team subject must be a live team in the org (ErrSubjectTeamNotFound).
//
// Both arms are org-scoped, so a real subject belonging to another org is
// indistinguishable from one that never existed — neither IsOrgMember nor
// TeamExistsInOrg is an existence oracle over other organisations. The grant
// path and the approver path both call this, so they answer an unknown subject
// identically.
func ValidateSubjectInOrg(ctx context.Context, c SubjectChecker, orgID uuid.UUID, subjectType SubjectType, subjectID uuid.UUID) error {
	switch subjectType {
	case SubjectUser:
		member, err := c.IsOrgMember(ctx, orgID, subjectID)
		if err != nil {
			return fmt.Errorf("checking subject membership: %w", err)
		}
		if !member {
			return ErrSubjectNotOrgMember
		}
	case SubjectTeam:
		exists, err := c.TeamExistsInOrg(ctx, orgID, subjectID)
		if err != nil {
			return fmt.Errorf("checking subject team: %w", err)
		}
		if !exists {
			return ErrSubjectTeamNotFound
		}
	default:
		return fmt.Errorf("unknown subject type %q", subjectType)
	}
	return nil
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
	// The subject-existence rule lives in ValidateSubjectInOrg so the workflow
	// approver surface shares this exact check rather than copying it.
	if err := ValidateSubjectInOrg(ctx, s.store, orgID, subjectType, subjectID); err != nil {
		return Grant{}, err
	}

	grant, err := s.store.CreateGrant(ctx, Grant{
		ID:          uuid.New(),
		OrgID:       orgID,
		SpaceID:     spaceID,
		SubjectType: subjectType,
		SubjectID:   subjectID,
		Role:        role,
		CreatedBy:   &createdBy,
	})
	if err != nil {
		return Grant{}, fmt.Errorf("creating grant: %w", err)
	}
	return grant, nil
}

// Get returns one grant.
func (s *GrantService) Get(ctx context.Context, id uuid.UUID) (Grant, error) {
	grant, err := s.store.GetGrant(ctx, id)
	if err != nil {
		return Grant{}, fmt.Errorf("getting grant: %w", err)
	}
	return grant, nil
}

// UpdateRole changes a grant's role.
func (s *GrantService) UpdateRole(ctx context.Context, id uuid.UUID, role Role) (Grant, error) {
	grant, err := s.store.UpdateGrantRole(ctx, id, role)
	if err != nil {
		return Grant{}, fmt.Errorf("updating grant role: %w", err)
	}
	return grant, nil
}

// Revoke deletes a grant. Effective access is computed per request, so the
// revocation takes effect on the next request with no cache to invalidate.
func (s *GrantService) Revoke(ctx context.Context, id uuid.UUID) error {
	if err := s.store.DeleteGrant(ctx, id); err != nil {
		return fmt.Errorf("revoking grant: %w", err)
	}
	return nil
}

// ListBySpace returns every grant on the space.
func (s *GrantService) ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]Grant, error) {
	grants, err := s.store.ListGrantsBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("listing grants: %w", err)
	}
	return grants, nil
}
