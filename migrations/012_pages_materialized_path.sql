-- +goose Up
-- +goose StatementBegin

-- Add materialized path column to pages.
-- Format: dot-separated UUID segments from root to self, e.g. "{root_id}.{parent_id}.{my_id}"
-- Root pages have path equal to their own ID.
ALTER TABLE pages ADD COLUMN path TEXT NOT NULL DEFAULT '';

-- Backfill paths for existing pages using a recursive CTE.
-- This handles any depth of existing tree.
WITH RECURSIVE page_tree AS (
    -- Root pages (no parent)
    SELECT id, parent_id, id::TEXT AS path
    FROM pages
    WHERE parent_id IS NULL

    UNION ALL

    -- Child pages
    SELECT p.id, p.parent_id, pt.path || '.' || p.id::TEXT
    FROM pages p
    JOIN page_tree pt ON p.parent_id = pt.id
)
UPDATE pages p
SET path = pt.path
FROM page_tree pt
WHERE p.id = pt.id;

-- Efficient lookup by space + path prefix (tree queries)
CREATE INDEX idx_pages_path ON pages (space_id, path) WHERE deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_pages_path;
ALTER TABLE pages DROP COLUMN IF EXISTS path;
-- +goose StatementEnd
