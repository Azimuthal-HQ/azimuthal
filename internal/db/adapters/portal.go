package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// PortalAdapter implements portal.Store over PostgreSQL.
type PortalAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewPortalAdapter creates a PortalAdapter. It takes a pool because two of its
// operations are transactional: issuing a magic link (supersede then create)
// and raising a request (insert with a retry on the ticket-number race).
func NewPortalAdapter(pool *pgxpool.Pool) *PortalAdapter {
	return &PortalAdapter{pool: pool, q: generated.New(pool)}
}

// ── Portals ──────────────────────────────────────────────────────────────

// PortalByKey returns the enabled portal with the given public key.
func (a *PortalAdapter) PortalByKey(ctx context.Context, key string) (portal.Portal, error) {
	row, err := a.q.GetPortalByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return portal.Portal{}, portal.ErrPortalNotFound
	}
	if err != nil {
		return portal.Portal{}, fmt.Errorf("portal adapter get by key: %w", err)
	}
	return portal.Portal{
		ID: row.ID, SpaceID: row.SpaceID, OrgID: row.OrgID, Key: row.PortalKey,
		Name: row.Name, Intro: row.Intro, Enabled: row.Enabled,
	}, nil
}

// PortalByID returns an enabled portal by id.
func (a *PortalAdapter) PortalByID(ctx context.Context, id uuid.UUID) (portal.Portal, error) {
	row, err := a.q.GetPortalByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return portal.Portal{}, portal.ErrPortalNotFound
	}
	if err != nil {
		return portal.Portal{}, fmt.Errorf("portal adapter get by id: %w", err)
	}
	return portal.Portal{
		ID: row.ID, SpaceID: row.SpaceID, OrgID: row.OrgID, Key: row.PortalKey,
		Name: row.Name, Intro: row.Intro, Enabled: row.Enabled,
	}, nil
}

// PortalBySpace returns a space's portal, enabled or not — the agent-side
// settings screen has to show a disabled one in order to offer re-enabling it.
func (a *PortalAdapter) PortalBySpace(ctx context.Context, spaceID uuid.UUID) (portal.Portal, error) {
	row, err := a.q.GetPortalBySpace(ctx, spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return portal.Portal{}, portal.ErrPortalNotFound
	}
	if err != nil {
		return portal.Portal{}, fmt.Errorf("portal adapter get by space: %w", err)
	}
	return portal.Portal{
		ID: row.ID, SpaceID: row.SpaceID, OrgID: row.OrgID, Key: row.PortalKey,
		Name: row.Name, Intro: row.Intro, Enabled: row.Enabled,
	}, nil
}

// CreatePortal opts a space into the customer portal.
func (a *PortalAdapter) CreatePortal(ctx context.Context, p portal.Portal, createdBy uuid.UUID) (portal.Portal, error) {
	row, err := a.q.CreatePortal(ctx, generated.CreatePortalParams{
		SpaceID:   p.SpaceID,
		PortalKey: p.Key,
		Name:      p.Name,
		Intro:     p.Intro,
		CreatedBy: createdBy,
	})
	if constraint, ok := uniqueViolation(err); ok {
		// space_id is UNIQUE, so a second portal for one space lands here
		// rather than creating a shadow portal with a different key.
		if constraint == "service_desk_portals_space_id_key" {
			return portal.Portal{}, portal.ErrPortalExists
		}
		return portal.Portal{}, fmt.Errorf("portal adapter create: %w", err)
	}
	if err != nil {
		return portal.Portal{}, fmt.Errorf("portal adapter create: %w", err)
	}
	return a.PortalBySpace(ctx, row.SpaceID)
}

// SetPortalEnabled toggles a portal without discarding its key, so that
// re-enabling does not invalidate every URL already shared.
func (a *PortalAdapter) SetPortalEnabled(ctx context.Context, spaceID uuid.UUID, enabled bool) (portal.Portal, error) {
	_, err := a.q.SetPortalEnabled(ctx, generated.SetPortalEnabledParams{SpaceID: spaceID, Enabled: enabled})
	if errors.Is(err, pgx.ErrNoRows) {
		return portal.Portal{}, portal.ErrPortalNotFound
	}
	if err != nil {
		return portal.Portal{}, fmt.Errorf("portal adapter set enabled: %w", err)
	}
	return a.PortalBySpace(ctx, spaceID)
}

// ── Requesters ───────────────────────────────────────────────────────────

// UpsertRequester finds or creates the identity for (org, email).
func (a *PortalAdapter) UpsertRequester(ctx context.Context, orgID uuid.UUID, email, displayName string) (portal.Requester, error) {
	row, err := a.q.UpsertRequester(ctx, generated.UpsertRequesterParams{
		OrgID: orgID, Email: email, DisplayName: displayName,
	})
	if err != nil {
		return portal.Requester{}, fmt.Errorf("portal adapter upsert requester: %w", err)
	}
	return requesterFromRow(row), nil
}

// RequesterByID returns one requester.
func (a *PortalAdapter) RequesterByID(ctx context.Context, id uuid.UUID) (portal.Requester, error) {
	row, err := a.q.GetRequesterByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return portal.Requester{}, portal.ErrRequestNotFound
	}
	if err != nil {
		return portal.Requester{}, fmt.Errorf("portal adapter get requester: %w", err)
	}
	return requesterFromRow(row), nil
}

// RequesterState is the guard's per-request revocation read. It is exactly one
// primary-key lookup, and must stay that way: spec §2.5 case 23 forbids a
// per-request authorisation cost that grows, and TestMatrixAPI23 counts these.
func (a *PortalAdapter) RequesterState(ctx context.Context, requesterID uuid.UUID) (portal.RequesterState, error) {
	row, err := a.q.GetRequesterState(ctx, requesterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return portal.RequesterState{}, portal.ErrInvalidSession
	}
	if err != nil {
		return portal.RequesterState{}, fmt.Errorf("portal adapter requester state: %w", err)
	}
	return portal.RequesterState{IsActive: row.IsActive, SessionGeneration: int(row.SessionGeneration)}, nil
}

// BumpRequesterSessions revokes every session the requester holds.
func (a *PortalAdapter) BumpRequesterSessions(ctx context.Context, requesterID uuid.UUID) error {
	if _, err := a.q.BumpRequesterSessions(ctx, requesterID); err != nil {
		return fmt.Errorf("portal adapter bump requester sessions: %w", err)
	}
	return nil
}

// ── Magic links ──────────────────────────────────────────────────────────

// CreateMagicLink supersedes the requester's outstanding links for this portal
// and stores the new one, in ONE transaction.
//
// The two statements have to commit together. Superseding without creating
// would leave a requester who asked for a link with none at all; creating
// without superseding would leave two live credentials in one inbox, and the
// older one would keep working after the newer had been used.
func (a *PortalAdapter) CreateMagicLink(ctx context.Context, requesterID, portalID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("portal adapter create magic link: begin: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.Error("portal magic link rollback", "error", rbErr)
		}
	}()

	q := a.q.WithTx(tx)
	if _, err := q.InvalidateOutstandingLinks(ctx, generated.InvalidateOutstandingLinksParams{
		RequesterID: requesterID, PortalID: portalID,
	}); err != nil {
		return fmt.Errorf("portal adapter invalidate outstanding links: %w", err)
	}
	if _, err := q.CreateMagicLink(ctx, generated.CreateMagicLinkParams{
		RequesterID: requesterID,
		PortalID:    portalID,
		TokenHash:   tokenHash,
		ExpiresAt:   pgTimestamp(expiresAt),
	}); err != nil {
		return fmt.Errorf("portal adapter create magic link: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("portal adapter create magic link: commit: %w", err)
	}
	return nil
}

// ConsumeMagicLink redeems a link. Zero rows means unknown, already consumed,
// superseded or expired — all four collapse to ErrInvalidLink, because telling
// the caller which one it was tells an attacker which to work on.
func (a *PortalAdapter) ConsumeMagicLink(ctx context.Context, tokenHash string) (portal.MagicLinkRedemption, error) {
	row, err := a.q.ConsumeMagicLink(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return portal.MagicLinkRedemption{}, portal.ErrInvalidLink
	}
	if err != nil {
		return portal.MagicLinkRedemption{}, fmt.Errorf("portal adapter consume magic link: %w", err)
	}
	return portal.MagicLinkRedemption{RequesterID: row.RequesterID, PortalID: row.PortalID}, nil
}

// ── Requests ─────────────────────────────────────────────────────────────

// maxRequestNumberAttempts bounds the retry on the ticket-number race.
//
// tickets has no per-space counter row (project_items got one in migration
// 031; tickets never did), so the number is derived from MAX(number) at insert
// time and two concurrent creators can pick the same one. The insert is a
// single statement, which narrows the window to that statement, and this retry
// closes what remains. Five attempts is generous: each retry re-reads the max,
// so it only loops while somebody else is committing in the same instant.
const maxRequestNumberAttempts = 5

// CreateRequest raises a portal-originated ticket, retrying the ticket-number
// race described at maxRequestNumberAttempts.
func (a *PortalAdapter) CreateRequest(ctx context.Context, portalID, spaceID, requesterID uuid.UUID, in portal.NewRequest) (portal.Request, error) {
	_ = portalID // the portal binds the session; the ticket belongs to the space
	var lastErr error
	for attempt := range maxRequestNumberAttempts {
		row, err := a.q.CreatePortalRequest(ctx, generated.CreatePortalRequestParams{
			SpaceID:     spaceID,
			Title:       in.Summary,
			Description: in.Description,
			RequesterID: pgtype.UUID{Bytes: requesterID, Valid: true},
		})
		if err == nil {
			return portal.Request{
				ID: row.ID, Summary: row.Title, Description: row.Description,
				Status: row.Status, CreatedAt: goTime(row.CreatedAt), UpdatedAt: goTime(row.UpdatedAt),
			}, nil
		}
		if constraint, ok := uniqueViolation(err); ok && constraint == "tickets_space_id_number_key" {
			lastErr = err
			slog.Warn("portal request number collision, retrying", "space_id", spaceID, "attempt", attempt+1)
			continue
		}
		return portal.Request{}, fmt.Errorf("portal adapter create request: %w", err)
	}
	return portal.Request{}, fmt.Errorf("portal adapter create request: exhausted number retries: %w", lastErr)
}

// ListRequests returns the requester's own requests in one space.
func (a *PortalAdapter) ListRequests(ctx context.Context, spaceID, requesterID uuid.UUID) ([]portal.Request, error) {
	rows, err := a.q.ListPortalRequests(ctx, generated.ListPortalRequestsParams{
		SpaceID: spaceID, RequesterID: pgtype.UUID{Bytes: requesterID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("portal adapter list requests: %w", err)
	}
	out := make([]portal.Request, 0, len(rows))
	for _, r := range rows {
		out = append(out, portal.Request{
			ID: r.ID, Summary: r.Title, Description: r.Description, Status: r.Status,
			CreatedAt: goTime(r.CreatedAt), UpdatedAt: goTime(r.UpdatedAt),
		})
	}
	return out, nil
}

// GetRequest returns one of the requester's own requests, or
// ErrRequestNotFound — which also covers somebody else's request.
func (a *PortalAdapter) GetRequest(ctx context.Context, spaceID, requesterID, requestID uuid.UUID) (portal.Request, error) {
	row, err := a.q.GetPortalRequest(ctx, generated.GetPortalRequestParams{
		ID: requestID, SpaceID: spaceID, RequesterID: pgtype.UUID{Bytes: requesterID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return portal.Request{}, portal.ErrRequestNotFound
	}
	if err != nil {
		return portal.Request{}, fmt.Errorf("portal adapter get request: %w", err)
	}
	return portal.Request{
		ID: row.ID, Summary: row.Title, Description: row.Description, Status: row.Status,
		CreatedAt: goTime(row.CreatedAt), UpdatedAt: goTime(row.UpdatedAt),
	}, nil
}

// AssigneeFor reports the ticket's assignee; uuid.Nil means unassigned.
func (a *PortalAdapter) AssigneeFor(ctx context.Context, requestID uuid.UUID) (uuid.UUID, error) {
	row, err := a.q.GetTicketAssignee(ctx, requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, portal.ErrRequestNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("portal adapter assignee: %w", err)
	}
	if !row.Valid {
		return uuid.Nil, nil
	}
	return uuid.UUID(row.Bytes), nil
}

// ── Messages ─────────────────────────────────────────────────────────────

// ListPublicMessages returns the public comments on a request.
func (a *PortalAdapter) ListPublicMessages(ctx context.Context, requestID uuid.UUID) ([]portal.Message, error) {
	rows, err := a.q.ListPortalTicketComments(ctx, requestID)
	if err != nil {
		return nil, fmt.Errorf("portal adapter list messages: %w", err)
	}
	out := make([]portal.Message, 0, len(rows))
	for _, r := range rows {
		out = append(out, portal.Message{
			ID: r.ID, AuthorLabel: r.AuthorLabel, FromRequester: r.FromRequester,
			Body: r.Body, CreatedAt: goTime(r.CreatedAt),
		})
	}
	return out, nil
}

// AppendRequesterMessage writes a requester's reply as a public comment.
func (a *PortalAdapter) AppendRequesterMessage(ctx context.Context, requestID, requesterID uuid.UUID, body string) (portal.Message, error) {
	row, err := a.q.CreateRequesterComment(ctx, generated.CreateRequesterCommentParams{
		EntityID:          requestID,
		AuthorRequesterID: pgtype.UUID{Bytes: requesterID, Valid: true},
		Body:              body,
	})
	if err != nil {
		return portal.Message{}, fmt.Errorf("portal adapter append message: %w", err)
	}
	return portal.Message{
		ID: row.ID, Body: row.Body, FromRequester: true, CreatedAt: goTime(row.CreatedAt),
	}, nil
}

func requesterFromRow(r generated.Requester) portal.Requester {
	return portal.Requester{
		ID:                r.ID,
		OrgID:             r.OrgID,
		Email:             r.Email,
		DisplayName:       r.DisplayName,
		IsActive:          r.IsActive,
		SessionGeneration: int(r.SessionGeneration),
		CreatedAt:         goTime(r.CreatedAt),
	}
}
