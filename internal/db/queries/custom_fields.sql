-- name: ListCustomFieldDefsByOrg :many
SELECT * FROM custom_field_defs WHERE org_id = $1 ORDER BY position, name;

-- name: ListActiveCustomFieldDefsByOrg :many
SELECT * FROM custom_field_defs
WHERE org_id = $1 AND archived_at IS NULL
ORDER BY position, name;

-- name: GetCustomFieldDefByID :one
SELECT * FROM custom_field_defs WHERE id = $1;

-- name: GetCustomFieldDefByOrgSlug :one
SELECT * FROM custom_field_defs WHERE org_id = $1 AND slug = $2;

-- name: CreateCustomFieldDef :one
INSERT INTO custom_field_defs (id, org_id, slug, name, field_type, options, position)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateCustomFieldDef :one
UPDATE custom_field_defs
SET name = $2, options = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetCustomFieldDefArchived :one
UPDATE custom_field_defs SET archived_at = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteCustomFieldDef :exec
DELETE FROM custom_field_defs WHERE id = $1;

-- name: MaxCustomFieldDefPosition :one
SELECT COALESCE(MAX(position), 0)::int AS max_position FROM custom_field_defs WHERE org_id = $1;

-- name: ListItemFieldValues :many
SELECT * FROM item_field_values WHERE item_id = $1 ORDER BY field_slug;

-- name: UpsertItemFieldValue :one
INSERT INTO item_field_values (id, item_id, field_slug, value)
VALUES ($1, $2, $3, $4)
ON CONFLICT (item_id, field_slug)
DO UPDATE SET value = EXCLUDED.value, updated_at = now()
RETURNING *;

-- name: DeleteItemFieldValue :exec
DELETE FROM item_field_values WHERE item_id = $1 AND field_slug = $2;
