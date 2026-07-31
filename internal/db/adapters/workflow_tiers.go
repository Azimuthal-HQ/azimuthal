package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// WorkflowTierAdapter implements workflow.TierStore using sqlc-generated
// queries over migrations 046 and 047.
//
// It is a separate adapter from WorkflowAdapter for the reason TierStore is a
// separate interface: the two have different consumers, and the tier tables did
// not exist when Repository's shape was settled.
//
// # Unknown vocabulary survives the trip
//
// Rows are converted verbatim. A guard kind, post-function kind or approver
// subject type this build does not recognise is carried into the domain type as
// the string it is, not dropped and not rejected here.
//
// That is the opposite of what internal/db/adapters/saved_views.go does with a
// filter document, and the difference is which way the failure points. An
// unreadable saved-view filter can only produce wrong RESULTS, so erroring on
// read is the safe answer. An unreadable guard is a RESTRICTION: dropping it
// permits, and erroring on read turns every transition through the edge into a
// 500 during a rolling deploy. Carrying it through lets workflow.Evaluate
// refuse it by name with a sentence a person can act on — fail closed, and
// explain. The same choice lets the admin surface render an unrecognised guard
// so somebody can delete it.
type WorkflowTierAdapter struct {
	q *generated.Queries
}

// NewWorkflowTierAdapter creates a WorkflowTierAdapter backed by the given
// queries.
func NewWorkflowTierAdapter(q *generated.Queries) *WorkflowTierAdapter {
	return &WorkflowTierAdapter{q: q}
}

// ─── Tier 1: guards ───────────────────────────────────────────────────────────

// GuardsForTransition returns every guard on a transition, ordered by
// (position, id).
func (a *WorkflowTierAdapter) GuardsForTransition(ctx context.Context, transitionID uuid.UUID) ([]workflow.Guard, error) {
	rows, err := a.q.ListTransitionGuards(ctx, transitionID)
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter list transition guards: %w", err)
	}
	return mapSlice(rows, rowToGuard), nil
}

// GuardsForWorkflow returns every guard in a workflow, for the admin surface.
func (a *WorkflowTierAdapter) GuardsForWorkflow(ctx context.Context, workflowID uuid.UUID) ([]workflow.Guard, error) {
	rows, err := a.q.ListWorkflowGuards(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter list workflow guards: %w", err)
	}
	return mapSlice(rows, rowToGuard), nil
}

// CreateGuard persists a guard. A shape the CHECK constraints refuse comes back
// as a plain error: the API validates first, so reaching the constraint means
// the two disagree, which is a defect rather than user input.
func (a *WorkflowTierAdapter) CreateGuard(ctx context.Context, g workflow.Guard) (workflow.Guard, error) {
	var capability *string
	if g.Capability != nil {
		capability = strPtr(string(*g.Capability))
	}
	var fieldKey *string
	if g.FieldKey != nil {
		fieldKey = strPtr(string(*g.FieldKey))
	}

	row, err := a.q.CreateTransitionGuard(ctx, generated.CreateTransitionGuardParams{
		TransitionID: g.TransitionID,
		GuardClass:   string(g.Class),
		Kind:         string(g.Kind),
		Position:     g.Position,
		Capability:   capability,
		TeamID:       pgUUID(g.TeamID),
		FieldKey:     fieldKey,
	})
	if isForeignKeyViolation(err) {
		return workflow.Guard{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.Guard{}, fmt.Errorf("workflow tier adapter create guard: %w", err)
	}
	return rowToGuard(row), nil
}

// DeleteGuard removes a guard, reporting ErrNotFound when nothing matched so a
// repeated delete does not read as success.
func (a *WorkflowTierAdapter) DeleteGuard(ctx context.Context, transitionID, id uuid.UUID) error {
	n, err := a.q.DeleteTransitionGuard(ctx, generated.DeleteTransitionGuardParams{ID: id, TransitionID: transitionID})
	if err != nil {
		return fmt.Errorf("workflow tier adapter delete guard: %w", err)
	}
	if n == 0 {
		return workflow.ErrNotFound
	}
	return nil
}

func rowToGuard(r generated.WorkflowTransitionGuard) workflow.Guard {
	g := workflow.Guard{
		ID:           r.ID,
		TransitionID: r.TransitionID,
		Class:        workflow.GuardClass(r.GuardClass),
		Kind:         workflow.GuardKind(r.Kind),
		Position:     r.Position,
		TeamID:       goUUIDPtr(r.TeamID),
	}
	if r.Capability != nil {
		c := access.Capability(*r.Capability)
		g.Capability = &c
	}
	if r.FieldKey != nil {
		f := workflow.FieldKey(*r.FieldKey)
		g.FieldKey = &f
	}
	return g
}

// ─── Tier 3: post-functions ───────────────────────────────────────────────────

// PostFunctionsForTransition returns every post-function on a transition,
// ordered by (position, id).
func (a *WorkflowTierAdapter) PostFunctionsForTransition(ctx context.Context, transitionID uuid.UUID) ([]workflow.PostFunction, error) {
	rows, err := a.q.ListTransitionPostFunctions(ctx, transitionID)
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter list transition post-functions: %w", err)
	}
	return mapSlice(rows, rowToPostFunction), nil
}

// PostFunctionsForWorkflow returns every post-function in a workflow.
func (a *WorkflowTierAdapter) PostFunctionsForWorkflow(ctx context.Context, workflowID uuid.UUID) ([]workflow.PostFunction, error) {
	rows, err := a.q.ListWorkflowPostFunctions(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter list workflow post-functions: %w", err)
	}
	return mapSlice(rows, rowToPostFunction), nil
}

// CreatePostFunction persists a post-function.
func (a *WorkflowTierAdapter) CreatePostFunction(ctx context.Context, p workflow.PostFunction) (workflow.PostFunction, error) {
	var fieldKey *string
	if p.FieldKey != nil {
		fieldKey = strPtr(string(*p.FieldKey))
	}

	row, err := a.q.CreateTransitionPostFunction(ctx, generated.CreateTransitionPostFunctionParams{
		TransitionID:   p.TransitionID,
		Kind:           string(p.Kind),
		Position:       p.Position,
		AssigneeUserID: pgUUID(p.AssigneeUserID),
		FieldKey:       fieldKey,
		FieldValue:     p.FieldValue,
	})
	if isForeignKeyViolation(err) {
		return workflow.PostFunction{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.PostFunction{}, fmt.Errorf("workflow tier adapter create post-function: %w", err)
	}
	return rowToPostFunction(row), nil
}

// DeletePostFunction removes a post-function.
func (a *WorkflowTierAdapter) DeletePostFunction(ctx context.Context, transitionID, id uuid.UUID) error {
	n, err := a.q.DeleteTransitionPostFunction(ctx, generated.DeleteTransitionPostFunctionParams{ID: id, TransitionID: transitionID})
	if err != nil {
		return fmt.Errorf("workflow tier adapter delete post-function: %w", err)
	}
	if n == 0 {
		return workflow.ErrNotFound
	}
	return nil
}

func rowToPostFunction(r generated.WorkflowTransitionPostFunction) workflow.PostFunction {
	p := workflow.PostFunction{
		ID:             r.ID,
		TransitionID:   r.TransitionID,
		Kind:           workflow.PostFunctionKind(r.Kind),
		Position:       r.Position,
		AssigneeUserID: goUUIDPtr(r.AssigneeUserID),
		FieldValue:     r.FieldValue,
	}
	if r.FieldKey != nil {
		f := workflow.PostFieldKey(*r.FieldKey)
		p.FieldKey = &f
	}
	return p
}

// ─── Tier 2: approver configuration ───────────────────────────────────────────

// ApproversForTransition returns the transition's configured approvers with
// their display names resolved.
func (a *WorkflowTierAdapter) ApproversForTransition(ctx context.Context, transitionID uuid.UUID) ([]workflow.Approver, error) {
	rows, err := a.q.ListTransitionApprovers(ctx, transitionID)
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter list transition approvers: %w", err)
	}
	out := make([]workflow.Approver, len(rows))
	for i, r := range rows {
		out[i] = workflow.Approver{
			ID:             r.ID,
			TransitionID:   r.TransitionID,
			SubjectType:    workflow.ApproverSubjectType(r.SubjectType),
			SubjectID:      r.SubjectID,
			SubjectName:    r.SubjectName,
			SubjectMissing: r.SubjectMissing,
		}
	}
	return out, nil
}

// ApproversForWorkflow returns every approver in a workflow, for the admin
// surface.
func (a *WorkflowTierAdapter) ApproversForWorkflow(ctx context.Context, workflowID uuid.UUID) ([]workflow.Approver, error) {
	rows, err := a.q.ListWorkflowApprovers(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter list workflow approvers: %w", err)
	}
	out := make([]workflow.Approver, len(rows))
	for i, r := range rows {
		out[i] = workflow.Approver{
			ID:             r.ID,
			TransitionID:   r.TransitionID,
			SubjectType:    workflow.ApproverSubjectType(r.SubjectType),
			SubjectID:      r.SubjectID,
			SubjectName:    r.SubjectName,
			SubjectMissing: r.SubjectMissing,
		}
	}
	return out, nil
}

// CreateApprover persists an approver. The unique key on
// (transition_id, subject_type, subject_id) makes adding the same subject twice
// a conflict rather than a duplicate row.
func (a *WorkflowTierAdapter) CreateApprover(ctx context.Context, ap workflow.Approver) (workflow.Approver, error) {
	row, err := a.q.CreateTransitionApprover(ctx, generated.CreateTransitionApproverParams{
		TransitionID: ap.TransitionID,
		SubjectType:  string(ap.SubjectType),
		SubjectID:    ap.SubjectID,
	})
	if _, dup := uniqueViolation(err); dup {
		return workflow.Approver{}, workflow.ErrApproverExists
	}
	if isForeignKeyViolation(err) {
		return workflow.Approver{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.Approver{}, fmt.Errorf("workflow tier adapter create approver: %w", err)
	}
	return workflow.Approver{
		ID:           row.ID,
		TransitionID: row.TransitionID,
		SubjectType:  workflow.ApproverSubjectType(row.SubjectType),
		SubjectID:    row.SubjectID,
	}, nil
}

// DeleteApprover removes an approver.
func (a *WorkflowTierAdapter) DeleteApprover(ctx context.Context, transitionID, id uuid.UUID) error {
	n, err := a.q.DeleteTransitionApprover(ctx, generated.DeleteTransitionApproverParams{ID: id, TransitionID: transitionID})
	if err != nil {
		return fmt.Errorf("workflow tier adapter delete approver: %w", err)
	}
	if n == 0 {
		return workflow.ErrNotFound
	}
	return nil
}

// ─── Tier 2: approval instances ───────────────────────────────────────────────

// CreateApproval records a request to traverse a gated transition.
//
// A second concurrent request for the same item loses on migration 047's
// partial unique index; the violation is mapped to ErrApprovalPending rather
// than surfacing as a 500. That is the pattern migration 034 established for
// the single-active-sprint index, and the reason the constraint is named.
func (a *WorkflowTierAdapter) CreateApproval(ctx context.Context, ap workflow.Approval) (workflow.Approval, error) {
	row, err := a.q.CreateApproval(ctx, generated.CreateApprovalParams{
		TransitionID: pgUUID(ap.TransitionID),
		EntityType:   string(ap.EntityType),
		EntityID:     ap.EntityID,
		SpaceID:      ap.SpaceID,
		FromStateID:  pgUUID(ap.FromStateID),
		ToStateID:    pgUUID(ap.ToStateID),
		FromStatus:   ap.FromStatus,
		ToStatus:     ap.ToStatus,
		RequestedBy:  ap.RequestedBy,
	})
	if _, dup := uniqueViolation(err); dup {
		return workflow.Approval{}, workflow.ErrApprovalPending
	}
	if err != nil {
		return workflow.Approval{}, fmt.Errorf("workflow tier adapter create approval: %w", err)
	}
	return rowToApproval(row), nil
}

// PendingApprovalForEntity returns the item's outstanding request.
func (a *WorkflowTierAdapter) PendingApprovalForEntity(
	ctx context.Context, entityType workflow.ApprovalEntityType, entityID uuid.UUID,
) (workflow.Approval, error) {
	row, err := a.q.GetPendingApprovalForEntity(ctx, generated.GetPendingApprovalForEntityParams{
		EntityType: string(entityType),
		EntityID:   entityID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return workflow.Approval{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.Approval{}, fmt.Errorf("workflow tier adapter get pending approval: %w", err)
	}
	return rowToApproval(row), nil
}

// GetApproval returns one request with its requester and decider names
// resolved.
func (a *WorkflowTierAdapter) GetApproval(ctx context.Context, id uuid.UUID) (workflow.Approval, error) {
	row, err := a.q.GetApproval(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return workflow.Approval{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.Approval{}, fmt.Errorf("workflow tier adapter get approval: %w", err)
	}
	ap := rowToApproval(row.WorkflowApproval)
	ap.RequestedByName = row.RequestedByName
	ap.DecidedByName = row.DecidedByName
	return ap, nil
}

// DecideApproval records a decision on a pending request.
//
// The UPDATE carries `decided_at IS NULL`, so a second approver deciding
// concurrently updates zero rows. Zero rows is then disambiguated by a
// follow-up read — already decided, or never existed — the way
// RevokeEntityShare distinguishes ErrShareAlreadyRevoked from ErrShareNotFound.
// Guessing instead would report "already decided" for an id that was never
// real.
func (a *WorkflowTierAdapter) DecideApproval(
	ctx context.Context, id, decidedBy uuid.UUID, d workflow.Decision, reason *string,
) (workflow.Approval, error) {
	decision := string(d)
	row, err := a.q.DecideApproval(ctx, generated.DecideApprovalParams{
		ID:        id,
		DecidedBy: pgUUID(&decidedBy),
		Decision:  &decision,
		Reason:    reason,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := a.q.GetApproval(ctx, id); getErr != nil {
			if errors.Is(getErr, pgx.ErrNoRows) {
				return workflow.Approval{}, workflow.ErrNotFound
			}
			return workflow.Approval{}, fmt.Errorf("workflow tier adapter decide approval: %w", getErr)
		}
		return workflow.Approval{}, workflow.ErrApprovalAlreadyDecided
	}
	if err != nil {
		return workflow.Approval{}, fmt.Errorf("workflow tier adapter decide approval: %w", err)
	}
	return rowToApproval(row), nil
}

// ApprovalsForEntity returns every request ever made about an item.
func (a *WorkflowTierAdapter) ApprovalsForEntity(
	ctx context.Context, spaceID uuid.UUID, entityType workflow.ApprovalEntityType, entityID uuid.UUID,
) ([]workflow.Approval, error) {
	rows, err := a.q.ListApprovalsForEntity(ctx, generated.ListApprovalsForEntityParams{
		EntityType: string(entityType),
		EntityID:   entityID,
		SpaceID:    spaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter list approvals for entity: %w", err)
	}
	out := make([]workflow.Approval, len(rows))
	for i, r := range rows {
		ap := rowToApproval(r.WorkflowApproval)
		ap.RequestedByName = r.RequestedByName
		ap.DecidedByName = r.DecidedByName
		out[i] = ap
	}
	return out, nil
}

// PendingApprovalsForSpace returns everything awaiting a decision in a space.
func (a *WorkflowTierAdapter) PendingApprovalsForSpace(ctx context.Context, spaceID uuid.UUID) ([]workflow.Approval, error) {
	rows, err := a.q.ListPendingApprovalsForSpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter list pending approvals: %w", err)
	}
	out := make([]workflow.Approval, len(rows))
	for i, r := range rows {
		ap := rowToApproval(r.WorkflowApproval)
		ap.RequestedByName = r.RequestedByName
		out[i] = ap
	}
	return out, nil
}

// PendingApprovalCountForTransition reports how many in-flight requests would
// be orphaned by deleting a transition.
func (a *WorkflowTierAdapter) PendingApprovalCountForTransition(ctx context.Context, transitionID uuid.UUID) (int64, error) {
	n, err := a.q.CountPendingApprovalsForTransition(ctx, pgUUID(&transitionID))
	if err != nil {
		return 0, fmt.Errorf("workflow tier adapter count pending approvals: %w", err)
	}
	return n, nil
}

func rowToApproval(r generated.WorkflowApproval) workflow.Approval {
	ap := workflow.Approval{
		ID:           r.ID,
		TransitionID: goUUIDPtr(r.TransitionID),
		EntityType:   workflow.ApprovalEntityType(r.EntityType),
		EntityID:     r.EntityID,
		SpaceID:      r.SpaceID,
		FromStateID:  goUUIDPtr(r.FromStateID),
		ToStateID:    goUUIDPtr(r.ToStateID),
		FromStatus:   r.FromStatus,
		ToStatus:     r.ToStatus,
		RequestedBy:  r.RequestedBy,
		RequestedAt:  goTime(r.RequestedAt),
		DecidedBy:    goUUIDPtr(r.DecidedBy),
		DecidedAt:    goTimePtr(r.DecidedAt),
		// Copied, not aliased: r is a value but its *string points into the
		// scanned row, and every other pointer field here is rebuilt rather
		// than shared. Reason is nil on every pending request.
		Reason: copyStr(r.Reason),
	}
	if r.Decision != nil {
		d := workflow.Decision(*r.Decision)
		ap.Decision = &d
	}
	return ap
}

// copyStr returns a pointer to a copy of the pointed-at string, or nil.
func copyStr(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

// ─── Resolution helpers for the chokepoint ────────────────────────────────────

// StateByName maps a status string onto the workflow state it names.
func (a *WorkflowTierAdapter) StateByName(ctx context.Context, workflowID uuid.UUID, name string) (*workflow.State, error) {
	row, err := a.q.GetWorkflowStateByName(ctx, generated.GetWorkflowStateByNameParams{
		WorkflowID: workflowID,
		Name:       name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, workflow.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter state by name: %w", err)
	}
	return rowToState(row), nil
}

// TransitionBetween returns the edge between two states.
func (a *WorkflowTierAdapter) TransitionBetween(ctx context.Context, workflowID, fromStateID, toStateID uuid.UUID) (*workflow.Transition, error) {
	row, err := a.q.GetTransitionByStates(ctx, generated.GetTransitionByStatesParams{
		WorkflowID:  workflowID,
		FromStateID: fromStateID,
		ToStateID:   toStateID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, workflow.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter transition between: %w", err)
	}
	return rowToTransition(row), nil
}

// EffectiveTeamIDs is the actor's ADR-0007 effective team set.
//
// It delegates to the same ListEffectiveTeamIDs query saved views use, which
// itself delegates to the effective_team_ids() schema function from migration
// 038. A second hand-written copy of that expansion is how an approver gate and
// a space grant come to disagree about who is in a team, and the direction they
// drift in is "one of them grants more".
func (a *WorkflowTierAdapter) EffectiveTeamIDs(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := a.q.ListEffectiveTeamIDs(ctx, generated.ListEffectiveTeamIDsParams{
		OrgID:  orgID,
		UserID: userID,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter effective team ids: %w", err)
	}
	return ids, nil
}

// EffectiveTeamMemberIDs is the inverse read: everyone for whom this team is in
// their effective set. See the query's header for why it asks
// effective_team_ids() rather than re-deriving the ancestry rule.
func (a *WorkflowTierAdapter) EffectiveTeamMemberIDs(
	ctx context.Context, orgID, teamID uuid.UUID,
) ([]uuid.UUID, error) {
	ids, err := a.q.ListEffectiveTeamMemberIDs(ctx, generated.ListEffectiveTeamMemberIDsParams{
		OrgID:  orgID,
		TeamID: teamID,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow tier adapter effective team member ids: %w", err)
	}
	return ids, nil
}

// mapSlice converts a slice of database rows into domain values.
func mapSlice[R any, T any](rows []R, f func(R) T) []T {
	out := make([]T, len(rows))
	for i, r := range rows {
		out[i] = f(r)
	}
	return out
}

// WorkflowIDForSpace reports which workflow a space uses, implementing
// tiergate.WorkflowResolver.
//
// A space with no workflow assigned returns workflow.ErrNotFound rather than a
// zero id, so the caller can tell "none configured" from "lookup failed". That
// distinction matters: assignment happens outside the space-create transaction
// and is explicitly best-effort, so an unassigned space is a supported live
// state and must mean "no tiers apply", never "refuse".
func (a *WorkflowTierAdapter) WorkflowIDForSpace(ctx context.Context, spaceID uuid.UUID) (uuid.UUID, error) {
	row, err := a.q.GetSpaceWorkflow(ctx, spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, workflow.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("workflow tier adapter workflow for space: %w", err)
	}
	return row.ID, nil
}
