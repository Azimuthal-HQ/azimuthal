-- +goose Up
-- +goose StatementBegin
CREATE TABLE items (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id     UUID        NOT NULL REFERENCES spaces (id),
    parent_id    UUID        REFERENCES items (id),
    kind         TEXT        NOT NULL CHECK (kind IN ('ticket', 'task', 'story', 'epic', 'bug')),
    title        TEXT        NOT NULL,
    description  TEXT,
    status       TEXT        NOT NULL DEFAULT 'open',
    priority     TEXT        NOT NULL DEFAULT 'medium' CHECK (priority IN ('urgent', 'high', 'medium', 'low')),
    reporter_id  UUID        NOT NULL REFERENCES users (id),
    assignee_id  UUID        REFERENCES users (id),
    sprint_id    UUID,
    labels       TEXT[]      NOT NULL DEFAULT '{}',
    due_at       TIMESTAMPTZ,
    resolved_at  TIMESTAMPTZ,
    rank         TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

CREATE INDEX idx_items_space_id    ON items (space_id)    WHERE deleted_at IS NULL;
CREATE INDEX idx_items_assignee_id ON items (assignee_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_items_status      ON items (space_id, status) WHERE deleted_at IS NULL;
CREATE INDEX idx_items_sprint_id   ON items (sprint_id)   WHERE deleted_at IS NULL AND sprint_id IS NOT NULL;

CREATE TABLE item_relations (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    from_id     UUID        NOT NULL REFERENCES items (id),
    to_id       UUID        NOT NULL REFERENCES items (id),
    kind        TEXT        NOT NULL CHECK (kind IN ('blocks', 'is_blocked_by', 'duplicates', 'relates_to', 'wiki_link')),
    created_by  UUID        NOT NULL REFERENCES users (id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (from_id, to_id, kind)
);

CREATE INDEX idx_item_relations_from ON item_relations (from_id);
CREATE INDEX idx_item_relations_to   ON item_relations (to_id);

CREATE TABLE labels (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID        NOT NULL REFERENCES organizations (id),
    name       TEXT        NOT NULL,
    color      TEXT        NOT NULL DEFAULT '#6b7280',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE TABLE sprints (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id    UUID        NOT NULL REFERENCES spaces (id),
    name        TEXT        NOT NULL,
    goal        TEXT,
    status      TEXT        NOT NULL DEFAULT 'planned' CHECK (status IN ('planned', 'active', 'completed')),
    starts_at   TIMESTAMPTZ,
    ends_at     TIMESTAMPTZ,
    created_by  UUID        NOT NULL REFERENCES users (id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sprints_space_id ON sprints (space_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sprints;
DROP TABLE IF EXISTS labels;
DROP TABLE IF EXISTS item_relations;
DROP TABLE IF EXISTS items;
-- +goose StatementEnd
