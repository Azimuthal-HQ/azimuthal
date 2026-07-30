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
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
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

// THE SAME CONTRACT ON THE WRITERS, which is where known-issues #24 found it
// still open. Every one of these is `:one ... RETURNING *`, so an id naming
// nothing comes back as pgx.ErrNoRows exactly as it does from a getter — and
// every one of them wrapped it instead of mapping it, so handleProjectError's
// default arm answered 500 while the routes above them annotate 404.
//
// Pinned here rather than over HTTP because the handlers pre-load the row, so
// the only way to reach these lines through the API is to lose a race with a
// concurrent delete. That makes the window real and untestable end-to-end at
// the same time; the adapter is where the contract can actually be asserted.
// Each fails against the unmapped adapter with a wrapped "no rows in result
// set" and passes after.
func TestProjectWriteAdapters_MissingRow_ReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	items := adapters.NewItemAdapter(generated.New(db.Pool))
	sprints := adapters.NewSprintAdapter(db.Pool)

	t.Run("ItemAdapter.Update", func(t *testing.T) {
		err := items.Update(ctx, &projects.Item{
			ID: uuid.New(), Title: "Gone", Status: "todo", Priority: "medium", Kind: "task",
		})
		require.ErrorIs(t, err, projects.ErrNotFound)
	})

	t.Run("ItemAdapter.UpdateStatus", func(t *testing.T) {
		_, err := items.UpdateStatus(ctx, uuid.New(), "in_progress")
		require.ErrorIs(t, err, projects.ErrNotFound)
	})

	t.Run("SprintAdapter.Update", func(t *testing.T) {
		err := sprints.Update(ctx, &projects.Sprint{ID: uuid.New(), Name: "Gone"})
		require.ErrorIs(t, err, projects.ErrNotFound)
	})

	t.Run("SprintAdapter.UpdateStatus", func(t *testing.T) {
		_, err := sprints.UpdateStatus(ctx, uuid.New(), projects.SprintStatusCompleted)
		require.ErrorIs(t, err, projects.ErrNotFound,
			"the arm that maps the one-active-per-space unique violation must not swallow the missing row")
	})

	// The same query again, inside the completion transaction. It is a second
	// call site of UpdateSprintStatus and was mapped by nothing, so completing
	// a sprint that had just been deleted rolled back and answered 500.
	t.Run("SprintAdapter.CompleteWithDisposition", func(t *testing.T) {
		_, err := sprints.CompleteWithDisposition(ctx, uuid.New(), nil, []string{"done"})
		require.ErrorIs(t, err, projects.ErrNotFound)
	})
}

// The duplicate-label 409 that handleProjectError has always had an arm for and
// nothing could ever reach: LabelAdapter.Create did not map the unique
// violation, so projects.ErrLabelDuplicate had no producer in the tree and a
// repeated name answered 500 (known-issues #24).
//
// It asserts the sentinel AND that unrelated writes still succeed, because a
// mapping that turned every write error into ErrLabelDuplicate would pass the
// first assertion on its own.
func TestLabelAdapter_DuplicateName_ReturnsErrLabelDuplicate(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	labels := adapters.NewLabelAdapter(generated.New(db.Pool))

	require.NoError(t, labels.Create(ctx,
		&projects.Label{ID: uuid.New(), OrgID: org.ID, Name: "urgent", Color: "#ff0000"}))

	require.ErrorIs(t, labels.Create(ctx,
		&projects.Label{ID: uuid.New(), OrgID: org.ID, Name: "urgent", Color: "#00ff00"}),
		projects.ErrLabelDuplicate, "the constraint name must match, or the arm stays dead")

	require.NoError(t, labels.Create(ctx,
		&projects.Label{ID: uuid.New(), OrgID: org.ID, Name: "later", Color: "#0000ff"}),
		"only the clash is a duplicate")

	// The same name in another org is not a duplicate — the constraint is
	// (org_id, name), and matching on the constraint name is what keeps that
	// true rather than assuming it.
	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	require.NoError(t, labels.Create(ctx,
		&projects.Label{ID: uuid.New(), OrgID: otherOrg.ID, Name: "urgent", Color: "#ff0000"}))
}
