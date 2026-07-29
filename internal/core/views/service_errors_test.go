package views

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Every store call in this package is wrapped before it is returned, and every
// one of those wraps is a branch nothing else reaches: a real database rarely
// fails in a test, and when it does the test fails for that reason instead.
//
// They are worth asserting rather than assuming. A store error that were
// swallowed — returned as a zero value and a nil error — is the failure mode
// that matters here: a saved view whose Get quietly returned an empty View
// would resolve as "no filter", and an empty filter matches everything the
// viewer can read. Each case below therefore asserts that the error SURFACES
// and that the caller's own sentinel is still reachable through it.

var errStore = errors.New("store is down")

// failingStore fails whichever methods the test switches on, and behaves
// otherwise so a test can reach the call it cares about.
type failingStore struct {
	view View
	// Which calls fail.
	failCreate, failGet, failUpdate, failDelete bool
	failList, failLiveSpaces, failTeams         bool
	failGetMany                                 bool
	// deleted reports what SoftDelete should claim it removed.
	deleted int64
}

func (f *failingStore) Create(context.Context, View) (View, error) {
	if f.failCreate {
		return View{}, errStore
	}
	return f.view, nil
}

func (f *failingStore) Get(_ context.Context, _, _ uuid.UUID) (View, error) {
	if f.failGet {
		return View{}, errStore
	}
	return f.view, nil
}

func (f *failingStore) Update(context.Context, View) (View, error) {
	if f.failUpdate {
		return View{}, errStore
	}
	return f.view, nil
}

func (f *failingStore) SoftDelete(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	if f.failDelete {
		return 0, errStore
	}
	return f.deleted, nil
}

func (f *failingStore) ListForViewer(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) ([]View, error) {
	if f.failList {
		return nil, errStore
	}
	return []View{f.view}, nil
}

func (f *failingStore) GetMany(context.Context, uuid.UUID, []uuid.UUID) ([]View, error) {
	if f.failGetMany {
		return nil, errStore
	}
	return []View{f.view}, nil
}

func (f *failingStore) LiveSpaceIDs(context.Context, uuid.UUID, []uuid.UUID) ([]uuid.UUID, error) {
	if f.failLiveSpaces {
		return nil, errStore
	}
	return nil, nil
}

func (f *failingStore) EffectiveTeamIDs(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	if f.failTeams {
		return nil, errStore
	}
	return []uuid.UUID{}, nil
}

type failingResults struct{ failTickets, failItems bool }

func (f failingResults) ListTickets(context.Context, FanoutParams) ([]Result, error) {
	if f.failTickets {
		return nil, errStore
	}
	return nil, nil
}

func (f failingResults) ListProjectItems(context.Context, FanoutParams) ([]Result, error) {
	if f.failItems {
		return nil, errStore
	}
	return nil, nil
}

type failingAggregates struct{ failCount, failBreakdown bool }

func (f failingAggregates) CountTickets(context.Context, FanoutParams) (int64, error) {
	if f.failCount {
		return 0, errStore
	}
	return 0, nil
}

func (f failingAggregates) CountProjectItems(context.Context, FanoutParams) (int64, error) {
	if f.failCount {
		return 0, errStore
	}
	return 0, nil
}

func (f failingAggregates) BreakdownTickets(context.Context, FanoutParams) ([]Bucket, error) {
	if f.failBreakdown {
		return nil, errStore
	}
	return nil, nil
}

func (f failingAggregates) BreakdownProjectItems(context.Context, FanoutParams) ([]Bucket, error) {
	if f.failBreakdown {
		return nil, errStore
	}
	return nil, nil
}

func ownedView(owner uuid.UUID) View {
	q, _ := ParseQuery([]byte(`{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	return View{ID: uuid.New(), OwnerID: owner, Name: "Mine", Query: q, Visibility: VisibilityPrivate}
}

func svcFor(store *failingStore) (*Service, Actor, uuid.UUID) {
	me := uuid.New()
	store.view = ownedView(me)
	return NewService(store, failingResults{}, failingAggregates{}),
		Actor{UserID: me}, uuid.New()
}

func draftFor() Draft {
	q, _ := ParseQuery([]byte(`{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	return Draft{Name: "Mine", Query: q, Visibility: VisibilityPrivate}
}

func TestService_StoreFailuresSurface(t *testing.T) {
	ctx := context.Background()

	t.Run("create", func(t *testing.T) {
		s, a, org := svcFor(&failingStore{failCreate: true})
		_, err := s.Create(ctx, org, a, draftFor())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("get", func(t *testing.T) {
		s, a, org := svcFor(&failingStore{failGet: true})
		_, err := s.Get(ctx, org, uuid.New(), a)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("update loads before it writes", func(t *testing.T) {
		s, a, org := svcFor(&failingStore{failGet: true})
		_, err := s.Update(ctx, org, uuid.New(), a, draftFor())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("update", func(t *testing.T) {
		s, a, org := svcFor(&failingStore{failUpdate: true})
		_, err := s.Update(ctx, org, uuid.New(), a, draftFor())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("delete loads before it writes", func(t *testing.T) {
		s, a, org := svcFor(&failingStore{failGet: true})
		require.ErrorIs(t, s.Delete(ctx, org, uuid.New(), a), errStore)
	})

	t.Run("delete", func(t *testing.T) {
		s, a, org := svcFor(&failingStore{failDelete: true})
		require.ErrorIs(t, s.Delete(ctx, org, uuid.New(), a), errStore)
	})

	t.Run("list", func(t *testing.T) {
		s, a, org := svcFor(&failingStore{failList: true})
		_, err := s.List(ctx, org, a)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("the team expansion", func(t *testing.T) {
		s, _, org := svcFor(&failingStore{failTeams: true})
		_, err := s.ActorFor(ctx, org, uuid.New(), false)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("results", func(t *testing.T) {
		s, a, org := svcFor(&failingStore{failGet: true})
		_, err := s.Results(ctx, org, uuid.New(), a, Viewer{}, "", 0)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("by ids", func(t *testing.T) {
		s, _, org := svcFor(&failingStore{failGetMany: true})
		_, err := s.ByIDs(ctx, org, []uuid.UUID{uuid.New()})
		require.ErrorIs(t, err, errStore)
	})
}

// A view is deleted only if the row was there. SoftDelete reporting zero rows
// means somebody else removed it between the read and the write, and that is
// ErrNotFound rather than a silent success — a UI that showed "deleted" for a
// row it did not delete is lying about what happened.
func TestService_DeleteReportsNotFoundWhenNothingChanged(t *testing.T) {
	s, a, org := svcFor(&failingStore{deleted: 0})
	require.ErrorIs(t, s.Delete(context.Background(), org, uuid.New(), a), ErrNotFound)
}

// The validity check runs over a whole page of views in one query. When that
// query fails the list fails: reporting every view as valid would tell people
// their scope was fine when nobody knows.
func TestService_ValidityCheckFailureFailsTheList(t *testing.T) {
	me := uuid.New()
	q, err := ParseQuery([]byte(
		`{"v":1,"filter":{"modules":["beacon"],"space_ids":["` + uuid.NewString() + `"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)

	store := &failingStore{failLiveSpaces: true}
	store.view = View{ID: uuid.New(), OwnerID: me, Name: "Scoped", Query: q, Visibility: VisibilityPrivate}
	s := NewService(store, failingResults{}, failingAggregates{})

	_, err = s.List(context.Background(), uuid.New(), Actor{UserID: me})
	require.ErrorIs(t, err, errStore)
}

// Each fan-out's error is attributed to its own module, so a failure names the
// half that failed rather than "resolving results".
func TestService_FanoutFailuresNameTheirModule(t *testing.T) {
	ctx := context.Background()
	me := uuid.New()
	v := Viewer{UserID: me, ReadableSpaceIDs: []uuid.UUID{uuid.New()}}
	both, err := ParseQuery([]byte(
		`{"v":1,"filter":{"modules":["beacon","vector"]},"sort":{"field":"updated_at","dir":"desc"}}`))
	require.NoError(t, err)

	store := &failingStore{}
	store.view = ownedView(me)

	beaconDown := NewService(store, failingResults{failTickets: true}, failingAggregates{})
	_, err = beaconDown.Preview(ctx, uuid.New(), both, v, "", 0)
	require.ErrorIs(t, err, errStore)
	require.Contains(t, err.Error(), "beacon")

	vectorDown := NewService(store, failingResults{failItems: true}, failingAggregates{})
	_, err = vectorDown.Preview(ctx, uuid.New(), both, v, "", 0)
	require.ErrorIs(t, err, errStore)
	require.Contains(t, err.Error(), "vector")

	countDown := NewService(store, failingResults{}, failingAggregates{failCount: true})
	_, err = countDown.AggregateQuery(ctx, uuid.New(), both, v, GroupNone)
	require.ErrorIs(t, err, errStore)

	groupDown := NewService(store, failingResults{}, failingAggregates{failBreakdown: true})
	_, err = groupDown.AggregateQuery(ctx, uuid.New(), both, v, GroupStatus)
	require.ErrorIs(t, err, errStore)
}

// A stored assignee that will not parse must FAIL the query rather than widen
// it. The API refuses such a document on the way in, so this is unreachable
// through a request — but a stored document is still data, and the failure
// mode it guards is the worst one available: dropping the term would turn
// "assigned to these three people" into "assigned to anybody".
func TestBuildParams_AnUnparseableStoredAssigneeFailsClosed(t *testing.T) {
	q := Query{
		V:      Version,
		Filter: Filter{Modules: []Module{ModuleBeacon}, Assignees: []string{"not-a-uuid"}},
		Sort:   DefaultSort(),
	}
	_, err := buildParams(q, Viewer{UserID: uuid.New()}, cursorPos{}, DefaultPageSize)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unparseable assignee")
}

// The unassigned token and a literal user id both resolve, and both set the
// filter flag — a filter naming only "unassigned" must not read as "no
// assignee filter at all".
func TestBuildParams_ResolvesEveryAssigneeToken(t *testing.T) {
	other := uuid.New()
	q := Query{
		V: Version,
		Filter: Filter{
			Modules:   []Module{ModuleBeacon},
			Assignees: []string{AssigneeUnassigned, other.String()},
		},
		Sort: DefaultSort(),
	}
	p, err := buildParams(q, Viewer{UserID: uuid.New()}, cursorPos{}, DefaultPageSize)
	require.NoError(t, err)
	require.True(t, p.FilterAssignee)
	require.True(t, p.IncludeUnassigned)
	require.Equal(t, []uuid.UUID{other}, p.AssigneeIDs)
}

// A cursor that is base64 but not one this build issued is refused rather than
// treated as the start of the results — resuming from the beginning would
// silently repeat a page.
func TestDecodeCursor_Refusals(t *testing.T) {
	empty, err := decodeCursor("")
	require.NoError(t, err)
	require.Equal(t, cursorPos{}, empty)

	_, err = decodeCursor("!!!not base64!!!")
	require.ErrorIs(t, err, ErrBadCursor)

	// Base64, but no separator.
	_, err = decodeCursor("aGVsbG8")
	require.ErrorIs(t, err, ErrBadCursor)

	// Separator, but the tail is not a uuid.
	_, err = decodeCursor(base64.RawURLEncoding.EncodeToString([]byte("key\x00nope")))
	require.ErrorIs(t, err, ErrBadCursor)

	// A real one round-trips, including a key that is itself empty — a NULL
	// due_at collapses to "", which is why the split is on the LAST separator.
	id := uuid.New()
	got, err := decodeCursor(encodeCursor(cursorPos{Key: "", ID: id}))
	require.NoError(t, err)
	require.Equal(t, cursorPos{Key: "", ID: id}, got)
}

// The merge orders ascending as well as descending, and the id breaks a tie —
// the two halves arrive independently sorted and must interleave, not
// concatenate.
func TestSortResults_AscendingAndTieBreak(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if a.String() > b.String() {
		a, b = b, a
	}
	rows := []Result{
		{ID: b, SortKey: "2"},
		{ID: a, SortKey: "1"},
		{ID: b, SortKey: "1"},
	}
	sortResults(rows, false)
	require.Equal(t, "1", rows[0].SortKey)
	require.Equal(t, a, rows[0].ID, "the id breaks a tie, in the same direction the SQL does")
	require.Equal(t, "2", rows[2].SortKey)

	sortResults(rows, true)
	require.Equal(t, "2", rows[0].SortKey)
}
