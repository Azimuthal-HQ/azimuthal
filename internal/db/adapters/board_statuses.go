package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// WorkflowStatusAdapter implements projects.WorkflowStateReader. The board has
// always taken its columns from the space's workflow states; board
// configuration validates against exactly the same vocabulary, so a
// configuration cannot be valid on one surface and wrong on the other.
type WorkflowStatusAdapter struct {
	q *generated.Queries
}

// NewWorkflowStatusAdapter creates a WorkflowStatusAdapter.
func NewWorkflowStatusAdapter(pool *pgxpool.Pool) *WorkflowStatusAdapter {
	return &WorkflowStatusAdapter{q: generated.New(pool)}
}

// StatusesForSpace returns the space's workflow state names in workflow order.
// An empty result is not an error: a space with no workflow falls back to the
// default vocabulary, which is what the board renders today.
func (a *WorkflowStatusAdapter) StatusesForSpace(ctx context.Context, spaceID uuid.UUID) ([]string, error) {
	states, err := a.q.GetSpaceWorkflowStates(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("workflow status adapter: %w", err)
	}
	names := make([]string, 0, len(states))
	for _, s := range states {
		names = append(names, s.Name)
	}
	return names, nil
}
