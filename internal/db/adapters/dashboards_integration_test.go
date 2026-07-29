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

// The dashboards store against a real database. These are the tests that hold
// migration 042's constraints in place: the FK that must not cascade, the
// partial unique index behind "one default", and the whole-collection layout
// write that must not leave a dashboard half-arranged.

func newDashboard(orgID, ownerID uuid.UUID, name string) dashboards.Dashboard {
	return dashboards.Dashboard{
		OrgID: orgID, OwnerID: ownerID, Name: name,
		Module: dashboards.ModuleHome, Visibility: views.VisibilityPrivate,
	}
}

func TestDashboardStore_RoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	created, err := store.Create(ctx, newDashboard(org.ID, user.ID, "Support health"))
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, created.ID)

	got, err := store.Get(ctx, org.ID, created.ID)
	require.NoError(t, err)
	require.Equal(t, "Support health", got.Name)
	require.Equal(t, dashboards.ModuleHome, got.Module)
	require.Equal(t, views.VisibilityPrivate, got.Visibility)
	require.False(t, got.IsDefault)
	require.False(t, got.IsSeeded)
	require.Equal(t, "Test User", got.OwnerName, "the owner name is joined, never looked up per row")

	got.Name = "Renamed"
	got.Visibility = views.VisibilityOrg
	_, err = store.Update(ctx, got)
	require.NoError(t, err)

	back, err := store.Get(ctx, org.ID, created.ID)
	require.NoError(t, err)
	require.Equal(t, "Renamed", back.Name)
	require.Equal(t, views.VisibilityOrg, back.Visibility)

	n, err := store.SoftDelete(ctx, org.ID, created.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	_, err = store.Get(ctx, org.ID, created.ID)
	require.ErrorIs(t, err, dashboards.ErrNotFound, "a soft-deleted dashboard is gone to every read")

	n, err = store.SoftDelete(ctx, org.ID, created.ID)
	require.NoError(t, err)
	require.Zero(t, n, "deleting twice reports nothing changed rather than erroring")
}

func TestDashboardStore_GetIsScopedToTheOrg(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	other := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	created, err := store.Create(ctx, newDashboard(org.ID, user.ID, "Mine"))
	require.NoError(t, err)

	_, err = store.Get(ctx, other.ID, created.ID)
	require.ErrorIs(t, err, dashboards.ErrNotFound,
		"a dashboard id from another organisation must not resolve, even with a correct uuid")
}

// MIGRATION 042's FK, THE ONE D57 IS ABOUT. Deleting a team must NULL the
// column, never delete the dashboard. Under ON DELETE CASCADE — which is what
// the spec sketch declares, character for character — this test fails by
// finding no row at all: somebody else's saved work, destroyed as a side
// effect of an unrelated administrative action.
//
// Fails-before: change the reference in migration 042 to ON DELETE CASCADE and
// the Get below returns ErrNotFound.
func TestDashboardStore_TeamDeletionDegradesRatherThanDestroys(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	team := testutil.DefaultTeamID(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	d := newDashboard(org.ID, user.ID, "Team board")
	d.Visibility = views.VisibilityTeam
	d.VisibilityTeamID = &team
	created, err := store.Create(ctx, d)
	require.NoError(t, err)

	// A hard delete: the CASCADE the sketch declares fires on this.
	_, err = db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, team)
	require.NoError(t, err)

	got, err := store.Get(ctx, org.ID, created.ID)
	require.NoError(t, err, "the dashboard must survive its audience team being deleted")
	require.Equal(t, views.VisibilityTeam, got.Visibility)
	require.Nil(t, got.VisibilityTeamID, "the column is nulled, which is the degraded state the API reports")

	// And the degraded row reaches nobody but its owner.
	require.True(t, got.CanSee(views.Actor{UserID: user.ID}))
	require.False(t, got.CanSee(views.Actor{UserID: uuid.New(), EffectiveTeamIDs: []uuid.UUID{team}}),
		"a team audience with no team must match nobody — fail closed, then prompt")
}

// The partial unique index is what makes "one default per owner per module" a
// database fact. The adapter stands the previous holder down in the same
// transaction, because the index is deliberately NOT deferrable.
func TestDashboardStore_OnlyOneDefaultPerOwnerAndModule(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	first := newDashboard(org.ID, user.ID, "First")
	first.IsDefault = true
	a, err := store.Create(ctx, first)
	require.NoError(t, err)

	second := newDashboard(org.ID, user.ID, "Second")
	second.IsDefault = true
	b, err := store.Create(ctx, second)
	require.NoError(t, err)

	got, err := store.DefaultFor(ctx, org.ID, user.ID, string(dashboards.ModuleHome))
	require.NoError(t, err)
	require.Equal(t, b.ID, got.ID, "the newest claim wins")

	old, err := store.Get(ctx, org.ID, a.ID)
	require.NoError(t, err)
	require.False(t, old.IsDefault, "the previous default stood down rather than colliding on the index")

	// A different module is a different slot.
	vector := newDashboard(org.ID, user.ID, "Sprint")
	vector.Module = dashboards.ModuleVector
	vector.IsDefault = true
	v, err := store.Create(ctx, vector)
	require.NoError(t, err)

	stillHome, err := store.DefaultFor(ctx, org.ID, user.ID, string(dashboards.ModuleHome))
	require.NoError(t, err)
	require.Equal(t, b.ID, stillHome.ID, "claiming the Vector slot must not touch the Home one")
	vecDefault, err := store.DefaultFor(ctx, org.ID, user.ID, string(dashboards.ModuleVector))
	require.NoError(t, err)
	require.Equal(t, v.ID, vecDefault.ID)
}

// A soft-deleted dashboard releases its default slot: the index is partial on
// deleted_at, so the next promotion must not collide with a dead row.
func TestDashboardStore_ADeletedDefaultReleasesTheSlot(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	first := newDashboard(org.ID, user.ID, "First")
	first.IsDefault = true
	a, err := store.Create(ctx, first)
	require.NoError(t, err)
	_, err = store.SoftDelete(ctx, org.ID, a.ID)
	require.NoError(t, err)

	_, err = store.DefaultFor(ctx, org.ID, user.ID, string(dashboards.ModuleHome))
	require.ErrorIs(t, err, dashboards.ErrNotFound)

	second := newDashboard(org.ID, user.ID, "Second")
	second.IsDefault = true
	_, err = store.Create(ctx, second)
	require.NoError(t, err, "a soft-deleted default must not block the next one")
}

func TestDashboardStore_ListForViewerMatchesTheThreeAudiences(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	owner := testutil.CreateTestUser(t, db.Pool, org.ID)
	reader := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	team := testutil.DefaultTeamID(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	private, err := store.Create(ctx, newDashboard(org.ID, owner.ID, "Private"))
	require.NoError(t, err)

	orgWide := newDashboard(org.ID, owner.ID, "Org wide")
	orgWide.Visibility = views.VisibilityOrg
	orgRow, err := store.Create(ctx, orgWide)
	require.NoError(t, err)

	teamShared := newDashboard(org.ID, owner.ID, "Team")
	teamShared.Visibility = views.VisibilityTeam
	teamShared.VisibilityTeamID = &team
	teamRow, err := store.Create(ctx, teamShared)
	require.NoError(t, err)

	seen := func(rows []dashboards.Dashboard) map[uuid.UUID]bool {
		out := map[uuid.UUID]bool{}
		for _, d := range rows {
			out[d.ID] = true
		}
		return out
	}

	// The reader is in the team.
	rows, err := store.ListForViewer(ctx, org.ID, reader.ID, []uuid.UUID{team}, "")
	require.NoError(t, err)
	got := seen(rows)
	require.False(t, got[private.ID], "a private dashboard must not list for anybody else")
	require.True(t, got[orgRow.ID])
	require.True(t, got[teamRow.ID])

	// The same reader with no teams loses the team-audience row and nothing
	// else — which is what proves the team branch is doing the work.
	rows, err = store.ListForViewer(ctx, org.ID, reader.ID, nil, "")
	require.NoError(t, err)
	got = seen(rows)
	require.False(t, got[teamRow.ID], "an empty effective team set must not match a team audience")
	require.True(t, got[orgRow.ID])

	// The owner sees all three.
	rows, err = store.ListForViewer(ctx, org.ID, owner.ID, nil, "")
	require.NoError(t, err)
	require.Len(t, rows, 3)
}

// A degraded team-audience dashboard must not list for the team's former
// members. The SQL tests visibility_team_id IS NOT NULL explicitly rather than
// leaning on `= ANY('{}')` being false, and this is that test.
func TestDashboardStore_ADegradedTeamDashboardListsForNobodyElse(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	owner := testutil.CreateTestUser(t, db.Pool, org.ID)
	reader := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	team := testutil.DefaultTeamID(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	d := newDashboard(org.ID, owner.ID, "Team")
	d.Visibility = views.VisibilityTeam
	d.VisibilityTeamID = &team
	created, err := store.Create(ctx, d)
	require.NoError(t, err)

	_, err = db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, team)
	require.NoError(t, err)

	rows, err := store.ListForViewer(ctx, org.ID, reader.ID, []uuid.UUID{team}, "")
	require.NoError(t, err)
	for _, r := range rows {
		require.NotEqual(t, created.ID, r.ID,
			"a dashboard whose audience team was deleted must reach nobody but its owner")
	}

	own, err := store.ListForViewer(ctx, org.ID, owner.ID, nil, "")
	require.NoError(t, err)
	require.Len(t, own, 1, "its owner still sees it, which is who the re-scope prompt is for")
}

func TestDashboardStore_ListFiltersByModule(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	_, err := store.Create(ctx, newDashboard(org.ID, user.ID, "Home one"))
	require.NoError(t, err)
	beacon := newDashboard(org.ID, user.ID, "Support")
	beacon.Module = dashboards.ModuleBeacon
	_, err = store.Create(ctx, beacon)
	require.NoError(t, err)

	rows, err := store.ListForViewer(ctx, org.ID, user.ID, nil, "beacon")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, dashboards.ModuleBeacon, rows[0].Module)

	rows, err = store.ListForViewer(ctx, org.ID, user.ID, nil, "")
	require.NoError(t, err)
	require.Len(t, rows, 2, "an empty module means every module, not none")
}

// ── Layout writes ───────────────────────────────────────────────────────────

func TestDashboardStore_ReplaceGadgetsIsAWholeCollectionWrite(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	d, err := store.Create(ctx, newDashboard(org.ID, user.ID, "Board"))
	require.NoError(t, err)

	limit := 9
	first := []dashboards.Gadget{
		{Key: string(dashboards.GadgetMyWork), Position: 0, ColSpan: 2, Config: dashboards.Config{Limit: &limit}},
		{Key: string(dashboards.GadgetRecentWork), Position: 1, ColSpan: 2},
		{Key: string(dashboards.GadgetNote), Position: 2, ColSpan: 4, Config: dashboards.Config{Body: "hello"}},
	}
	written, err := store.ReplaceGadgets(ctx, d.ID, first)
	require.NoError(t, err)
	require.Len(t, written, 3)

	got, err := store.ListGadgets(ctx, d.ID)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, int32(0), got[0].Position, "gadgets come back in display order")
	require.NotNil(t, got[0].Config.Limit)
	require.Equal(t, 9, *got[0].Config.Limit, "the config document round-trips through jsonb")
	require.Equal(t, "hello", got[2].Config.Body)

	// A reordering write reuses the same positions. It must not collide with
	// the rows it is replacing — which it cannot, because the delete lands
	// first inside the same transaction.
	second := []dashboards.Gadget{
		{Key: string(dashboards.GadgetNote), Position: 0, ColSpan: 4, Config: dashboards.Config{Body: "hello"}},
		{Key: string(dashboards.GadgetMyWork), Position: 1, ColSpan: 2},
	}
	_, err = store.ReplaceGadgets(ctx, d.ID, second)
	require.NoError(t, err)

	got, err = store.ListGadgets(ctx, d.ID)
	require.NoError(t, err)
	require.Len(t, got, 2, "the previous collection is gone, not merged with")
	require.Equal(t, string(dashboards.GadgetNote), got[0].Key)
}

// The whole point of the transaction: a layout write that fails part-way must
// leave the old layout intact rather than an empty dashboard. The second
// gadget here carries a key the registry does not define, which the insert
// helper refuses — after the delete has already run inside the transaction.
//
// Fails-before: run the delete and the inserts outside a transaction (each its
// own implicit one) and the dashboard is left with zero gadgets.
func TestDashboardStore_AFailedLayoutWriteLeavesTheOldOneIntact(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	d, err := store.Create(ctx, newDashboard(org.ID, user.ID, "Board"))
	require.NoError(t, err)
	_, err = store.ReplaceGadgets(ctx, d.ID, []dashboards.Gadget{
		{Key: string(dashboards.GadgetMyWork), Position: 0, ColSpan: 2},
		{Key: string(dashboards.GadgetRecentWork), Position: 1, ColSpan: 2},
	})
	require.NoError(t, err)

	_, err = store.ReplaceGadgets(ctx, d.ID, []dashboards.Gadget{
		{Key: string(dashboards.GadgetNote), Position: 0, ColSpan: 4},
		{Key: "burndown", Position: 1, ColSpan: 2},
	})
	require.Error(t, err)

	got, err := store.ListGadgets(ctx, d.ID)
	require.NoError(t, err)
	require.Len(t, got, 2, "a rejected layout must not have deleted the one that was there")
	require.Equal(t, string(dashboards.GadgetMyWork), got[0].Key)
}

// Deleting a saved view must not delete the gadgets that point at it — the
// tile survives with a null reference and renders "pick another view". CASCADE
// here would silently rearrange somebody's dashboard.
func TestDashboardStore_DeletingAViewNullsTheGadgetRatherThanRemovingIt(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)
	viewStore := adapters.NewSavedViewAdapter(db.Pool)

	v, err := viewStore.Create(ctx, views.View{
		OrgID: org.ID, OwnerID: user.ID, Name: "Open",
		Query: beaconQuery(t, `"modules":["beacon"]`), Visibility: views.VisibilityPrivate,
	})
	require.NoError(t, err)

	d, err := store.Create(ctx, newDashboard(org.ID, user.ID, "Board"))
	require.NoError(t, err)
	_, err = store.ReplaceGadgets(ctx, d.ID, []dashboards.Gadget{
		{Key: string(dashboards.GadgetViewResults), Position: 0, ColSpan: 2, SavedViewID: &v.ID},
	})
	require.NoError(t, err)

	_, err = db.Pool.Exec(ctx, `DELETE FROM saved_views WHERE id = $1`, v.ID)
	require.NoError(t, err)

	got, err := store.ListGadgets(ctx, d.ID)
	require.NoError(t, err)
	require.Len(t, got, 1, "the tile keeps its slot, its span and its place in the layout")
	require.Nil(t, got[0].SavedViewID)
}

// Deleting a dashboard takes its gadgets with it. Here CASCADE is correct: a
// gadget has no meaning without the dashboard that arranges it.
func TestDashboardStore_DeletingADashboardCascadesToItsGadgets(t *testing.T) {
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

	_, err = db.Pool.Exec(ctx, `DELETE FROM dashboards WHERE id = $1`, d.ID)
	require.NoError(t, err)

	var n int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM dashboard_gadgets WHERE dashboard_id = $1`, d.ID).Scan(&n))
	require.Zero(t, n)
}

// A stored key this build does not define must LOAD — that is what makes the
// placeholder tile possible. Migration 042 deliberately puts no CHECK on
// gadget_key for exactly this.
//
// Fails-before: add a CHECK constraint listing the known keys and the insert
// below fails, taking the whole dashboard read with it.
func TestDashboardStore_AnUnknownStoredKeyStillLoads(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	d, err := store.Create(ctx, newDashboard(org.ID, user.ID, "Board"))
	require.NoError(t, err)

	// Straight to SQL: the API refuses this key, and the case being tested is
	// a row an older or newer build already wrote.
	_, err = db.Pool.Exec(ctx,
		`INSERT INTO dashboard_gadgets (dashboard_id, gadget_key, position, col_span, config)
		 VALUES ($1, 'burndown', 0, 2, '{"velocity":3}')`, d.ID)
	require.NoError(t, err, "the schema must admit a key this build does not know")

	got, err := store.ListGadgets(ctx, d.ID)
	require.NoError(t, err, "a dashboard holding an unknown gadget must still load")
	require.Len(t, got, 1)
	require.Equal(t, "burndown", got[0].Key, "the key is carried through verbatim so the tile can be labelled")
	require.Equal(t, dashboards.Config{}, got[0].Config,
		"a config this build cannot interpret degrades to the zero value rather than failing the read")
}

// ── Starter seeding ─────────────────────────────────────────────────────────

func TestDashboardStore_CreateStarterIsIdempotentByConstraint(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewDashboardAdapter(db.Pool)

	starter := dashboards.Dashboard{
		OrgID: org.ID, OwnerID: user.ID, Name: dashboards.StarterName,
		Module: dashboards.ModuleHome, IsDefault: true, IsSeeded: true,
		Visibility: views.VisibilityPrivate,
	}

	created, err := store.CreateStarter(ctx, starter, dashboards.StarterLayout())
	require.NoError(t, err)
	require.True(t, created)

	// The second call is the concurrent-tab case: ON CONFLICT DO NOTHING, so
	// no second dashboard and no second set of gadgets.
	created, err = store.CreateStarter(ctx, starter, dashboards.StarterLayout())
	require.NoError(t, err)
	require.False(t, created, "seeding twice must produce nothing, not a second Home dashboard")

	rows, err := store.ListForViewer(ctx, org.ID, user.ID, nil, string(dashboards.ModuleHome))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].IsSeeded)

	gadgets, err := store.ListGadgets(ctx, rows[0].ID)
	require.NoError(t, err)
	require.Len(t, gadgets, 3, "and no duplicate gadgets from the second attempt")
	require.NotEmpty(t, gadgets[2].Config.Body, "the getting-started note ships with its text")
}
