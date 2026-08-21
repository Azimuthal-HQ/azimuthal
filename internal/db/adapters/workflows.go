package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// WorkflowAdapter implements workflow.Repository using sqlc-generated queries.
type WorkflowAdapter struct {
	q *generated.Queries
}

// NewWorkflowAdapter creates a WorkflowAdapter backed by the given queries.
func NewWorkflowAdapter(q *generated.Queries) *WorkflowAdapter {
	return &WorkflowAdapter{q: q}
}

// GetWorkflow retrieves a workflow by ID. Returns workflow.ErrNotFound if
// absent.
func (a *WorkflowAdapter) GetWorkflow(ctx context.Context, id uuid.UUID) (*workflow.Workflow, error) {
	row, err := a.q.GetWorkflow(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, workflow.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("workflow adapter get: %w", err)
	}
	return rowToWorkflow(row), nil
}

// GetDefaultWorkflow retrieves the default workflow for an org and entity
// type. Returns workflow.ErrNotFound if none is configured.
func (a *WorkflowAdapter) GetDefaultWorkflow(ctx context.Context, orgID uuid.UUID, appliesTo string) (*workflow.Workflow, error) {
	row, err := a.q.GetDefaultWorkflow(ctx, generated.GetDefaultWorkflowParams{
		OrgID:     orgID,
		AppliesTo: appliesTo,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, workflow.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("workflow adapter get default: %w", err)
	}
	return rowToWorkflow(row), nil
}

// ListWorkflows retrieves all workflows for an org.
func (a *WorkflowAdapter) ListWorkflows(ctx context.Context, orgID uuid.UUID) ([]*workflow.Workflow, error) {
	rows, err := a.q.ListWorkflows(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("workflow adapter list: %w", err)
	}
	out := make([]*workflow.Workflow, len(rows))
	for i, r := range rows {
		out[i] = rowToWorkflow(r)
	}
	return out, nil
}

// CreateWorkflow persists a new workflow.
func (a *WorkflowAdapter) CreateWorkflow(ctx context.Context, w *workflow.Workflow) error {
	row, err := a.q.CreateWorkflow(ctx, generated.CreateWorkflowParams{
		OrgID:       w.OrgID,
		Name:        w.Name,
		Description: w.Description,
		IsDefault:   w.IsDefault,
		AppliesTo:   w.AppliesTo,
	})
	if err != nil {
		return fmt.Errorf("workflow adapter create: %w", err)
	}
	w.ID = row.ID
	w.CreatedAt = row.CreatedAt.Time
	w.UpdatedAt = row.UpdatedAt.Time
	return nil
}

// UpdateWorkflow persists workflow metadata changes.
func (a *WorkflowAdapter) UpdateWorkflow(ctx context.Context, w *workflow.Workflow) error {
	row, err := a.q.UpdateWorkflow(ctx, generated.UpdateWorkflowParams{
		ID:          w.ID,
		Name:        w.Name,
		Description: w.Description,
		IsDefault:   w.IsDefault,
		AppliesTo:   w.AppliesTo,
	})
	if err != nil {
		return fmt.Errorf("workflow adapter update: %w", err)
	}
	w.UpdatedAt = row.UpdatedAt.Time
	return nil
}

// DeleteWorkflow removes a workflow.
//
// A workflow_state_id foreign key (migration 016) is ON DELETE NO ACTION on both
// tickets and project_items, so the cascade that removes the workflow's states
// is refused by the database while any item still points into one of them. That
// is a real, expected refusal — the historical-reference case the handler's
// space-count guard cannot see — so it is mapped to ErrWorkflowInUse (409)
// rather than surfacing as a raw constraint 500.
func (a *WorkflowAdapter) DeleteWorkflow(ctx context.Context, id uuid.UUID) error {
	if err := a.q.DeleteWorkflow(ctx, id); err != nil {
		if isForeignKeyViolation(err) {
			return workflow.ErrWorkflowInUse
		}
		return fmt.Errorf("workflow adapter delete: %w", err)
	}
	return nil
}

// ListStates retrieves all states for a workflow, ordered by position.
func (a *WorkflowAdapter) ListStates(ctx context.Context, workflowID uuid.UUID) ([]*workflow.State, error) {
	rows, err := a.q.ListWorkflowStates(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow adapter list states: %w", err)
	}
	out := make([]*workflow.State, len(rows))
	for i, r := range rows {
		out[i] = rowToState(r)
	}
	return out, nil
}

// GetState retrieves a single state by ID. Returns workflow.ErrNotFound if
// absent.
func (a *WorkflowAdapter) GetState(ctx context.Context, id uuid.UUID) (*workflow.State, error) {
	row, err := a.q.GetWorkflowState(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, workflow.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("workflow adapter get state: %w", err)
	}
	return rowToState(row), nil
}

// GetInitialState retrieves the initial state for a workflow. Returns
// workflow.ErrNotFound if the workflow has no initial state (or does not
// exist).
func (a *WorkflowAdapter) GetInitialState(ctx context.Context, workflowID uuid.UUID) (*workflow.State, error) {
	row, err := a.q.GetInitialWorkflowState(ctx, workflowID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, workflow.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("workflow adapter get initial state: %w", err)
	}
	return rowToState(row), nil
}

// CreateState persists a new state.
func (a *WorkflowAdapter) CreateState(ctx context.Context, s *workflow.State) error {
	row, err := a.q.CreateWorkflowState(ctx, generated.CreateWorkflowStateParams{
		WorkflowID: s.WorkflowID,
		Name:       s.Name,
		Category:   s.Category,
		Color:      s.Color,
		Position:   s.Position,
		IsInitial:  s.IsInitial,
	})
	if err != nil {
		return fmt.Errorf("workflow adapter create state: %w", err)
	}
	s.ID = row.ID
	s.CreatedAt = row.CreatedAt.Time
	return nil
}

// UpdateState persists state changes.
func (a *WorkflowAdapter) UpdateState(ctx context.Context, s *workflow.State) error {
	_, err := a.q.UpdateWorkflowState(ctx, generated.UpdateWorkflowStateParams{
		ID:        s.ID,
		Name:      s.Name,
		Category:  s.Category,
		Color:     s.Color,
		Position:  s.Position,
		IsInitial: s.IsInitial,
	})
	if err != nil {
		return fmt.Errorf("workflow adapter update state: %w", err)
	}
	return nil
}

// DeleteState removes a state.
func (a *WorkflowAdapter) DeleteState(ctx context.Context, id uuid.UUID) error {
	if err := a.q.DeleteWorkflowState(ctx, id); err != nil {
		return fmt.Errorf("workflow adapter delete state: %w", err)
	}
	return nil
}

// ListTransitions retrieves all transitions for a workflow.
func (a *WorkflowAdapter) ListTransitions(ctx context.Context, workflowID uuid.UUID) ([]*workflow.Transition, error) {
	rows, err := a.q.ListWorkflowTransitions(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow adapter list transitions: %w", err)
	}
	out := make([]*workflow.Transition, len(rows))
	for i, r := range rows {
		out[i] = rowToTransition(r)
	}
	return out, nil
}

// ListAvailableTransitions returns transitions reachable from currentStateID.
func (a *WorkflowAdapter) ListAvailableTransitions(ctx context.Context, workflowID uuid.UUID, currentStateID uuid.UUID) ([]*workflow.Transition, error) {
	rows, err := a.q.ListAvailableTransitions(ctx, generated.ListAvailableTransitionsParams{
		WorkflowID:  workflowID,
		FromStateID: currentStateID,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow adapter list available transitions: %w", err)
	}
	out := make([]*workflow.Transition, len(rows))
	for i, r := range rows {
		out[i] = rowToTransition(r)
	}
	return out, nil
}

// CreateTransition persists a new transition.
//
// Returns workflow.ErrStateNotInWorkflow when either endpoint is not a state of
// t.WorkflowID. The query refuses that case by matching no rows rather than by
// raising an error, so pgx.ErrNoRows here is the predicate speaking, not a
// failure — see CreateWorkflowTransition in internal/db/queries/workflows.sql
// for why the check is in the statement rather than in front of it.
func (a *WorkflowAdapter) CreateTransition(ctx context.Context, t *workflow.Transition) error {
	row, err := a.q.CreateWorkflowTransition(ctx, generated.CreateWorkflowTransitionParams{
		WorkflowID:  t.WorkflowID,
		FromStateID: t.FromStateID,
		ToStateID:   t.ToStateID,
		Name:        t.Name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return workflow.ErrStateNotInWorkflow
	}
	if err != nil {
		return fmt.Errorf("workflow adapter create transition: %w", err)
	}
	t.ID = row.ID
	t.CreatedAt = row.CreatedAt.Time
	return nil
}

// DeleteTransition removes a transition, scoped to its workflow.
//
// The workflow_id predicate lives in the DELETE (see DeleteWorkflowTransition in
// internal/db/queries/workflows.sql), so a transition of another workflow — or
// none at all — matches no rows. Zero rows affected is the predicate refusing the
// delete, not a failure, so it maps to ErrTransitionNotInWorkflow rather than an
// error, mirroring how CreateTransition treats pgx.ErrNoRows above.
func (a *WorkflowAdapter) DeleteTransition(ctx context.Context, workflowID, id uuid.UUID) error {
	rows, err := a.q.DeleteWorkflowTransition(ctx, generated.DeleteWorkflowTransitionParams{
		ID:         id,
		WorkflowID: workflowID,
	})
	if err != nil {
		return fmt.Errorf("workflow adapter delete transition: %w", err)
	}
	if rows == 0 {
		return workflow.ErrTransitionNotInWorkflow
	}
	return nil
}

// AssignDefaultWorkflowToSpace finds the default workflow for an org+spaceType
// and assigns it to the given space. spaceType "codex" is intentionally skipped.
func (a *WorkflowAdapter) AssignDefaultWorkflowToSpace(ctx context.Context, orgID uuid.UUID, spaceType string, spaceID uuid.UUID) error {
	var appliesTo string
	switch spaceType {
	case "beacon":
		appliesTo = "tickets"
	case "vector":
		appliesTo = "project_items"
	default:
		return nil // codex spaces don't get a workflow
	}

	wf, err := a.q.GetDefaultWorkflow(ctx, generated.GetDefaultWorkflowParams{
		OrgID:     orgID,
		AppliesTo: appliesTo,
	})
	if err != nil {
		return fmt.Errorf("workflow adapter assign to space: %w", err)
	}

	if err := a.q.AssignWorkflowToSpace(ctx, generated.AssignWorkflowToSpaceParams{
		WorkflowID: pgtype.UUID{Bytes: wf.ID, Valid: true},
		ID:         spaceID,
	}); err != nil {
		return fmt.Errorf("workflow adapter assign workflow to space: %w", err)
	}
	return nil
}

// SeedDefaultWorkflows creates the two default workflows for a new org using
// individual queries wrapped in application-level logic.
func (a *WorkflowAdapter) SeedDefaultWorkflows(ctx context.Context, orgID uuid.UUID) error {
	if err := seedTicketWorkflow(ctx, a.q, orgID); err != nil {
		return err
	}
	return seedProjectWorkflow(ctx, a.q, orgID)
}

func seedTicketWorkflow(ctx context.Context, q *generated.Queries, orgID uuid.UUID) error {
	desc := "Default workflow for service desk tickets"
	wf, err := q.CreateWorkflow(ctx, generated.CreateWorkflowParams{
		OrgID:       orgID,
		Name:        "Default Service Desk",
		Description: &desc,
		IsDefault:   true,
		AppliesTo:   "tickets",
	})
	if err != nil {
		return fmt.Errorf("seed ticket workflow: %w", err)
	}

	ids := makeUUIDs(4)
	if err := q.BulkCreateWorkflowStates(ctx, generated.BulkCreateWorkflowStatesParams{
		Column1: ids,
		Column2: []uuid.UUID{wf.ID, wf.ID, wf.ID, wf.ID},
		Column3: []string{"open", "in_progress", "resolved", "closed"},
		Column4: []string{"todo", "in_progress", "done", "done"},
		Column5: []string{"#3b82f6", "#f59e0b", "#10b981", "#6b7280"},
		Column6: []int32{0, 1, 2, 3},
		Column7: []bool{true, false, false, false},
	}); err != nil {
		return fmt.Errorf("seed ticket workflow states: %w", err)
	}

	open, inProgress, resolved, closed := ids[0], ids[1], ids[2], ids[3]
	// The last three are the one-step-back reverse edges (resolved -> in_progress,
	// closed -> resolved, closed -> in_progress) so a ticket is never forced
	// through `open` to move backward. Kept in lockstep with the hardcoded Go
	// state machine (internal/core/tickets/status.go) and migration 029, which
	// backfills the same edges into pre-existing installs.
	wfIDs := repeatUUID(wf.ID, 11)
	from := []uuid.UUID{open, open, inProgress, inProgress, inProgress, resolved, resolved, closed, resolved, closed, closed}
	to := []uuid.UUID{inProgress, closed, resolved, open, closed, closed, open, open, inProgress, resolved, inProgress}
	names := []string{"Start Progress", "Close", "Resolve", "Reopen", "Close", "Close", "Reopen", "Reopen", "Resume Progress", "Reopen", "Resume Progress"}

	if err := q.BulkCreateWorkflowTransitions(ctx, generated.BulkCreateWorkflowTransitionsParams{
		Column1: wfIDs,
		Column2: from,
		Column3: to,
		Column4: names,
	}); err != nil {
		return fmt.Errorf("seed ticket workflow transitions: %w", err)
	}
	return nil
}

func seedProjectWorkflow(ctx context.Context, q *generated.Queries, orgID uuid.UUID) error {
	desc := "Default workflow for project items"
	wf, err := q.CreateWorkflow(ctx, generated.CreateWorkflowParams{
		OrgID:       orgID,
		Name:        "Default Project",
		Description: &desc,
		IsDefault:   true,
		AppliesTo:   "project_items",
	})
	if err != nil {
		return fmt.Errorf("seed project workflow: %w", err)
	}

	ids := makeUUIDs(5)
	if err := q.BulkCreateWorkflowStates(ctx, generated.BulkCreateWorkflowStatesParams{
		Column1: ids,
		Column2: []uuid.UUID{wf.ID, wf.ID, wf.ID, wf.ID, wf.ID},
		Column3: []string{"backlog", "todo", "in_progress", "in_review", "done"},
		Column4: []string{"todo", "todo", "in_progress", "in_progress", "done"},
		Column5: []string{"#9ca3af", "#3b82f6", "#f59e0b", "#8b5cf6", "#10b981"},
		Column6: []int32{0, 1, 2, 3, 4},
		Column7: []bool{true, false, false, false, false},
	}); err != nil {
		return fmt.Errorf("seed project workflow states: %w", err)
	}

	backlog, todo, inProgress, inReview, done := ids[0], ids[1], ids[2], ids[3], ids[4]
	wfIDs := repeatUUID(wf.ID, 10)
	from := []uuid.UUID{backlog, backlog, todo, todo, inProgress, inProgress, inProgress, inReview, inReview, done}
	to := []uuid.UUID{todo, inProgress, inProgress, backlog, inReview, todo, backlog, done, inProgress, inProgress}
	names := []string{
		"Start", "Start Progress", "Start Progress", "Move to Backlog",
		"Submit for Review", "Move to Todo", "Move to Backlog",
		"Approve", "Request Changes", "Reopen",
	}

	if err := q.BulkCreateWorkflowTransitions(ctx, generated.BulkCreateWorkflowTransitionsParams{
		Column1: wfIDs,
		Column2: from,
		Column3: to,
		Column4: names,
	}); err != nil {
		return fmt.Errorf("seed project workflow transitions: %w", err)
	}
	return nil
}

func makeUUIDs(n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
	}
	return ids
}

func repeatUUID(id uuid.UUID, n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = id
	}
	return ids
}

func rowToWorkflow(r generated.Workflow) *workflow.Workflow {
	return &workflow.Workflow{
		ID:          r.ID,
		OrgID:       r.OrgID,
		Name:        r.Name,
		Description: r.Description,
		IsDefault:   r.IsDefault,
		AppliesTo:   r.AppliesTo,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
}

func rowToState(r generated.WorkflowState) *workflow.State {
	return &workflow.State{
		ID:         r.ID,
		WorkflowID: r.WorkflowID,
		Name:       r.Name,
		Category:   r.Category,
		Color:      r.Color,
		Position:   r.Position,
		IsInitial:  r.IsInitial,
		CreatedAt:  r.CreatedAt.Time,
	}
}

func rowToTransition(r generated.WorkflowTransition) *workflow.Transition {
	return &workflow.Transition{
		ID:          r.ID,
		WorkflowID:  r.WorkflowID,
		FromStateID: r.FromStateID,
		ToStateID:   r.ToStateID,
		Name:        r.Name,
		CreatedAt:   r.CreatedAt.Time,
	}
}
