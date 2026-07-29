package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// The queue half of SavedViewAdapter (migration 039). Split into its own file
// because it is the only part that needs a pool rather than just the queries:
// a reorder is several UPDATEs that have to land together.

var _ views.QueueStore = (*SavedViewAdapter)(nil)

func queueFromRow(r generated.SavedView, ownerName string) (views.View, error) {
	v, err := viewFromRow(r, ownerName, nil)
	if err != nil {
		return views.View{}, err
	}
	v.SpaceID = goUUIDPtr(r.SpaceID)
	v.Position = r.Position
	return v, nil
}

// ListQueues returns a space's queues in display order.
func (a *SavedViewAdapter) ListQueues(ctx context.Context, orgID, spaceID uuid.UUID) ([]views.View, error) {
	rows, err := a.q.ListQueuesForSpace(ctx, generated.ListQueuesForSpaceParams{
		OrgID: orgID, SpaceID: pgUUID(&spaceID),
	})
	if err != nil {
		return nil, fmt.Errorf("queue adapter list: %w", err)
	}
	out := make([]views.View, 0, len(rows))
	for _, r := range rows {
		v, err := queueFromRow(generated.SavedView{
			ID: r.ID, OrgID: r.OrgID, OwnerID: r.OwnerID, Name: r.Name,
			Description: r.Description, Query: r.Query, Visibility: r.Visibility,
			VisibilityTeamID: r.VisibilityTeamID, CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt, DeletedAt: r.DeletedAt,
			SpaceID: r.SpaceID, Position: r.Position,
		}, r.OwnerName)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetQueue returns one queue, or views.ErrQueueNotInSpace.
func (a *SavedViewAdapter) GetQueue(ctx context.Context, orgID, spaceID, id uuid.UUID) (views.View, error) {
	row, err := a.q.GetQueue(ctx, generated.GetQueueParams{
		ID: id, OrgID: orgID, SpaceID: pgUUID(&spaceID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Deliberately the same error whether the id names nothing, names
			// an ordinary saved view, or names a queue in a different space:
			// the caller has space-read on THIS space and nothing else, so
			// distinguishing those would answer questions about other spaces.
			return views.View{}, views.ErrQueueNotInSpace
		}
		return views.View{}, fmt.Errorf("queue adapter get: %w", err)
	}
	return queueFromRow(row, "")
}

// NextQueuePosition returns one past the space's last live queue.
func (a *SavedViewAdapter) NextQueuePosition(ctx context.Context, spaceID uuid.UUID) (int32, error) {
	n, err := a.q.NextQueuePosition(ctx, pgUUID(&spaceID))
	if err != nil {
		return 0, fmt.Errorf("queue adapter next position: %w", err)
	}
	return n, nil
}

func queueInsertParams(v views.View, raw []byte) generated.CreateQueueParams {
	return generated.CreateQueueParams{
		OrgID:       v.OrgID,
		OwnerID:     v.OwnerID,
		SpaceID:     pgUUID(v.SpaceID),
		Position:    v.Position,
		Name:        v.Name,
		Description: v.Description,
		Query:       raw,
	}
}

// CreateQueue inserts a queue.
func (a *SavedViewAdapter) CreateQueue(ctx context.Context, v views.View) (views.View, error) {
	raw, err := v.Query.Encode()
	if err != nil {
		return views.View{}, fmt.Errorf("encoding the queue's filter document: %w", err)
	}
	row, err := a.q.CreateQueue(ctx, queueInsertParams(v, raw))
	if err != nil {
		if name, ok := uniqueViolation(err); ok && name == "saved_views_space_name_key" {
			return views.View{}, views.ErrQueueNameTaken
		}
		return views.View{}, fmt.Errorf("queue adapter create: %w", err)
	}
	return queueFromRow(row, "")
}

// CreateQueueIfAbsent is the idempotent insert behind "create default queues".
// It reports whether a row was actually written, so the caller can say how
// many it added rather than claiming four every time.
func (a *SavedViewAdapter) CreateQueueIfAbsent(ctx context.Context, v views.View) (bool, error) {
	raw, err := v.Query.Encode()
	if err != nil {
		return false, fmt.Errorf("encoding the queue's filter document: %w", err)
	}
	p := queueInsertParams(v, raw)
	n, err := a.q.CreateQueueIfAbsent(ctx, generated.CreateQueueIfAbsentParams(p))
	if err != nil {
		return false, fmt.Errorf("queue adapter seed: %w", err)
	}
	return n > 0, nil
}

// UpdateQueue writes a queue's name, description and query. Position is not
// touched here — see views.QueueService.Update.
func (a *SavedViewAdapter) UpdateQueue(ctx context.Context, v views.View) (views.View, error) {
	raw, err := v.Query.Encode()
	if err != nil {
		return views.View{}, fmt.Errorf("encoding the queue's filter document: %w", err)
	}
	row, err := a.q.UpdateQueue(ctx, generated.UpdateQueueParams{
		ID: v.ID, OrgID: v.OrgID, SpaceID: pgUUID(v.SpaceID),
		Name: v.Name, Description: v.Description, Query: raw,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return views.View{}, views.ErrQueueNotInSpace
		}
		if name, ok := uniqueViolation(err); ok && name == "saved_views_space_name_key" {
			return views.View{}, views.ErrQueueNameTaken
		}
		return views.View{}, fmt.Errorf("queue adapter update: %w", err)
	}
	return queueFromRow(row, "")
}

// DeleteQueue soft-deletes a queue and reports how many rows it touched.
func (a *SavedViewAdapter) DeleteQueue(ctx context.Context, orgID, spaceID, id uuid.UUID) (int64, error) {
	n, err := a.q.SoftDeleteQueue(ctx, generated.SoftDeleteQueueParams{
		ID: id, OrgID: orgID, SpaceID: pgUUID(&spaceID),
	})
	if err != nil {
		return 0, fmt.Errorf("queue adapter delete: %w", err)
	}
	return n, nil
}

// ReorderQueues renumbers a space's queues in ONE transaction.
//
// The transaction is the whole point. saved_views_space_position_key is
// DEFERRABLE INITIALLY DEFERRED (migration 039, on migration 035's precedent),
// so the intermediate states — where two queues briefly claim one position —
// are legal until COMMIT. Without the transaction each UPDATE would be its own
// implicit one and the second write would collide with the first; the usual
// workaround, shuffling every row to a temporary high position and back, is
// exactly what the deferral exists to make unnecessary.
//
// A failure rolls the whole ordering back rather than leaving it half applied,
// which matters because a half-applied reorder is not visibly wrong — it just
// puts a queue somewhere nobody chose.
func (a *SavedViewAdapter) ReorderQueues(ctx context.Context, orgID, spaceID uuid.UUID, ordered []uuid.UUID) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("queue adapter reorder begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := a.q.WithTx(tx)
	for i, id := range ordered {
		n, err := qtx.SetQueuePosition(ctx, generated.SetQueuePositionParams{
			ID: id, OrgID: orgID, SpaceID: pgUUID(&spaceID), Position: int32Ptr(int32(i)),
		})
		if err != nil {
			return fmt.Errorf("queue adapter reorder: %w", err)
		}
		if n == 0 {
			// The service already checked the set is a permutation of the
			// space's live queues, so zero rows here means it changed
			// underneath us. Abort rather than commit a partial order.
			return views.ErrQueueNotInSpace
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("queue adapter reorder commit: %w", err)
	}
	return nil
}

// SpaceWorkflowStatuses returns a space's workflow state names by category.
func (a *SavedViewAdapter) SpaceWorkflowStatuses(ctx context.Context, spaceID uuid.UUID) ([]views.WorkflowStatus, error) {
	rows, err := a.q.ListSpaceWorkflowStatuses(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("queue adapter workflow statuses: %w", err)
	}
	out := make([]views.WorkflowStatus, 0, len(rows))
	for _, r := range rows {
		out = append(out, views.WorkflowStatus{Name: r.Name, Category: r.Category})
	}
	return out, nil
}

func int32Ptr(v int32) *int32 { return &v }
