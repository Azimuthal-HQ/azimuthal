package views

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Aggregate's own logic — the module fan-out, the cross-module merge, the
// bucket cap and the two refusals — against a fake store. The SQL half is
// covered against real PostgreSQL in
// internal/db/adapters/saved_view_aggregates_integration_test.go, which is
// where the access arrays actually live.

type fakeAggregates struct {
	ticketCount int64
	itemCount   int64
	ticketRows  []Bucket
	itemRows    []Bucket

	// What the fan-outs were actually called with, so a test can assert that
	// the access arrays and the group field reached the store unchanged.
	ticketParams *FanoutParams
	itemParams   *FanoutParams
}

func (f *fakeAggregates) CountTickets(_ context.Context, p FanoutParams) (int64, error) {
	f.ticketParams = &p
	return f.ticketCount, nil
}

func (f *fakeAggregates) CountProjectItems(_ context.Context, p FanoutParams) (int64, error) {
	f.itemParams = &p
	return f.itemCount, nil
}

func (f *fakeAggregates) BreakdownTickets(_ context.Context, p FanoutParams) ([]Bucket, error) {
	f.ticketParams = &p
	return f.ticketRows, nil
}

func (f *fakeAggregates) BreakdownProjectItems(_ context.Context, p FanoutParams) ([]Bucket, error) {
	f.itemParams = &p
	return f.itemRows, nil
}

func bothModules(t *testing.T) Query {
	t.Helper()
	q, err := ParseQuery([]byte(`{"v":1,"filter":{"modules":["beacon","vector"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)
	return q
}

func vectorOnly(t *testing.T) Query {
	t.Helper()
	q, err := ParseQuery([]byte(`{"v":1,"filter":{"modules":["vector"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)
	return q
}

func reader() Viewer {
	return Viewer{UserID: uuid.New(), ReadableSpaceIDs: []uuid.UUID{uuid.New()}}
}

func TestAggregate_TotalsBothModules(t *testing.T) {
	store := &fakeAggregates{ticketCount: 3, itemCount: 4}

	got, err := Aggregate(context.Background(), store, bothModules(t), reader(), GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(7), got.Total, "a cross-module count is the sum of the two halves")
	require.Empty(t, got.Buckets, "no group field means no buckets, not an empty breakdown")
	require.False(t, got.Truncated)
}

// A viewer with no readable spaces and no shares must be answered with zero
// WITHOUT a round trip. The short-circuit is not what makes the endpoint safe
// — the arrays are — but a store call here would mean the empty-access case
// depends on `= ANY('{}')` behaving, which is trivia rather than intent.
//
// Fails-before: delete canReachModule from the two conditions and ticketParams
// stops being nil.
func TestAggregate_NoAccessNeverReachesTheStore(t *testing.T) {
	store := &fakeAggregates{ticketCount: 99, itemCount: 99}

	got, err := Aggregate(context.Background(), store, bothModules(t), Viewer{UserID: uuid.New()}, GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(0), got.Total)
	require.Nil(t, store.ticketParams, "the beacon fan-out must not run for a viewer who can read nothing")
	require.Nil(t, store.itemParams, "the vector fan-out must not run for a viewer who can read nothing")
}

// A viewer who can read no space but holds a direct share still has to be
// asked — that share is exactly what the ADR-0008 exception exists for, and it
// is per-module.
func TestAggregate_AShareAloneStillReachesItsOwnModule(t *testing.T) {
	store := &fakeAggregates{ticketCount: 1}
	v := Viewer{UserID: uuid.New(), SharedTicketIDs: []uuid.UUID{uuid.New()}}

	got, err := Aggregate(context.Background(), store, bothModules(t), v, GroupNone)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Total)
	require.NotNil(t, store.ticketParams, "a shared ticket must still be counted")
	require.Nil(t, store.itemParams, "a shared TICKET says nothing about the item fan-out")
}

func TestAggregate_MergesBucketsAcrossModules(t *testing.T) {
	store := &fakeAggregates{
		ticketCount: 5, itemCount: 4,
		ticketRows: []Bucket{{Key: "open", Label: "open", Count: 3}, {Key: "done", Label: "done", Count: 2}},
		itemRows:   []Bucket{{Key: "open", Label: "open", Count: 4}},
	}

	got, err := Aggregate(context.Background(), store, bothModules(t), reader(), GroupStatus)
	require.NoError(t, err)
	require.Equal(t, int64(9), got.Total)
	require.Len(t, got.Buckets, 2)
	require.Equal(t, "open", got.Buckets[0].Key)
	require.Equal(t, int64(7), got.Buckets[0].Count, "a status present in both modules is summed, not listed twice")
	require.Equal(t, int64(2), got.Buckets[1].Count, "buckets order by count descending")

	var sum int64
	for _, b := range got.Buckets {
		sum += b.Count
	}
	require.Equal(t, got.Total, sum, "the buckets must account for every counted row")
}

// A label supplied by only one module still reaches the merged bucket: an
// assignee id is the key, and only whichever half joined the user row carries
// the name.
func TestAggregate_MergeKeepsTheFirstNonEmptyLabel(t *testing.T) {
	store := &fakeAggregates{
		ticketRows: []Bucket{{Key: "u1", Label: "", Count: 1}},
		itemRows:   []Bucket{{Key: "u1", Label: "Ada", Count: 1}},
	}

	got, err := Aggregate(context.Background(), store, bothModules(t), reader(), GroupAssignee)
	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	require.Equal(t, "Ada", got.Buckets[0].Label)
}

// Nothing is dropped past the cap. The rollup bucket is explicit, counts the
// rows it absorbed, and says how many buckets it stands for — so the tile can
// label it rather than showing a silent truncation.
//
// Fails-before: change capBuckets to `return all[:MaxBuckets], true` and the
// sum assertion fails by exactly the rolled-up count.
func TestAggregate_CapsBucketsWithoutLosingCounts(t *testing.T) {
	rows := make([]Bucket, 0, MaxBuckets+5)
	var want int64
	for i := range MaxBuckets + 5 {
		// Descending counts so the ordering is unambiguous and the last five
		// are the ones rolled up.
		n := int64(MaxBuckets + 5 - i)
		rows = append(rows, Bucket{Key: string(rune('a'+i%26)) + string(rune('0'+i/26)), Count: n})
		want += n
	}
	store := &fakeAggregates{ticketCount: want, ticketRows: rows}

	q, err := ParseQuery([]byte(`{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)

	got, err := Aggregate(context.Background(), store, q, reader(), GroupStatus)
	require.NoError(t, err)
	require.True(t, got.Truncated)
	require.Len(t, got.Buckets, MaxBuckets+1, "the cap plus one rollup bucket")

	last := got.Buckets[len(got.Buckets)-1]
	require.True(t, last.Other)
	require.Equal(t, 5, last.OtherBuckets)

	var sum int64
	for _, b := range got.Buckets {
		sum += b.Count
	}
	require.Equal(t, want, sum, "a capped breakdown must still sum to the total")
}

// The Vector-only field rule, in both directions. It is the same refusal the
// filter vocabulary makes about `kinds`, for the same reason: a breakdown that
// reported every ticket as untyped is a defect its author cannot see.
func TestAggregate_KindIsRefusedAlongsideBeacon(t *testing.T) {
	store := &fakeAggregates{}

	_, err := Aggregate(context.Background(), store, bothModules(t), reader(), GroupKind)
	require.ErrorIs(t, err, ErrGroupFieldModule)
	require.Nil(t, store.itemParams, "the refusal happens before any fan-out runs")

	_, err = Aggregate(context.Background(), store, vectorOnly(t), reader(), GroupKind)
	require.NoError(t, err, "a Vector-only view may be grouped by kind")
}

func TestParseGroupField(t *testing.T) {
	for _, s := range []string{"status", "priority", "assignee", "kind"} {
		got, err := ParseGroupField(s)
		require.NoError(t, err)
		require.Equal(t, GroupField(s), got)
	}

	got, err := ParseGroupField("")
	require.NoError(t, err)
	require.Equal(t, GroupNone, got, "no group field is a legal request meaning count only")

	for _, s := range []string{"space", "text", "assignee_id", "STATUS", "labels"} {
		_, err := ParseGroupField(s)
		require.ErrorIs(t, err, ErrUnknownGroupField,
			"%q is not in the vocabulary and must be refused rather than silently grouped as one bucket", s)
	}
}

// The group field must reach the store: the SQL selects its bucket expression
// from it, so a lost value would collapse every row into the ELSE ” bucket.
func TestAggregate_PassesTheGroupFieldToTheFanout(t *testing.T) {
	store := &fakeAggregates{}

	_, err := Aggregate(context.Background(), store, vectorOnly(t), reader(), GroupPriority)
	require.NoError(t, err)
	require.NotNil(t, store.itemParams)
	require.Equal(t, "priority", store.itemParams.GroupBy)
}

// An aggregate has no page. Carrying a limit or a cursor into a COUNT would be
// harmless in the SQL as written and misleading in the parameters, which is
// how the next person adds a LIMIT to a count.
func TestAggregate_CarriesNoPage(t *testing.T) {
	store := &fakeAggregates{}

	_, err := Aggregate(context.Background(), store, vectorOnly(t), reader(), GroupNone)
	require.NoError(t, err)
	require.NotNil(t, store.itemParams)
	require.Zero(t, store.itemParams.Limit)
	require.Empty(t, store.itemParams.CursorKey)
	require.Equal(t, uuid.Nil, store.itemParams.CursorID)
}

// The `me` token resolves against the CALLING user here exactly as it does on
// the results path, which is what makes a shared count gadget mean each
// viewer's own work.
func TestAggregate_ResolvesTheMeTokenPerViewer(t *testing.T) {
	store := &fakeAggregates{}
	q, err := ParseQuery([]byte(
		`{"v":1,"filter":{"modules":["vector"],"assignees":["me"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)

	me := uuid.New()
	_, err = Aggregate(context.Background(), store, q,
		Viewer{UserID: me, ReadableSpaceIDs: []uuid.UUID{uuid.New()}}, GroupNone)
	require.NoError(t, err)
	require.NotNil(t, store.itemParams)
	require.True(t, store.itemParams.FilterAssignee)
	require.Equal(t, []uuid.UUID{me}, store.itemParams.AssigneeIDs,
		"`me` must become the caller's id, never the view author's")
}

func TestAggregate_RefusesAnInvalidQuery(t *testing.T) {
	store := &fakeAggregates{}

	_, err := Aggregate(context.Background(), store, Query{V: 1}, reader(), GroupNone)
	require.Error(t, err, "a document with no module names no table and must not be run")
	require.Nil(t, store.ticketParams)
}
