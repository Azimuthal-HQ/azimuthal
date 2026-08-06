-- +goose Up
-- +goose StatementBegin

-- ── Polymorphic custom-field values: item_field_values → entity_field_values ─
--
-- The custom-field engine (migration 033) was Vector-only for exactly one line
-- of DDL: item_id NOT NULL REFERENCES project_items ON DELETE CASCADE. A
-- Beacon ticket's UUID was refused by the database, so tickets could not hold
-- a custom field value at any layer. This migration removes that reason, using
-- migration 015's polymorphic technique step for step: add nullable
-- entity_type/entity_id → backfill → delete unbackfillable rows → SET NOT NULL
-- → CHECK on the shared three-value vocabulary → drop the FK → rename the
-- table and each index explicitly → composite (entity_type, entity_id) access
-- path.
ALTER TABLE item_field_values ADD COLUMN entity_type TEXT;
ALTER TABLE item_field_values ADD COLUMN entity_id   UUID;

-- Backfill: every existing value row hangs off a project item — the old FK
-- guaranteed it, so no membership subquery is needed the way 015's comments
-- backfill needed one to split tickets from items.
UPDATE item_field_values SET entity_type = 'project_item', entity_id = item_id;

-- Remove any rows that can't be backfilled (item_id null after a down/up
-- cycle). None exist on a forward path — item_id is NOT NULL here — but 015
-- carries the same belt-and-braces delete for the same reason.
DELETE FROM item_field_values WHERE entity_id IS NULL;

-- Apply NOT NULL after backfill (all existing rows are covered).
ALTER TABLE item_field_values ALTER COLUMN entity_type SET NOT NULL;
ALTER TABLE item_field_values ALTER COLUMN entity_id   SET NOT NULL;

-- One vocabulary, not two: the same three-value set migration 015 fixed for
-- comments and entity_relations, even though no page field surface exists yet.
ALTER TABLE item_field_values ADD CONSTRAINT entity_field_values_entity_type_check
    CHECK (entity_type IN ('ticket', 'project_item', 'page'));

-- Drop the FK constraint so any UUID (ticket, project_item, page) can hold
-- values. The consequence, on purpose: orphan value rows become possible in
-- principle. In practice nothing in the product hard-deletes a project_items,
-- tickets or pages row — all three soft-delete via deleted_at — so the cascade
-- being removed here has never fired.
ALTER TABLE item_field_values DROP CONSTRAINT IF EXISTS item_field_values_item_id_fkey;

-- Old item_id column kept for one release; dropped in a v0.5 migration. New
-- rows write entity_type/entity_id only, so the NOT NULL has to go with the FK.
ALTER TABLE item_field_values ALTER COLUMN item_id DROP NOT NULL;

-- Rename table and each index explicitly (constraint-owned indexes rename
-- through their constraints; renaming a table renames neither).
ALTER TABLE item_field_values RENAME TO entity_field_values;
ALTER TABLE entity_field_values RENAME CONSTRAINT item_field_values_pkey TO entity_field_values_pkey;
ALTER TABLE entity_field_values RENAME CONSTRAINT item_field_values_item_id_field_slug_key TO entity_field_values_item_id_field_slug_key;
ALTER INDEX idx_item_field_values_item RENAME TO idx_entity_field_values_item;

-- The polymorphic identity: one value per (entity_type, entity_id, field_slug).
-- Its unique index is also the (entity_type, entity_id) composite access path,
-- so no separate idx_..._poly index is created — 015 added _poly indexes
-- because entity_relations had no unique constraint covering that prefix; here
-- one would duplicate this index column for column.
ALTER TABLE entity_field_values ADD CONSTRAINT entity_field_values_entity_slug_key
    UNIQUE (entity_type, entity_id, field_slug);

-- ── Field scopes: which spaces and entity types a field is attached to ───────
--
-- required deliberately does NOT live on custom_field_defs. Definitions are
-- org-scoped, so a required flag there would make the field required in every
-- space, in both modules, on every form at once — the first person to mark a
-- field required would break every form that cannot supply it. Requiredness is
-- a property of an ATTACHMENT: this field, in this space, on this entity type.
-- (Jira's field configurations per project/issue type, in miniature.)
--
-- A field with no scope rows appears on no form. There is intentionally no
-- "no rows means show everywhere" fallback in code — that second path would
-- make "scoped nowhere" and "scoped everywhere" the same observable state.
--
-- Scope rows reference the definition by id and die with it (CASCADE), unlike
-- values, which are keyed by slug and deliberately survive their definition
-- (migration 033's comment, the customfields package doc, reconciliation D48).
CREATE TABLE custom_field_scopes (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    field_id    UUID        NOT NULL REFERENCES custom_field_defs (id) ON DELETE CASCADE,
    space_id    UUID        NOT NULL REFERENCES spaces (id) ON DELETE CASCADE,
    entity_type TEXT        NOT NULL CHECK (entity_type IN ('ticket', 'project_item', 'page')),
    required    BOOLEAN     NOT NULL DEFAULT false,
    position    INT         NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (field_id, space_id, entity_type)
);

CREATE INDEX idx_custom_field_scopes_space ON custom_field_scopes (space_id, entity_type);

-- Backfill: a scope row for every existing definition against every Vector
-- space in its org, entity_type 'project_item', required false, position from
-- the definition. Before scopes, every org field appeared on every Vector item
-- form; without these rows, scope-governed rendering would make every existing
-- field vanish from every item on day one — a behaviour regression introduced
-- by a migration. With them, day-one behaviour is bit-identical.
--
-- Archived definitions are included: unarchiving one restored it to every item
-- form before, and must land in the same place after. Soft-deleted spaces are
-- included too — their forms render nowhere either way, and a restored space
-- must come back with the fields it had.
--
-- This backfill inserts rows and rejects nothing, so it cannot invalidate any
-- stored data (required stays false everywhere until an admin says otherwise).
INSERT INTO custom_field_scopes (field_id, space_id, entity_type, required, position)
SELECT d.id, s.id, 'project_item', false, d.position
FROM custom_field_defs d
JOIN spaces s ON s.org_id = d.org_id AND s.type = 'vector';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS custom_field_scopes;

-- Values written for tickets and pages after the Up have no item_id to return
-- to; the pre-polymorphic schema cannot represent them, so the Down removes
-- them — the same lossy shape as 015's Down dropping entity_type/entity_id.
DELETE FROM entity_field_values WHERE item_id IS NULL;

ALTER TABLE entity_field_values DROP CONSTRAINT IF EXISTS entity_field_values_entity_slug_key;
ALTER TABLE entity_field_values RENAME CONSTRAINT entity_field_values_pkey TO item_field_values_pkey;
ALTER TABLE entity_field_values RENAME CONSTRAINT entity_field_values_item_id_field_slug_key TO item_field_values_item_id_field_slug_key;
ALTER INDEX idx_entity_field_values_item RENAME TO idx_item_field_values_item;
ALTER TABLE entity_field_values RENAME TO item_field_values;

ALTER TABLE item_field_values ALTER COLUMN item_id SET NOT NULL;
ALTER TABLE item_field_values
    ADD CONSTRAINT item_field_values_item_id_fkey
    FOREIGN KEY (item_id) REFERENCES project_items (id) ON DELETE CASCADE;
ALTER TABLE item_field_values DROP CONSTRAINT IF EXISTS entity_field_values_entity_type_check;
ALTER TABLE item_field_values DROP COLUMN IF EXISTS entity_type;
ALTER TABLE item_field_values DROP COLUMN IF EXISTS entity_id;
-- +goose StatementEnd
