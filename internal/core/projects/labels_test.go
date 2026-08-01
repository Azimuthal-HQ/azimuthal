package projects

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// stubLabelRepo is an in-memory LabelRepository for testing.
type stubLabelRepo struct {
	labels map[uuid.UUID]*Label
}

func newStubLabelRepo() *stubLabelRepo {
	return &stubLabelRepo{labels: make(map[uuid.UUID]*Label)}
}

func (r *stubLabelRepo) Create(_ context.Context, label *Label) error {
	for _, existing := range r.labels {
		if existing.OrgID == label.OrgID && existing.Name == label.Name {
			return ErrLabelDuplicate
		}
	}
	r.labels[label.ID] = label
	return nil
}

func (r *stubLabelRepo) ListByOrg(_ context.Context, orgID uuid.UUID) ([]*Label, error) {
	result := make([]*Label, 0)
	for _, label := range r.labels {
		if label.OrgID == orgID {
			result = append(result, label)
		}
	}
	return result, nil
}

// DeleteInOrg mirrors DeleteLabelInOrg: the row goes only if this organisation
// owns it, and the call reports success either way.
//
// The predecessor returned ErrNotFound for an unknown id, which the real
// adapter has never done — the query is :exec and reports no row count — so the
// test written against it was pinning a contract that existed only here.
func (r *stubLabelRepo) DeleteInOrg(_ context.Context, id, orgID uuid.UUID) error {
	label, ok := r.labels[id]
	if !ok || label.OrgID != orgID {
		return nil
	}
	delete(r.labels, id)
	return nil
}

func TestLabelService_CreateLabel(t *testing.T) {
	svc := NewLabelService(newStubLabelRepo())
	orgID := uuid.New()

	label, err := svc.CreateLabel(context.Background(), &Label{
		OrgID: orgID,
		Name:  "bug",
		Color: "#ff0000",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if label.ID == (uuid.UUID{}) {
		t.Error("label must have a non-zero UUID")
	}
	if label.Color != "#ff0000" {
		t.Errorf("expected color #ff0000, got %s", label.Color)
	}
}

func TestLabelService_CreateLabel_DefaultColor(t *testing.T) {
	svc := NewLabelService(newStubLabelRepo())

	label, err := svc.CreateLabel(context.Background(), &Label{
		OrgID: uuid.New(),
		Name:  "enhancement",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if label.Color != DefaultLabelColor {
		t.Errorf("expected default color %s, got %s", DefaultLabelColor, label.Color)
	}
}

func TestLabelService_CreateLabel_NameRequired(t *testing.T) {
	svc := NewLabelService(newStubLabelRepo())

	_, err := svc.CreateLabel(context.Background(), &Label{
		OrgID: uuid.New(),
		Name:  "",
	})
	if !errors.Is(err, ErrNameRequired) {
		t.Errorf("expected ErrNameRequired, got %v", err)
	}
}

func TestLabelService_CreateLabel_Duplicate(t *testing.T) {
	svc := NewLabelService(newStubLabelRepo())
	orgID := uuid.New()

	if _, err := svc.CreateLabel(context.Background(), &Label{OrgID: orgID, Name: "bug"}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.CreateLabel(context.Background(), &Label{OrgID: orgID, Name: "bug"})
	if !errors.Is(err, ErrLabelDuplicate) {
		t.Errorf("expected ErrLabelDuplicate, got %v", err)
	}
}

func TestLabelService_ListLabels(t *testing.T) {
	repo := newStubLabelRepo()
	svc := NewLabelService(repo)
	orgID := uuid.New()

	for _, name := range []string{"bug", "feature", "docs"} {
		if _, err := svc.CreateLabel(context.Background(), &Label{OrgID: orgID, Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	// Label in different org.
	if _, err := svc.CreateLabel(context.Background(), &Label{OrgID: uuid.New(), Name: "other"}); err != nil {
		t.Fatal(err)
	}

	labels, err := svc.ListLabels(context.Background(), orgID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(labels) != 3 {
		t.Errorf("expected 3 labels, got %d", len(labels))
	}
}

func TestLabelService_DeleteLabel(t *testing.T) {
	repo := newStubLabelRepo()
	svc := NewLabelService(repo)
	orgID := uuid.New()

	label, _ := svc.CreateLabel(context.Background(), &Label{OrgID: orgID, Name: "bug"})
	if err := svc.DeleteLabel(context.Background(), label.ID, orgID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	labels, _ := svc.ListLabels(context.Background(), orgID)
	if len(labels) != 0 {
		t.Errorf("expected 0 labels after delete, got %d", len(labels))
	}
}

// A label belonging to another organisation is not deleted, and the caller
// cannot tell that apart from an id that never existed.
//
// The route is open to any org member, so before the org predicate a member of
// any organisation could hard-delete any label in the installation, and labels
// have no soft delete to recover from. This replaces a test that asserted
// ErrNotFound for an unknown id — a contract only the double ever had.
func TestLabelService_DeleteLabel_OtherOrgIsRefusedIndistinguishably(t *testing.T) {
	repo := newStubLabelRepo()
	svc := NewLabelService(repo)
	owning, attacker := uuid.New(), uuid.New()

	label, err := svc.CreateLabel(context.Background(), &Label{OrgID: owning, Name: "bug"})
	if err != nil {
		t.Fatal(err)
	}

	foreignErr := svc.DeleteLabel(context.Background(), label.ID, attacker)
	if foreignErr != nil {
		t.Fatalf("another org's label must not error, got %v", foreignErr)
	}
	remaining, _ := svc.ListLabels(context.Background(), owning)
	if len(remaining) != 1 {
		t.Errorf("another org's label must survive, %d remaining", len(remaining))
	}

	if absentErr := svc.DeleteLabel(context.Background(), uuid.New(), attacker); absentErr != foreignErr { //nolint:errorlint // identical-nil is the assertion
		t.Errorf("foreign and nonexistent must answer identically: %v vs %v", foreignErr, absentErr)
	}

	// The owning org still deletes, or refusing everything would pass above.
	if err := svc.DeleteLabel(context.Background(), label.ID, owning); err != nil {
		t.Fatalf("the owning org must still delete: %v", err)
	}
	if left, _ := svc.ListLabels(context.Background(), owning); len(left) != 0 {
		t.Errorf("expected 0 labels after the owning org deleted, got %d", len(left))
	}
}
