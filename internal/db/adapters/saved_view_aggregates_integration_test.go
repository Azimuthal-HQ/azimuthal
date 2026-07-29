package adapters_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The grouped fan-outs against a real database. This is the layer where the
// ADR-0008 exception lives for aggregates exactly as it does for results: the
// readable-space set and the shared-entity set ARE the access control, and the
// COUNT either honours them or it leaks a number about work the caller cannot
// see.
//
// Every test here is written so that WIDENING the access arrays makes it fail.

func bucketMap(bs []views.Bucket) map[string]int64 {
	out := map[string]int64{}
	for _, b := range bs {
		out[b.Key] = b.Count
	}
	return out
}

// A count is a read. A ticket in a space the viewer cannot read must not be
// counted — and the same query run by somebody who CAN read that space must
// count it, so the test distinguishes "filtered correctly" from "counted
// nothing at all".
//
// Fails-before: add the hidden space to ReadableSpaceIDs in the first call and
// the total becomes 2.
func TestViewAggregate_CountsNothingFromAnUnreadableSpace(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)

	open := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	hidden := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	testutil.SetSpaceVisibility(t, db.Pool, hidden.ID, "hidden")

	insertTicket(t, db.Pool, open.ID, user.ID, 1, "Visible one", "open", "high", nil)
	insertTicket(t, db.Pool, hidden.ID, user.ID, 2, "Hidden one", "open", "high", nil)

	q := beaconQuery(t, `"modules":["beacon"]`)

	narrow := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{open.ID}}
	got, err := views.Aggregate(ctx, orgScoped{store, org.ID}, q, narrow, views.GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Total,
		"a ticket in an unreadable space must not reach the count")

	wide := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{open.ID, hidden.ID}}
	got, err = views.Aggregate(ctx, orgScoped{store, org.ID}, q, wide, views.GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Total,
		"the same query counts it for somebody who can read that space — so the first assertion is a filter, not an empty result")
}

// The same for a breakdown: an unreadable row must not swell any bucket, and
// a status that exists ONLY in an unreadable space must not appear at all.
// A bucket key is itself a disclosure — "there is work in a state called
// embargoed" is something the viewer should not learn.
func TestViewAggregate_BreakdownLeaksNoBucketFromAnUnreadableSpace(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)

	open := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	hidden := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	testutil.SetSpaceVisibility(t, db.Pool, hidden.ID, "hidden")

	insertTicket(t, db.Pool, open.ID, user.ID, 1, "A", "open", "high", nil)
	insertTicket(t, db.Pool, open.ID, user.ID, 2, "B", "open", "low", nil)
	insertTicket(t, db.Pool, hidden.ID, user.ID, 3, "C", "embargoed", "high", nil)

	q := beaconQuery(t, `"modules":["beacon"]`)
	narrow := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{open.ID}}

	got, err := views.Aggregate(ctx, orgScoped{store, org.ID}, q, narrow, views.GroupStatus)
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Total)
	buckets := bucketMap(got.Buckets)
	require.Equal(t, int64(2), buckets["open"])
	require.NotContains(t, buckets, "embargoed",
		"a status that exists only in an unreadable space must not appear as a bucket — the key is itself a disclosure")
}

// The ADR-0008 exception, taken deliberately: a directly shared ticket in an
// otherwise unreadable space IS counted, because a saved view unions the
// caller's shares. Without this the number on a count gadget would disagree
// with the rows the same view lists.
func TestViewAggregate_CountsADirectlySharedTicketFromAnUnreadableSpace(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)

	hidden := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	testutil.SetSpaceVisibility(t, db.Pool, hidden.ID, "hidden")
	shared := insertTicket(t, db.Pool, hidden.ID, user.ID, 1, "Shared", "open", "high", nil)
	insertTicket(t, db.Pool, hidden.ID, user.ID, 2, "Not shared", "open", "high", nil)

	q := beaconQuery(t, `"modules":["beacon"]`)
	v := views.Viewer{UserID: user.ID, SharedTicketIDs: []uuid.UUID{shared}}

	got, err := views.Aggregate(ctx, orgScoped{store, org.ID}, q, v, views.GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Total,
		"exactly the shared entity: the share widens the count by one row, not by a space")
}

// A count is not bounded by a page. This is the whole reason the endpoint
// exists — fetching pages and counting them in the client would stop at
// MaxPageSize and under-report precisely the busy view somebody puts a count
// on.
//
// Fails-before: implement the count as len(Resolve(...).Results) and this
// returns 200 instead of 205.
func TestViewAggregate_CountIsNotBoundedByThePageSize(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	const n = views.MaxPageSize + 5
	for i := range n {
		insertTicket(t, db.Pool, space.ID, user.ID, int32(i+1), "Bulk", "open", "medium", nil)
	}

	v := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}
	got, err := views.Aggregate(ctx, orgScoped{store, org.ID}, beaconQuery(t, `"modules":["beacon"]`), v, views.GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(n), got.Total)
}

// Cross-module: the total is the sum of both halves and the buckets merge by
// key, which is ADR-0009's "fan out per module, merge in the API layer" for
// aggregates.
func TestViewAggregate_MergesBothModules(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)

	beacon := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	vector := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")

	insertTicket(t, db.Pool, beacon.ID, user.ID, 1, "T1", "open", "high", nil)
	insertTicket(t, db.Pool, beacon.ID, user.ID, 2, "T2", "done", "low", nil)
	insertItem(t, db.Pool, org.ID, vector.ID, user.ID, 1, "I1", "open", "high", "bug", nil)

	q := beaconQuery(t, `"modules":["beacon","vector"]`)
	v := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{beacon.ID, vector.ID}}

	got, err := views.Aggregate(ctx, orgScoped{store, org.ID}, q, v, views.GroupStatus)
	require.NoError(t, err)
	require.Equal(t, int64(3), got.Total)
	buckets := bucketMap(got.Buckets)
	require.Equal(t, int64(2), buckets["open"], "a status present in both tables is one bucket, summed")
	require.Equal(t, int64(1), buckets["done"])
}

// Unassigned work is a real bucket. Collapsing it away would make the buckets
// stop summing to the total, and "how much has nobody on it" is one of the
// questions a breakdown is for.
func TestViewAggregate_BreakdownByAssigneeKeepsTheUnassignedBucket(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	insertTicket(t, db.Pool, space.ID, user.ID, 1, "Mine", "open", "high", &user.ID)
	insertTicket(t, db.Pool, space.ID, user.ID, 2, "Nobody's", "open", "high", nil)

	v := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}
	got, err := views.Aggregate(ctx, orgScoped{store, org.ID},
		beaconQuery(t, `"modules":["beacon"]`), v, views.GroupAssignee)
	require.NoError(t, err)

	buckets := bucketMap(got.Buckets)
	require.Equal(t, int64(1), buckets[user.ID.String()])
	require.Equal(t, int64(1), buckets[""], "unassigned is a bucket, not a row to drop")

	var sum int64
	for _, b := range got.Buckets {
		sum += b.Count
	}
	require.Equal(t, got.Total, sum)

	for _, b := range got.Buckets {
		if b.Key == user.ID.String() {
			require.Equal(t, "Test User", b.Label,
				"the assignee's name is joined in the fan-out, never looked up per bucket")
		}
	}
}

func TestViewAggregate_BreakdownByPriorityAndKind(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")

	insertItem(t, db.Pool, org.ID, space.ID, user.ID, 1, "A", "open", "high", "bug", nil)
	insertItem(t, db.Pool, org.ID, space.ID, user.ID, 2, "B", "open", "high", "story", nil)
	insertItem(t, db.Pool, org.ID, space.ID, user.ID, 3, "C", "open", "low", "bug", nil)

	q := beaconQuery(t, `"modules":["vector"]`)
	v := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}

	byPriority, err := views.Aggregate(ctx, orgScoped{store, org.ID}, q, v, views.GroupPriority)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"high": 2, "low": 1}, bucketMap(byPriority.Buckets))

	byKind, err := views.Aggregate(ctx, orgScoped{store, org.ID}, q, v, views.GroupKind)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"bug": 2, "story": 1}, bucketMap(byKind.Buckets))
}

// The filter is applied to the aggregate exactly as it is to the results — the
// two queries share their WHERE clause, and a count that ignored the filter
// would be a number nobody can reconcile with the list beneath it.
func TestViewAggregate_HonoursTheFilterDocument(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	insertTicket(t, db.Pool, space.ID, user.ID, 1, "Open one", "open", "high", nil)
	insertTicket(t, db.Pool, space.ID, user.ID, 2, "Closed one", "closed", "high", nil)
	insertTicket(t, db.Pool, space.ID, user.ID, 3, "Mine", "open", "high", &user.ID)

	v := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}

	byStatus, err := views.Aggregate(ctx, orgScoped{store, org.ID},
		beaconQuery(t, `"modules":["beacon"],"statuses":["open"]`), v, views.GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(2), byStatus.Total)

	// The `me` token resolves to the CALLER, which is what makes a shared
	// count gadget mean each viewer's own work.
	mine, err := views.Aggregate(ctx, orgScoped{store, org.ID},
		beaconQuery(t, `"modules":["beacon"],"assignees":["me"]`), v, views.GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(1), mine.Total)

	other := views.Viewer{UserID: uuid.New(), ReadableSpaceIDs: []uuid.UUID{space.ID}}
	theirs, err := views.Aggregate(ctx, orgScoped{store, org.ID},
		beaconQuery(t, `"modules":["beacon"],"assignees":["me"]`), other, views.GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(0), theirs.Total,
		"the same document counts differently for a different viewer — that is the design")
}

// A soft-deleted ticket is gone from a count as it is from a list. A count
// that included them would drift from the rows beneath it by exactly the
// number of things somebody had cleaned up.
func TestViewAggregate_IgnoresSoftDeletedRows(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	insertTicket(t, db.Pool, space.ID, user.ID, 1, "Alive", "open", "high", nil)
	dead := insertTicket(t, db.Pool, space.ID, user.ID, 2, "Deleted", "open", "high", nil)
	_, err := db.Pool.Exec(ctx, `UPDATE tickets SET deleted_at = now() WHERE id = $1`, dead)
	require.NoError(t, err)

	v := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{space.ID}}
	got, err := views.Aggregate(ctx, orgScoped{store, org.ID},
		beaconQuery(t, `"modules":["beacon"]`), v, views.GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Total)
}

// A count is org-scoped like every other read. A ticket in another
// organisation's space must not be counted even if its id somehow reaches the
// readable array.
func TestViewAggregate_IsScopedToTheOrg(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	other := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	otherUser := testutil.CreateTestUser(t, db.Pool, other.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)

	mine := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	theirs := testutil.CreateTestSpace(t, db.Pool, other.ID, otherUser.ID, "beacon")
	insertTicket(t, db.Pool, mine.ID, user.ID, 1, "Mine", "open", "high", nil)
	insertTicket(t, db.Pool, theirs.ID, otherUser.ID, 1, "Theirs", "open", "high", nil)

	// Deliberately widened past what any resolver would produce.
	v := views.Viewer{UserID: user.ID, ReadableSpaceIDs: []uuid.UUID{mine.ID, theirs.ID}}
	got, err := views.Aggregate(ctx, orgScoped{store, org.ID},
		beaconQuery(t, `"modules":["beacon"]`), v, views.GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Total,
		"the org predicate holds even when the readable array is wrong — defence in depth")
}

// orgScoped stamps the org id onto every fan-out, exactly as
// views.Service does. Tests drive views.Aggregate directly rather than through
// the service so the store contract is what is under test.
type orgScoped struct {
	inner *adapters.SavedViewAdapter
	orgID uuid.UUID
}

func (o orgScoped) CountTickets(ctx context.Context, p views.FanoutParams) (int64, error) {
	p.OrgID = o.orgID
	return o.inner.CountTickets(ctx, p)
}

func (o orgScoped) CountProjectItems(ctx context.Context, p views.FanoutParams) (int64, error) {
	p.OrgID = o.orgID
	return o.inner.CountProjectItems(ctx, p)
}

func (o orgScoped) BreakdownTickets(ctx context.Context, p views.FanoutParams) ([]views.Bucket, error) {
	p.OrgID = o.orgID
	return o.inner.BreakdownTickets(ctx, p)
}

func (o orgScoped) BreakdownProjectItems(ctx context.Context, p views.FanoutParams) ([]views.Bucket, error) {
	p.OrgID = o.orgID
	return o.inner.BreakdownProjectItems(ctx, p)
}

// GetMany is audience-blind by design: the dashboard loader has to tell a
// deleted view from an unreadable one, so the audience is applied in Go. This
// pins the two properties that make that safe — soft-deleted rows and
// space-bound queues never come back at all.
func TestSavedViewStore_GetManyExcludesDeletedRowsAndQueues(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	store := adapters.NewSavedViewAdapter(db.Pool)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")

	live, err := store.Create(ctx, views.View{
		OrgID: org.ID, OwnerID: user.ID, Name: "Live",
		Query: beaconQuery(t, `"modules":["beacon"]`), Visibility: views.VisibilityPrivate,
	})
	require.NoError(t, err)

	gone, err := store.Create(ctx, views.View{
		OrgID: org.ID, OwnerID: user.ID, Name: "Gone",
		Query: beaconQuery(t, `"modules":["beacon"]`), Visibility: views.VisibilityPrivate,
	})
	require.NoError(t, err)
	_, err = store.SoftDelete(ctx, org.ID, gone.ID)
	require.NoError(t, err)

	pos := int32(0)
	queue, err := store.CreateQueue(ctx, views.View{
		OrgID: org.ID, OwnerID: user.ID, SpaceID: &space.ID, Position: &pos,
		Name: "All open", Query: beaconQuery(t, `"modules":["beacon"]`),
		Visibility: views.VisibilitySpace,
	})
	require.NoError(t, err)

	rows, err := store.GetMany(ctx, org.ID, []uuid.UUID{live.ID, gone.ID, queue.ID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, live.ID, rows[0].ID)

	// A queue must never be attachable to a gadget: its audience is enforced
	// by the space-read guard on the route that serves it, and nothing outside
	// that route may widen it.
	for _, r := range rows {
		require.NotEqual(t, queue.ID, r.ID)
	}

	// And it is org-scoped like everything else.
	other := testutil.CreateTestOrg(t, db.Pool)
	rows, err = store.GetMany(ctx, other.ID, []uuid.UUID{live.ID})
	require.NoError(t, err)
	require.Empty(t, rows)
}
