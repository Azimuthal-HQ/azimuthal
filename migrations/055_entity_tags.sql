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

-- ── labels TEXT[] converges onto entity tags ─────────────────────────────────
-- tickets.labels and project_items.labels (migrations 004/010/014) were a
-- write-only field: four handlers and a workflow post-function wrote them, and
-- no read path — no filter, no search arm, no React component — ever showed
-- them to anybody. Their meaning moves here.
--
-- Backfill first: every stored label becomes an entity tag, so nothing a user
-- typed is dropped by the convergence. The normalization rule mirrors
-- itemtypes.Slugify — the repository's one slug helper, whose output the
-- tags_slug_format CHECK is written against — as closely as SQL can state it:
--
--   * lowercase;
--   * every run of characters outside [a-z0-9] collapses to one underscore
--     (Slugify treats '_' itself as a separator too, so runs containing it
--     collapse the same way);
--   * leading and trailing underscores are trimmed;
--   * a label that normalises to nothing — pure punctuation, pure non-ASCII —
--     is dropped, exactly as Slugify maps it to the empty slug.
--
-- Labels that normalise alike collapse to ONE tag. The display name kept is
-- the alphabetically first spelling: the array elements carry no authorship
-- order across rows, so "first typed" is not knowable here, and a
-- deterministic choice beats a plan-dependent one. Where the slug already
-- exists as a tag, the existing name wins, same as UpsertTag.

WITH labelled AS (
    SELECT 'ticket'::text AS entity_type, t.id AS entity_id, s.org_id,
           unnest(t.labels) AS label
    FROM tickets t JOIN spaces s ON s.id = t.space_id
    UNION ALL
    SELECT 'project_item', pi.id, s.org_id, unnest(pi.labels)
    FROM project_items pi JOIN spaces s ON s.id = pi.space_id
), normalised AS (
    SELECT entity_type, entity_id, org_id, label,
           btrim(regexp_replace(lower(label), '[^a-z0-9]+', '_', 'g'), '_') AS slug
    FROM labelled
), usable AS (
    SELECT * FROM normalised WHERE slug ~ '^[a-z0-9][a-z0-9_]*$'
), first_spelling AS (
    SELECT DISTINCT ON (org_id, slug) org_id, slug, label
    FROM usable
    ORDER BY org_id, slug, label
), minted AS (
    INSERT INTO tags (org_id, slug, name)
    SELECT org_id, slug, label FROM first_spelling
    ON CONFLICT (org_id, slug) DO NOTHING
    RETURNING id, org_id, slug
)
INSERT INTO entity_tags (entity_type, entity_id, tag_id)
SELECT DISTINCT u.entity_type, u.entity_id, tg.id
FROM usable u
-- A data-modifying CTE's rows are not visible to reads of the same table in
-- the same statement, so the join takes minted's RETURNING for the tags this
-- statement created and the table's snapshot for the ones that already
-- existed.
JOIN (SELECT id, org_id, slug FROM minted
      UNION
      SELECT id, org_id, slug FROM tags) tg
  ON tg.org_id = u.org_id AND tg.slug = u.slug
ON CONFLICT DO NOTHING;

ALTER TABLE tickets DROP COLUMN labels;
ALTER TABLE project_items DROP COLUMN labels;

-- The workflow vocabulary converts rather than dying with the column: a
-- stored guard or post-function on 'labels' would otherwise become "field not
-- present" — refusing every transition that carries it, forever, with no
-- error a person could act on. The field key becomes 'tags', backed by
-- entity_tags. Constraint first, rows second, constraint back third: the old
-- CHECK does not admit 'tags' and the new one does not admit 'labels', so no
-- ordering with the constraint in place lets the UPDATE run.

ALTER TABLE workflow_transition_guards
    DROP CONSTRAINT workflow_transition_guards_field_key_valid;
UPDATE workflow_transition_guards SET field_key = 'tags' WHERE field_key = 'labels';
ALTER TABLE workflow_transition_guards
    ADD CONSTRAINT workflow_transition_guards_field_key_valid
    CHECK (field_key IS NULL OR field_key IN (
        'assignee_id',
        'due_at',
        'description',
        'tags'
    ));

ALTER TABLE workflow_transition_post_functions
    DROP CONSTRAINT workflow_transition_post_functions_field_key_valid;
UPDATE workflow_transition_post_functions SET field_key = 'tags' WHERE field_key = 'labels';
ALTER TABLE workflow_transition_post_functions
    ADD CONSTRAINT workflow_transition_post_functions_field_key_valid
    CHECK (field_key IS NULL OR field_key IN ('due_at', 'tags'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- LOSSY, in both halves, and deliberately stated rather than implied:
--
--   * ticket and project_item tag associations have no representation in the
--     page-only shape and are deleted below;
--   * the labels columns come back EMPTY — the up migration folded their
--     values into org tags, and unpicking "which tag row came from which
--     array" is not recorded anywhere. A round trip down and up therefore
--     loses every pre-migration label value.

-- The workflow vocabulary converts back first, while the rows still exist.
ALTER TABLE workflow_transition_guards
    DROP CONSTRAINT workflow_transition_guards_field_key_valid;
UPDATE workflow_transition_guards SET field_key = 'labels' WHERE field_key = 'tags';
ALTER TABLE workflow_transition_guards
    ADD CONSTRAINT workflow_transition_guards_field_key_valid
    CHECK (field_key IS NULL OR field_key IN (
        'assignee_id',
        'due_at',
        'description',
        'labels'
    ));

ALTER TABLE workflow_transition_post_functions
    DROP CONSTRAINT workflow_transition_post_functions_field_key_valid;
UPDATE workflow_transition_post_functions SET field_key = 'labels' WHERE field_key = 'tags';
ALTER TABLE workflow_transition_post_functions
    ADD CONSTRAINT workflow_transition_post_functions_field_key_valid
    CHECK (field_key IS NULL OR field_key IN ('due_at', 'labels'));

ALTER TABLE tickets ADD COLUMN labels TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE project_items ADD COLUMN labels TEXT[] NOT NULL DEFAULT '{}';

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
