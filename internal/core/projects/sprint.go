package projects

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Sprint status constants.
const (
	SprintStatusPlanned   = "planned"
	SprintStatusActive    = "active"
	SprintStatusCompleted = "completed"
)

// validSprintTransitions defines the allowed state machine transitions.
// planned → active → completed
var validSprintTransitions = map[string]map[string]bool{
	SprintStatusPlanned: {
		SprintStatusActive: true,
	},
	SprintStatusActive: {
		SprintStatusCompleted: true,
	},
}

// Sprint represents a time-boxed iteration within a project space.
type Sprint struct {
	ID        uuid.UUID  `json:"id"`
	SpaceID   uuid.UUID  `json:"space_id"`
	Name      string     `json:"name"`
	Goal      string     `json:"goal"`
	Status    string     `json:"status"`
	StartsAt  *time.Time `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	CreatedBy uuid.UUID  `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// SprintRepository defines the data access contract for sprints.
type SprintRepository interface {
	// Create persists a new sprint.
	Create(ctx context.Context, sprint *Sprint) error
	// GetByID retrieves a sprint by primary key. Returns ErrNotFound if absent.
	GetByID(ctx context.Context, id uuid.UUID) (*Sprint, error)
	// GetActiveBySpace returns the currently active sprint for a space.
	// Returns ErrNotFound if no active sprint exists.
	GetActiveBySpace(ctx context.Context, spaceID uuid.UUID) (*Sprint, error)
	// Update persists changes to a sprint (name, goal, dates).
	Update(ctx context.Context, sprint *Sprint) error
	// UpdateStatus changes the sprint status.
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (*Sprint, error)
	// ListBySpace returns all sprints in a space, ordered by creation date descending.
	ListBySpace(ctx context.Context, spaceID uuid.UUID) ([]*Sprint, error)
}

// SprintService handles sprint lifecycle management.
type SprintService struct {
	repo SprintRepository
}

// NewSprintService creates a SprintService backed by the given repository.
func NewSprintService(repo SprintRepository) *SprintService {
	return &SprintService{repo: repo}
}

// CreateSprint validates and persists a new sprint in planned status.
func (s *SprintService) CreateSprint(ctx context.Context, sprint *Sprint) (*Sprint, error) {
	if sprint.Name == "" {
		return nil, fmt.Errorf("creating sprint: %w", ErrNameRequired)
	}

	sprint.ID = uuid.New()
	sprint.Status = SprintStatusPlanned
	now := time.Now().UTC()
	sprint.CreatedAt = now
	sprint.UpdatedAt = now

	if err := s.repo.Create(ctx, sprint); err != nil {
		return nil, fmt.Errorf("creating sprint: %w", err)
	}
	return sprint, nil
}

// GetSprint retrieves a sprint by ID.
func (s *SprintService) GetSprint(ctx context.Context, id uuid.UUID) (*Sprint, error) {
	sprint, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting sprint: %w", err)
	}
	return sprint, nil
}

// UpdateSprint validates and persists changes to sprint details.
func (s *SprintService) UpdateSprint(ctx context.Context, sprint *Sprint) (*Sprint, error) {
	if sprint.Name == "" {
		return nil, fmt.Errorf("updating sprint: %w", ErrNameRequired)
	}

	sprint.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, sprint); err != nil {
		return nil, fmt.Errorf("updating sprint: %w", err)
	}
	return sprint, nil
}

// StartSprint transitions a sprint from planned to active.
// Returns ErrSprintActive if another sprint is already active in the same space.
// Returns ErrInvalidTransition if the sprint is not in planned status.
func (s *SprintService) StartSprint(ctx context.Context, id uuid.UUID) (*Sprint, error) {
	sprint, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("starting sprint: %w", err)
	}

	if err := validateTransition(sprint.Status, SprintStatusActive); err != nil {
		return nil, fmt.Errorf("starting sprint: %w", err)
	}

	// Check no other sprint is active in this space.
	active, err := s.repo.GetActiveBySpace(ctx, sprint.SpaceID)
	if err == nil && active.ID != id {
		return nil, fmt.Errorf("starting sprint: %w", ErrSprintActive)
	}

	updated, err := s.repo.UpdateStatus(ctx, id, SprintStatusActive)
	if err != nil {
		return nil, fmt.Errorf("starting sprint: %w", err)
	}
	return updated, nil
}

// CompleteSprint transitions a sprint from active to completed.
// Returns ErrInvalidTransition if the sprint is not in active status.
func (s *SprintService) CompleteSprint(ctx context.Context, id uuid.UUID) (*Sprint, error) {
	sprint, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("completing sprint: %w", err)
	}

	if err := validateTransition(sprint.Status, SprintStatusCompleted); err != nil {
		return nil, fmt.Errorf("completing sprint: %w", err)
	}

	updated, err := s.repo.UpdateStatus(ctx, id, SprintStatusCompleted)
	if err != nil {
		return nil, fmt.Errorf("completing sprint: %w", err)
	}
	return updated, nil
}

// ListSprintsBySpace returns all sprints in a space.
func (s *SprintService) ListSprintsBySpace(ctx context.Context, spaceID uuid.UUID) ([]*Sprint, error) {
	sprints, err := s.repo.ListBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("listing sprints: %w", err)
	}
	return sprints, nil
}

// GetActiveSprint returns the currently active sprint for a space.
func (s *SprintService) GetActiveSprint(ctx context.Context, spaceID uuid.UUID) (*Sprint, error) {
	sprint, err := s.repo.GetActiveBySpace(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("getting active sprint: %w", err)
	}
	return sprint, nil
}

// validateTransition checks that a sprint status change is allowed.
func validateTransition(from, to string) error {
	targets, ok := validSprintTransitions[from]
	if !ok || !targets[to] {
		return ErrInvalidTransition
	}
	return nil
}
