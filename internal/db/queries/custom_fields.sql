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

-- name: ListEntityFieldValues :many
-- An entity's stored custom-field values, reconciled against the space the
-- request named. entity_field_values carries no space_id — the values are
-- readable exactly when their entity is — so the space test resolves the
-- entity per type, which is also what makes a soft-deleted entity's values
-- stop being readable. Same three-arm discriminator shape as
-- EntityRelationTargetIsReadable in items.sql: each arm carries its own
-- deleted_at IS NULL and space predicate.
SELECT v.* FROM entity_field_values v
WHERE v.entity_type = @entity_type::text
  AND v.entity_id = @entity_id
  AND EXISTS (
    SELECT 1 FROM tickets t
     WHERE @entity_type::text = 'ticket'
       AND t.id = v.entity_id
       AND t.space_id = @space_id
       AND t.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM project_items pi
     WHERE @entity_type::text = 'project_item'
       AND pi.id = v.entity_id
       AND pi.space_id = @space_id
       AND pi.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM pages pg
     WHERE @entity_type::text = 'page'
       AND pg.id = v.entity_id
       AND pg.space_id = @space_id
       AND pg.deleted_at IS NULL
  )
ORDER BY v.field_slug;

-- name: UpsertEntityFieldValue :one
-- The write carries its own space reconciliation. The predecessor
-- (UpsertItemFieldValue) had no space predicate and no org predicate at all —
-- the entire write-path authorization was the calling convention that the one
-- handler calling it resolved the item through the space first. Now the
-- statement itself refuses: an upsert addressed at an entity outside
-- @space_id, soft-deleted, or of another type proposes zero rows, so nothing
-- is inserted, no conflict fires, nothing is updated, and no row returns —
-- the caller maps that to the same 404 a nonexistent entity gets. Predicate
-- in the query, not check-after-load; unreadable == nonexistent, no oracle.
INSERT INTO entity_field_values (id, entity_type, entity_id, field_slug, value)
SELECT @id, @entity_type::text, @entity_id, @field_slug, @value
WHERE EXISTS (
    SELECT 1 FROM tickets t
     WHERE @entity_type::text = 'ticket'
       AND t.id = @entity_id
       AND t.space_id = @space_id
       AND t.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM project_items pi
     WHERE @entity_type::text = 'project_item'
       AND pi.id = @entity_id
       AND pi.space_id = @space_id
       AND pi.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM pages pg
     WHERE @entity_type::text = 'page'
       AND pg.id = @entity_id
       AND pg.space_id = @space_id
       AND pg.deleted_at IS NULL
)
ON CONFLICT (entity_type, entity_id, field_slug)
DO UPDATE SET value = EXCLUDED.value, updated_at = now()
RETURNING *;

-- name: DeleteEntityFieldValue :execrows
-- Same in-statement reconciliation as the upsert, for the same reason: a
-- delete addressed at an entity outside @space_id must affect zero rows
-- rather than trusting the caller to have resolved the entity first.
DELETE FROM entity_field_values v
WHERE v.entity_type = @entity_type::text
  AND v.entity_id = @entity_id
  AND v.field_slug = @field_slug
  AND EXISTS (
    SELECT 1 FROM tickets t
     WHERE @entity_type::text = 'ticket'
       AND t.id = v.entity_id
       AND t.space_id = @space_id
       AND t.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM project_items pi
     WHERE @entity_type::text = 'project_item'
       AND pi.id = v.entity_id
       AND pi.space_id = @space_id
       AND pi.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM pages pg
     WHERE @entity_type::text = 'page'
       AND pg.id = v.entity_id
       AND pg.space_id = @space_id
       AND pg.deleted_at IS NULL
  );

-- name: CountEntityFieldValuesByOrgSlug :one
-- Counts the org's live entities still holding a value under a field slug.
-- Used to refuse a NEW definition whose slug would silently adopt values left
-- behind by a deleted definition (values are stored by slug and outlive their
-- definitions, by design — migration 033, reconciliation D48).
--
-- entity_field_values has no org column: values hang off entities. Items
-- carry org_id directly (denormalised by migration 031); tickets and pages
-- reach it through their NOT NULL space_id.
--
-- Soft-deleted entities are excluded: their values are unreachable, so
-- counting them would refuse a legitimate field name over data nobody can see.
SELECT count(*) FROM entity_field_values v
WHERE v.field_slug = @field_slug
  AND EXISTS (
    SELECT 1 FROM project_items i
     WHERE v.entity_type = 'project_item'
       AND i.id = v.entity_id
       AND i.org_id = @org_id
       AND i.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM tickets t
      JOIN spaces ts ON ts.id = t.space_id
     WHERE v.entity_type = 'ticket'
       AND t.id = v.entity_id
       AND ts.org_id = @org_id
       AND t.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM pages p
      JOIN spaces ps ON ps.id = p.space_id
     WHERE v.entity_type = 'page'
       AND p.id = v.entity_id
       AND ps.org_id = @org_id
       AND p.deleted_at IS NULL
  );

-- name: ListCustomFieldScopesByField :many
SELECT * FROM custom_field_scopes WHERE field_id = $1 ORDER BY entity_type, created_at;

-- name: ListCustomFieldScopesForSpaceEntity :many
-- The scope rows governing one form: which fields appear for this entity type
-- in this space, in what order, and which of them are required there.
SELECT * FROM custom_field_scopes
WHERE space_id = $1 AND entity_type = $2
ORDER BY position, created_at;

-- name: GetCustomFieldScope :one
SELECT * FROM custom_field_scopes
WHERE field_id = $1 AND space_id = $2 AND entity_type = $3;

-- name: UpsertCustomFieldScope :one
-- The org predicate is in the statement: @space_id is caller-supplied, and
-- without the EXISTS an org admin could attach their field to another
-- organisation's space (and, later, mark it required there). A space outside
-- the org — or soft-deleted — proposes zero rows and the caller answers the
-- same 404 an unknown space gets. Position is set on first attach and kept on
-- re-attach: toggling required must not reshuffle the form.
INSERT INTO custom_field_scopes (id, field_id, space_id, entity_type, required, position)
SELECT @id, @field_id, @space_id, @entity_type::text, @required, @position
WHERE EXISTS (
    SELECT 1 FROM spaces s
     WHERE s.id = @space_id AND s.org_id = @org_id AND s.deleted_at IS NULL
)
ON CONFLICT (field_id, space_id, entity_type)
DO UPDATE SET required = EXCLUDED.required, updated_at = now()
RETURNING *;

-- name: DeleteCustomFieldScope :execrows
DELETE FROM custom_field_scopes
WHERE field_id = $1 AND space_id = $2 AND entity_type = $3;
