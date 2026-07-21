package adapters

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// BulkGrantAdapter implements access.BulkStore. Preview and Apply share one
// diff function; Apply runs it inside a single transaction with the org's
// team grants locked FOR UPDATE, so the applied diff cannot differ from the
// one computed in that transaction, and writes its audit events through the
// same transaction — a failure anywhere rolls back grants and audit rows
// together. That in-transaction audit write is a deliberate departure from
// the handler-layer audit convention: the batch's audit trail is part of
// its atomicity contract, not best-effort.
type BulkGrantAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewBulkGrantAdapter creates a BulkGrantAdapter.
func NewBulkGrantAdapter(pool *pgxpool.Pool) *BulkGrantAdapter {
	return &BulkGrantAdapter{pool: pool, q: generated.New(pool)}
}

// MatrixData loads teams (with member counts), spaces, and team grants in
// four queries — constant regardless of matrix size (matrix case 23).
func (a *BulkGrantAdapter) MatrixData(ctx context.Context, orgID uuid.UUID) (access.MatrixData, error) {
	teams, err := a.q.ListTeamsByOrg(ctx, orgID)
	if err != nil {
		return access.MatrixData{}, fmt.Errorf("bulk adapter matrix: teams: %w", err)
	}
	counts, err := a.q.CountTeamMembersByOrg(ctx, orgID)
	if err != nil {
		return access.MatrixData{}, fmt.Errorf("bulk adapter matrix: member counts: %w", err)
	}
	spaces, err := a.q.ListSpacesByOrg(ctx, orgID)
	if err != nil {
		return access.MatrixData{}, fmt.Errorf("bulk adapter matrix: spaces: %w", err)
	}
	grants, err := a.q.ListTeamGrantsByOrg(ctx, orgID)
	if err != nil {
		return access.MatrixData{}, fmt.Errorf("bulk adapter matrix: grants: %w", err)
	}

	countByTeam := make(map[uuid.UUID]int, len(counts))
	for _, c := range counts {
		countByTeam[c.TeamID] = int(c.MemberCount)
	}

	out := access.MatrixData{
		Teams:  make([]access.MatrixTeam, 0, len(teams)),
		Spaces: make([]access.MatrixSpace, 0, len(spaces)),
		Grants: make([]access.MatrixGrant, 0, len(grants)),
	}
	for _, t := range teams {
		out.Teams = append(out.Teams, access.MatrixTeam{
			ID:          t.ID,
			ParentID:    goUUIDPtr(t.ParentID),
			Path:        t.Path,
			Name:        t.Name,
			IsDefault:   t.IsDefault,
			MemberCount: countByTeam[t.ID],
		})
	}
	for _, s := range spaces {
		out.Spaces = append(out.Spaces, access.MatrixSpace{
			ID:         s.ID,
			Name:       s.Name,
			Type:       s.Type,
			Visibility: s.Visibility,
		})
	}
	for _, g := range grants {
		out.Grants = append(out.Grants, access.MatrixGrant{
			ID:      g.ID,
			TeamID:  g.TeamID,
			SpaceID: g.SpaceID,
			Role:    g.Role,
		})
	}
	return out, nil
}

// grantKey identifies one matrix cell.
type grantKey struct{ teamID, spaceID uuid.UUID }

// existingGrant is the current state of a cell that has a grant row.
type existingGrant struct {
	id   uuid.UUID
	role string
}

// computeBulkDiff is THE diff: both Preview and Apply classify every
// requested change against current state with this function, which is what
// makes the preview counts and the applied counts the same computation.
func computeBulkDiff(changes []access.BulkChange, current map[grantKey]existingGrant) access.BulkResult {
	res := access.BulkResult{Actions: make([]access.BulkAction, 0, len(changes))}
	for _, c := range changes {
		cur, exists := current[grantKey{c.TeamID, c.SpaceID}]
		action := classifyChange(c, cur, exists)
		switch action.Action {
		case "noop":
			res.Noops++
		case "revoke":
			res.Revokes++
		case "create":
			res.Creates++
		case "update":
			res.Updates++
		}
		res.Actions = append(res.Actions, action)
	}
	return res
}

// classifyChange decides what one requested cell state does to the existing
// grant there.
func classifyChange(c access.BulkChange, cur existingGrant, exists bool) access.BulkAction {
	action := access.BulkAction{TeamID: c.TeamID, SpaceID: c.SpaceID}
	if c.Role == nil {
		if !exists {
			action.Action = "noop"
			return action
		}
		action.Action = "revoke"
		action.FromRole = cur.role
		return action
	}
	if !exists {
		action.Action = "create"
		action.ToRole = c.Role.String()
		return action
	}
	if cur.role == c.Role.String() {
		action.Action = "noop"
		action.FromRole = cur.role
		action.ToRole = cur.role
		return action
	}
	action.Action = "update"
	action.FromRole = cur.role
	action.ToRole = c.Role.String()
	return action
}

// validateBulkTargets rejects changes naming teams or spaces that are not
// live members of the org. The whole batch fails — no partial application.
func validateBulkTargets(changes []access.BulkChange, teams map[uuid.UUID]struct{}, spaces map[uuid.UUID]struct{}) error {
	for _, c := range changes {
		if _, ok := teams[c.TeamID]; !ok {
			return access.ErrBulkUnknownTeam
		}
		if _, ok := spaces[c.SpaceID]; !ok {
			return access.ErrBulkUnknownSpace
		}
	}
	return nil
}

// loadTargetSets returns the org's live team and space id sets.
func loadTargetSets(ctx context.Context, q *generated.Queries, orgID uuid.UUID) (map[uuid.UUID]struct{}, map[uuid.UUID]struct{}, error) {
	teams, err := q.ListTeamsByOrg(ctx, orgID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading teams: %w", err)
	}
	spaceIDs, err := q.ListSpaceIDsByOrg(ctx, orgID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading spaces: %w", err)
	}
	teamSet := make(map[uuid.UUID]struct{}, len(teams))
	for _, t := range teams {
		teamSet[t.ID] = struct{}{}
	}
	spaceSet := make(map[uuid.UUID]struct{}, len(spaceIDs))
	for _, id := range spaceIDs {
		spaceSet[id] = struct{}{}
	}
	return teamSet, spaceSet, nil
}

// PreviewBulk computes the diff without applying it.
func (a *BulkGrantAdapter) PreviewBulk(ctx context.Context, orgID uuid.UUID, changes []access.BulkChange) (access.BulkResult, error) {
	teamSet, spaceSet, err := loadTargetSets(ctx, a.q, orgID)
	if err != nil {
		return access.BulkResult{}, fmt.Errorf("bulk adapter preview: %w", err)
	}
	if err := validateBulkTargets(changes, teamSet, spaceSet); err != nil {
		return access.BulkResult{}, err
	}
	rows, err := a.q.ListTeamGrantsByOrg(ctx, orgID)
	if err != nil {
		return access.BulkResult{}, fmt.Errorf("bulk adapter preview: grants: %w", err)
	}
	current := make(map[grantKey]existingGrant, len(rows))
	for _, g := range rows {
		current[grantKey{g.TeamID, g.SpaceID}] = existingGrant{id: g.ID, role: g.Role}
	}
	return computeBulkDiff(changes, current), nil
}

// ApplyBulk applies the diff as one transaction: lock the org's team
// grants, recompute the diff against the locked state, execute it, and
// write one audit event per non-noop action with one shared batch_id. Any
// failure rolls back everything, audit rows included.
func (a *BulkGrantAdapter) ApplyBulk(ctx context.Context, orgID, actorID uuid.UUID, changes []access.BulkChange, ticketRef string) (access.BulkResult, error) {
	batchID := uuid.New()
	var res access.BulkResult

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return access.BulkResult{}, fmt.Errorf("bulk adapter apply: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := a.q.WithTx(tx)

	teamSet, spaceSet, err := loadTargetSets(ctx, q, orgID)
	if err != nil {
		return access.BulkResult{}, fmt.Errorf("bulk adapter apply: %w", err)
	}
	if err := validateBulkTargets(changes, teamSet, spaceSet); err != nil {
		return access.BulkResult{}, err
	}

	locked, err := q.ListTeamGrantsByOrgForUpdate(ctx, orgID)
	if err != nil {
		return access.BulkResult{}, fmt.Errorf("bulk adapter apply: locking grants: %w", err)
	}
	current := make(map[grantKey]existingGrant, len(locked))
	for _, g := range locked {
		current[grantKey{g.TeamID, g.SpaceID}] = existingGrant{id: g.ID, role: g.Role}
	}

	res = computeBulkDiff(changes, current)
	res.BatchID = batchID

	auditCtx := bulkAuditContext{orgID: orgID, actorID: actorID, batchID: batchID, ticketRef: ticketRef}
	for _, action := range res.Actions {
		if action.Action == "noop" {
			continue
		}
		if err := executeBulkAction(ctx, q, orgID, actorID, action, current, auditCtx); err != nil {
			return access.BulkResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return access.BulkResult{}, fmt.Errorf("bulk adapter apply: commit: %w", err)
	}
	return res, nil
}

// bulkAuditContext carries the batch identity every audit event shares.
type bulkAuditContext struct {
	orgID     uuid.UUID
	actorID   uuid.UUID
	batchID   uuid.UUID
	ticketRef string
}

// executeBulkAction applies one diff line inside the transaction and writes
// its audit event. Any failure fails the whole batch.
func executeBulkAction(ctx context.Context, q *generated.Queries, orgID, actorID uuid.UUID, action access.BulkAction, current map[grantKey]existingGrant, auditCtx bulkAuditContext) error {
	var grantID uuid.UUID
	var eventType audit.EventType
	meta := map[string]string{
		"space_id":     action.SpaceID.String(),
		"subject_type": "team",
		"subject_id":   action.TeamID.String(),
	}
	switch action.Action {
	case "create":
		row, err := q.CreateSpaceGrant(ctx, generated.CreateSpaceGrantParams{
			ID:          uuid.New(),
			OrgID:       orgID,
			SpaceID:     action.SpaceID,
			SubjectType: "team",
			SubjectID:   action.TeamID,
			Role:        action.ToRole,
			CreatedBy:   pgUUID(&actorID),
		})
		if err != nil {
			return fmt.Errorf("bulk adapter apply: create %s/%s: %w", action.TeamID, action.SpaceID, err)
		}
		grantID = row.ID
		eventType = audit.EventTypeGrantCreated
		meta["role"] = action.ToRole
	case "update":
		cur := current[grantKey{action.TeamID, action.SpaceID}]
		if _, err := q.UpdateSpaceGrantRole(ctx, generated.UpdateSpaceGrantRoleParams{ID: cur.id, Role: action.ToRole}); err != nil {
			return fmt.Errorf("bulk adapter apply: update %s: %w", cur.id, err)
		}
		grantID = cur.id
		eventType = audit.EventTypeGrantUpdated
		meta["role"] = action.ToRole
		meta["previous_role"] = action.FromRole
	case "revoke":
		cur := current[grantKey{action.TeamID, action.SpaceID}]
		if err := q.DeleteSpaceGrant(ctx, cur.id); err != nil {
			return fmt.Errorf("bulk adapter apply: revoke %s: %w", cur.id, err)
		}
		grantID = cur.id
		eventType = audit.EventTypeGrantRevoked
		meta["role"] = action.FromRole
	}
	return writeBulkAuditEvent(ctx, q, auditCtx, eventType, grantID, meta)
}

// writeBulkAuditEvent records one batch event through the transaction. The
// audit trail is part of the batch's atomicity contract: failing to record
// it fails the batch.
func writeBulkAuditEvent(ctx context.Context, q *generated.Queries, auditCtx bulkAuditContext, eventType audit.EventType, grantID uuid.UUID, meta map[string]string) error {
	payload, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("bulk adapter apply: marshalling audit payload: %w", err)
	}
	var tref *string
	if auditCtx.ticketRef != "" {
		tref = &auditCtx.ticketRef
	}
	if _, err := q.CreateAuditEvent(ctx, generated.CreateAuditEventParams{
		ID:         uuid.New(),
		OrgID:      auditCtx.orgID,
		ActorID:    pgtype.UUID{Bytes: auditCtx.actorID, Valid: true},
		Action:     string(eventType),
		EntityKind: "grant",
		EntityID:   grantID,
		Payload:    payload,
		BatchID:    pgtype.UUID{Bytes: auditCtx.batchID, Valid: true},
		TicketRef:  tref,
	}); err != nil {
		return fmt.Errorf("bulk adapter apply: audit event: %w", err)
	}
	return nil
}
