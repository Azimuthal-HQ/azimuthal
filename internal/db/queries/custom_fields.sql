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
-- An item's stored custom-field values, reconciled against the space the
-- request named. item_field_values carries no space_id — the values are
-- readable exactly when their item is — so the test joins the item, which is
-- also what makes a soft-deleted item's values stop being readable.
SELECT v.* FROM item_field_values v
JOIN project_items pi ON pi.id = v.item_id
WHERE v.item_id = @item_id
  AND pi.space_id = @space_id
  AND pi.deleted_at IS NULL
ORDER BY v.field_slug;

-- name: UpsertItemFieldValue :one
INSERT INTO item_field_values (id, item_id, field_slug, value)
VALUES ($1, $2, $3, $4)
ON CONFLICT (item_id, field_slug)
DO UPDATE SET value = EXCLUDED.value, updated_at = now()
RETURNING *;

-- name: DeleteItemFieldValue :exec
DELETE FROM item_field_values WHERE item_id = $1 AND field_slug = $2;

-- name: CountItemFieldValuesByOrgSlug :one
-- Counts the org's live items still holding a value under a field slug. Used to
-- refuse a NEW definition whose slug would silently adopt values left behind by
-- a deleted definition (values are stored by slug and outlive their
-- definitions, by design — migration 033).
--
-- item_field_values has no org column: values hang off project items. The org
-- is read from project_items.org_id, which migration 031 denormalised onto the
-- item and made NOT NULL, and which idx_project_items_org_id indexes — rather
-- than joined through spaces, which would also have to reason about a
-- soft-deleted space whose items are still holding values.
--
-- Soft-deleted items are excluded: their values are unreachable, so counting
-- them would refuse a legitimate field name over data nobody can see.
SELECT count(*) FROM item_field_values v
JOIN project_items i ON i.id = v.item_id
WHERE i.org_id = $1
  AND v.field_slug = $2
  AND i.deleted_at IS NULL;
