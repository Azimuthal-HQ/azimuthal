package projects

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// stubRelationRepo is an in-memory RelationRepository for testing the service's
// own logic — validation, the target refusal, and direction filtering.
//
// It deliberately proves nothing about disclosure. The readable-space predicate
// this phase added lives in SQL, and a stub that re-implements it in Go would
// assert its own reimplementation rather than the query the server runs (D45).
// The leak, the write refusal and the reciprocal visibility are all pinned in
// relations_integration_test.go against real PostgreSQL.
type stubRelationRepo struct {
	stored map[uuid.UUID]*NewRelation
	// readable is the set of target ids TargetIsReadable answers true for.
	readable map[uuid.UUID]bool
	// spaces maps a stored relation to the space that touches it, standing in
	// for the endpoint join DeleteEntityRelationInSpace does in SQL.
	spaces map[uuid.UUID]uuid.UUID
	// listing is returned verbatim by ListForEntity.
	listing []*Relation
}

func newStubRelationRepo() *stubRelationRepo {
	return &stubRelationRepo{
		stored:   make(map[uuid.UUID]*NewRelation),
		readable: make(map[uuid.UUID]bool),
		spaces:   make(map[uuid.UUID]uuid.UUID),
	}
}

func (r *stubRelationRepo) Create(_ context.Context, id uuid.UUID, rel *NewRelation) error {
	r.stored[id] = rel
	return nil
}

func (r *stubRelationRepo) TargetIsReadable(_ context.Context, targetID uuid.UUID, _ string, _ []uuid.UUID) (bool, error) {
	return r.readable[targetID], nil
}

func (r *stubRelationRepo) ListForEntity(_ context.Context, _ uuid.UUID, _ string, _ []uuid.UUID) ([]*Relation, error) {
	return r.listing, nil
}

// DeleteInSpace mirrors DeleteEntityRelationInSpace rather than a convenient
// map delete: the row goes only if the named space touches it, and the call
// reports success either way.
//
// The predecessor returned ErrNotFound for an unknown id, which the real
// adapter has never done — the query is :exec and reports no row count — so a
// test written against it was asserting a contract that existed only in the
// double. Silence on a miss is now load-bearing: it is what makes a relation in
// another organisation indistinguishable from one that was never there.
func (r *stubRelationRepo) DeleteInSpace(_ context.Context, id, spaceID uuid.UUID) error {
	if r.spaces[id] != spaceID {
		return nil
	}
	delete(r.stored, id)
	return nil
}

// allowTarget marks a target id readable, the way a real readable space would.
func (r *stubRelationRepo) allowTarget(id uuid.UUID) { r.readable[id] = true }

func makeNewRelation(fromID, toID uuid.UUID) *NewRelation {
	return &NewRelation{
		FromID:    fromID,
		FromType:  EntityTypeProjectItem,
		ToID:      toID,
		ToType:    EntityTypeProjectItem,
		Kind:      RelationRelatesTo,
		CreatedBy: uuid.New(),
	}
}

func TestRelationService_CreateRelation(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	fromID, toID := uuid.New(), uuid.New()
	repo.allowTarget(toID)

	rel, err := svc.CreateRelation(context.Background(), makeNewRelation(fromID, toID), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel.ID == (uuid.UUID{}) {
		t.Error("relation must have a non-zero UUID")
	}
	if !rel.FarReadable || rel.FarID == nil || *rel.FarID != toID {
		t.Errorf("created relation must name the target it just proved readable, got %+v", rel)
	}
}

func TestRelationService_CreateRelation_AllKinds(t *testing.T) {
	for kind := range ValidRelationKinds {
		t.Run(kind, func(t *testing.T) {
			repo := newStubRelationRepo()
			svc := NewRelationService(repo)
			rel := makeNewRelation(uuid.New(), uuid.New())
			rel.Kind = kind
			repo.allowTarget(rel.ToID)
			if _, err := svc.CreateRelation(context.Background(), rel, nil); err != nil {
				t.Fatalf("unexpected error for kind %s: %v", kind, err)
			}
		})
	}
}

func TestRelationService_CreateRelation_InvalidKind(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	rel := makeNewRelation(uuid.New(), uuid.New())
	rel.Kind = "invalid"
	repo.allowTarget(rel.ToID)

	_, err := svc.CreateRelation(context.Background(), rel, nil)
	if !errors.Is(err, ErrInvalidRelationKind) {
		t.Errorf("expected ErrInvalidRelationKind, got %v", err)
	}
}

func TestRelationService_CreateRelation_InvalidEntityType(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	rel := makeNewRelation(uuid.New(), uuid.New())
	rel.ToType = "space"
	repo.allowTarget(rel.ToID)

	_, err := svc.CreateRelation(context.Background(), rel, nil)
	if !errors.Is(err, ErrInvalidEntityType) {
		t.Errorf("expected ErrInvalidEntityType, got %v", err)
	}
	if len(repo.stored) != 0 {
		t.Error("an entity type outside the CHECK constraint set must not reach the database")
	}
}

func TestRelationService_CreateRelation_SelfRelation(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	id := uuid.New()
	repo.allowTarget(id)

	_, err := svc.CreateRelation(context.Background(), makeNewRelation(id, id), nil)
	if !errors.Is(err, ErrSelfRelation) {
		t.Errorf("expected ErrSelfRelation, got %v", err)
	}
}

// TestRelationService_CreateRelation_UnresolvableTarget is the service-level
// half of the write fix: a target the repository will not vouch for is refused,
// and — the part that matters — nothing is written.
//
// Deleting the TargetIsReadable call in CreateRelation makes this fail on both
// assertions, which is the point: before the fix there was no such call, and
// any UUID at all was stored.
func TestRelationService_CreateRelation_UnresolvableTarget(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	// Note: allowTarget is NOT called, so the repository answers false — which
	// is what it does both for a nonexistent target and an unreadable one.
	rel := makeNewRelation(uuid.New(), uuid.New())

	_, err := svc.CreateRelation(context.Background(), rel, nil)
	if !errors.Is(err, ErrRelationTargetNotFound) {
		t.Errorf("expected ErrRelationTargetNotFound, got %v", err)
	}
	if len(repo.stored) != 0 {
		t.Errorf("a refused relation must not be persisted, found %d rows", len(repo.stored))
	}
}

func TestRelationService_ListRelations(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	repo.listing = []*Relation{
		{ID: uuid.New(), Kind: RelationRelatesTo, Direction: DirectionOutgoing},
		{ID: uuid.New(), Kind: RelationBlocks, Direction: DirectionIncoming},
	}

	rels, err := svc.ListRelations(context.Background(), uuid.New(), EntityTypeProjectItem, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relations, got %d", len(rels))
	}
}

func TestRelationService_DeleteRelation(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	toID := uuid.New()
	repo.allowTarget(toID)
	space := uuid.New()

	rel, err := svc.CreateRelation(context.Background(), makeNewRelation(uuid.New(), toID), nil)
	if err != nil {
		t.Fatal(err)
	}
	repo.spaces[rel.ID] = space

	if err := svc.DeleteRelation(context.Background(), rel.ID, space); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.stored) != 0 {
		t.Errorf("expected 0 stored relations after delete, got %d", len(repo.stored))
	}
}

// A relation no endpoint of which lives in the space the caller named is not
// deleted, and the caller cannot tell that from an id that never existed.
//
// Both halves matter. Without the first the route is a cross-organisation
// delete; without the second it is an existence oracle in place of one. The
// test this replaces asserted ErrNotFound for an unknown id — a contract only
// the double ever had, since the query is :exec and reports no row count.
func TestRelationService_DeleteRelation_OtherSpaceIsRefusedIndistinguishably(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	toID := uuid.New()
	repo.allowTarget(toID)
	owning, caller := uuid.New(), uuid.New()

	rel, err := svc.CreateRelation(context.Background(), makeNewRelation(uuid.New(), toID), nil)
	if err != nil {
		t.Fatal(err)
	}
	repo.spaces[rel.ID] = owning

	foreignErr := svc.DeleteRelation(context.Background(), rel.ID, caller)
	if foreignErr != nil {
		t.Fatalf("a relation in another space must not error, got %v", foreignErr)
	}
	if len(repo.stored) != 1 {
		t.Errorf("a relation in another space must survive, %d stored", len(repo.stored))
	}

	absentErr := svc.DeleteRelation(context.Background(), uuid.New(), caller)
	if !errors.Is(foreignErr, absentErr) || foreignErr != absentErr { //nolint:errorlint // comparing two nils by identity is the point
		t.Errorf("unreadable and nonexistent must answer identically: %v vs %v", foreignErr, absentErr)
	}

	// And the same relation through the space that does touch it still works,
	// or refusing everything would pass the assertions above.
	if err := svc.DeleteRelation(context.Background(), rel.ID, owning); err != nil {
		t.Fatalf("the owning space must still delete: %v", err)
	}
	if len(repo.stored) != 0 {
		t.Errorf("expected 0 stored relations after the owning space deleted, got %d", len(repo.stored))
	}
}

// TestRelationService_GetBlockers_BothDirections pins the direction rule that
// reciprocal visibility introduced. An item is blocked either because it said
// so itself (outgoing is_blocked_by) or because something else said so about it
// (incoming blocks) — and the two must not be confused, because before the
// reverse direction was unioned in only the first could ever appear.
//
// Filtering on kind alone — the previous implementation — puts the incoming
// "blocks" row in GetBlocking instead, i.e. reports that this item blocks the
// thing that is blocking it. Both subtests fail if the direction switch in
// filterByDirectedKind is removed.
func TestRelationService_GetBlockers_BothDirections(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	itemID := uuid.New()

	outgoingBlockedBy := &Relation{ID: uuid.New(), Kind: RelationIsBlockedBy, Direction: DirectionOutgoing}
	incomingBlocks := &Relation{ID: uuid.New(), Kind: RelationBlocks, Direction: DirectionIncoming}
	outgoingBlocks := &Relation{ID: uuid.New(), Kind: RelationBlocks, Direction: DirectionOutgoing}
	incomingBlockedBy := &Relation{ID: uuid.New(), Kind: RelationIsBlockedBy, Direction: DirectionIncoming}
	unrelated := &Relation{ID: uuid.New(), Kind: RelationRelatesTo, Direction: DirectionOutgoing}
	repo.listing = []*Relation{outgoingBlockedBy, incomingBlocks, outgoingBlocks, incomingBlockedBy, unrelated}

	t.Run("blockers", func(t *testing.T) {
		got, err := svc.GetBlockers(context.Background(), itemID, EntityTypeProjectItem, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertRelationIDs(t, got, outgoingBlockedBy.ID, incomingBlocks.ID)
	})

	t.Run("blocking", func(t *testing.T) {
		got, err := svc.GetBlocking(context.Background(), itemID, EntityTypeProjectItem, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertRelationIDs(t, got, outgoingBlocks.ID, incomingBlockedBy.ID)
	})
}

func assertRelationIDs(t *testing.T, got []*Relation, want ...uuid.UUID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d relations, got %d", len(want), len(got))
	}
	seen := make(map[uuid.UUID]bool, len(got))
	for _, rel := range got {
		seen[rel.ID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("expected relation %s in the result", id)
		}
	}
}
