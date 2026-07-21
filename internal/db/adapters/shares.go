package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// ShareAdapter implements access.ShareResolutionStore and access.ShareStore
// over sqlc-generated queries (v0.3 spec §4 migration 026, §5, ADR-0008).
type ShareAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewShareAdapter creates a ShareAdapter.
func NewShareAdapter(pool *pgxpool.Pool) *ShareAdapter {
	return &ShareAdapter{pool: pool, q: generated.New(pool)}
}

// ResolveShareRows runs the once-per-request share resolution (spec §5):
// one query, constant regardless of how many shares exist. Revocation and
// expiry filter here, which is what makes both immediate.
func (a *ShareAdapter) ResolveShareRows(ctx context.Context, orgID, userID uuid.UUID) ([]access.ShareRow, error) {
	rows, err := a.q.ResolveShareRows(ctx, generated.ResolveShareRowsParams{UserID: userID, OrgID: orgID})
	if err != nil {
		return nil, fmt.Errorf("resolving share rows: %w", err)
	}
	out := make([]access.ShareRow, 0, len(rows))
	for _, r := range rows {
		row := access.ShareRow{
			EntityType: r.EntityType,
			EntityID:   r.EntityID,
			Cascade:    r.Cascade,
			RootPath:   r.RootPath,
		}
		if r.RootSpaceID.Valid {
			row.RootSpaceID = goUUIDPtr(r.RootSpaceID)
		}
		out = append(out, row)
	}
	return out, nil
}

// LookupSharedEntity resolves a shareable entity to its org and space (and,
// for pages, its materialized path). Each per-type branch defers its
// row→ref mapping and error normalisation to lookupRef.
func (a *ShareAdapter) LookupSharedEntity(ctx context.Context, entityType string, id uuid.UUID) (access.SharedEntityRef, error) {
	switch entityType {
	case access.ShareEntityPage:
		row, err := a.q.LookupSharedPage(ctx, id)
		return lookupRef(access.SharedEntityRef{OrgID: row.OrgID, SpaceID: row.SpaceID, PagePath: row.Path}, "page", err)
	case access.ShareEntityTicket:
		row, err := a.q.LookupSharedTicket(ctx, id)
		return lookupRef(access.SharedEntityRef{OrgID: row.OrgID, SpaceID: row.SpaceID}, "ticket", err)
	case access.ShareEntityProjectItem:
		row, err := a.q.LookupSharedProjectItem(ctx, id)
		return lookupRef(access.SharedEntityRef{OrgID: row.OrgID, SpaceID: row.SpaceID}, "project item", err)
	default:
		return access.SharedEntityRef{}, access.ErrInvalidShareEntityType
	}
}

// lookupRef normalises a per-type lookup: a no-rows error becomes the
// existence-preserving ErrSharedEntityNotFound; any other error is wrapped;
// success returns the already-built ref.
func lookupRef(ref access.SharedEntityRef, kind string, err error) (access.SharedEntityRef, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return access.SharedEntityRef{}, access.ErrSharedEntityNotFound
	}
	if err != nil {
		return access.SharedEntityRef{}, fmt.Errorf("looking up shared %s: %w", kind, err)
	}
	return ref, nil
}

// TeamExistsInOrg reports whether a live team with this id exists in the
// org — team-audience validation, mirroring the grant-subject rule.
func (a *ShareAdapter) TeamExistsInOrg(ctx context.Context, orgID, teamID uuid.UUID) (bool, error) {
	t, err := a.q.GetTeamByID(ctx, teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking share audience team: %w", err)
	}
	return t.OrgID == orgID && !t.DeletedAt.Valid, nil
}

// CreateShare inserts a share row. The partial unique index on active
// shares maps to ErrDuplicateShare; an audience FK failure means the team
// vanished between validation and insert.
func (a *ShareAdapter) CreateShare(ctx context.Context, s access.Share) (access.Share, error) {
	row, err := a.q.CreateEntityShare(ctx, generated.CreateEntityShareParams{
		ID:         s.ID,
		OrgID:      s.OrgID,
		SpaceID:    s.SpaceID,
		EntityType: s.EntityType,
		EntityID:   s.EntityID,
		Audience:   string(s.Audience),
		AudienceID: pgUUID(s.AudienceID),
		Cascade:    s.Cascade,
		ExpiresAt:  pgTimestampPtr(s.ExpiresAt),
		CreatedBy:  s.CreatedBy,
	})
	if _, ok := uniqueViolation(err); ok {
		return access.Share{}, access.ErrDuplicateShare
	}
	if isForeignKeyViolation(err) {
		return access.Share{}, access.ErrShareAudienceTeamNotFound
	}
	if err != nil {
		return access.Share{}, fmt.Errorf("creating share: %w", err)
	}
	return dbShareToDomain(row), nil
}

// GetShare returns one share, revoked included.
func (a *ShareAdapter) GetShare(ctx context.Context, id uuid.UUID) (access.Share, error) {
	row, err := a.q.GetEntityShare(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Share{}, access.ErrShareNotFound
	}
	if err != nil {
		return access.Share{}, fmt.Errorf("getting share: %w", err)
	}
	return dbShareToDomain(row), nil
}

// RevokeShare sets revoked_at. Zero rows means the share either never
// existed or was already revoked — disambiguated with a follow-up read.
func (a *ShareAdapter) RevokeShare(ctx context.Context, id uuid.UUID) (access.Share, error) {
	row, err := a.q.RevokeEntityShare(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := a.q.GetEntityShare(ctx, id); getErr == nil {
			return access.Share{}, access.ErrShareAlreadyRevoked
		}
		return access.Share{}, access.ErrShareNotFound
	}
	if err != nil {
		return access.Share{}, fmt.Errorf("revoking share: %w", err)
	}
	return dbShareToDomain(row), nil
}

// ListSharesByEntity returns the entity's unrevoked shares.
func (a *ShareAdapter) ListSharesByEntity(ctx context.Context, orgID uuid.UUID, entityType string, entityID uuid.UUID) ([]access.Share, error) {
	rows, err := a.q.ListSharesByEntity(ctx, generated.ListSharesByEntityParams{
		OrgID:      orgID,
		EntityType: entityType,
		EntityID:   entityID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing shares: %w", err)
	}
	out := make([]access.Share, 0, len(rows))
	for _, r := range rows {
		out = append(out, dbShareToDomain(r))
	}
	return out, nil
}

// CountPageSubtree counts the page plus its live descendants.
func (a *ShareAdapter) CountPageSubtree(ctx context.Context, spaceID, pageID uuid.UUID, path string) (int64, error) {
	n, err := a.q.CountPageSubtree(ctx, generated.CountPageSubtreeParams{
		SpaceID:     spaceID,
		PageID:      pageID,
		PathPattern: access.SubtreeLikePattern(path),
	})
	if err != nil {
		return 0, fmt.Errorf("counting page subtree: %w", err)
	}
	return n, nil
}

// ListActiveSharesForSpacePages returns every active page share rooted in
// the space (one query, constant regardless of page count) — the ShareBadge
// annotation source for a whole space.
func (a *ShareAdapter) ListActiveSharesForSpacePages(ctx context.Context, spaceID uuid.UUID) ([]access.SpacePageShare, error) {
	rows, err := a.q.ListActiveSharesForSpacePages(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("listing space page shares: %w", err)
	}
	out := make([]access.SpacePageShare, 0, len(rows))
	for _, r := range rows {
		out = append(out, access.SpacePageShare{EntityID: r.EntityID, Cascade: r.Cascade, RootPath: r.RootPath})
	}
	return out, nil
}

// CountActiveSharesForPageSubtree counts live shares on the page and its
// descendants — what a cross-space move would revoke.
func (a *ShareAdapter) CountActiveSharesForPageSubtree(ctx context.Context, spaceID, pageID uuid.UUID, path string) (int64, error) {
	n, err := a.q.CountActiveSharesForPageSubtree(ctx, generated.CountActiveSharesForPageSubtreeParams{
		SpaceID:     spaceID,
		PageID:      pageID,
		PathPattern: access.SubtreeLikePattern(path),
	})
	if err != nil {
		return 0, fmt.Errorf("counting subtree shares: %w", err)
	}
	return n, nil
}

// dbShareToDomain converts a generated.EntityShare.
func dbShareToDomain(s generated.EntityShare) access.Share {
	return access.Share{
		ID:         s.ID,
		OrgID:      s.OrgID,
		SpaceID:    s.SpaceID,
		EntityType: s.EntityType,
		EntityID:   s.EntityID,
		Audience:   access.ShareAudience(s.Audience),
		AudienceID: goUUIDPtr(s.AudienceID),
		Cascade:    s.Cascade,
		ExpiresAt:  goTimePtr(s.ExpiresAt),
		CreatedBy:  s.CreatedBy,
		CreatedAt:  goTime(s.CreatedAt),
		RevokedAt:  goTimePtr(s.RevokedAt),
	}
}
