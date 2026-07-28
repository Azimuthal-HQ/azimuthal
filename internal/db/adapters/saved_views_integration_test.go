package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// These tests drive the two cross-space result fan-outs against a real
// database. They are the layer where the ADR-0008 exception actually lives:
// the readable-space set and the shared-entity set are the access control, and
// the queries either honour them or they leak.
//
// Every one of them is written so that WIDENING the access arrays makes it
// fail. That is the negative-test question from spec §2 — a test that passes
// with the filter deleted asserts nothing.

func insertTicket(t *testing.T, pool *pgxpool.Pool, spaceID, reporter uuid.UUID, number int32, title, status, priority string, assignee *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO tickets (id, space_id, number, title, reporter_id, status, priority, assignee_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, spaceID, number, title, reporter, status, priority, assignee)
	require.NoError(t, err)
	return id
}

func insertItem(t *testing.T, pool *pgxpool.Pool, orgID, spaceID, reporter uuid.UUID, number int32, title, status, priority, kind string, assignee *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO project_items (id, org_id, space_id, number, kind, title, reporter_id, item_key, status, priority, assignee_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, orgID, spaceID, number, kind, title, reporter, "ITM-"+uuid.NewString()[:8], status, priority, assignee)
	require.NoError(t, err)
	return id
}

func beaconQuery(t *testing.T, body string) views.Query {
	t.Helper()
	q, err := views.ParseQuery([]byte(`{"v":1,"sort":{"field":"updated_at","dir":"desc"},"filter":{` + body + `}}`))
	require.NoError(t, err)
	return q
}

func resultIDs(p views.Page) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	for _, r := range p.Results {
		out[r.ID] = true
	}
	return out
}

// TestViewResults_TicketInUnreadableSpaceDoesNotLeak is the A1-typeahead mold
// applied to saved views: a ticket whose title matches the view's text term,
// sitting in a space the viewer cannot read, must not appear — and the same
// view run by someone who CAN read that space must show it, so the test
// distinguishes "filtered correctly" from "returned nothing at all".
//
// Fails-before: add the hidden space to ReadableSpaceIDs and the first
// assertion fails. That is the mutation this test exists to catch.
func TestViewResults_TicketInUnreadableSpaceDoesNotLeak(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	open := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	hidden := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	testutil.SetSpaceVisibility(t, db.Pool, hidden.ID, "hidden")

	visible := insertTicket(t, db.Pool, open.ID, user.ID, 1, "payment gateway timeout", "open", "high", nil)
	secret := insertTicket(t, db.Pool, hidden.ID, user.ID, 1, "payment gateway credentials", "open", "high", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	q := beaconQuery(t, `"modules":["beacon"],"text":"payment gateway"`)

	outsider := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{open.ID}}
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, outsider, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.True(t, got[visible], "the readable-space ticket must be returned")
	require.False(t, got[secret], "a ticket in a space the viewer cannot read must never appear in view results")

	insider := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{open.ID, hidden.ID}}
	page, err = views.Resolve(ctx, orgStore(a, org.ID), q, insider, "", 50)
	require.NoError(t, err)
	require.True(t, resultIDs(page)[secret],
		"the same view must show the ticket to a viewer who can read its space, or the first half proves nothing")
}

// TestViewResults_ShareUnionReachesAnUnreadableSpace is the other half of the
// ADR-0008 exception, and the DoD line for P4: an entity reachable ONLY
// through a share appears for its recipient, from a space they cannot read.
//
// Fails-before: drop SharedTicketIDs and the ticket disappears.
func TestViewResults_ShareUnionReachesAnUnreadableSpace(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	closed := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	testutil.SetSpaceVisibility(t, db.Pool, closed.ID, "hidden")
	shared := insertTicket(t, db.Pool, closed.ID, user.ID, 1, "shared incident", "open", "urgent", nil)
	alsoThere := insertTicket(t, db.Pool, closed.ID, user.ID, 2, "unshared incident", "open", "urgent", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	q := beaconQuery(t, `"modules":["beacon"]`)

	// No readable spaces at all — the only route to this row is the share.
	recipient := views.Viewer{UserID: user.ID, SharedTicketIDs: []uuid.UUID{shared}}
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, recipient, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.True(t, got[shared], "a directly shared ticket must appear even with no space access")
	require.False(t, got[alsoThere], "the share must reach exactly one entity, not its whole space")
}

// TestViewResults_MeTokenResolvesPerViewer is the divergence test the phase
// brief asks for by name: ONE shared view, two viewers, different results.
//
// Fails-before: resolve `me` to the view's owner (or to any fixed id) instead
// of to the caller and one of the two assertions fails.
func TestViewResults_MeTokenResolvesPerViewer(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	alice := testutil.CreateTestUser(t, db.Pool, org.ID)
	bob := testutil.CreateTestUser(t, db.Pool, org.ID)

	space := testutil.CreateTestSpace(t, db.Pool, org.ID, alice.ID, "beacon")
	aliceTicket := insertTicket(t, db.Pool, space.ID, alice.ID, 1, "alice work", "open", "medium", &alice.ID)
	bobTicket := insertTicket(t, db.Pool, space.ID, alice.ID, 2, "bob work", "open", "medium", &bob.ID)
	nobodys := insertTicket(t, db.Pool, space.ID, alice.ID, 3, "nobody work", "open", "medium", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	// One query document, stored once, read by both.
	q := beaconQuery(t, `"modules":["beacon"],"assignees":["me"]`)

	forAlice, err := views.Resolve(ctx, orgStore(a, org.ID), q,
		views.Viewer{UserID: alice.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}, "", 50)
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]bool{aliceTicket: true}, resultIDs(forAlice),
		"the same saved view must mean 'assigned to Alice' for Alice")

	forBob, err := views.Resolve(ctx, orgStore(a, org.ID), q,
		views.Viewer{UserID: bob.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}, "", 50)
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]bool{bobTicket: true}, resultIDs(forBob),
		"and 'assigned to Bob' for Bob, from the same stored document")

	require.NotContains(t, resultIDs(forAlice), nobodys)
}

// TestViewResults_UnassignedToken covers the other queue-shaped token, which
// is a NULL test rather than an equality test and so takes its own branch in
// the SQL.
func TestViewResults_UnassignedToken(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	assigned := insertTicket(t, db.Pool, space.ID, user.ID, 1, "taken", "open", "low", &user.ID)
	free := insertTicket(t, db.Pool, space.ID, user.ID, 2, "free", "open", "low", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	q := beaconQuery(t, `"modules":["beacon"],"assignees":["unassigned"]`)
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q,
		views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.True(t, got[free])
	require.False(t, got[assigned])
}

// TestViewResults_TextTermWildcardsAreLiteral pins the same property S11 pins
// for the ticket typeahead, on the third ILIKE site in the product. The term
// is escaped by access.EscapeLike rather than by a third copy of the in-SQL
// replace() idiom.
//
// Fails-before: drop the EscapeLike call in buildParams and "100%" matches
// "100 percent complete" too, because `%` becomes a wildcard.
func TestViewResults_TextTermWildcardsAreLiteral(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	literal := insertTicket(t, db.Pool, space.ID, user.ID, 1, "rollout is 100% done", "open", "low", nil)
	decoy := insertTicket(t, db.Pool, space.ID, user.ID, 2, "rollout is 100 percent done", "open", "low", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	q := beaconQuery(t, `"modules":["beacon"],"text":"100%"`)
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q,
		views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.True(t, got[literal], "the literal percent sign must match itself")
	require.False(t, got[decoy], "a percent sign in the caller's term must not act as a wildcard")
}

// TestViewResults_CrossModuleMergeAndCursor is the paging contract: a view
// spanning both modules pages through every row exactly once, in one order,
// with no gap and no repeat across the module boundary.
//
// This is the test that would catch the collation trap. If the SQL ordered by
// database collation while the Go merge compared bytewise, the two halves
// would interleave differently and a row would be skipped or duplicated at a
// page boundary.
func TestViewResults_CrossModuleMergeAndCursor(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	beaconSpace := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	vectorSpace := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")

	want := map[uuid.UUID]bool{}
	// Interleave titles so a title sort genuinely mixes the two modules
	// rather than concatenating them.
	for i := int32(1); i <= 6; i++ {
		want[insertTicket(t, db.Pool, beaconSpace.ID, user.ID, i, string(rune('a'+2*i))+" ticket", "open", "low", nil)] = true
		want[insertItem(t, db.Pool, org.ID, vectorSpace.ID, user.ID, i, string(rune('b'+2*i))+" item", "open", "low", "task", nil)] = true
	}

	a := adapters.NewSavedViewAdapter(db.Pool)
	q, err := views.ParseQuery([]byte(`{"v":1,"sort":{"field":"title","dir":"asc"},"filter":{"modules":["beacon","vector"]}}`))
	require.NoError(t, err)
	viewer := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{beaconSpace.ID, vectorSpace.ID}}

	seen := map[uuid.UUID]bool{}
	var order []string
	cursor := ""
	for page := 0; page < 20; page++ {
		p, err := views.Resolve(ctx, orgStore(a, org.ID), q, viewer, cursor, 5)
		require.NoError(t, err)
		for _, r := range p.Results {
			require.False(t, seen[r.ID], "row %s returned twice across pages", r.Key)
			seen[r.ID] = true
			order = append(order, r.SortKey)
		}
		if !p.HasMore {
			break
		}
		cursor = p.NextCursor
		require.NotEmpty(t, cursor, "HasMore must come with a cursor")
	}
	require.Equal(t, want, seen, "paging must visit every row exactly once")

	// And the merged order must be globally sorted, not per-module sorted.
	for i := 1; i < len(order); i++ {
		require.LessOrEqual(t, order[i-1], order[i],
			"merged results must be in one global order across both modules")
	}
}

// TestViewResults_VectorOnlyFieldsFilter proves kind and sprint filtering
// reach project_items. They are Vector-only because tickets have neither
// column — verified against the database, and enforced at write time.
func TestViewResults_VectorOnlyFieldsFilter(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")

	bug := insertItem(t, db.Pool, org.ID, space.ID, user.ID, 1, "a bug", "open", "high", "bug", nil)
	task := insertItem(t, db.Pool, org.ID, space.ID, user.ID, 2, "a task", "open", "high", "task", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	q, err := views.ParseQuery([]byte(`{"v":1,"sort":{"field":"updated_at","dir":"desc"},"filter":{"modules":["vector"],"kinds":["bug"]}}`))
	require.NoError(t, err)
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q,
		views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}, "", 50)
	require.NoError(t, err)
	got := resultIDs(page)
	require.True(t, got[bug])
	require.False(t, got[task])
}

// TestViewResults_NoAccessReturnsNothingWithoutQuerying covers the
// short-circuit. A caller with no readable space and no share is answered
// with an empty page.
func TestViewResults_NoAccessReturnsNothing(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	insertTicket(t, db.Pool, space.ID, user.ID, 1, "invisible", "open", "low", nil)

	a := adapters.NewSavedViewAdapter(db.Pool)
	q := beaconQuery(t, `"modules":["beacon"]`)
	page, err := views.Resolve(ctx, orgStore(a, org.ID), q, views.Viewer{UserID: user.ID}, "", 50)
	require.NoError(t, err)
	require.Empty(t, page.Results)
	require.False(t, page.HasMore)
}

// TestSavedViewStore_RoundTripAndTeamDegradation covers the row lifecycle and
// the ADR-0009 case-C1 degradation the migration is shaped for: deleting the
// audience team must NOT delete the view and must NOT error — it leaves a
// view whose team is gone, which the service reports as invalid.
//
// Fails-before: change the FK to ON DELETE CASCADE (the spec sketch's version)
// and the Get after the team delete returns ErrNotFound instead of a row.
func TestSavedViewStore_RoundTripAndTeamDegradation(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)

	// teams.path is the migration-022 materialized path (UUID[]), NOT NULL and
	// with no default; a root team's path is just itself.
	teamID := uuid.New()
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO teams (id, org_id, slug, name, path) VALUES ($1,$2,$3,$4,ARRAY[$1]::uuid[])`,
		teamID, org.ID, "squad-"+uuid.NewString()[:8], "Squad")
	require.NoError(t, err)

	a := adapters.NewSavedViewAdapter(db.Pool)
	q := beaconQuery(t, `"modules":["beacon"],"assignees":["me"]`)
	created, err := a.Create(ctx, views.View{
		OrgID: org.ID, OwnerID: user.ID, Name: "Squad work",
		Query: q, Visibility: views.VisibilityTeam, VisibilityTeamID: &teamID,
	})
	require.NoError(t, err)
	require.Equal(t, views.VisibilityTeam, created.Visibility)
	require.Equal(t, teamID, *created.VisibilityTeamID)
	// The stored document must come back as the same document, `me` intact.
	require.Equal(t, []string{"me"}, created.Query.Filter.Assignees)

	_, err = db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, teamID)
	require.NoError(t, err, "deleting a team must not be blocked by a view that references it")

	after, err := a.Get(ctx, org.ID, created.ID)
	require.NoError(t, err, "the view must survive its audience team being deleted (ADR-0009 case C1)")
	require.Nil(t, after.VisibilityTeamID, "the team reference is nulled, not cascaded")
	require.Equal(t, views.VisibilityTeam, after.Visibility,
		"visibility stays 'team' so the owner is told to re-scope rather than silently losing the share")
}

// orgStore binds an org id onto the adapter for the package-level Resolve,
// which is org-agnostic so it can be unit-tested against a fake.
func orgStore(a *adapters.SavedViewAdapter, orgID uuid.UUID) views.ResultStore {
	return orgBound{a: a, orgID: orgID}
}

type orgBound struct {
	a     *adapters.SavedViewAdapter
	orgID uuid.UUID
}

func (o orgBound) ListTickets(ctx context.Context, p views.FanoutParams) ([]views.Result, error) {
	p.OrgID = o.orgID
	return o.a.ListTickets(ctx, p)
}

func (o orgBound) ListProjectItems(ctx context.Context, p views.FanoutParams) ([]views.Result, error) {
	p.OrgID = o.orgID
	return o.a.ListProjectItems(ctx, p)
}
