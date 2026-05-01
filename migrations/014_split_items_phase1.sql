-- +goose Up
-- +goose StatementBegin

-- ── Tickets table (service-desk specific) ────────────────────────────────────
CREATE TABLE tickets (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id     UUID        NOT NULL REFERENCES spaces (id),
    number       INT         NOT NULL,
    title        TEXT        NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL DEFAULT 'open',
    priority     TEXT        NOT NULL DEFAULT 'medium' CHECK (priority IN ('urgent','high','medium','low')),
    reporter_id  UUID        NOT NULL REFERENCES users (id),
    assignee_id  UUID        REFERENCES users (id),
    labels       TEXT[]      NOT NULL DEFAULT '{}',
    due_at       TIMESTAMPTZ,
    resolved_at  TIMESTAMPTZ,
    rank         TEXT        NOT NULL DEFAULT '0|aaaaaa:',
    search_vector TSVECTOR,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    UNIQUE (space_id, number)
);

CREATE INDEX idx_tickets_space_id    ON tickets (space_id)    WHERE deleted_at IS NULL;
CREATE INDEX idx_tickets_assignee_id ON tickets (assignee_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_tickets_status      ON tickets (space_id, status) WHERE deleted_at IS NULL;
CREATE INDEX idx_tickets_search      ON tickets USING GIN (search_vector) WHERE deleted_at IS NULL;

CREATE OR REPLACE FUNCTION update_tickets_search_vector()
RETURNS TRIGGER AS $$
BEGIN
    NEW.search_vector := to_tsvector('english', coalesce(NEW.title, '') || ' ' || coalesce(NEW.description, ''));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER tickets_search_vector_update
    BEFORE INSERT OR UPDATE ON tickets
    FOR EACH ROW EXECUTE FUNCTION update_tickets_search_vector();

-- ── Project items table ──────────────────────────────────────────────────────
CREATE TABLE project_items (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id     UUID        NOT NULL REFERENCES spaces (id),
    parent_id    UUID        REFERENCES project_items (id),
    number       INT         NOT NULL,
    kind         TEXT        NOT NULL CHECK (kind IN ('task','story','epic','bug')),
    title        TEXT        NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL DEFAULT 'open',
    priority     TEXT        NOT NULL DEFAULT 'medium' CHECK (priority IN ('urgent','high','medium','low')),
    reporter_id  UUID        NOT NULL REFERENCES users (id),
    assignee_id  UUID        REFERENCES users (id),
    sprint_id    UUID        REFERENCES sprints (id) ON DELETE SET NULL,
    labels       TEXT[]      NOT NULL DEFAULT '{}',
    due_at       TIMESTAMPTZ,
    resolved_at  TIMESTAMPTZ,
    rank         TEXT        NOT NULL DEFAULT '0|aaaaaa:',
    search_vector TSVECTOR,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    UNIQUE (space_id, number)
);

CREATE INDEX idx_project_items_space_id    ON project_items (space_id)    WHERE deleted_at IS NULL;
CREATE INDEX idx_project_items_assignee_id ON project_items (assignee_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_project_items_status      ON project_items (space_id, status) WHERE deleted_at IS NULL;
CREATE INDEX idx_project_items_sprint_id   ON project_items (sprint_id)   WHERE deleted_at IS NULL AND sprint_id IS NOT NULL;
CREATE INDEX idx_project_items_search      ON project_items USING GIN (search_vector) WHERE deleted_at IS NULL;

CREATE OR REPLACE FUNCTION update_project_items_search_vector()
RETURNS TRIGGER AS $$
BEGIN
    NEW.search_vector := to_tsvector('english', coalesce(NEW.title, '') || ' ' || coalesce(NEW.description, ''));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER project_items_search_vector_update
    BEFORE INSERT OR UPDATE ON project_items
    FOR EACH ROW EXECUTE FUNCTION update_project_items_search_vector();

-- ── Backfill tickets from items (kind = 'ticket') ────────────────────────────
INSERT INTO tickets (id, space_id, number, title, description, status, priority,
                     reporter_id, assignee_id, labels, due_at, resolved_at, rank,
                     created_at, updated_at, deleted_at)
SELECT id, space_id,
       COALESCE(number, ROW_NUMBER() OVER (PARTITION BY space_id ORDER BY created_at)),
       title,
       COALESCE(description, ''),
       status, priority, reporter_id, assignee_id, labels, due_at, resolved_at,
       CASE WHEN rank = '' THEN '0|aaaaaa:' ELSE rank END,
       created_at, updated_at, deleted_at
FROM items
WHERE kind = 'ticket';

-- ── Backfill project_items from items (kind IN task/story/epic/bug) ──────────
INSERT INTO project_items (id, space_id, parent_id, number, kind, title, description, status,
                           priority, reporter_id, assignee_id, sprint_id, labels, due_at,
                           resolved_at, rank, created_at, updated_at, deleted_at)
SELECT id, space_id, parent_id,
       COALESCE(number, ROW_NUMBER() OVER (PARTITION BY space_id ORDER BY created_at)),
       kind, title,
       COALESCE(description, ''),
       status, priority, reporter_id, assignee_id, sprint_id, labels, due_at, resolved_at,
       CASE WHEN rank = '' THEN '0|aaaaaa:' ELSE rank END,
       created_at, updated_at, deleted_at
FROM items
WHERE kind IN ('task', 'story', 'epic', 'bug');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS project_items;
DROP TABLE IF EXISTS tickets;
-- +goose StatementEnd
