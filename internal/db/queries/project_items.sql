-- name: CreateProjectItem :one
-- Assigns number and item_key atomically. The data-modifying seq CTE bumps the
-- per-space counter with an ON CONFLICT upsert, which row-locks the counter so
-- concurrent creators serialise (no duplicate or reused numbers); sp resolves
-- the space's org_id and key for the item_key (<SPACE_KEY>-<n>). Because it is a
-- single statement, the counter bump and the item insert commit or roll back
-- together — a failed insert leaves no gap.
WITH seq AS (
    INSERT INTO project_item_sequences (space_id, last_number)
    VALUES ($2, 1)
    ON CONFLICT (space_id) DO UPDATE
        SET last_number = project_item_sequences.last_number + 1
    RETURNING last_number
),
sp AS (
    SELECT org_id, key FROM spaces WHERE id = $2
)
INSERT INTO project_items (id, space_id, org_id, parent_id, number, item_key, kind,
                           title, description, status, priority, reporter_id,
                           assignee_id, sprint_id, labels, due_at, rank)
SELECT $1, $2, sp.org_id, $3, seq.last_number, sp.key || '-' || seq.last_number,
       $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
FROM seq, sp
RETURNING *;

-- name: GetProjectItemByID :one
SELECT * FROM project_items WHERE id = $1 AND deleted_at IS NULL;

-- name: GetProjectItemByOrgKey :one
-- Resolves a human-readable key (e.g. VEC-123) to an item within an org.
SELECT * FROM project_items
WHERE org_id = $1 AND item_key = $2 AND deleted_at IS NULL;

-- name: ListProjectItemsBySpace :many
SELECT * FROM project_items
WHERE space_id = $1 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListProjectItemsByStatus :many
SELECT * FROM project_items
WHERE space_id = $1 AND status = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListProjectItemsByAssignee :many
SELECT * FROM project_items
WHERE space_id = $1 AND assignee_id = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListProjectItemsBySprint :many
SELECT * FROM project_items
WHERE sprint_id = $1 AND deleted_at IS NULL
ORDER BY rank ASC;

-- name: UpdateProjectItem :one
UPDATE project_items
SET title = $2, description = $3, status = $4, priority = $5,
    assignee_id = $6, labels = $7, due_at = $8, rank = $9,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateProjectItemStatus :one
UPDATE project_items
SET status = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateProjectItemWorkflowState :one
UPDATE project_items
SET status = $2, workflow_state_id = $3, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateProjectItemSprint :exec
UPDATE project_items
SET sprint_id = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateProjectItemRank :exec
UPDATE project_items
SET rank = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteProjectItem :exec
UPDATE project_items SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: SearchProjectItems :many
SELECT * FROM project_items
WHERE space_id = $1
  AND deleted_at IS NULL
  AND search_vector @@ plainto_tsquery('english', $2)
ORDER BY ts_rank(search_vector, plainto_tsquery('english', $2)) DESC
LIMIT $3;
