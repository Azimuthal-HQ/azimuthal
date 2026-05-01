-- +goose Up
-- +goose StatementBegin

ALTER TABLE spaces ADD COLUMN key TEXT;

-- Backfill: strip non-alphanumeric chars, uppercase, max 8 chars.
-- e.g. "service_desk-abc" → "SERVICE", "hr-desk" → "HR"
UPDATE spaces
SET key = UPPER(
    SUBSTRING(
        REGEXP_REPLACE(SPLIT_PART(slug, '-', 1), '[^a-zA-Z0-9]', '', 'g')
        FROM 1 FOR 8
    )
);

-- Any slug that produced an empty key falls back to 'SPACE'.
UPDATE spaces SET key = 'SPACE' WHERE key = '' OR key IS NULL;

-- Resolve within-org duplicates by appending a numeric suffix.
-- Oldest space keeps the plain key; subsequent ones get key2, key3, …
WITH ranked AS (
    SELECT id,
           key AS base_key,
           ROW_NUMBER() OVER (PARTITION BY org_id, key ORDER BY created_at) AS rn
    FROM spaces
)
UPDATE spaces s
SET key = SUBSTRING(r.base_key FROM 1 FOR 8) || r.rn::text
FROM ranked r
WHERE s.id = r.id AND r.rn > 1;

ALTER TABLE spaces ALTER COLUMN key SET NOT NULL;
ALTER TABLE spaces ADD CONSTRAINT spaces_key_format CHECK (key ~ '^[A-Z0-9]{1,10}$');
CREATE UNIQUE INDEX idx_spaces_org_key ON spaces (org_id, key) WHERE deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_spaces_org_key;
ALTER TABLE spaces DROP CONSTRAINT IF EXISTS spaces_key_format;
ALTER TABLE spaces DROP COLUMN IF EXISTS key;
-- +goose StatementEnd
