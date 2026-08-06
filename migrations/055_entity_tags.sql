-- +goose Up
-- +goose StatementBegin

-- ── page_tags → entity_tags ──────────────────────────────────────────────────
-- Tags become entity-generic: pages, tickets and project items all carry them.
-- This follows migration 015's satellite pattern (comments, entity_relations)
-- verbatim: add entity_type/entity_id, backfill, NOT NULL + CHECK, drop the
-- entity-side FK, rename the table and its indexes. page_id is kept for one
-- release, exactly as 015 kept comments.item_id/page_id.
--
-- Dropping the FK to pages loses ON DELETE CASCADE from the entity side, and
-- that is a deliberate trade rather than an oversight. The cascade has never
-- fired: no code path hard-deletes pages, tickets, project_items, spaces,
-- organizations, users, comments or attachments — every deletion in this
-- product is a soft delete — so the guarantee being given up has never once
-- executed. The polymorphic columns cannot carry an FK at all (three possible
-- referents), which is the same reasoning 015 recorded when it dropped
-- item_relations' from_id/to_id constraints. The tag-side FK stays: tags can
-- be hard-deleted in principle, and a deleted tag must take its associations
-- with it rather than leaving rows pointing at nothing.

ALTER TABLE page_tags ADD COLUMN entity_type TEXT;
ALTER TABLE page_tags ADD COLUMN entity_id   UUID;

-- Every existing association is a page's.
UPDATE page_tags SET entity_type = 'page', entity_id = page_id;

ALTER TABLE page_tags ALTER COLUMN entity_type SET NOT NULL;
ALTER TABLE page_tags ALTER COLUMN entity_id   SET NOT NULL;

ALTER TABLE page_tags ADD CONSTRAINT entity_tags_entity_type_check
    CHECK (entity_type IN ('ticket', 'project_item', 'page'));

-- The old identity was PRIMARY KEY (page_id, tag_id); the polymorphic identity
-- is the triple. Same reasoning as migration 040: the key IS the row's
-- identity, and it is what makes "tag an entity twice" impossible without
-- application code being careful. Its index also answers "this entity's tags"
-- by prefix, so the entity side needs no second index.
ALTER TABLE page_tags DROP CONSTRAINT page_tags_pkey;
ALTER TABLE page_tags ADD CONSTRAINT entity_tags_pkey
    PRIMARY KEY (entity_type, entity_id, tag_id);

-- See the header: the entity-side FK cannot survive polymorphism, and the
-- cascade it carried has never executed.
ALTER TABLE page_tags DROP CONSTRAINT page_tags_page_id_fkey;

-- Kept for one release per the 015 pattern; new rows never write it. Dropped
-- by a v0.5 migration alongside comments' legacy columns.
ALTER TABLE page_tags ALTER COLUMN page_id DROP NOT NULL;

ALTER TABLE page_tags RENAME TO entity_tags;
-- Postgres does not rename a table's indexes or constraints with it.
ALTER INDEX page_tags_tag_idx RENAME TO entity_tags_tag_idx;
ALTER TABLE entity_tags RENAME CONSTRAINT page_tags_tag_id_fkey TO entity_tags_tag_id_fkey;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Lossy: associations carried by tickets and project items have no
-- representation in the page-only shape and are dropped with the columns.
DELETE FROM entity_tags WHERE entity_type <> 'page';

ALTER TABLE entity_tags RENAME CONSTRAINT entity_tags_tag_id_fkey TO page_tags_tag_id_fkey;
ALTER INDEX entity_tags_tag_idx RENAME TO page_tags_tag_idx;
ALTER TABLE entity_tags RENAME TO page_tags;

-- Rows written after the migration carry only the polymorphic columns.
UPDATE page_tags SET page_id = entity_id WHERE page_id IS NULL;
ALTER TABLE page_tags ALTER COLUMN page_id SET NOT NULL;

ALTER TABLE page_tags DROP CONSTRAINT entity_tags_pkey;
ALTER TABLE page_tags ADD CONSTRAINT page_tags_pkey PRIMARY KEY (page_id, tag_id);
ALTER TABLE page_tags ADD CONSTRAINT page_tags_page_id_fkey
    FOREIGN KEY (page_id) REFERENCES pages (id) ON DELETE CASCADE;

ALTER TABLE page_tags DROP CONSTRAINT entity_tags_entity_type_check;
ALTER TABLE page_tags DROP COLUMN entity_type;
ALTER TABLE page_tags DROP COLUMN entity_id;

-- +goose StatementEnd
