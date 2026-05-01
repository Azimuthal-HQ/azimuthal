package workflow

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// DBEngine is the production Engine backed by the workflow Repository.
type DBEngine struct {
	repo Repository
}

// NewDBEngine creates a DBEngine using the given repository.
func NewDBEngine(repo Repository) *DBEngine {
	return &DBEngine{repo: repo}
}

// AvailableTransitions returns all transitions reachable from currentStateID.
func (e *DBEngine) AvailableTransitions(ctx context.Context, workflowID uuid.UUID, currentStateID uuid.UUID) ([]*Transition, error) {
	ts, err := e.repo.ListAvailableTransitions(ctx, workflowID, currentStateID)
	if err != nil {
		return nil, fmt.Errorf("workflow engine list available transitions: %w", err)
	}
	return ts, nil
}

// ValidateTransition returns ErrInvalidTransition if no transition exists from
// currentStateID to targetStateID within the workflow.
func (e *DBEngine) ValidateTransition(ctx context.Context, workflowID uuid.UUID, currentStateID uuid.UUID, targetStateID uuid.UUID) error {
	ts, err := e.repo.ListAvailableTransitions(ctx, workflowID, currentStateID)
	if err != nil {
		return fmt.Errorf("workflow engine validate transition: %w", err)
	}
	for _, t := range ts {
		if t.ToStateID == targetStateID {
			return nil
		}
	}
	return fmt.Errorf("state %q → %q: %w", currentStateID, targetStateID, ErrInvalidTransition)
}

// ResolveStateName finds a workflow state by name.
func (e *DBEngine) ResolveStateName(ctx context.Context, workflowID uuid.UUID, name string) (*State, error) {
	states, err := e.repo.ListStates(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow engine resolve state name: %w", err)
	}
	for _, s := range states {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, fmt.Errorf("state %q in workflow %q: %w", name, workflowID, ErrNotFound)
}
