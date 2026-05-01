-- +goose Up
-- +goose StatementBegin
ALTER TABLE items ADD COLUMN search_vector TSVECTOR
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B')
    ) STORED;

CREATE INDEX idx_items_search ON items USING GIN (search_vector) WHERE deleted_at IS NULL;

ALTER TABLE pages ADD COLUMN search_vector TSVECTOR
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(content, '')), 'B')
    ) STORED;

CREATE INDEX idx_pages_search ON pages USING GIN (search_vector) WHERE deleted_at IS NULL;

ALTER TABLE items
    ADD CONSTRAINT fk_items_sprint_id
    FOREIGN KEY (sprint_id) REFERENCES sprints (id)
    ON DELETE SET NULL;

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_organizations_updated_at
    BEFORE UPDATE ON organizations FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_spaces_updated_at
    BEFORE UPDATE ON spaces FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_items_updated_at
    BEFORE UPDATE ON items FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_pages_updated_at
    BEFORE UPDATE ON pages FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_comments_updated_at
    BEFORE UPDATE ON comments FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_sprints_updated_at
    BEFORE UPDATE ON sprints FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_sprints_updated_at   ON sprints;
DROP TRIGGER IF EXISTS trg_comments_updated_at  ON comments;
DROP TRIGGER IF EXISTS trg_pages_updated_at     ON pages;
DROP TRIGGER IF EXISTS trg_items_updated_at     ON items;
DROP TRIGGER IF EXISTS trg_spaces_updated_at    ON spaces;
DROP TRIGGER IF EXISTS trg_users_updated_at     ON users;
DROP TRIGGER IF EXISTS trg_organizations_updated_at ON organizations;
DROP FUNCTION IF EXISTS set_updated_at();
ALTER TABLE items DROP CONSTRAINT IF EXISTS fk_items_sprint_id;
DROP INDEX IF EXISTS idx_pages_search;
DROP INDEX IF EXISTS idx_items_search;
ALTER TABLE pages DROP COLUMN IF EXISTS search_vector;
ALTER TABLE items DROP COLUMN IF EXISTS search_vector;
-- +goose StatementEnd
