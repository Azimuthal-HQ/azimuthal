package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// BoardConfigAdapter implements projects.BoardConfigRepository over the
// board_columns and board_column_statuses tables. It holds the pool as well as
// the queries: replacing a configuration and deleting a column are both
// multi-statement operations that must be atomic.
type BoardConfigAdapter struct {
	pool *pgxpool.Pool
	q    *generated.Queries
}

// NewBoardConfigAdapter creates a BoardConfigAdapter backed by the given pool.
func NewBoardConfigAdapter(pool *pgxpool.Pool) *BoardConfigAdapter {
	return &BoardConfigAdapter{pool: pool, q: generated.New(pool)}
}

// ListColumns returns a space's stored columns in position order, each with
// its mapped statuses. An empty result means the space has no stored config.
func (a *BoardConfigAdapter) ListColumns(ctx context.Context, spaceID uuid.UUID) ([]projects.BoardColumn, error) {
	rows, err := a.q.ListBoardColumns(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("board config adapter list columns: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	statusRows, err := a.q.ListBoardColumnStatuses(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("board config adapter list statuses: %w", err)
	}
	byColumn := make(map[uuid.UUID][]string, len(rows))
	for _, sr := range statusRows {
		byColumn[sr.ColumnID] = append(byColumn[sr.ColumnID], sr.Status)
	}

	columns := make([]projects.BoardColumn, 0, len(rows))
	for _, row := range rows {
		columns = append(columns, projects.BoardColumn{
			ID:        row.ID,
			SpaceID:   row.SpaceID,
			Name:      row.Name,
			Position:  int(row.Position),
			WIPLimit:  intPtrFromInt32(row.WipLimit),
			Statuses:  byColumn[row.ID],
			CreatedAt: row.CreatedAt.Time,
			UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return columns, nil
}

// ReplaceConfig atomically swaps a space's whole configuration. Passing no
// columns clears it, which returns the space to the derived default.
//
// Delete-then-insert rather than a diff: the status mappings carry an ON
// DELETE RESTRICT foreign key, so the mappings must go before their columns,
// and a full replace is the only order that is correct regardless of how the
// caller rearranged things. It runs in one transaction, so a reader never sees
// a board mid-rewrite.
func (a *BoardConfigAdapter) ReplaceConfig(ctx context.Context, spaceID uuid.UUID, columns []projects.BoardColumn) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("board config adapter replace: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	if err := clearConfig(ctx, qtx, spaceID); err != nil {
		return err
	}
	if err := writeColumns(ctx, qtx, spaceID, columns); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("board config adapter replace: commit: %w", err)
	}
	return nil
}

// clearConfig removes a space's status mappings and then its columns. The
// order matters: the mappings hold an ON DELETE RESTRICT reference, so the
// columns cannot go first.
func clearConfig(ctx context.Context, q *generated.Queries, spaceID uuid.UUID) error {
	existing, err := q.ListBoardColumnStatuses(ctx, spaceID)
	if err != nil {
		return fmt.Errorf("board config adapter replace: read statuses: %w", err)
	}
	for _, st := range existing {
		if err := q.DeleteBoardColumnStatus(ctx, generated.DeleteBoardColumnStatusParams{
			SpaceID: spaceID,
			Status:  st.Status,
		}); err != nil {
			return fmt.Errorf("board config adapter replace: clear status %q: %w", st.Status, err)
		}
	}
	if err := q.DeleteBoardColumnsBySpace(ctx, spaceID); err != nil {
		return fmt.Errorf("board config adapter replace: clear columns: %w", err)
	}
	return nil
}

// writeColumns inserts the new columns and their status mappings.
func writeColumns(ctx context.Context, q *generated.Queries, spaceID uuid.UUID, columns []projects.BoardColumn) error {
	for _, col := range columns {
		if _, err := q.CreateBoardColumn(ctx, generated.CreateBoardColumnParams{
			ID:       col.ID,
			SpaceID:  spaceID,
			Name:     col.Name,
			Position: int32(col.Position), //nolint:gosec // column positions are small ordinals, bounded by the column count
			WipLimit: int32PtrFromInt(col.WIPLimit),
		}); err != nil {
			return fmt.Errorf("board config adapter replace: create column %q: %w", col.Name, err)
		}
		for _, status := range col.Statuses {
			if err := q.UpsertBoardColumnStatus(ctx, generated.UpsertBoardColumnStatusParams{
				SpaceID:  spaceID,
				Status:   status,
				ColumnID: col.ID,
			}); err != nil {
				return fmt.Errorf("board config adapter replace: map status %q: %w", status, err)
			}
		}
	}
	return nil
}

// DeleteColumn re-homes a column's statuses onto remapTo and then removes it,
// atomically. The re-home must come first: board_column_statuses.column_id is
// ON DELETE RESTRICT, so the database itself refuses to drop a column that
// still owns statuses.
func (a *BoardConfigAdapter) DeleteColumn(ctx context.Context, spaceID, columnID, remapTo uuid.UUID) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("board config adapter delete column: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	if err := qtx.ReassignBoardColumnStatuses(ctx, generated.ReassignBoardColumnStatusesParams{
		SpaceID:    spaceID,
		ColumnID:   columnID,
		ColumnID_2: remapTo,
	}); err != nil {
		return fmt.Errorf("board config adapter delete column: reassign statuses: %w", err)
	}
	if err := qtx.DeleteBoardColumn(ctx, columnID); err != nil {
		return fmt.Errorf("board config adapter delete column: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("board config adapter delete column: commit: %w", err)
	}
	return nil
}

// intPtrFromInt32 converts a nullable generated int32 to *int.
func intPtrFromInt32(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

// int32PtrFromInt converts a nullable *int to the generated *int32.
func int32PtrFromInt(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // WIP limits are validated positive and small before reaching here
	return &n
}
