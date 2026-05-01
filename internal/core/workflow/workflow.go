// Package workflow implements the workflow engine: state machines that govern
// how tickets and project items move through user-defined lifecycle states.
package workflow

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Workflow is an org-scoped state machine definition.
type Workflow struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"org_id"`
	Name        string     `json:"name"`
	Description *string    `json:"description"`
	IsDefault   bool       `json:"is_default"`
	AppliesTo   string     `json:"applies_to"` // "tickets", "project_items", "both"
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// State is a named node in a workflow graph.
type State struct {
	ID         uuid.UUID `json:"id"`
	WorkflowID uuid.UUID `json:"workflow_id"`
	Name       string    `json:"name"`
	Category   string    `json:"category"` // "todo", "in_progress", "done"
	Color      string    `json:"color"`
	Position   int32     `json:"position"`
	IsInitial  bool      `json:"is_initial"`
	CreatedAt  time.Time `json:"created_at"`
}

// Transition is a named, directed edge between two states.
type Transition struct {
	ID          uuid.UUID `json:"id"`
	WorkflowID  uuid.UUID `json:"workflow_id"`
	FromStateID uuid.UUID `json:"from_state_id"`
	ToStateID   uuid.UUID `json:"to_state_id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
}

// Repository defines data access for workflow objects.
type Repository interface {
	// GetWorkflow retrieves a workflow by ID.
	GetWorkflow(ctx context.Context, id uuid.UUID) (*Workflow, error)
	// GetDefaultWorkflow retrieves the default workflow for an org and entity type.
	GetDefaultWorkflow(ctx context.Context, orgID uuid.UUID, appliesTo string) (*Workflow, error)
	// ListWorkflows retrieves all workflows for an org.
	ListWorkflows(ctx context.Context, orgID uuid.UUID) ([]*Workflow, error)
	// CreateWorkflow persists a new workflow.
	CreateWorkflow(ctx context.Context, w *Workflow) error
	// UpdateWorkflow persists workflow metadata changes.
	UpdateWorkflow(ctx context.Context, w *Workflow) error
	// DeleteWorkflow removes a workflow.
	DeleteWorkflow(ctx context.Context, id uuid.UUID) error

	// ListStates retrieves all states for a workflow, ordered by position.
	ListStates(ctx context.Context, workflowID uuid.UUID) ([]*State, error)
	// GetState retrieves a single state by ID.
	GetState(ctx context.Context, id uuid.UUID) (*State, error)
	// GetInitialState retrieves the initial state for a workflow.
	GetInitialState(ctx context.Context, workflowID uuid.UUID) (*State, error)
	// CreateState persists a new state.
	CreateState(ctx context.Context, s *State) error
	// UpdateState persists state changes.
	UpdateState(ctx context.Context, s *State) error
	// DeleteState removes a state.
	DeleteState(ctx context.Context, id uuid.UUID) error

	// ListTransitions retrieves all transitions for a workflow.
	ListTransitions(ctx context.Context, workflowID uuid.UUID) ([]*Transition, error)
	// ListAvailableTransitions returns transitions whose from_state_id matches currentStateID.
	ListAvailableTransitions(ctx context.Context, workflowID uuid.UUID, currentStateID uuid.UUID) ([]*Transition, error)
	// CreateTransition persists a new transition.
	CreateTransition(ctx context.Context, t *Transition) error
	// DeleteTransition removes a transition.
	DeleteTransition(ctx context.Context, id uuid.UUID) error

	// SeedDefaultWorkflows creates the two default workflows for a new org.
	SeedDefaultWorkflows(ctx context.Context, orgID uuid.UUID) error
}

// Engine validates and executes workflow transitions for entities.
type Engine interface {
	// AvailableTransitions returns the transitions reachable from the given state
	// within the workflow attached to the given entity.
	AvailableTransitions(ctx context.Context, workflowID uuid.UUID, currentStateID uuid.UUID) ([]*Transition, error)
	// ValidateTransition checks whether moving from currentStateID to targetStateID
	// is permitted. Returns ErrInvalidTransition if not.
	ValidateTransition(ctx context.Context, workflowID uuid.UUID, currentStateID uuid.UUID, targetStateID uuid.UUID) error
	// ResolveStateName looks up a state by name within a workflow.
	ResolveStateName(ctx context.Context, workflowID uuid.UUID, name string) (*State, error)
}
