-- name: CreateProjectItem :one
INSERT INTO project_items (id, space_id, parent_id, number, kind, title, description,
                           status, priority, reporter_id, assignee_id, sprint_id,
                           labels, due_at, rank)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: GetProjectItemByID :one
SELECT * FROM project_items WHERE id = $1 AND deleted_at IS NULL;

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

-- name: GetProjectItemMaxNumber :one
SELECT COALESCE(MAX(number), 0)::bigint AS max_number FROM project_items WHERE space_id = $1;
