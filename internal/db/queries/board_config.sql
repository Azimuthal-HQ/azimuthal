-- name: ListBoardColumns :many
SELECT id, space_id, name, position, wip_limit, created_at, updated_at
FROM board_columns
WHERE space_id = $1
ORDER BY position;

-- name: GetBoardColumn :one
SELECT id, space_id, name, position, wip_limit, created_at, updated_at
FROM board_columns
WHERE id = $1;

-- name: CreateBoardColumn :one
INSERT INTO board_columns (id, space_id, name, position, wip_limit)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, space_id, name, position, wip_limit, created_at, updated_at;

-- name: UpdateBoardColumn :one
UPDATE board_columns
SET name = $2, position = $3, wip_limit = $4, updated_at = now()
WHERE id = $1
RETURNING id, space_id, name, position, wip_limit, created_at, updated_at;

-- name: DeleteBoardColumn :exec
DELETE FROM board_columns WHERE id = $1;

-- name: DeleteBoardColumnsBySpace :exec
DELETE FROM board_columns WHERE space_id = $1;

-- name: ListBoardColumnStatuses :many
SELECT space_id, status, column_id
FROM board_column_statuses
WHERE space_id = $1
ORDER BY status;

-- name: UpsertBoardColumnStatus :exec
INSERT INTO board_column_statuses (space_id, status, column_id)
VALUES ($1, $2, $3)
ON CONFLICT (space_id, status) DO UPDATE SET column_id = EXCLUDED.column_id;

-- name: DeleteBoardColumnStatus :exec
DELETE FROM board_column_statuses WHERE space_id = $1 AND status = $2;

-- ReassignBoardColumnStatuses re-homes every status owned by one column onto
-- another. This is what makes a column deletable: the FK is ON DELETE
-- RESTRICT, so the statuses must move first. Scoped by space_id as well as
-- column_id so a caller that muddles ids from two spaces touches nothing.
-- name: ReassignBoardColumnStatuses :exec
UPDATE board_column_statuses
SET column_id = $3
WHERE space_id = $1 AND column_id = $2;

-- name: CountBoardColumnStatuses :one
SELECT COUNT(*) FROM board_column_statuses WHERE column_id = $1;
