-- +goose Up
-- P2.8: Add per-space item number (like Jira's PROJ-42)

ALTER TABLE items ADD COLUMN IF NOT EXISTS number INT;

-- Backfill existing rows with sequential numbers per space ordered by created_at
WITH ranked AS (
  SELECT id,
         ROW_NUMBER() OVER (PARTITION BY space_id ORDER BY created_at) AS rn
  FROM items
  WHERE deleted_at IS NULL
)
UPDATE items
SET number = ranked.rn
FROM ranked
WHERE items.id = ranked.id AND items.number IS NULL;

-- Also backfill soft-deleted rows so the constraint holds
WITH ranked AS (
  SELECT id,
         ROW_NUMBER() OVER (PARTITION BY space_id ORDER BY created_at) AS rn
  FROM items
  WHERE deleted_at IS NOT NULL
)
UPDATE items
SET number = (
  SELECT COALESCE(MAX(number), 0) + ranked.rn
  FROM items i2
  WHERE i2.space_id = items.space_id AND i2.deleted_at IS NULL
)
FROM ranked
WHERE items.id = ranked.id AND items.number IS NULL;

-- Unique constraint per space
ALTER TABLE items ADD CONSTRAINT items_space_number_unique UNIQUE (space_id, number);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_item_number()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.number IS NULL THEN
    SELECT COALESCE(MAX(number), 0) + 1
    INTO NEW.number
    FROM items
    WHERE space_id = NEW.space_id;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER trg_items_set_number
  BEFORE INSERT ON items
  FOR EACH ROW
  EXECUTE FUNCTION set_item_number();

-- +goose Down
DROP TRIGGER IF EXISTS trg_items_set_number ON items;
DROP FUNCTION IF EXISTS set_item_number();
ALTER TABLE items DROP CONSTRAINT IF EXISTS items_space_number_unique;
ALTER TABLE items DROP COLUMN IF EXISTS number;
