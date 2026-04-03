-- name: CreateItem :one
INSERT INTO items (id, space_id, parent_id, kind, title, description, status, priority, reporter_id, assignee_id, labels, due_at, rank)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetItemByID :one
SELECT * FROM items WHERE id = $1 AND deleted_at IS NULL;

-- name: ListItemsBySpace :many
SELECT * FROM items
WHERE space_id = $1 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListItemsByStatus :many
SELECT * FROM items
WHERE space_id = $1 AND status = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListItemsByAssignee :many
SELECT * FROM items
WHERE space_id = $1 AND assignee_id = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListItemsBySprint :many
SELECT * FROM items
WHERE sprint_id = $1 AND deleted_at IS NULL
ORDER BY rank ASC;

-- name: UpdateItem :one
UPDATE items
SET title = $2, description = $3, status = $4, priority = $5,
    assignee_id = $6, labels = $7, due_at = $8, rank = $9
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateItemStatus :one
UPDATE items SET status = $2 WHERE id = $1 AND deleted_at IS NULL RETURNING *;

-- name: UpdateItemSprint :exec
UPDATE items SET sprint_id = $2 WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteItem :exec
UPDATE items SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: SearchItems :many
SELECT * FROM items
WHERE space_id = $1
  AND deleted_at IS NULL
  AND search_vector @@ plainto_tsquery('english', $2)
ORDER BY ts_rank(search_vector, plainto_tsquery('english', $2)) DESC
LIMIT $3;

-- name: CreateItemRelation :one
INSERT INTO item_relations (id, from_id, to_id, kind, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListItemRelations :many
SELECT ir.id, ir.from_id, ir.to_id, ir.kind, ir.created_by, ir.created_at,
       i.title AS to_title, i.status AS to_status, i.kind AS to_kind
FROM item_relations ir
JOIN items i ON i.id = ir.to_id
WHERE ir.from_id = $1;

-- name: DeleteItemRelation :exec
DELETE FROM item_relations WHERE id = $1;

-- name: CreateLabel :one
INSERT INTO labels (id, org_id, name, color) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListLabelsByOrg :many
SELECT * FROM labels WHERE org_id = $1 ORDER BY name ASC;

-- name: DeleteLabel :exec
DELETE FROM labels WHERE id = $1;

-- name: CreateSprint :one
INSERT INTO sprints (id, space_id, name, goal, status, starts_at, ends_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetSprintByID :one
SELECT * FROM sprints WHERE id = $1;

-- name: ListSprintsBySpace :many
SELECT * FROM sprints WHERE space_id = $1 ORDER BY created_at DESC;

-- name: GetActiveSprintBySpace :one
SELECT * FROM sprints WHERE space_id = $1 AND status = 'active' LIMIT 1;

-- name: UpdateSprintStatus :one
UPDATE sprints SET status = $2 WHERE id = $1 RETURNING *;

-- name: UpdateSprint :one
UPDATE sprints SET name = $2, goal = $3, starts_at = $4, ends_at = $5 WHERE id = $1 RETURNING *;
