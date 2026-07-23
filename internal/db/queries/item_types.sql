-- name: ListItemTypesByOrg :many
SELECT * FROM item_types WHERE org_id = $1 ORDER BY position, name;

-- name: ListActiveItemTypesByOrg :many
SELECT * FROM item_types
WHERE org_id = $1 AND archived_at IS NULL
ORDER BY position, name;

-- name: GetItemTypeByID :one
SELECT * FROM item_types WHERE id = $1;

-- name: GetItemTypeByOrgSlug :one
SELECT * FROM item_types WHERE org_id = $1 AND slug = $2;

-- name: CreateItemType :one
INSERT INTO item_types (id, org_id, slug, name, position)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: RenameItemType :one
UPDATE item_types SET name = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetItemTypeArchived :one
UPDATE item_types SET archived_at = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteItemType :exec
DELETE FROM item_types WHERE id = $1;

-- name: MaxItemTypePosition :one
SELECT COALESCE(MAX(position), 0)::int AS max_position FROM item_types WHERE org_id = $1;

-- name: CountItemsOfType :one
-- Counts every item (soft-deleted included) whose type is this slug — a
-- soft-deleted item still references the type and can be restored, so the type
-- is "referenced" and must not be hard-deletable.
SELECT count(*)::bigint FROM project_items WHERE org_id = $1 AND kind = $2;

-- name: SeedDefaultItemTypes :exec
INSERT INTO item_types (org_id, slug, name, position)
VALUES ($1, 'task', 'Task', 1), ($1, 'story', 'Story', 2),
       ($1, 'bug', 'Bug', 3), ($1, 'epic', 'Epic', 4)
ON CONFLICT (org_id, slug) DO NOTHING;
