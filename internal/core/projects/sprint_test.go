package projects

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// timeNowUTC is a helper to get current UTC time for tests.
func timeNowUTC() time.Time {
	return time.Now().UTC()
}

// stubSprintRepo is an in-memory SprintRepository for testing.
type stubSprintRepo struct {
	sprints map[uuid.UUID]*Sprint
}

func newStubSprintRepo() *stubSprintRepo {
	return &stubSprintRepo{sprints: make(map[uuid.UUID]*Sprint)}
}

func (r *stubSprintRepo) Create(_ context.Context, sprint *Sprint) error {
	r.sprints[sprint.ID] = sprint
	return nil
}

func (r *stubSprintRepo) GetByID(_ context.Context, id uuid.UUID) (*Sprint, error) {
	sprint, ok := r.sprints[id]
	if !ok {
		return nil, ErrNotFound
	}
	return sprint, nil
}

func (r *stubSprintRepo) GetActiveBySpace(_ context.Context, spaceID uuid.UUID) (*Sprint, error) {
	for _, sprint := range r.sprints {
		if sprint.SpaceID == spaceID && sprint.Status == SprintStatusActive {
			return sprint, nil
		}
	}
	return nil, ErrNotFound
}

func (r *stubSprintRepo) Update(_ context.Context, sprint *Sprint) error {
	if _, ok := r.sprints[sprint.ID]; !ok {
		return ErrNotFound
	}
	r.sprints[sprint.ID] = sprint
	return nil
}

func (r *stubSprintRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string) (*Sprint, error) {
	sprint, ok := r.sprints[id]
	if !ok {
		return nil, ErrNotFound
	}
	sprint.Status = status
	return sprint, nil
}

func (r *stubSprintRepo) ListBySpace(_ context.Context, spaceID uuid.UUID) ([]*Sprint, error) {
	result := make([]*Sprint, 0)
	for _, sprint := range r.sprints {
		if sprint.SpaceID == spaceID {
			result = append(result, sprint)
		}
	}
	return result, nil
}

func makeSprint(spaceID uuid.UUID) *Sprint {
	return &Sprint{
		SpaceID:   spaceID,
		Name:      "Sprint 1",
		Goal:      "Complete features",
		CreatedBy: uuid.New(),
	}
}

func TestSprintService_CreateSprint(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	spaceID := uuid.New()

	sprint, err := svc.CreateSprint(context.Background(), makeSprint(spaceID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sprint.ID == (uuid.UUID{}) {
		t.Error("sprint must have a non-zero UUID")
	}
	if sprint.Status != SprintStatusPlanned {
		t.Errorf("expected planned status, got %s", sprint.Status)
	}
}

func TestSprintService_CreateSprint_NameRequired(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	sprint := makeSprint(uuid.New())
	sprint.Name = ""

	_, err := svc.CreateSprint(context.Background(), sprint)
	if !errors.Is(err, ErrNameRequired) {
		t.Errorf("expected ErrNameRequired, got %v", err)
	}
}

func TestSprintService_GetSprint(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	created, _ := svc.CreateSprint(context.Background(), makeSprint(uuid.New()))

	got, err := svc.GetSprint(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "Sprint 1" {
		t.Errorf("wrong name: %s", got.Name)
	}
}

func TestSprintService_GetSprint_NotFound(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	_, err := svc.GetSprint(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSprintService_UpdateSprint(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	created, _ := svc.CreateSprint(context.Background(), makeSprint(uuid.New()))

	created.Name = "Sprint 1 - Updated"
	updated, err := svc.UpdateSprint(context.Background(), created)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Name != "Sprint 1 - Updated" {
		t.Errorf("expected updated name, got %s", updated.Name)
	}
}

func TestSprintService_UpdateSprint_NameRequired(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	created, _ := svc.CreateSprint(context.Background(), makeSprint(uuid.New()))

	created.Name = ""
	_, err := svc.UpdateSprint(context.Background(), created)
	if !errors.Is(err, ErrNameRequired) {
		t.Errorf("expected ErrNameRequired, got %v", err)
	}
}

func TestSprintService_StartSprint(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	spaceID := uuid.New()
	created, _ := svc.CreateSprint(context.Background(), makeSprint(spaceID))

	started, err := svc.StartSprint(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if started.Status != SprintStatusActive {
		t.Errorf("expected active status, got %s", started.Status)
	}
}

func TestSprintService_StartSprint_AlreadyActive(t *testing.T) {
	repo := newStubSprintRepo()
	svc := NewSprintService(repo)
	spaceID := uuid.New()

	first, _ := svc.CreateSprint(context.Background(), makeSprint(spaceID))
	if _, err := svc.StartSprint(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}

	second, _ := svc.CreateSprint(context.Background(), makeSprint(spaceID))
	_, err := svc.StartSprint(context.Background(), second.ID)
	if !errors.Is(err, ErrSprintActive) {
		t.Errorf("expected ErrSprintActive, got %v", err)
	}
}

func TestSprintService_StartSprint_InvalidTransition_FromCompleted(t *testing.T) {
	repo := newStubSprintRepo()
	svc := NewSprintService(repo)
	spaceID := uuid.New()

	sprint, _ := svc.CreateSprint(context.Background(), makeSprint(spaceID))
	if _, err := svc.StartSprint(context.Background(), sprint.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteSprint(context.Background(), sprint.ID); err != nil {
		t.Fatal(err)
	}

	_, err := svc.StartSprint(context.Background(), sprint.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestSprintService_CompleteSprint(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	spaceID := uuid.New()
	created, _ := svc.CreateSprint(context.Background(), makeSprint(spaceID))
	if _, err := svc.StartSprint(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}

	completed, err := svc.CompleteSprint(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if completed.Status != SprintStatusCompleted {
		t.Errorf("expected completed status, got %s", completed.Status)
	}
}

func TestSprintService_CompleteSprint_InvalidTransition_FromPlanned(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	created, _ := svc.CreateSprint(context.Background(), makeSprint(uuid.New()))

	_, err := svc.CompleteSprint(context.Background(), created.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestSprintService_FullLifecycle(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	spaceID := uuid.New()

	// Create → planned.
	sprint, err := svc.CreateSprint(context.Background(), makeSprint(spaceID))
	if err != nil {
		t.Fatal(err)
	}
	if sprint.Status != SprintStatusPlanned {
		t.Fatalf("expected planned, got %s", sprint.Status)
	}

	// Start → active.
	sprint, err = svc.StartSprint(context.Background(), sprint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sprint.Status != SprintStatusActive {
		t.Fatalf("expected active, got %s", sprint.Status)
	}

	// Complete → completed.
	sprint, err = svc.CompleteSprint(context.Background(), sprint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sprint.Status != SprintStatusCompleted {
		t.Fatalf("expected completed, got %s", sprint.Status)
	}
}

func TestSprintService_ListSprintsBySpace(t *testing.T) {
	repo := newStubSprintRepo()
	svc := NewSprintService(repo)
	spaceID := uuid.New()

	for i := 0; i < 3; i++ {
		if _, err := svc.CreateSprint(context.Background(), makeSprint(spaceID)); err != nil {
			t.Fatal(err)
		}
	}
	// Sprint in different space.
	if _, err := svc.CreateSprint(context.Background(), makeSprint(uuid.New())); err != nil {
		t.Fatal(err)
	}

	sprints, err := svc.ListSprintsBySpace(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sprints) != 3 {
		t.Errorf("expected 3 sprints, got %d", len(sprints))
	}
}

func TestSprintService_GetActiveSprint(t *testing.T) {
	repo := newStubSprintRepo()
	svc := NewSprintService(repo)
	spaceID := uuid.New()

	sprint, _ := svc.CreateSprint(context.Background(), makeSprint(spaceID))
	if _, err := svc.StartSprint(context.Background(), sprint.ID); err != nil {
		t.Fatal(err)
	}

	active, err := svc.GetActiveSprint(context.Background(), spaceID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if active.ID != sprint.ID {
		t.Error("wrong active sprint returned")
	}
}

func TestSprintService_GetActiveSprint_NoneActive(t *testing.T) {
	svc := NewSprintService(newStubSprintRepo())
	_, err := svc.GetActiveSprint(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestValidateTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{"planned to active", SprintStatusPlanned, SprintStatusActive, false},
		{"active to completed", SprintStatusActive, SprintStatusCompleted, false},
		{"planned to completed", SprintStatusPlanned, SprintStatusCompleted, true},
		{"completed to active", SprintStatusCompleted, SprintStatusActive, true},
		{"completed to planned", SprintStatusCompleted, SprintStatusPlanned, true},
		{"active to planned", SprintStatusActive, SprintStatusPlanned, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTransition(tt.from, tt.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTransition(%s, %s) error = %v, wantErr %v", tt.from, tt.to, err, tt.wantErr)
			}
		})
	}
}
