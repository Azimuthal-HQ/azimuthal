package adapters_test

// Repository-contract coverage for the "unmapped pgx.ErrNoRows" defect
// family: every adapter getter must translate an absent row into its domain
// package's ErrNotFound sentinel (house pattern: signing_key.go), never leak
// a wrapped pgx.ErrNoRows. The getters with an HTTP GET surface are proven
// end-to-end in internal/core/api/notfound_integration_test.go; the ones
// below have no direct GET endpoint, so the contract is pinned at the
// adapter boundary. Each test fails before the mapping fix and passes after.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

func TestUserAdapter_GetByID_Nonexistent_ReturnsErrNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	adapter := adapters.NewUserAdapter(db.Pool, org.ID)

	_, err := adapter.GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, auth.ErrNotFound)
}

func TestUserAdapter_GetByEmail_Nonexistent_ReturnsErrNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	adapter := adapters.NewUserAdapter(db.Pool, org.ID)

	_, err := adapter.GetByEmail(context.Background(), "nobody@azimuthal.dev")
	require.ErrorIs(t, err, auth.ErrNotFound)
}

func TestSessionAdapter_GetByToken_Nonexistent_ReturnsErrNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	adapter := adapters.NewSessionAdapter(generated.New(db.Pool))

	_, err := adapter.GetByToken(context.Background(), "no-such-token")
	require.ErrorIs(t, err, auth.ErrNotFound)
}

func TestWorkflowAdapter_GetWorkflow_Nonexistent_ReturnsErrNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	adapter := adapters.NewWorkflowAdapter(generated.New(db.Pool))

	_, err := adapter.GetWorkflow(context.Background(), uuid.New())
	require.ErrorIs(t, err, workflow.ErrNotFound)
}

func TestWorkflowAdapter_GetDefaultWorkflow_NoneSeeded_ReturnsErrNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	adapter := adapters.NewWorkflowAdapter(generated.New(db.Pool))

	// A fresh org has no seeded workflows, so no default exists.
	_, err := adapter.GetDefaultWorkflow(context.Background(), org.ID, "tickets")
	require.ErrorIs(t, err, workflow.ErrNotFound)
}

func TestWorkflowAdapter_GetState_Nonexistent_ReturnsErrNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	adapter := adapters.NewWorkflowAdapter(generated.New(db.Pool))

	_, err := adapter.GetState(context.Background(), uuid.New())
	require.ErrorIs(t, err, workflow.ErrNotFound)
}

func TestWorkflowAdapter_GetInitialState_NonexistentWorkflow_ReturnsErrNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	adapter := adapters.NewWorkflowAdapter(generated.New(db.Pool))

	_, err := adapter.GetInitialState(context.Background(), uuid.New())
	require.ErrorIs(t, err, workflow.ErrNotFound)
}
