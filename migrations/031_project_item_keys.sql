-- +goose Up
-- +goose StatementBegin

-- ── Vector item keys (VEC-123) ───────────────────────────────────────────────
-- Every project item gets a permanent, human-readable key: <SPACE_KEY>-<n>,
-- where <n> is a per-space monotonic counter. Keys are assigned at creation
-- inside the creation transaction, are immutable, and are org-unique. They are
-- the reference-mapping substrate for the future Jira importer, which will map
-- external keys onto items by (org_id, item_key).

-- Per-space counter. A dedicated counter row — bumped by an atomic upsert in the
-- same statement that inserts the item — makes assignment concurrency-safe: the
-- ON CONFLICT DO UPDATE row-locks the counter, serialising concurrent creators
-- for a space so they queue rather than collide on UNIQUE (space_id, number).
-- last_number is the highest number handed out so far (0 before the first item).
CREATE TABLE project_item_sequences (
    space_id    UUID   PRIMARY KEY REFERENCES spaces (id) ON DELETE CASCADE,
    last_number BIGINT NOT NULL
);

-- Seed counters from existing items (soft-deleted included, so numbers are never
-- reused) so freshly-assigned numbers continue the per-space sequence.
INSERT INTO project_item_sequences (space_id, last_number)
SELECT space_id, MAX(number)
FROM project_items
GROUP BY space_id;

-- org_id is denormalised onto the item so keys can be org-unique at the database
-- level — project_items has no org column of its own (org is reached via spaces)
-- — and so the importer can resolve an external key to an item within an org in
-- a single indexed lookup.
ALTER TABLE project_items ADD COLUMN org_id UUID REFERENCES organizations (id);
UPDATE project_items pi SET org_id = s.org_id FROM spaces s WHERE pi.space_id = s.id;
ALTER TABLE project_items ALTER COLUMN org_id SET NOT NULL;

-- item_key: permanent human-readable key. Backfilled in creation order — number
-- is already assigned per-space in creation order (created_at, id tiebreak) by
-- migration 014's ROW_NUMBER backfill and the adapter's monotonic assignment
-- since, so <SPACE_KEY>-<number> reproduces creation order deterministically.
-- Deriving the key from the stored number (rather than a fresh ROW_NUMBER) keeps
-- item_key and number in lockstep and makes this backfill idempotent: re-running
-- it yields byte-for-byte the same keys.
ALTER TABLE project_items ADD COLUMN item_key TEXT;
UPDATE project_items pi
SET item_key = s.key || '-' || pi.number
FROM spaces s
WHERE pi.space_id = s.id;
ALTER TABLE project_items ALTER COLUMN item_key SET NOT NULL;

-- Org-unique and (by immutability convention) permanent. Two orgs may reuse a
-- SPACE key, so uniqueness is scoped to the org rather than global.
CREATE UNIQUE INDEX idx_project_items_org_key ON project_items (org_id, item_key);
CREATE INDEX idx_project_items_org_id ON project_items (org_id) WHERE deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_project_items_org_id;
DROP INDEX IF EXISTS idx_project_items_org_key;
ALTER TABLE project_items DROP COLUMN IF EXISTS item_key;
ALTER TABLE project_items DROP COLUMN IF EXISTS org_id;
DROP TABLE IF EXISTS project_item_sequences;
-- +goose StatementEnd
