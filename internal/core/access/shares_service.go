package access

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Share is an entity share row in domain form (ADR-0008).
type Share struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	SpaceID    uuid.UUID
	EntityType string
	EntityID   uuid.UUID
	Audience   ShareAudience
	AudienceID *uuid.UUID
	Cascade    bool
	ExpiresAt  *time.Time
	CreatedBy  uuid.UUID
	CreatedAt  time.Time
	RevokedAt  *time.Time
}

// Expired reports whether the share is past its expiry. Expiry denies in
// the resolution query; this mirror exists for list output only.
func (s Share) Expired() bool {
	return s.ExpiresAt != nil && !s.ExpiresAt.After(time.Now())
}

// SpacePageShare is one active page share rooted in a space, with the root's
// current path — enough for a space reader to compute, per page, whether it
// is directly shared or covered by a cascade (ShareBadge, ADR-0008 rule 5).
type SpacePageShare struct {
	EntityID uuid.UUID
	Cascade  bool
	RootPath string
}

// SharedEntityRef locates a shareable entity: its org and space, and — for
// pages — the materialized path that cascade coverage is computed from.
type SharedEntityRef struct {
	OrgID   uuid.UUID
	SpaceID uuid.UUID
	// PagePath is set only for pages.
	PagePath string
}

// ShareStore is the persistence contract for the share lifecycle.
type ShareStore interface {
	// LookupSharedEntity resolves (entityType, id) to its org/space (and
	// page path). ErrSharedEntityNotFound when no live entity exists.
	LookupSharedEntity(ctx context.Context, entityType string, id uuid.UUID) (SharedEntityRef, error)
	// TeamExistsInOrg reports whether a live team with this id exists in
	// the org (audience validation).
	TeamExistsInOrg(ctx context.Context, orgID, teamID uuid.UUID) (bool, error)
	// CreateShare inserts the row. ErrDuplicateShare when an active share
	// for the same (entity, audience) cell exists.
	CreateShare(ctx context.Context, s Share) (Share, error)
	// GetShare returns one share (revoked included). ErrShareNotFound.
	GetShare(ctx context.Context, id uuid.UUID) (Share, error)
	// RevokeShare sets revoked_at. ErrShareAlreadyRevoked when the row was
	// already revoked; ErrShareNotFound when it does not exist.
	RevokeShare(ctx context.Context, id uuid.UUID) (Share, error)
	// ListSharesByEntity returns the entity's unrevoked shares (expired
	// included — the UI labels them).
	ListSharesByEntity(ctx context.Context, orgID uuid.UUID, entityType string, entityID uuid.UUID) ([]Share, error)
	// CountPageSubtree counts a page plus its descendants — the cascade
	// confirmation number (ADR-0008 rule 7), served by the API.
	CountPageSubtree(ctx context.Context, spaceID, pageID uuid.UUID, path string) (int64, error)
	// CountActiveSharesForPageSubtree counts live shares on a page and its
	// descendants — the cross-space move warning number (rule 9).
	CountActiveSharesForPageSubtree(ctx context.Context, spaceID, pageID uuid.UUID, path string) (int64, error)
}

// ShareService owns share lifecycle rules. Capability enforcement
// (manage_shares) stays at the handler layer with the request's Resolution;
// this service owns referential validation and the ADR-0008 constraints.
type ShareService struct {
	store ShareStore
}

// NewShareService creates a ShareService.
func NewShareService(store ShareStore) *ShareService {
	return &ShareService{store: store}
}

// CreateShareInput is the caller's choice set for one share — data, not
// policy: audience, cascade, and expiry live on the row (ADR-0008).
type CreateShareInput struct {
	OrgID      uuid.UUID
	EntityType string
	EntityID   uuid.UUID
	Audience   ShareAudience
	AudienceID *uuid.UUID
	Cascade    bool
	ExpiresAt  *time.Time
	CreatedBy  uuid.UUID
}

// LookupEntity resolves the target entity for authorisation: handlers call
// this first, 404 on ErrSharedEntityNotFound or an org mismatch, then check
// manage_shares against the returned space.
func (s *ShareService) LookupEntity(ctx context.Context, orgID uuid.UUID, entityType string, entityID uuid.UUID) (SharedEntityRef, error) {
	if !ValidShareEntityType(entityType) {
		return SharedEntityRef{}, ErrInvalidShareEntityType
	}
	ref, err := s.store.LookupSharedEntity(ctx, entityType, entityID)
	if err != nil {
		return SharedEntityRef{}, fmt.Errorf("looking up shared entity: %w", err)
	}
	if ref.OrgID != orgID {
		// An entity from another org does not exist as far as this org's
		// callers can tell.
		return SharedEntityRef{}, ErrSharedEntityNotFound
	}
	return ref, nil
}

// Create validates ADR-0008's constraints and inserts the share. The
// handler has already resolved the entity (LookupEntity) and checked
// manage_shares on its space.
func (s *ShareService) Create(ctx context.Context, ref SharedEntityRef, in CreateShareInput) (Share, error) {
	if err := s.validateAudience(ctx, in); err != nil {
		return Share{}, err
	}
	if in.Cascade && in.EntityType != ShareEntityPage {
		return Share{}, ErrShareCascadeNotPage
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return Share{}, ErrShareExpiryNotFuture
	}

	share, err := s.store.CreateShare(ctx, Share{
		ID:         uuid.New(),
		OrgID:      in.OrgID,
		SpaceID:    ref.SpaceID,
		EntityType: in.EntityType,
		EntityID:   in.EntityID,
		Audience:   in.Audience,
		AudienceID: in.AudienceID,
		Cascade:    in.Cascade,
		ExpiresAt:  in.ExpiresAt,
		CreatedBy:  in.CreatedBy,
	})
	if err != nil {
		return Share{}, fmt.Errorf("creating share: %w", err)
	}
	return share, nil
}

// validateAudience enforces the org/team audience rules (mirroring the
// entity_shares_audience_id_present CHECK): org carries no team; team must
// name a live team of the org.
func (s *ShareService) validateAudience(ctx context.Context, in CreateShareInput) error {
	switch in.Audience {
	case AudienceOrg:
		if in.AudienceID != nil {
			return ErrShareAudienceIDForbidden
		}
		return nil
	case AudienceTeam:
		if in.AudienceID == nil {
			return ErrShareAudienceIDRequired
		}
		exists, err := s.store.TeamExistsInOrg(ctx, in.OrgID, *in.AudienceID)
		if err != nil {
			return fmt.Errorf("checking share audience team: %w", err)
		}
		if !exists {
			return ErrShareAudienceTeamNotFound
		}
		return nil
	default:
		return ErrInvalidShareAudience
	}
}

// Get returns one share scoped to the org: a share of another org does not
// exist as far as this org's callers can tell.
func (s *ShareService) Get(ctx context.Context, orgID, id uuid.UUID) (Share, error) {
	share, err := s.store.GetShare(ctx, id)
	if err != nil {
		return Share{}, fmt.Errorf("getting share: %w", err)
	}
	if share.OrgID != orgID {
		return Share{}, ErrShareNotFound
	}
	return share, nil
}

// Revoke sets revoked_at. The handler has already loaded the share (Get)
// and checked manage_shares on its space. Readable sets are computed per
// request, so revocation denies on the very next request (ADR-0008 rule 11).
func (s *ShareService) Revoke(ctx context.Context, id uuid.UUID) (Share, error) {
	share, err := s.store.RevokeShare(ctx, id)
	if err != nil {
		return Share{}, fmt.Errorf("revoking share: %w", err)
	}
	return share, nil
}

// ListByEntity returns the entity's unrevoked shares.
func (s *ShareService) ListByEntity(ctx context.Context, orgID uuid.UUID, entityType string, entityID uuid.UUID) ([]Share, error) {
	shares, err := s.store.ListSharesByEntity(ctx, orgID, entityType, entityID)
	if err != nil {
		return nil, fmt.Errorf("listing shares: %w", err)
	}
	return shares, nil
}

// CoversForCaller reports whether the request's resolved share coverage
// (SharedEntities, cached on the context by the ResolveShares middleware)
// includes the entity. It is the single authorisation gate for every
// share-authorised route — the standalone read route and the shared
// attachment path both call it, so coverage is decided in exactly one
// place. For a page, cascade coverage needs the page's CURRENT space and
// path; those are resolved here and used only for the decision, never
// returned. Fails closed: no coverage on the context, a bad entity type, or
// a lookup error all deny.
func (s *ShareService) CoversForCaller(ctx context.Context, entityType string, id uuid.UUID) bool {
	se := SharedEntitiesFromContext(ctx)
	if se == nil {
		return false
	}
	if !ValidShareEntityType(entityType) {
		return false
	}
	if entityType != ShareEntityPage {
		return se.CoversEntity(entityType, id)
	}
	// A direct page share needs no container lookup.
	if se.CoversEntity(ShareEntityPage, id) {
		return true
	}
	ref, err := s.store.LookupSharedEntity(ctx, ShareEntityPage, id)
	if err != nil {
		return false
	}
	return se.CoversPage(id, ref.SpaceID, ref.PagePath)
}

// CascadePreview returns how many pages (root included) a cascading share
// on the page would cover right now.
func (s *ShareService) CascadePreview(ctx context.Context, ref SharedEntityRef, pageID uuid.UUID) (int64, error) {
	n, err := s.store.CountPageSubtree(ctx, ref.SpaceID, pageID, ref.PagePath)
	if err != nil {
		return 0, fmt.Errorf("counting cascade subtree: %w", err)
	}
	return n, nil
}
