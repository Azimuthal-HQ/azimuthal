package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/workflow"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// StatusesForSpace supplies the board's status vocabulary, and the board uses
// it for two things: it derives the default column set from it — one column per
// status, in the order returned — and it refuses to save a configuration that
// leaves any of those statuses without a column. So the names and their order
// are both load-bearing, and both are asserted literally below.
//
// The tests run against real PostgreSQL because everything they check lives in
// SQL rather than in Go: the ordering, and the space-to-workflow join that
// decides whose vocabulary a space gets.

type statusFixture struct {
	db      *testutil.TestDB
	orgID   uuid.UUID
	userID  uuid.UUID
	wf      *adapters.WorkflowAdapter
	adapter *adapters.WorkflowStatusAdapter
}

// newStatusFixture builds an org as provisioning leaves it — with the two
// default workflows seeded. Spaces are added per test, because whether a space
// has a workflow at all is what several of these tests turn on.
func newStatusFixture(t *testing.T) (context.Context, *statusFixture) {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	ctx := context.Background()

	wf := adapters.NewWorkflowAdapter(generated.New(db.Pool))
	require.NoError(t, wf.SeedDefaultWorkflows(ctx, org.ID))

	return ctx, &statusFixture{
		db:      db,
		orgID:   org.ID,
		userID:  user.ID,
		wf:      wf,
		adapter: adapters.NewWorkflowStatusAdapter(db.Pool),
	}
}

func (f *statusFixture) space(t *testing.T, spaceType string) uuid.UUID {
	t.Helper()
	return testutil.CreateTestSpace(t, f.db.Pool, f.orgID, f.userID, spaceType).ID
}

// spaceWithDefaultWorkflow follows the production path for a new space: the
// spaces handler creates it, then assigns the org's default workflow for its
// type.
func (f *statusFixture) spaceWithDefaultWorkflow(t *testing.T, spaceType string) uuid.UUID {
	t.Helper()
	id := f.space(t, spaceType)
	require.NoError(t, f.wf.AssignDefaultWorkflowToSpace(context.Background(), f.orgID, spaceType, id))
	return id
}

func TestWorkflowStatusAdapter_VectorSpaceReturnsSeededWorkflowOrder(t *testing.T) {
	ctx, f := newStatusFixture(t)
	spaceID := f.spaceWithDefaultWorkflow(t, "vector")

	got, err := f.adapter.StatusesForSpace(ctx, spaceID)
	require.NoError(t, err)

	// Exact slice equality rather than ElementsMatch, because this order is the
	// left-to-right order of the board's columns. Sorted alphabetically the same
	// names read backlog, done, in_progress, in_review, todo — finished work
	// sitting second, next to the backlog — so an order-insensitive assertion
	// here would accept a board nobody could use.
	require.Equal(t,
		[]string{"backlog", "todo", "in_progress", "in_review", "done"}, got,
		"a vector space's vocabulary is its project workflow's states, in position order")

	// The seeded vocabulary must stay distinguishable from the vocabulary the
	// service falls back to when this call returns nothing. If the two ever
	// coincide, the assertion above stops being able to tell a working adapter
	// from one that read no rows at all.
	require.NotEqual(t, projects.DefaultColumnNames, got)
}

// The order is the workflow's declared position, not the order the states
// happened to be inserted in. This is the case that separates the two: a
// workflow built back to front, which an admin creating states through the
// workflow API can produce simply by adding the last column first.
func TestWorkflowStatusAdapter_OrderFollowsPositionNotInsertion(t *testing.T) {
	ctx, f := newStatusFixture(t)
	spaceID := f.space(t, "vector")

	custom := &workflow.Workflow{OrgID: f.orgID, Name: "Reversed", AppliesTo: "project_items"}
	require.NoError(t, f.wf.CreateWorkflow(ctx, custom))

	for _, s := range []struct {
		name     string
		position int32
	}{
		{"third", 2}, {"second", 1}, {"first", 0},
	} {
		require.NoError(t, f.wf.CreateState(ctx, &workflow.State{
			WorkflowID: custom.ID,
			Name:       s.name,
			Category:   "todo",
			Color:      "#3b82f6",
			Position:   s.position,
			IsInitial:  s.position == 0,
		}))
	}

	// The same query the spaces handler assigns a default workflow with; this
	// workflow simply is not the org default, so it is assigned directly.
	require.NoError(t, generated.New(f.db.Pool).AssignWorkflowToSpace(ctx, generated.AssignWorkflowToSpaceParams{
		WorkflowID: pgtype.UUID{Bytes: custom.ID, Valid: true},
		ID:         spaceID,
	}))

	got, err := f.adapter.StatusesForSpace(ctx, spaceID)
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second", "third"}, got,
		"states must come back in position order even when they were created in reverse")
}

func TestWorkflowStatusAdapter_SpaceWithNoWorkflowIsEmptyNotAnError(t *testing.T) {
	ctx, f := newStatusFixture(t)
	unassigned := f.space(t, "vector")
	assigned := f.spaceWithDefaultWorkflow(t, "vector")

	got, err := f.adapter.StatusesForSpace(ctx, unassigned)

	// Emptiness is a supported answer, not a failure: the service reads it as
	// "this space has no workflow" and falls back to the default vocabulary. An
	// error instead would put a 500 in front of a board that renders fine.
	require.NoError(t, err)
	require.Empty(t, got)

	// The org does have workflows, and a sibling space resolves them — so the
	// empty answer above is a statement about this space's own workflow_id, not
	// a fixture that forgot to seed anything.
	sibling, err := f.adapter.StatusesForSpace(ctx, assigned)
	require.NoError(t, err)
	require.NotEmpty(t, sibling)
}

// Two spaces in one org, on different workflows, keep separate vocabularies.
// The failure this guards is a join that widens from the space to the org:
// both lists would then be the union of all nine states, and each board would
// offer statuses its items can never hold — which SaveConfig would then insist
// were given columns.
func TestWorkflowStatusAdapter_VocabularyIsPerSpace(t *testing.T) {
	ctx, f := newStatusFixture(t)
	vector := f.spaceWithDefaultWorkflow(t, "vector")
	beacon := f.spaceWithDefaultWorkflow(t, "beacon")

	gotVector, err := f.adapter.StatusesForSpace(ctx, vector)
	require.NoError(t, err)
	gotBeacon, err := f.adapter.StatusesForSpace(ctx, beacon)
	require.NoError(t, err)

	require.Equal(t, []string{"backlog", "todo", "in_progress", "in_review", "done"}, gotVector)
	require.Equal(t, []string{"open", "in_progress", "resolved", "closed"}, gotBeacon)
}
