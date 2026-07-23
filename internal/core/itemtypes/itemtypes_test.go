package itemtypes

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// stubRepo is an in-memory itemtypes.Repository for unit tests.
type stubRepo struct {
	byID    map[uuid.UUID]*ItemType
	bySlug  map[string]*ItemType // key: orgID|slug
	counts  map[string]int       // key: orgID|slug → referencing items
	maxPos  int
	seedErr error
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		byID:   map[uuid.UUID]*ItemType{},
		bySlug: map[string]*ItemType{},
		counts: map[string]int{},
	}
}

func key(orgID uuid.UUID, slug string) string { return orgID.String() + "|" + slug }

func (r *stubRepo) ListByOrg(_ context.Context, _ uuid.UUID) ([]*ItemType, error) { return nil, nil }
func (r *stubRepo) ListActiveByOrg(_ context.Context, _ uuid.UUID) ([]*ItemType, error) {
	return nil, nil
}
func (r *stubRepo) GetByID(_ context.Context, id uuid.UUID) (*ItemType, error) {
	if t, ok := r.byID[id]; ok {
		return t, nil
	}
	return nil, ErrNotFound
}
func (r *stubRepo) GetByOrgSlug(_ context.Context, orgID uuid.UUID, slug string) (*ItemType, error) {
	if t, ok := r.bySlug[key(orgID, slug)]; ok {
		return t, nil
	}
	return nil, ErrNotFound
}
func (r *stubRepo) Create(_ context.Context, t *ItemType) error {
	r.byID[t.ID] = t
	r.bySlug[key(t.OrgID, t.Slug)] = t
	return nil
}
func (r *stubRepo) Rename(_ context.Context, id uuid.UUID, name string) (*ItemType, error) {
	t, ok := r.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	t.Name = name
	return t, nil
}
func (r *stubRepo) SetArchived(_ context.Context, id uuid.UUID, archived bool) (*ItemType, error) {
	t, ok := r.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	if archived {
		now := t.CreatedAt
		t.ArchivedAt = &now
	} else {
		t.ArchivedAt = nil
	}
	return t, nil
}
func (r *stubRepo) Delete(_ context.Context, id uuid.UUID) error {
	t, ok := r.byID[id]
	if !ok {
		return ErrNotFound
	}
	delete(r.byID, id)
	delete(r.bySlug, key(t.OrgID, t.Slug))
	return nil
}
func (r *stubRepo) CountItemsOfType(_ context.Context, orgID uuid.UUID, slug string) (int, error) {
	return r.counts[key(orgID, slug)], nil
}
func (r *stubRepo) NextPosition(_ context.Context, _ uuid.UUID) (int, error) {
	r.maxPos++
	return r.maxPos, nil
}
func (r *stubRepo) SeedDefaults(_ context.Context, _ uuid.UUID) error { return r.seedErr }

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Task":             "task",
		"Story":            "story",
		"Spike / Research": "spike_research",
		"  Bug  ":          "bug",
		"UPPER_case":       "upper_case",
		"multi   space":    "multi_space",
		"weird---dashes":   "weird_dashes",
		"leading!!":        "leading",
		"!!!":              "",
		"café":             "caf",
		"epic2":            "epic2",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestService_Create(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	org := uuid.New()

	tp, err := svc.Create(context.Background(), org, "Spike")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tp.Slug != "spike" || tp.Name != "Spike" {
		t.Errorf("unexpected type: %+v", tp)
	}
	if tp.Position != 1 {
		t.Errorf("expected position 1, got %d", tp.Position)
	}

	// Duplicate slug (same normalized name) → ErrDuplicate.
	if _, err := svc.Create(context.Background(), org, "spike"); !errors.Is(err, ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
	// Empty → ErrNameRequired.
	if _, err := svc.Create(context.Background(), org, "   "); !errors.Is(err, ErrNameRequired) {
		t.Errorf("expected ErrNameRequired, got %v", err)
	}
	// No alphanumeric content → ErrInvalidName.
	if _, err := svc.Create(context.Background(), org, "!!!"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got %v", err)
	}
}

func TestService_Delete_ReferencedGuard(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	org := uuid.New()
	tp, err := svc.Create(context.Background(), org, "Spike")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Referenced → ErrReferenced, and the type is NOT deleted.
	repo.counts[key(org, tp.Slug)] = 3
	if err := svc.Delete(context.Background(), org, tp.ID); !errors.Is(err, ErrReferenced) {
		t.Fatalf("expected ErrReferenced, got %v", err)
	}
	if _, ok := repo.byID[tp.ID]; !ok {
		t.Error("referenced type must remain after a refused delete")
	}

	// Unreferenced → deletes.
	repo.counts[key(org, tp.Slug)] = 0
	if err := svc.Delete(context.Background(), org, tp.ID); err != nil {
		t.Fatalf("Delete unreferenced: %v", err)
	}
	if _, ok := repo.byID[tp.ID]; ok {
		t.Error("unreferenced type must be deleted")
	}
}

func TestService_Rename_KeepsSlug(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	org := uuid.New()
	tp, _ := svc.Create(context.Background(), org, "Spike")

	renamed, err := svc.Rename(context.Background(), org, tp.ID, "Investigation")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if renamed.Name != "Investigation" {
		t.Errorf("name not updated: %q", renamed.Name)
	}
	if renamed.Slug != "spike" {
		t.Errorf("slug must not change on rename, got %q", renamed.Slug)
	}
}

func TestService_CrossOrgIsolation(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	orgA, orgB := uuid.New(), uuid.New()
	tp, _ := svc.Create(context.Background(), orgA, "Spike")

	// orgB cannot rename/archive/delete orgA's type — it reads as not found.
	if _, err := svc.Rename(context.Background(), orgB, tp.ID, "X"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-org rename: expected ErrNotFound, got %v", err)
	}
	if err := svc.Delete(context.Background(), orgB, tp.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-org delete: expected ErrNotFound, got %v", err)
	}
}

func TestService_IsActiveType(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	org := uuid.New()
	tp, _ := svc.Create(context.Background(), org, "Spike")

	ok, err := svc.IsActiveType(context.Background(), org, "spike")
	if err != nil || !ok {
		t.Fatalf("active type: ok=%v err=%v", ok, err)
	}
	if ok, _ := svc.IsActiveType(context.Background(), org, "nope"); ok {
		t.Error("undefined type must be inactive")
	}
	// Archived → inactive.
	if _, err := svc.SetArchived(context.Background(), org, tp.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if ok, _ := svc.IsActiveType(context.Background(), org, "spike"); ok {
		t.Error("archived type must be inactive")
	}
}
