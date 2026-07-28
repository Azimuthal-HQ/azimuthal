package views

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// A queue is a saved view bound to a space and ordered among that space's
// queues (migration 039). It is not a second model, and deliberately not a
// second results path: Resolve is the same function, the filter vocabulary is
// the same vocabulary, and the `me` token means the same thing. Everything in
// this file is about the BINDING — which space, which position, who may change
// it — and nothing in it is about resolving results.
//
// Audience. A queue carries VisibilitySpace, which means "everyone who can
// read the space". That is enforced by the route rather than re-derived per
// row: the queue endpoints sit behind the space-read guards, so a caller who
// reaches them has already been established as able to read the space. The
// generic /views list excludes space-bound rows for the same reason — it has
// no space guard to lean on.

// VisibilitySpace is the audience of a queue: the readers of its space.
const VisibilitySpace Visibility = "space"

// Errors specific to queues.
var (
	ErrNotAQueue       = errors.New("that saved view is not a queue in this space")
	ErrQueueNameTaken  = errors.New("this space already has a queue with that name")
	ErrReorderMismatch = errors.New("a reorder must list exactly the space's current queues, once each")
	ErrQueueNotInSpace = errors.New("queue not found in this space")
)

// DefaultQueue is one of the four queues the "create default queues" action
// offers. The set is fixed in code, not configurable: it is a starting point a
// space admin then edits, and a configurable default set is a second place for
// the same decision to live.
type DefaultQueue struct {
	Name        string
	Description string
	// build turns the space's own workflow vocabulary into a filter. "Open"
	// and "resolved" are not literals in this product — they are whichever
	// states the space's workflow puts in each category — so the filter is
	// derived per space at creation time rather than hardcoded.
	build func(spaceID uuid.UUID, open, done []string) Query
}

func queueQuery(spaceID uuid.UUID, statuses []string, assignees []string, sort Sort) Query {
	return Query{
		V: Version,
		Filter: Filter{
			Modules:   []Module{ModuleBeacon},
			SpaceIDs:  []uuid.UUID{spaceID},
			Statuses:  statuses,
			Assignees: assignees,
		},
		Sort: sort,
	}
}

// DefaultQueues is the JSM-parity starting set.
var DefaultQueues = []DefaultQueue{
	{
		Name:        "All open",
		Description: "Everything not yet resolved.",
		build: func(s uuid.UUID, open, _ []string) Query {
			return queueQuery(s, open, nil, Sort{Field: "updated_at", Dir: "desc"})
		},
	},
	{
		Name:        "Assigned to me",
		Description: "Open work assigned to you. Resolves per agent.",
		build: func(s uuid.UUID, open, _ []string) Query {
			return queueQuery(s, open, []string{AssigneeMe}, Sort{Field: "updated_at", Dir: "desc"})
		},
	},
	{
		Name:        "Unassigned",
		Description: "Open work with nobody on it yet.",
		build: func(s uuid.UUID, open, _ []string) Query {
			return queueQuery(s, open, []string{AssigneeUnassigned}, Sort{Field: "created_at", Dir: "asc"})
		},
	},
	{
		Name:        "Recently resolved",
		Description: "Closed work, most recently resolved first.",
		build: func(s uuid.UUID, _, done []string) Query {
			return queueQuery(s, done, nil, Sort{Field: "resolved_at", Dir: "desc"})
		},
	},
}

// WorkflowStatus is one of a space's workflow states.
type WorkflowStatus struct {
	Name     string
	Category string // "todo", "in_progress" or "done"
}

// QueueStore is the persistence seam for the space binding.
type QueueStore interface {
	ListQueues(ctx context.Context, orgID, spaceID uuid.UUID) ([]View, error)
	GetQueue(ctx context.Context, orgID, spaceID, id uuid.UUID) (View, error)
	CreateQueue(ctx context.Context, v View) (View, error)
	// CreateQueueIfAbsent inserts only when the space has no live queue of
	// that name, and reports whether it inserted. Idempotence is a database
	// constraint here, not a check-then-insert.
	CreateQueueIfAbsent(ctx context.Context, v View) (bool, error)
	UpdateQueue(ctx context.Context, v View) (View, error)
	DeleteQueue(ctx context.Context, orgID, spaceID, id uuid.UUID) (int64, error)
	// ReorderQueues writes every position in ONE transaction. The unique
	// constraint is DEFERRABLE INITIALLY DEFERRED so the intermediate states
	// are legal until commit.
	ReorderQueues(ctx context.Context, orgID, spaceID uuid.UUID, ordered []uuid.UUID) error
	NextQueuePosition(ctx context.Context, spaceID uuid.UUID) (int32, error)
	SpaceWorkflowStatuses(ctx context.Context, spaceID uuid.UUID) ([]WorkflowStatus, error)
}

// QueueService owns the queue lifecycle. It deliberately does NOT own results:
// a queue's rows come from Service.Preview on the queue's own stored query,
// through the same path an ordinary saved view takes.
type QueueService struct {
	store QueueStore
}

// NewQueueService creates a QueueService.
func NewQueueService(store QueueStore) *QueueService { return &QueueService{store: store} }

// List returns a space's queues in display order.
func (s *QueueService) List(ctx context.Context, orgID, spaceID uuid.UUID) ([]View, error) {
	rows, err := s.store.ListQueues(ctx, orgID, spaceID)
	if err != nil {
		return nil, fmt.Errorf("listing queues: %w", err)
	}
	return rows, nil
}

// Get returns one queue.
func (s *QueueService) Get(ctx context.Context, orgID, spaceID, id uuid.UUID) (View, error) {
	v, err := s.store.GetQueue(ctx, orgID, spaceID, id)
	if err != nil {
		return View{}, fmt.Errorf("loading the queue: %w", err)
	}
	return v, nil
}

// ErrQueueModule reports a queue asking for a module its space cannot serve.
var ErrQueueModule = errors.New("a Beacon queue reads Beacon tickets; it cannot query Vector items")

// bindToSpace forces a queue's filter to its own space and refuses a module
// set the binding makes meaningless.
//
// The space override is not a validation, it is an authority: a queue whose
// results could leave the container the sidebar says it belongs to is a saved
// view wearing a queue's clothes, and the space-read guard on the route would
// no longer bound what it returns. There is no legitimate request this rewrites
// into something the caller did not want.
//
// The MODULE check is the other half, and it exists because the override
// creates the hole. Once space_ids is pinned to a Beacon space, a filter naming
// the Vector module can never match anything -- project_items live in Vector
// spaces -- so the queue would sit in the sidebar returning nothing, forever,
// for a reason its author cannot see. That is the same defect class as naming
// `kinds` alongside Beacon, and it gets the same treatment: refused at write
// time rather than silently empty.
//
// It is written as "Beacon only" rather than "whatever this space's type is"
// because queues are a Beacon surface in this phase -- the route is reached
// from BeaconSidebar and the default set is ticket-shaped. When queues come to
// Vector this becomes a comparison against the bound space's own type, which is
// one query and a wider test, not a redesign.
//
// A queue that could name other spaces would be a saved view wearing a queue's
// clothes: its results would leave the container the sidebar says it belongs
// to, and the space-read guard on the route would no longer bound what it
// returns. The binding is the authority here, so it overrides whatever the
// caller sent rather than validating it — there is no legitimate request that
// this rewrites into something the caller did not want.
func bindToSpace(q Query, spaceID uuid.UUID) (Query, error) {
	if q.Filter.HasModule(ModuleVector) {
		return Query{}, ErrQueueModule
	}
	q.Filter.Modules = []Module{ModuleBeacon}
	q.Filter.SpaceIDs = []uuid.UUID{spaceID}
	return q, nil
}

// Create adds a queue at the end of the space's order.
func (s *QueueService) Create(ctx context.Context, orgID, spaceID, ownerID uuid.UUID, d Draft) (View, error) {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return View{}, ErrNameRequired
	}
	if len([]rune(d.Name)) > MaxNameLen {
		return View{}, fmt.Errorf("a queue name may be at most %d characters", MaxNameLen)
	}
	q, err := bindToSpace(d.Query, spaceID)
	if err != nil {
		return View{}, err
	}
	if err := q.Validate(); err != nil {
		return View{}, err
	}
	pos, err := s.store.NextQueuePosition(ctx, spaceID)
	if err != nil {
		return View{}, fmt.Errorf("finding the next queue position: %w", err)
	}
	created, err := s.store.CreateQueue(ctx, View{
		OrgID: orgID, OwnerID: ownerID, SpaceID: &spaceID, Position: &pos,
		Name: d.Name, Description: d.Description, Query: q, Visibility: VisibilitySpace,
	})
	if err != nil {
		return View{}, fmt.Errorf("creating the queue: %w", err)
	}
	return created, nil
}

// Update changes a queue's name, description and query. Position is moved by
// Reorder, never here — a partial reorder through a single-queue update is how
// an ordering ends up with two rows claiming one slot.
func (s *QueueService) Update(ctx context.Context, orgID, spaceID, id uuid.UUID, d Draft) (View, error) {
	existing, err := s.store.GetQueue(ctx, orgID, spaceID, id)
	if err != nil {
		return View{}, fmt.Errorf("loading the queue to update: %w", err)
	}
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return View{}, ErrNameRequired
	}
	q, err := bindToSpace(d.Query, spaceID)
	if err != nil {
		return View{}, err
	}
	if err := q.Validate(); err != nil {
		return View{}, err
	}
	existing.Name = d.Name
	existing.Description = d.Description
	existing.Query = q
	updated, err := s.store.UpdateQueue(ctx, existing)
	if err != nil {
		return View{}, fmt.Errorf("updating the queue: %w", err)
	}
	return updated, nil
}

// Delete soft-deletes a queue.
func (s *QueueService) Delete(ctx context.Context, orgID, spaceID, id uuid.UUID) error {
	n, err := s.store.DeleteQueue(ctx, orgID, spaceID, id)
	if err != nil {
		return fmt.Errorf("deleting the queue: %w", err)
	}
	if n == 0 {
		return ErrQueueNotInSpace
	}
	return nil
}

// Reorder sets the whole order at once.
//
// The request must be a PERMUTATION of the space's current queues: every live
// queue exactly once, and nothing else. A partial list would leave the
// unmentioned queues at stale positions and silently interleave them, which is
// the kind of ordering bug nobody reports because it looks like a preference.
func (s *QueueService) Reorder(ctx context.Context, orgID, spaceID uuid.UUID, ordered []uuid.UUID) error {
	current, err := s.store.ListQueues(ctx, orgID, spaceID)
	if err != nil {
		return fmt.Errorf("loading the current order: %w", err)
	}
	if len(ordered) != len(current) {
		return ErrReorderMismatch
	}
	want := make(map[uuid.UUID]struct{}, len(current))
	for _, q := range current {
		want[q.ID] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(ordered))
	for _, id := range ordered {
		if _, ok := want[id]; !ok {
			return ErrReorderMismatch
		}
		if _, dup := seen[id]; dup {
			return ErrReorderMismatch
		}
		seen[id] = struct{}{}
	}
	if err := s.store.ReorderQueues(ctx, orgID, spaceID, ordered); err != nil {
		return fmt.Errorf("reordering queues: %w", err)
	}
	return nil
}

// CreateDefaults creates whichever of the four default queues the space does
// not already have, and reports how many it created.
//
// Idempotent, and idempotent by CONSTRUCTION: each insert is ON CONFLICT DO
// NOTHING against the (space_id, name) unique index, so two agents pressing
// the button at the same moment cannot produce duplicates. A check-then-insert
// would have that race.
//
// There is deliberately no seeding migration and no seed-on-space-creation
// hook. A backfill would touch every existing space in one unreviewable step;
// seeding at creation would put these filter documents on a path that never
// shows them to anybody, and would need its own answer for spaces that already
// exist. This runs through the same guarded path a manual create takes.
func (s *QueueService) CreateDefaults(ctx context.Context, orgID, spaceID, ownerID uuid.UUID) (int, error) {
	statuses, err := s.store.SpaceWorkflowStatuses(ctx, spaceID)
	if err != nil {
		return 0, fmt.Errorf("reading the space workflow: %w", err)
	}
	var open, done []string
	for _, st := range statuses {
		if st.Category == "done" {
			done = append(done, st.Name)
			continue
		}
		open = append(open, st.Name)
	}
	// A space whose workflow has no state in a category gets a queue with no
	// status filter rather than one that can never match: an empty list means
	// "no filter" in the vocabulary, and "every ticket" is a more useful
	// starting point than "none".
	pos, err := s.store.NextQueuePosition(ctx, spaceID)
	if err != nil {
		return 0, fmt.Errorf("finding the next queue position: %w", err)
	}
	created := 0
	for _, d := range DefaultQueues {
		q := d.build(spaceID, open, done)
		if err := q.Validate(); err != nil {
			return created, fmt.Errorf("default queue %q: %w", d.Name, err)
		}
		p := pos
		inserted, err := s.store.CreateQueueIfAbsent(ctx, View{
			OrgID: orgID, OwnerID: ownerID, SpaceID: &spaceID, Position: &p,
			Name: d.Name, Description: d.Description, Query: q, Visibility: VisibilitySpace,
		})
		if err != nil {
			return created, fmt.Errorf("seeding queue %q: %w", d.Name, err)
		}
		if inserted {
			created++
			pos++
		}
	}
	return created, nil
}
