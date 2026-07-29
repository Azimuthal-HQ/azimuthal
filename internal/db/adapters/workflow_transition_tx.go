package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// WorkflowTransitionTxAdapter applies a status change and its ADR-0011
// post-function effects in one transaction, with the audit row inside it.
//
// It is shared-surfaces.md Convention B, and it is used only where that
// convention applies: a transition that carries post-functions. A transition
// with none stays on the single-UPDATE path it has always taken — see
// workflow.TransitionApplier for why that distinction is deliberate rather than
// an inconsistency.
type WorkflowTransitionTxAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewWorkflowTransitionTxAdapter creates the adapter over a pool.
func NewWorkflowTransitionTxAdapter(pool *pgxpool.Pool) *WorkflowTransitionTxAdapter {
	return &WorkflowTransitionTxAdapter{pool: pool, q: generated.New(pool)}
}

// ApplyTransition writes the status, every planned effect, and the audit row in
// one transaction.
//
// A failure anywhere rolls back all three and returns an error naming what
// failed. There is no partial outcome: a transition that could not apply its
// post-functions does not happen, which is the contract ADR-0011's fixed action
// set is only meaningful under.
func (a *WorkflowTransitionTxAdapter) ApplyTransition(ctx context.Context, in workflow.ApplyInput) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("apply transition: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	if err := a.writeStatus(ctx, qtx, in); err != nil {
		return err
	}
	if err := a.writeEffects(ctx, qtx, in); err != nil {
		return err
	}
	if err := writeTransitionAuditTx(ctx, qtx, in); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("apply transition: commit: %w", err)
	}
	return nil
}

func (a *WorkflowTransitionTxAdapter) writeStatus(ctx context.Context, qtx *generated.Queries, in workflow.ApplyInput) error {
	stateID := pgUUID(in.ToStateID)

	switch in.EntityType {
	case workflow.ApprovalEntityTicket:
		if _, err := qtx.UpdateTicketWorkflowState(ctx, generated.UpdateTicketWorkflowStateParams{
			ID: in.EntityID, Status: in.ToStatus, WorkflowStateID: stateID,
		}); err != nil {
			return fmt.Errorf("apply transition: writing ticket status: %w", err)
		}
	case workflow.ApprovalEntityItem:
		if _, err := qtx.UpdateProjectItemWorkflowState(ctx, generated.UpdateProjectItemWorkflowStateParams{
			ID: in.EntityID, Status: in.ToStatus, WorkflowStateID: stateID,
		}); err != nil {
			return fmt.Errorf("apply transition: writing item status: %w", err)
		}
	default:
		// An entity kind this build cannot write is not silently skipped: the
		// transition fails. Same fail-closed direction as an unknown guard.
		return fmt.Errorf("apply transition: unknown entity type %q", in.EntityType)
	}
	return nil
}

// writeEffects folds every planned effect into one UPDATE per entity.
//
// The set_* flags distinguish "do not touch" from "set to NULL", because NULL
// is a real outcome here: assign_to with no user means unassign, and a cleared
// due date means exactly that. Collapsing the two is the partial-PATCH
// tri-state defect this repository has already shipped once.
func (a *WorkflowTransitionTxAdapter) writeEffects(ctx context.Context, qtx *generated.Queries, in workflow.ApplyInput) error {
	if len(in.Effects) == 0 {
		return nil
	}

	f := foldEffects(in.Effects)
	setAssignee, assignee := f.setAssignee, f.assignee
	setDueAt, dueAt := f.setDueAt, f.dueAt
	setLabels, labels := f.setLabels, f.labels

	switch in.EntityType {
	case workflow.ApprovalEntityTicket:
		if err := qtx.ApplyTicketEffects(ctx, generated.ApplyTicketEffectsParams{
			SetAssignee: setAssignee, AssigneeID: assignee,
			SetDueAt: setDueAt, DueAt: dueAt,
			SetLabels: setLabels, Labels: labels,
			ID: in.EntityID,
		}); err != nil {
			return fmt.Errorf("apply transition: applying ticket post-functions: %w", err)
		}
	case workflow.ApprovalEntityItem:
		if err := qtx.ApplyProjectItemEffects(ctx, generated.ApplyProjectItemEffectsParams{
			SetAssignee: setAssignee, AssigneeID: assignee,
			SetDueAt: setDueAt, DueAt: dueAt,
			SetLabels: setLabels, Labels: labels,
			ID: in.EntityID,
		}); err != nil {
			return fmt.Errorf("apply transition: applying item post-functions: %w", err)
		}
	default:
		return fmt.Errorf("apply transition: unknown entity type %q", in.EntityType)
	}
	return nil
}

// effectFold is the collapsed set of column writes for one transition.
type effectFold struct {
	setAssignee, setDueAt, setLabels bool
	assignee                         pgtype.UUID
	dueAt                            pgtype.Timestamptz
	labels                           []string
}

// foldEffects collapses the planned effects into one write per column.
//
// Later effects win over earlier ones writing the same field — the ordinary
// meaning of a sequence, and why the read is ordered by (position, id).
func foldEffects(effects []workflow.Effect) effectFold {
	f := effectFold{labels: []string{}}
	for _, e := range effects {
		switch {
		case e.SetAssignee != nil:
			f.setAssignee, f.assignee = true, pgUUID(*e.SetAssignee)
		case e.SetDueAt != nil:
			f.setDueAt, f.dueAt = true, pgTimestampPtr(*e.SetDueAt)
		case e.SetLabels != nil:
			f.setLabels = true
			if *e.SetLabels != nil {
				f.labels = *e.SetLabels
			}
		}
	}
	return f
}

// writeTransitionAuditTx records the status change through the mutation's own
// transaction. Failing to record it fails the mutation, which is the point of
// Convention B: a trail claiming the effects applied must roll back with them.
func writeTransitionAuditTx(ctx context.Context, qtx *generated.Queries, in workflow.ApplyInput) error {
	action := audit.EventTypeTicketStatusChange
	kind := "ticket"
	if in.EntityType == workflow.ApprovalEntityItem {
		action = audit.EventTypeItemStatusChange
		kind = "item"
	}

	meta := map[string]string{"to": in.ToStatus}
	if in.TransitionID != nil {
		meta["workflow_transition_id"] = in.TransitionID.String()
	}
	if in.ApprovalID != nil {
		meta["approval_id"] = in.ApprovalID.String()
	}
	if applied := describeEffects(in.Effects); applied != "" {
		meta["post_functions"] = applied
	}

	payload, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("apply transition: marshalling audit payload: %w", err)
	}
	if _, err := qtx.CreateAuditEvent(ctx, generated.CreateAuditEventParams{
		ID:         uuid.New(),
		OrgID:      in.OrgID,
		ActorID:    pgtype.UUID{Bytes: in.ActorID, Valid: true},
		Action:     string(action),
		EntityKind: kind,
		EntityID:   in.EntityID,
		Payload:    payload,
	}); err != nil {
		return fmt.Errorf("apply transition: writing status-change audit event: %w", err)
	}
	return nil
}

// describeEffects names what ran, flattened.
//
// audit_log payloads are map[string]string and the admin viewer types them as
// Record<string, string>; a nested object renders as [object Object] there. So
// the applied set is one comma-separated field rather than structured JSON.
func describeEffects(effects []workflow.Effect) string {
	names := make([]string, 0, len(effects))
	for _, e := range effects {
		switch {
		case e.SetAssignee != nil:
			names = append(names, "assign_to")
		case e.SetDueAt != nil:
			names = append(names, "set_field:due_at")
		case e.SetLabels != nil:
			names = append(names, "set_field:labels")
		}
	}
	return strings.Join(names, ",")
}
