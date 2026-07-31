package projects

import (
	"context"
	"errors"
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

// DoneStatuses is the terminal ("done") item-status set: items in one of these
// states are considered complete. Sprint completion leaves done items on the
// completed sprint and returns everything else to the backlog (or a next
// sprint). Board customization (a later phase) lets a space map statuses to
// columns; this remains the definition of "finished" until then.
var DoneStatuses = []string{"done", "closed", "resolved"}

// IsDoneStatus reports whether status is one of the terminal DoneStatuses.
func IsDoneStatus(status string) bool {
	for _, s := range DoneStatuses {
		if s == status {
			return true
		}
	}
	return false
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
	//
	// No space reconciliation: reserved for internal resolution that compares
	// spaces itself (validateNextSprint). A route wants GetByIDInSpace.
	GetByID(ctx context.Context, id uuid.UUID) (*Sprint, error)
	// GetByIDInSpace retrieves a sprint reconciled against the space the
	// request named. Returns ErrNotFound if absent OR in another space —
	// indistinguishable on purpose, so the route is not an existence oracle.
	GetByIDInSpace(ctx context.Context, spaceID, id uuid.UUID) (*Sprint, error)
	// GetActiveBySpace returns the currently active sprint for a space.
	// Returns ErrNotFound if no active sprint exists.
	GetActiveBySpace(ctx context.Context, spaceID uuid.UUID) (*Sprint, error)
	// Update persists changes to a sprint (name, goal, dates).
	Update(ctx context.Context, sprint *Sprint) error
	// UpdateStatus changes the sprint status.
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) (*Sprint, error)
	// CompleteWithDisposition marks the sprint completed and, atomically, moves
	// every item whose status is not in doneStatuses off it — to nextSprintID
	// when non-nil, otherwise back to the backlog. Done items stay on the sprint.
	CompleteWithDisposition(ctx context.Context, id uuid.UUID, nextSprintID *uuid.UUID, doneStatuses []string) (*Sprint, error)
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

// GetSprint retrieves a sprint by ID, reconciled against the space the request
// named. The route proves the caller may read that space and proves nothing
// about the sprint id, so without this a sprint's name, goal and dates were
// readable across every space boundary.
func (s *SprintService) GetSprint(ctx context.Context, spaceID, id uuid.UUID) (*Sprint, error) {
	sprint, err := s.repo.GetByIDInSpace(ctx, spaceID, id)
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
func (s *SprintService) StartSprint(ctx context.Context, spaceID, id uuid.UUID) (*Sprint, error) {
	sprint, err := s.repo.GetByIDInSpace(ctx, spaceID, id)
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

// CompleteOptions controls what happens to a sprint's incomplete items when it
// is completed. NextSprintID nil returns them to the backlog; non-nil moves
// them to that sprint (Jira's "move to next sprint" on completion).
type CompleteOptions struct {
	NextSprintID *uuid.UUID
}

// CompleteSprint transitions a sprint from active to completed and disposes of
// its incomplete items per opts: to the backlog by default, or to a chosen next
// sprint. Done items (DoneStatuses) stay on the completed sprint.
//
// Returns ErrInvalidTransition if the sprint is not active, and
// ErrInvalidNextSprint if opts.NextSprintID names a sprint that does not exist,
// is in another space, is the sprint being completed, or is already completed.
func (s *SprintService) CompleteSprint(ctx context.Context, spaceID, id uuid.UUID, opts CompleteOptions) (*Sprint, error) {
	sprint, err := s.repo.GetByIDInSpace(ctx, spaceID, id)
	if err != nil {
		return nil, fmt.Errorf("completing sprint: %w", err)
	}

	if err := validateTransition(sprint.Status, SprintStatusCompleted); err != nil {
		return nil, fmt.Errorf("completing sprint: %w", err)
	}

	if err := s.validateNextSprint(ctx, sprint, opts.NextSprintID); err != nil {
		return nil, fmt.Errorf("completing sprint: %w", err)
	}

	updated, err := s.repo.CompleteWithDisposition(ctx, id, opts.NextSprintID, DoneStatuses)
	if err != nil {
		return nil, fmt.Errorf("completing sprint: %w", err)
	}
	return updated, nil
}

// validateNextSprint checks that a chosen carry-over sprint is a legitimate
// destination: it must exist, live in the same space as the sprint being
// completed, differ from it, and not itself be completed.
func (s *SprintService) validateNextSprint(ctx context.Context, completing *Sprint, nextID *uuid.UUID) error {
	if nextID == nil {
		return nil
	}
	if *nextID == completing.ID {
		return ErrInvalidNextSprint
	}
	next, err := s.repo.GetByID(ctx, *nextID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrInvalidNextSprint
		}
		return fmt.Errorf("loading next sprint: %w", err)
	}
	if next.SpaceID != completing.SpaceID || next.Status == SprintStatusCompleted {
		return ErrInvalidNextSprint
	}
	return nil
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
