package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/dashboards"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The edges of the saved-view, queue and dashboard adapters: the branches that
// map a database outcome onto a domain sentinel.
//
// They matter because the layer above matches on those sentinels. A store that
// returned a wrapped pgx error where the service expects views.ErrNotFound
// produces a 500 for a missing row, and the difference between "does not
// exist" and "something broke" is exactly what a caller acts on.

func svEdgeQuery(t *testing.T) views.Query {
	t.Helper()
	return beaconQuery(t, `"modules":["beacon"]`)
}

// A missing row is ErrNotFound, not a wrapped driver error — on every read and
// every write that names an id.
func TestSavedViewStore_MissingRowsMapToErrNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)
	ghost := uuid.New()

	_, err := store.Get(ctx, org.ID, ghost)
	require.ErrorIs(t, err, views.ErrNotFound)

	_, err = store.Update(ctx, views.View{
		ID: ghost, OrgID: org.ID, OwnerID: user.ID, Name: "Ghost",
		Query: svEdgeQuery(t), Visibility: views.VisibilityPrivate,
	})
	require.ErrorIs(t, err, views.ErrNotFound)

	n, err := store.SoftDelete(ctx, org.ID, ghost)
	require.NoError(t, err, "deleting nothing is not an error; it is nothing")
	require.Zero(t, n)
}

// A view that exists in another organisation is invisible here, and answers
// the same "not found" a nonexistent one does — so the endpoint above cannot
// be used to confirm that an id exists somewhere.
func TestSavedViewStore_IsScopedToTheOrgOnEveryOperation(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	other := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)

	v, err := store.Create(ctx, views.View{
		OrgID: org.ID, OwnerID: user.ID, Name: "Mine",
		Query: svEdgeQuery(t), Visibility: views.VisibilityPrivate,
	})
	require.NoError(t, err)

	_, err = store.Get(ctx, other.ID, v.ID)
	require.ErrorIs(t, err, views.ErrNotFound)

	n, err := store.SoftDelete(ctx, other.ID, v.ID)
	require.NoError(t, err)
	require.Zero(t, n, "another org's soft-delete must not reach this row")
}

// LiveSpaceIDs short-circuits an empty ask rather than issuing a query with an
// empty array — the validity check runs on every list, and a page of views
// naming no space at all is the common case.
func TestSavedViewStore_LiveSpaceIDsHandlesTheEmptyAsk(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)

	got, err := store.LiveSpaceIDs(ctx, org.ID, nil)
	require.NoError(t, err)
	require.Empty(t, got)

	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	ghost := uuid.New()
	got, err = store.LiveSpaceIDs(ctx, org.ID, []uuid.UUID{space.ID, ghost})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{space.ID}, got, "a deleted or invented space id must not come back live")
}

// A stored filter document this build cannot parse is a HARD error, not a
// fallback. An empty filter would match everything the viewer can read — a
// saved view that silently widened to "everything" is the worst possible
// failure mode for this table.
func TestSavedViewStore_AnUnreadableStoredDocumentIsAnError(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)

	id := uuid.New()
	// Straight to SQL: the API refuses a document with an unknown field, and
	// the case under test is a row a different build wrote.
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO saved_views (id, org_id, owner_id, name, query, visibility)
		 VALUES ($1,$2,$3,'Broken','{"v":1,"filter":{"modules":["beacon"],"nope":1},"sort":{"field":"updated_at","dir":"desc"}}','private')`,
		id, org.ID, user.ID)
	require.NoError(t, err)

	_, err = store.Get(ctx, org.ID, id)
	require.Error(t, err)
	require.ErrorIs(t, err, views.ErrUnknownField)
	require.Contains(t, err.Error(), id.String(), "the error must name the row so it can be found and fixed")
}

// A queue name is unique per space among live rows, and the violation maps to
// the domain sentinel rather than to a raw constraint error — the handler
// answers 409 from it.
func TestQueueStore_ADuplicateNameMapsToTheSentinel(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	store := adapters.NewSavedViewAdapter(db.Pool)

	mk := func(pos int32) views.View {
		p := pos
		return views.View{
			OrgID: org.ID, OwnerID: user.ID, SpaceID: &space.ID, Position: &p,
			Name: "All open", Query: svEdgeQuery(t), Visibility: views.VisibilitySpace,
		}
	}
	_, err := store.CreateQueue(ctx, mk(0))
	require.NoError(t, err)

	_, err = store.CreateQueue(ctx, mk(1))
	require.ErrorIs(t, err, views.ErrQueueNameTaken)

	// CreateQueueIfAbsent is the idempotent form: it reports that it did
	// nothing rather than failing, which is what makes the default-queue
	// button safe to press twice.
	inserted, err := store.CreateQueueIfAbsent(ctx, mk(2))
	require.NoError(t, err)
	require.False(t, inserted)
}

// A queue read or write naming a space it does not belong to finds nothing.
// The binding is the authority: a queue reachable through another space's
// route would escape the guard that bounds it.
func TestQueueStore_IsScopedToItsSpace(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	other := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	store := adapters.NewSavedViewAdapter(db.Pool)

	pos := int32(0)
	q, err := store.CreateQueue(ctx, views.View{
		OrgID: org.ID, OwnerID: user.ID, SpaceID: &space.ID, Position: &pos,
		Name: "Mine", Query: svEdgeQuery(t), Visibility: views.VisibilitySpace,
	})
	require.NoError(t, err)

	_, err = store.GetQueue(ctx, org.ID, other.ID, q.ID)
	require.ErrorIs(t, err, views.ErrQueueNotInSpace)

	n, err := store.DeleteQueue(ctx, org.ID, other.ID, q.ID)
	require.NoError(t, err)
	require.Zero(t, n)

	// And a reorder naming a queue that is not in the space fails rather than
	// silently renumbering nothing.
	require.Error(t, store.ReorderQueues(ctx, org.ID, other.ID, []uuid.UUID{q.ID}))
}

// NextQueuePosition counts from zero on an empty space and steps past the
// highest live position afterwards, so a new queue lands at the end rather
// than colliding.
func TestQueueStore_NextPositionStepsPastTheLast(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	store := adapters.NewSavedViewAdapter(db.Pool)

	first, err := store.NextQueuePosition(ctx, space.ID)
	require.NoError(t, err)
	require.Equal(t, int32(0), first)

	pos := first
	_, err = store.CreateQueue(ctx, views.View{
		OrgID: org.ID, OwnerID: user.ID, SpaceID: &space.ID, Position: &pos,
		Name: "First", Query: svEdgeQuery(t), Visibility: views.VisibilitySpace,
	})
	require.NoError(t, err)

	next, err := store.NextQueuePosition(ctx, space.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), next)
}

// A space with no workflow answers with no statuses rather than erroring, so
// the default-queue set degrades to "no status filter" instead of failing.
func TestQueueStore_SpaceWorkflowStatuses(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	store := adapters.NewSavedViewAdapter(db.Pool)

	got, err := store.SpaceWorkflowStatuses(ctx, space.ID)
	require.NoError(t, err)
	// The fixture space has no workflow assigned, which is the degraded case
	// CreateDefaults is written to survive.
	require.Empty(t, got)

	_, err = store.SpaceWorkflowStatuses(ctx, uuid.New())
	require.NoError(t, err, "an unknown space is empty, not an error")
}

// ── Dashboards ──────────────────────────────────────────────────────────────

func TestDashboardStore_MissingRowsMapToErrNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)
	ghost := uuid.New()

	_, err := store.Get(ctx, org.ID, ghost)
	require.ErrorIs(t, err, dashboards.ErrNotFound)

	_, err = store.Update(ctx, dashboards.Dashboard{
		ID: ghost, OrgID: org.ID, OwnerID: user.ID, Name: "Ghost",
		Module: dashboards.ModuleHome, Visibility: views.VisibilityPrivate,
	})
	require.ErrorIs(t, err, dashboards.ErrNotFound)

	_, err = store.DefaultFor(ctx, org.ID, user.ID, string(dashboards.ModuleHome))
	require.ErrorIs(t, err, dashboards.ErrNotFound)
}

// Promoting an existing dashboard to default stands the previous holder down
// in the same transaction. The update path is a different code path from the
// create path and needs its own case: dashboards_one_default is not
// deferrable, so an update that promoted without demoting would collide.
func TestDashboardStore_UpdateCanClaimTheDefaultSlot(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	first := newDashboard(org.ID, user.ID, "First")
	first.IsDefault = true
	a, err := store.Create(ctx, first)
	require.NoError(t, err)

	second, err := store.Create(ctx, newDashboard(org.ID, user.ID, "Second"))
	require.NoError(t, err)

	second.IsDefault = true
	_, err = store.Update(ctx, second)
	require.NoError(t, err, "an update that claims the default slot must demote the holder first")

	got, err := store.DefaultFor(ctx, org.ID, user.ID, string(dashboards.ModuleHome))
	require.NoError(t, err)
	require.Equal(t, second.ID, got.ID)

	demoted, err := store.Get(ctx, org.ID, a.ID)
	require.NoError(t, err)
	require.False(t, demoted.IsDefault)
}

// An empty layout is a legal layout: clearing a dashboard is something a
// person may do, and it must not be mistaken for a failed write.
func TestDashboardStore_AnEmptyLayoutIsLegal(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	d, err := store.Create(ctx, newDashboard(org.ID, user.ID, "Board"))
	require.NoError(t, err)
	_, err = store.ReplaceGadgets(ctx, d.ID, []dashboards.Gadget{
		{Key: string(dashboards.GadgetMyWork), Position: 0, ColSpan: 2},
	})
	require.NoError(t, err)

	written, err := store.ReplaceGadgets(ctx, d.ID, nil)
	require.NoError(t, err)
	require.Empty(t, written)

	got, err := store.ListGadgets(ctx, d.ID)
	require.NoError(t, err)
	require.Empty(t, got)
}
