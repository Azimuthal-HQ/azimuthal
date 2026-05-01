-- +goose Up
-- +goose StatementBegin

-- ── Polymorphic comments: add entity_type / entity_id ────────────────────────
ALTER TABLE comments ADD COLUMN entity_type TEXT;
ALTER TABLE comments ADD COLUMN entity_id   UUID;

-- Backfill entity_type/entity_id from existing item_id/page_id columns.
-- Tickets first (kind='ticket' rows that were backfilled into tickets table).
UPDATE comments
SET entity_type = 'ticket', entity_id = item_id
WHERE item_id IS NOT NULL
  AND item_id IN (SELECT id FROM tickets);

-- Project items.
UPDATE comments
SET entity_type = 'project_item', entity_id = item_id
WHERE item_id IS NOT NULL
  AND item_id IN (SELECT id FROM project_items);

-- Wiki pages.
UPDATE comments
SET entity_type = 'page', entity_id = page_id
WHERE page_id IS NOT NULL;

-- Remove any rows that can't be backfilled (orphaned during a down/up cycle
-- where entity_type was dropped and item_id/page_id were also null).
DELETE FROM comments WHERE entity_type IS NULL AND item_id IS NULL AND page_id IS NULL;

-- Apply NOT NULL after backfill (all existing rows should be covered).
ALTER TABLE comments ALTER COLUMN entity_type SET NOT NULL;
ALTER TABLE comments ALTER COLUMN entity_id   SET NOT NULL;

ALTER TABLE comments ADD CONSTRAINT comments_entity_type_check
    CHECK (entity_type IN ('ticket', 'project_item', 'page'));

-- Drop the old XOR constraint (item_id/page_id mutual-exclusion).
ALTER TABLE comments DROP CONSTRAINT IF EXISTS comments_must_have_target;

-- Old item_id/page_id columns kept for one release; dropped in v0.3 migration.

CREATE INDEX idx_comments_entity ON comments (entity_type, entity_id) WHERE deleted_at IS NULL;

-- ── Polymorphic entity_relations (replaces item_relations) ───────────────────
ALTER TABLE item_relations ADD COLUMN from_type TEXT;
ALTER TABLE item_relations ADD COLUMN to_type   TEXT;

-- Backfill from_type (all existing from_ids are project items or tickets).
UPDATE item_relations
SET from_type = 'ticket'
WHERE from_id IN (SELECT id FROM tickets);

UPDATE item_relations
SET from_type = 'project_item'
WHERE from_id IN (SELECT id FROM project_items);

-- Backfill to_type.
UPDATE item_relations
SET to_type = 'ticket'
WHERE to_id IN (SELECT id FROM tickets);

UPDATE item_relations
SET to_type = 'project_item'
WHERE to_id IN (SELECT id FROM project_items);

-- Default any remaining nulls (shouldn't happen on a clean DB, but be safe).
UPDATE item_relations SET from_type = 'project_item' WHERE from_type IS NULL;
UPDATE item_relations SET to_type   = 'project_item' WHERE to_type   IS NULL;

ALTER TABLE item_relations ALTER COLUMN from_type SET NOT NULL;
ALTER TABLE item_relations ALTER COLUMN to_type   SET NOT NULL;

ALTER TABLE item_relations ADD CONSTRAINT entity_relations_from_type_check
    CHECK (from_type IN ('ticket', 'project_item', 'page'));
ALTER TABLE item_relations ADD CONSTRAINT entity_relations_to_type_check
    CHECK (to_type IN ('ticket', 'project_item', 'page'));

-- Drop the FK constraints so any UUID (ticket, project_item, page) can be linked.
ALTER TABLE item_relations DROP CONSTRAINT IF EXISTS item_relations_from_id_fkey;
ALTER TABLE item_relations DROP CONSTRAINT IF EXISTS item_relations_to_id_fkey;

-- Rename table and indexes.
ALTER TABLE item_relations RENAME TO entity_relations;
ALTER INDEX idx_item_relations_from RENAME TO idx_entity_relations_from;
ALTER INDEX idx_item_relations_to   RENAME TO idx_entity_relations_to;

CREATE INDEX idx_entity_relations_from_poly ON entity_relations (from_type, from_id);
CREATE INDEX idx_entity_relations_to_poly   ON entity_relations (to_type,   to_id);

-- ── Rename items → items_archive (FK constraints already gone above) ─────────
-- Comments item_id still references items(id) via FK; rename keeps the reference
-- intact (Postgres tracks by OID, not name). Safe to rename after dropping
-- item_relations FKs; comments.item_id FK will now reference items_archive.
ALTER TABLE items RENAME TO items_archive;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE items_archive RENAME TO items;
ALTER TABLE entity_relations RENAME TO item_relations;
ALTER INDEX IF EXISTS idx_entity_relations_from RENAME TO idx_item_relations_from;
ALTER INDEX IF EXISTS idx_entity_relations_to   RENAME TO idx_item_relations_to;
DROP INDEX IF EXISTS idx_entity_relations_from_poly;
DROP INDEX IF EXISTS idx_entity_relations_to_poly;
DROP INDEX IF EXISTS idx_comments_entity;
ALTER TABLE item_relations DROP COLUMN IF EXISTS from_type;
ALTER TABLE item_relations DROP COLUMN IF EXISTS to_type;
ALTER TABLE comments DROP COLUMN IF EXISTS entity_type;
ALTER TABLE comments DROP COLUMN IF EXISTS entity_id;
-- NOTE: tickets and project_items are created by migration 014 and are dropped
-- by migration 014's Down. Do not drop them here.
-- +goose StatementEnd
