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

func (r *stubLabelRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.labels[id]; !ok {
		return ErrNotFound
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
	if err := svc.DeleteLabel(context.Background(), label.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	labels, _ := svc.ListLabels(context.Background(), orgID)
	if len(labels) != 0 {
		t.Errorf("expected 0 labels after delete, got %d", len(labels))
	}
}

func TestLabelService_DeleteLabel_NotFound(t *testing.T) {
	svc := NewLabelService(newStubLabelRepo())
	err := svc.DeleteLabel(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
