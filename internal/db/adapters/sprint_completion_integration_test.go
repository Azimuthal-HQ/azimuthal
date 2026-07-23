package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// makeSprintItem creates and persists an item assigned to sprintID with the
// given status, returning the created item.
func makeSprintItem(t *testing.T, ctx context.Context, ia *adapters.ItemAdapter, spaceID, reporterID uuid.UUID, sprintID *uuid.UUID, status string) *projects.Item {
	t.Helper()
	item := &projects.Item{
		ID: uuid.New(), SpaceID: spaceID,
		Kind: "task", Title: "Item " + status, Status: status, Priority: "medium",
		ReporterID: reporterID,
	}
	require.NoError(t, ia.Create(ctx, item))
	require.NoError(t, ia.UpdateSprint(ctx, item.ID, sprintID))
	return item
}

// sprintIDOf returns the item's current sprint_id as read back from the DB.
func sprintIDOf(t *testing.T, ctx context.Context, ia *adapters.ItemAdapter, id uuid.UUID) *uuid.UUID {
	t.Helper()
	got, err := ia.GetByID(ctx, id)
	require.NoError(t, err)
	return got.SprintID
}

// TestSprintAdapter_CompleteWithDisposition_ToBacklog verifies that completing
// a sprint returns its incomplete items to the backlog (sprint_id = NULL) while
// leaving done items on the completed sprint.
func TestSprintAdapter_CompleteWithDisposition_ToBacklog(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	queries := generated.New(db.Pool)
	itemAdapter := adapters.NewItemAdapter(queries)
	sprintAdapter := adapters.NewSprintAdapter(db.Pool)
	ctx := context.Background()

	sprint := &projects.Sprint{
		ID: uuid.New(), SpaceID: space.ID,
		Name: "Sprint 1", Status: "active", CreatedBy: user.ID,
	}
	require.NoError(t, sprintAdapter.Create(ctx, sprint))

	openItem := makeSprintItem(t, ctx, itemAdapter, space.ID, user.ID, &sprint.ID, "open")
	inProgress := makeSprintItem(t, ctx, itemAdapter, space.ID, user.ID, &sprint.ID, "in_progress")
	doneItem := makeSprintItem(t, ctx, itemAdapter, space.ID, user.ID, &sprint.ID, "done")

	updated, err := sprintAdapter.CompleteWithDisposition(ctx, sprint.ID, nil, projects.DoneStatuses)
	require.NoError(t, err)
	require.Equal(t, projects.SprintStatusCompleted, updated.Status)

	// The two incomplete items returned to the backlog.
	require.Nil(t, sprintIDOf(t, ctx, itemAdapter, openItem.ID), "open item should be back in backlog")
	require.Nil(t, sprintIDOf(t, ctx, itemAdapter, inProgress.ID), "in-progress item should be back in backlog")

	// The done item stayed on the completed sprint — proves the done-status
	// filter is applied (this assertion fails if the WHERE NOT(...) clause is
	// dropped and every item is swept off the sprint).
	got := sprintIDOf(t, ctx, itemAdapter, doneItem.ID)
	require.NotNil(t, got, "done item must stay on the completed sprint")
	require.Equal(t, sprint.ID, *got)
}

// TestSprintAdapter_CompleteWithDisposition_ToNextSprint verifies that
// completing a sprint moves its incomplete items to a chosen next sprint while
// leaving done items on the completed sprint.
func TestSprintAdapter_CompleteWithDisposition_ToNextSprint(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	queries := generated.New(db.Pool)
	itemAdapter := adapters.NewItemAdapter(queries)
	sprintAdapter := adapters.NewSprintAdapter(db.Pool)
	ctx := context.Background()

	current := &projects.Sprint{
		ID: uuid.New(), SpaceID: space.ID,
		Name: "Current", Status: "active", CreatedBy: user.ID,
	}
	require.NoError(t, sprintAdapter.Create(ctx, current))
	next := &projects.Sprint{
		ID: uuid.New(), SpaceID: space.ID,
		Name: "Next", Status: "planned", CreatedBy: user.ID,
	}
	require.NoError(t, sprintAdapter.Create(ctx, next))

	openItem := makeSprintItem(t, ctx, itemAdapter, space.ID, user.ID, &current.ID, "open")
	doneItem := makeSprintItem(t, ctx, itemAdapter, space.ID, user.ID, &current.ID, "resolved")

	_, err := sprintAdapter.CompleteWithDisposition(ctx, current.ID, &next.ID, projects.DoneStatuses)
	require.NoError(t, err)

	// The incomplete item carried over to the next sprint.
	got := sprintIDOf(t, ctx, itemAdapter, openItem.ID)
	require.NotNil(t, got)
	require.Equal(t, next.ID, *got, "incomplete item should move to the next sprint")

	// The done item stayed on the completed sprint.
	stayed := sprintIDOf(t, ctx, itemAdapter, doneItem.ID)
	require.NotNil(t, stayed)
	require.Equal(t, current.ID, *stayed)

	// The next sprint now lists exactly the carried-over item.
	carried, err := itemAdapter.ListBySprint(ctx, next.ID)
	require.NoError(t, err)
	require.Len(t, carried, 1)
	require.Equal(t, openItem.ID, carried[0].ID)
}

// TestSprintAdapter_SingleActivePerSpace_Constraint verifies the migration-034
// partial unique index: a space may hold at most one active sprint, and the
// adapter maps the violation to ErrSprintActive. This test dies if the index is
// removed — without it, the second activation succeeds and the require.ErrorIs
// fails.
func TestSprintAdapter_SingleActivePerSpace_Constraint(t *testing.T) {
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	otherSpace := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	sprintAdapter := adapters.NewSprintAdapter(db.Pool)
	ctx := context.Background()

	first := &projects.Sprint{ID: uuid.New(), SpaceID: space.ID, Name: "First", Status: "planned", CreatedBy: user.ID}
	second := &projects.Sprint{ID: uuid.New(), SpaceID: space.ID, Name: "Second", Status: "planned", CreatedBy: user.ID}
	require.NoError(t, sprintAdapter.Create(ctx, first))
	require.NoError(t, sprintAdapter.Create(ctx, second))

	// Activating the first is fine.
	_, err := sprintAdapter.UpdateStatus(ctx, first.ID, projects.SprintStatusActive)
	require.NoError(t, err)

	// Activating a second sprint in the same space violates the constraint and
	// is surfaced as ErrSprintActive.
	_, err = sprintAdapter.UpdateStatus(ctx, second.ID, projects.SprintStatusActive)
	require.ErrorIs(t, err, projects.ErrSprintActive)

	// A different space may have its own active sprint concurrently.
	third := &projects.Sprint{ID: uuid.New(), SpaceID: otherSpace.ID, Name: "Third", Status: "planned", CreatedBy: user.ID}
	require.NoError(t, sprintAdapter.Create(ctx, third))
	_, err = sprintAdapter.UpdateStatus(ctx, third.ID, projects.SprintStatusActive)
	require.NoError(t, err, "a second space may have its own active sprint")
}
