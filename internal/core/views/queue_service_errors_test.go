package views

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// THE QUEUE LIFECYCLE'S REFUSALS AND ITS STORE FAILURES.
//
// The integration suite drives the queue routes against a real database, which
// proves the happy paths and the guards. It cannot reach two things: a store
// that fails mid-operation, and CreateDefaults' branch on a workflow that
// actually has a `done` category. Both are covered here against a fake store.
//
// This is the one legitimate use of a fake in this package and it is not a
// database mock: QueueStore is a domain seam, the real coverage of the SQL
// behind it lives in internal/db/adapters/*_integration_test.go, and what is
// asserted here is the SERVICE's behaviour when that seam misbehaves — which no
// amount of real postgres can produce on demand.

var errQueueStore = errors.New("the queue store gave up")

// fakeQueueStore fails whichever call its flags name and answers plausibly
// otherwise. Every field is one specific failure, so a test names the failure
// it is about rather than constructing a broken object.
type fakeQueueStore struct {
	failList     bool
	failGet      bool
	failCreate   bool
	failUpdate   bool
	failDelete   bool
	failReorder  bool
	failNextPos  bool
	failStatuses bool
	failAbsent   bool

	statuses []WorkflowStatus
	queues   []View
	deleted  int64
	inserted bool

	// createdNames records what CreateQueueIfAbsent was asked to write, in
	// order — the assertion that the default set is what it says it is.
	createdNames []string
	lastQuery    Query
}

func (f *fakeQueueStore) ListQueues(context.Context, uuid.UUID, uuid.UUID) ([]View, error) {
	if f.failList {
		return nil, errQueueStore
	}
	return f.queues, nil
}

func (f *fakeQueueStore) GetQueue(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (View, error) {
	if f.failGet {
		return View{}, errQueueStore
	}
	return View{ID: uuid.New(), Name: "Existing"}, nil
}

func (f *fakeQueueStore) CreateQueue(_ context.Context, v View) (View, error) {
	if f.failCreate {
		return View{}, errQueueStore
	}
	f.lastQuery = v.Query
	return v, nil
}

func (f *fakeQueueStore) CreateQueueIfAbsent(_ context.Context, v View) (bool, error) {
	if f.failAbsent {
		return false, errQueueStore
	}
	f.createdNames = append(f.createdNames, v.Name)
	f.lastQuery = v.Query
	return f.inserted, nil
}

func (f *fakeQueueStore) UpdateQueue(_ context.Context, v View) (View, error) {
	if f.failUpdate {
		return View{}, errQueueStore
	}
	return v, nil
}

func (f *fakeQueueStore) DeleteQueue(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (int64, error) {
	if f.failDelete {
		return 0, errQueueStore
	}
	return f.deleted, nil
}

func (f *fakeQueueStore) ReorderQueues(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) error {
	if f.failReorder {
		return errQueueStore
	}
	return nil
}

func (f *fakeQueueStore) NextQueuePosition(context.Context, uuid.UUID) (int32, error) {
	if f.failNextPos {
		return 0, errQueueStore
	}
	return 3, nil
}

func (f *fakeQueueStore) SpaceWorkflowStatuses(context.Context, uuid.UUID) ([]WorkflowStatus, error) {
	if f.failStatuses {
		return nil, errQueueStore
	}
	return f.statuses, nil
}

func beaconDraft(name string) Draft {
	return Draft{Name: name, Query: Query{
		V:      Version,
		Filter: Filter{Modules: []Module{ModuleBeacon}},
		Sort:   Sort{Field: "updated_at", Dir: "desc"},
	}}
}

// Every store failure in the queue lifecycle propagates wrapped rather than
// being swallowed into a zero value. A swallowed failure here reads as "the
// space has no queues" or "the reorder was applied", both of which are lies the
// caller acts on.
func TestQueueService_StoreFailuresPropagate(t *testing.T) {
	ctx := context.Background()
	org, space, owner, id := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	cases := map[string]func(s *QueueService) error{
		"list": func(s *QueueService) error {
			_, err := s.List(ctx, org, space)
			return err
		},
		"get": func(s *QueueService) error {
			_, err := s.Get(ctx, org, space, id)
			return err
		},
		"create: the position lookup": func(s *QueueService) error {
			_, err := s.Create(ctx, org, space, owner, beaconDraft("Q"))
			return err
		},
		"update: loading the existing row": func(s *QueueService) error {
			_, err := s.Update(ctx, org, space, id, beaconDraft("Q"))
			return err
		},
		"delete": func(s *QueueService) error { return s.Delete(ctx, org, space, id) },
		"reorder: loading the current order": func(s *QueueService) error {
			return s.Reorder(ctx, org, space, nil)
		},
		"create defaults: reading the workflow": func(s *QueueService) error {
			_, err := s.CreateDefaults(ctx, org, space, owner)
			return err
		},
	}
	stores := map[string]*fakeQueueStore{
		"list":                                  {failList: true},
		"get":                                   {failGet: true},
		"create: the position lookup":           {failNextPos: true},
		"update: loading the existing row":      {failGet: true},
		"delete":                                {failDelete: true},
		"reorder: loading the current order":    {failList: true},
		"create defaults: reading the workflow": {failStatuses: true},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			err := call(NewQueueService(stores[name]))
			require.ErrorIs(t, err, errQueueStore, "the store's failure must survive to the caller")
			require.NotEqual(t, errQueueStore.Error(), err.Error(),
				"and must be wrapped with what was being attempted")
		})
	}
}

// The write itself failing after every check passed is its own case: the
// position was already read, so a swallowed error here would report a queue
// that does not exist.
func TestQueueService_TheWritesThemselvesPropagate(t *testing.T) {
	ctx := context.Background()
	org, space, owner, id := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	_, err := NewQueueService(&fakeQueueStore{failCreate: true}).
		Create(ctx, org, space, owner, beaconDraft("Q"))
	require.ErrorIs(t, err, errQueueStore)

	_, err = NewQueueService(&fakeQueueStore{failUpdate: true}).
		Update(ctx, org, space, id, beaconDraft("Q"))
	require.ErrorIs(t, err, errQueueStore)

	store := &fakeQueueStore{failReorder: true, queues: []View{{ID: id}}}
	require.ErrorIs(t, NewQueueService(store).Reorder(ctx, org, space, []uuid.UUID{id}), errQueueStore)

	_, err = NewQueueService(&fakeQueueStore{failAbsent: true, statuses: []WorkflowStatus{
		{Name: "Open", Category: "todo"},
	}}).CreateDefaults(ctx, org, space, owner)
	require.ErrorIs(t, err, errQueueStore)
}

// A queue's name is required and bounded, and the binding refuses a Vector
// module — the queue would sit in the sidebar returning nothing forever,
// because project_items do not live in Beacon spaces.
func TestQueueService_RefusesWhatItCannotHonour(t *testing.T) {
	ctx := context.Background()
	org, space, owner, id := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	s := NewQueueService(&fakeQueueStore{})

	_, err := s.Create(ctx, org, space, owner, beaconDraft("   "))
	require.ErrorIs(t, err, ErrNameRequired, "whitespace is not a name")

	long := beaconDraft(strings.Repeat("q", MaxNameLen+1))
	_, err = s.Create(ctx, org, space, owner, long)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most")

	// Update trims and refuses too — the same rule, applied at the same place
	// in the sequence, rather than only at create time.
	_, err = s.Update(ctx, org, space, id, beaconDraft(" "))
	require.ErrorIs(t, err, ErrNameRequired)

	vector := beaconDraft("Vector queue")
	vector.Query.Filter.Modules = []Module{ModuleVector}
	_, err = s.Create(ctx, org, space, owner, vector)
	require.ErrorIs(t, err, ErrQueueModule)

	_, err = s.Update(ctx, org, space, id, vector)
	require.ErrorIs(t, err, ErrQueueModule)

	// A document that fails its own validation is refused before any write.
	bad := beaconDraft("Bad sort")
	bad.Query.Sort = Sort{Field: "not_a_field", Dir: "desc"}
	_, err = s.Create(ctx, org, space, owner, bad)
	require.Error(t, err)
	_, err = s.Update(ctx, org, space, id, bad)
	require.Error(t, err)
}

// Deleting a queue that the space does not hold is ErrQueueNotInSpace, not a
// silent success. Reporting "deleted" for a row nobody deleted is a lie the
// caller cannot detect.
func TestQueueService_DeletingNothingIsNotFound(t *testing.T) {
	err := NewQueueService(&fakeQueueStore{deleted: 0}).
		Delete(context.Background(), uuid.New(), uuid.New(), uuid.New())
	require.ErrorIs(t, err, ErrQueueNotInSpace)

	require.NoError(t, NewQueueService(&fakeQueueStore{deleted: 1}).
		Delete(context.Background(), uuid.New(), uuid.New(), uuid.New()))
}

// A reorder must be a PERMUTATION of the space's live queues. A partial list
// would leave the unmentioned queues at stale positions and silently interleave
// them — an ordering bug nobody reports, because it looks like a preference.
func TestQueueService_ReorderRefusesAnythingButAPermutation(t *testing.T) {
	ctx := context.Background()
	org, space := uuid.New(), uuid.New()
	a, b := uuid.New(), uuid.New()
	store := &fakeQueueStore{queues: []View{{ID: a}, {ID: b}}}
	s := NewQueueService(store)

	require.ErrorIs(t, s.Reorder(ctx, org, space, []uuid.UUID{a}), ErrReorderMismatch,
		"a short list is a partial reorder")
	require.ErrorIs(t, s.Reorder(ctx, org, space, []uuid.UUID{a, uuid.New()}), ErrReorderMismatch,
		"an id the space does not hold is refused rather than ignored")
	require.ErrorIs(t, s.Reorder(ctx, org, space, []uuid.UUID{a, a}), ErrReorderMismatch,
		"the right length with a duplicate is still not a permutation")
	require.NoError(t, s.Reorder(ctx, org, space, []uuid.UUID{b, a}))
}

// CreateDefaults derives its filters from the SPACE'S OWN workflow vocabulary,
// so "open" and "resolved" are whichever states that space puts in each
// category. The `done` split is the branch the integration suite never reaches
// with a workflow that has both categories populated.
func TestQueueService_DefaultsSplitTheWorkflowByCategory(t *testing.T) {
	store := &fakeQueueStore{
		inserted: true,
		statuses: []WorkflowStatus{
			{Name: "To do", Category: "todo"},
			{Name: "In progress", Category: "in_progress"},
			{Name: "Shipped", Category: "done"},
			{Name: "Won't do", Category: "done"},
		},
	}
	n, err := NewQueueService(store).
		CreateDefaults(context.Background(), uuid.New(), uuid.New(), uuid.New())
	require.NoError(t, err)
	require.Equal(t, len(DefaultQueues), n, "every default is created on a space that has none")
	require.Equal(t, []string{"All open", "Assigned to me", "Unassigned", "Recently resolved"},
		store.createdNames)

	// "Recently resolved" is built last, so lastQuery is its filter: the two
	// `done` states and nothing else. A queue built from every status instead
	// would look right and return the whole space.
	require.Equal(t, []string{"Shipped", "Won't do"}, store.lastQuery.Filter.Statuses)

	// Idempotence is the store's, by constraint — the service reports what was
	// actually inserted rather than what it attempted.
	quiet := &fakeQueueStore{inserted: false, statuses: store.statuses}
	n, err = NewQueueService(quiet).
		CreateDefaults(context.Background(), uuid.New(), uuid.New(), uuid.New())
	require.NoError(t, err)
	require.Zero(t, n, "a space that already has them is told nothing was created")
	require.Len(t, quiet.createdNames, len(DefaultQueues), "and all four were still attempted")
}
