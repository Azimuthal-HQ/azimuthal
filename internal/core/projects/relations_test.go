package projects

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// stubRelationRepo is an in-memory RelationRepository for testing.
type stubRelationRepo struct {
	relations map[uuid.UUID]*Relation
}

func newStubRelationRepo() *stubRelationRepo {
	return &stubRelationRepo{relations: make(map[uuid.UUID]*Relation)}
}

func (r *stubRelationRepo) Create(_ context.Context, rel *Relation) error {
	r.relations[rel.ID] = rel
	return nil
}

func (r *stubRelationRepo) ListByItem(_ context.Context, fromID uuid.UUID) ([]*Relation, error) {
	result := make([]*Relation, 0)
	for _, rel := range r.relations {
		if rel.FromID == fromID {
			result = append(result, rel)
		}
	}
	return result, nil
}

func (r *stubRelationRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.relations[id]; !ok {
		return ErrNotFound
	}
	delete(r.relations, id)
	return nil
}

func makeRelation(fromID, toID uuid.UUID) *Relation {
	return &Relation{
		FromID:    fromID,
		ToID:      toID,
		Kind:      RelationRelatesTo,
		CreatedBy: uuid.New(),
	}
}

func TestRelationService_CreateRelation(t *testing.T) {
	svc := NewRelationService(newStubRelationRepo())
	fromID := uuid.New()
	toID := uuid.New()

	rel, err := svc.CreateRelation(context.Background(), makeRelation(fromID, toID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel.ID == (uuid.UUID{}) {
		t.Error("relation must have a non-zero UUID")
	}
}

func TestRelationService_CreateRelation_AllKinds(t *testing.T) {
	for kind := range ValidRelationKinds {
		t.Run(kind, func(t *testing.T) {
			svc := NewRelationService(newStubRelationRepo())
			rel := makeRelation(uuid.New(), uuid.New())
			rel.Kind = kind
			if _, err := svc.CreateRelation(context.Background(), rel); err != nil {
				t.Fatalf("unexpected error for kind %s: %v", kind, err)
			}
		})
	}
}

func TestRelationService_CreateRelation_InvalidKind(t *testing.T) {
	svc := NewRelationService(newStubRelationRepo())
	rel := makeRelation(uuid.New(), uuid.New())
	rel.Kind = "invalid"

	_, err := svc.CreateRelation(context.Background(), rel)
	if !errors.Is(err, ErrInvalidRelationKind) {
		t.Errorf("expected ErrInvalidRelationKind, got %v", err)
	}
}

func TestRelationService_CreateRelation_SelfRelation(t *testing.T) {
	svc := NewRelationService(newStubRelationRepo())
	id := uuid.New()
	rel := makeRelation(id, id)

	_, err := svc.CreateRelation(context.Background(), rel)
	if !errors.Is(err, ErrSelfRelation) {
		t.Errorf("expected ErrSelfRelation, got %v", err)
	}
}

func TestRelationService_ListRelations(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	fromID := uuid.New()

	for i := 0; i < 3; i++ {
		if _, err := svc.CreateRelation(context.Background(), makeRelation(fromID, uuid.New())); err != nil {
			t.Fatal(err)
		}
	}
	// Relation from different item.
	if _, err := svc.CreateRelation(context.Background(), makeRelation(uuid.New(), uuid.New())); err != nil {
		t.Fatal(err)
	}

	rels, err := svc.ListRelations(context.Background(), fromID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rels) != 3 {
		t.Errorf("expected 3 relations, got %d", len(rels))
	}
}

func TestRelationService_DeleteRelation(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)

	rel, _ := svc.CreateRelation(context.Background(), makeRelation(uuid.New(), uuid.New()))
	if err := svc.DeleteRelation(context.Background(), rel.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rels, _ := svc.ListRelations(context.Background(), rel.FromID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relations after delete, got %d", len(rels))
	}
}

func TestRelationService_DeleteRelation_NotFound(t *testing.T) {
	svc := NewRelationService(newStubRelationRepo())
	err := svc.DeleteRelation(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRelationService_GetBlockers(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	itemID := uuid.New()

	blocker := makeRelation(itemID, uuid.New())
	blocker.Kind = RelationIsBlockedBy
	if _, err := svc.CreateRelation(context.Background(), blocker); err != nil {
		t.Fatal(err)
	}

	unrelated := makeRelation(itemID, uuid.New())
	unrelated.Kind = RelationRelatesTo
	if _, err := svc.CreateRelation(context.Background(), unrelated); err != nil {
		t.Fatal(err)
	}

	blockers, err := svc.GetBlockers(context.Background(), itemID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blockers) != 1 {
		t.Errorf("expected 1 blocker, got %d", len(blockers))
	}
}

func TestRelationService_GetBlocking(t *testing.T) {
	repo := newStubRelationRepo()
	svc := NewRelationService(repo)
	itemID := uuid.New()

	blocking := makeRelation(itemID, uuid.New())
	blocking.Kind = RelationBlocks
	if _, err := svc.CreateRelation(context.Background(), blocking); err != nil {
		t.Fatal(err)
	}

	unrelated := makeRelation(itemID, uuid.New())
	unrelated.Kind = RelationDuplicates
	if _, err := svc.CreateRelation(context.Background(), unrelated); err != nil {
		t.Fatal(err)
	}

	result, err := svc.GetBlocking(context.Background(), itemID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 blocking relation, got %d", len(result))
	}
}
