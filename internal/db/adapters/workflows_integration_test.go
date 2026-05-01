package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

func setupWorkflow(t *testing.T) (*testutil.TestDB, uuid.UUID, *adapters.WorkflowAdapter) {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	queries := generated.New(db.Pool)
	adapter := adapters.NewWorkflowAdapter(queries)
	return db, org.ID, adapter
}

func TestWorkflowAdapter_SeedDefaultWorkflows(t *testing.T) {
	_, orgID, adapter := setupWorkflow(t)
	ctx := context.Background()

	err := adapter.SeedDefaultWorkflows(ctx, orgID)
	require.NoError(t, err)

	// Should have created two default workflows.
	wfs, err := adapter.ListWorkflows(ctx, orgID)
	require.NoError(t, err)
	require.Len(t, wfs, 2)
}

func TestWorkflowAdapter_GetDefaultWorkflow(t *testing.T) {
	_, orgID, adapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, adapter.SeedDefaultWorkflows(ctx, orgID))

	wf, err := adapter.GetDefaultWorkflow(ctx, orgID, "tickets")
	require.NoError(t, err)
	require.Equal(t, "tickets", wf.AppliesTo)
	require.True(t, wf.IsDefault)

	wf2, err := adapter.GetDefaultWorkflow(ctx, orgID, "project_items")
	require.NoError(t, err)
	require.Equal(t, "project_items", wf2.AppliesTo)
}

func TestWorkflowAdapter_GetState(t *testing.T) {
	_, orgID, adapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, adapter.SeedDefaultWorkflows(ctx, orgID))

	wf, err := adapter.GetDefaultWorkflow(ctx, orgID, "tickets")
	require.NoError(t, err)

	states, err := adapter.ListStates(ctx, wf.ID)
	require.NoError(t, err)
	require.NotEmpty(t, states)

	got, err := adapter.GetState(ctx, states[0].ID)
	require.NoError(t, err)
	require.Equal(t, states[0].ID, got.ID)
}

func TestWorkflowAdapter_GetInitialState(t *testing.T) {
	_, orgID, adapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, adapter.SeedDefaultWorkflows(ctx, orgID))

	wf, err := adapter.GetDefaultWorkflow(ctx, orgID, "tickets")
	require.NoError(t, err)

	initial, err := adapter.GetInitialState(ctx, wf.ID)
	require.NoError(t, err)
	require.True(t, initial.IsInitial)
	require.Equal(t, "open", initial.Name)
}

func TestWorkflowAdapter_UpdateState(t *testing.T) {
	_, orgID, adapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, adapter.SeedDefaultWorkflows(ctx, orgID))

	wf, err := adapter.GetDefaultWorkflow(ctx, orgID, "tickets")
	require.NoError(t, err)

	states, err := adapter.ListStates(ctx, wf.ID)
	require.NoError(t, err)

	state := states[0]
	state.Color = "#abcdef"
	err = adapter.UpdateState(ctx, state)
	require.NoError(t, err)

	updated, err := adapter.GetState(ctx, state.ID)
	require.NoError(t, err)
	require.Equal(t, "#abcdef", updated.Color)
}

func TestWorkflowAdapter_ListAvailableTransitions(t *testing.T) {
	_, orgID, adapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, adapter.SeedDefaultWorkflows(ctx, orgID))

	wf, err := adapter.GetDefaultWorkflow(ctx, orgID, "tickets")
	require.NoError(t, err)

	initial, err := adapter.GetInitialState(ctx, wf.ID)
	require.NoError(t, err)

	transitions, err := adapter.ListAvailableTransitions(ctx, wf.ID, initial.ID)
	require.NoError(t, err)
	require.NotEmpty(t, transitions, "open state should have transitions")
}

func TestWorkflowAdapter_AssignDefaultWorkflowToSpace(t *testing.T) {
	db, orgID, adapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, adapter.SeedDefaultWorkflows(ctx, orgID))

	queries := generated.New(db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, orgID)
	space := testutil.CreateTestSpace(t, db.Pool, orgID, user.ID, "service_desk")

	err := adapter.AssignDefaultWorkflowToSpace(ctx, orgID, "service_desk", space.ID)
	require.NoError(t, err)

	// Verify the space now has a workflow assigned.
	var workflowID *uuid.UUID
	err = db.Pool.QueryRow(ctx, "SELECT workflow_id FROM spaces WHERE id = $1", space.ID).Scan(&workflowID)
	require.NoError(t, err)
	require.NotNil(t, workflowID)

	// Wiki spaces should be skipped.
	wikiSpace := testutil.CreateTestSpace(t, db.Pool, orgID, user.ID, "wiki")
	err = adapter.AssignDefaultWorkflowToSpace(ctx, orgID, "wiki", wikiSpace.ID)
	require.NoError(t, err)

	_ = queries // suppress unused import
}

func TestWorkflowEngine_AvailableTransitions(t *testing.T) {
	_, orgID, wfAdapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, wfAdapter.SeedDefaultWorkflows(ctx, orgID))

	engine := workflow.NewDBEngine(wfAdapter)

	wf, err := wfAdapter.GetDefaultWorkflow(ctx, orgID, "tickets")
	require.NoError(t, err)

	initial, err := wfAdapter.GetInitialState(ctx, wf.ID)
	require.NoError(t, err)

	transitions, err := engine.AvailableTransitions(ctx, wf.ID, initial.ID)
	require.NoError(t, err)
	require.NotEmpty(t, transitions)
}

func TestWorkflowEngine_ValidateTransition_Valid(t *testing.T) {
	_, orgID, wfAdapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, wfAdapter.SeedDefaultWorkflows(ctx, orgID))

	engine := workflow.NewDBEngine(wfAdapter)

	wf, err := wfAdapter.GetDefaultWorkflow(ctx, orgID, "tickets")
	require.NoError(t, err)

	initial, err := wfAdapter.GetInitialState(ctx, wf.ID)
	require.NoError(t, err)

	transitions, err := engine.AvailableTransitions(ctx, wf.ID, initial.ID)
	require.NoError(t, err)
	require.NotEmpty(t, transitions)

	// Validate a valid transition.
	err = engine.ValidateTransition(ctx, wf.ID, initial.ID, transitions[0].ToStateID)
	require.NoError(t, err)
}

func TestWorkflowEngine_ValidateTransition_Invalid(t *testing.T) {
	_, orgID, wfAdapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, wfAdapter.SeedDefaultWorkflows(ctx, orgID))

	engine := workflow.NewDBEngine(wfAdapter)

	wf, err := wfAdapter.GetDefaultWorkflow(ctx, orgID, "tickets")
	require.NoError(t, err)

	initial, err := wfAdapter.GetInitialState(ctx, wf.ID)
	require.NoError(t, err)

	// Validate a transition to a non-existent state.
	err = engine.ValidateTransition(ctx, wf.ID, initial.ID, uuid.New())
	require.ErrorIs(t, err, workflow.ErrInvalidTransition)
}

func TestWorkflowEngine_ResolveStateName(t *testing.T) {
	_, orgID, wfAdapter := setupWorkflow(t)
	ctx := context.Background()

	require.NoError(t, wfAdapter.SeedDefaultWorkflows(ctx, orgID))

	engine := workflow.NewDBEngine(wfAdapter)

	wf, err := wfAdapter.GetDefaultWorkflow(ctx, orgID, "tickets")
	require.NoError(t, err)

	state, err := engine.ResolveStateName(ctx, wf.ID, "open")
	require.NoError(t, err)
	require.Equal(t, "open", state.Name)

	_, err = engine.ResolveStateName(ctx, wf.ID, "nonexistent")
	require.ErrorIs(t, err, workflow.ErrNotFound)
}
