-- +goose Up
-- +goose StatementBegin
CREATE TABLE pages (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id    UUID        NOT NULL REFERENCES spaces (id),
    parent_id   UUID        REFERENCES pages (id),
    title       TEXT        NOT NULL,
    content     TEXT        NOT NULL DEFAULT '',
    version     INTEGER     NOT NULL DEFAULT 1,
    author_id   UUID        NOT NULL REFERENCES users (id),
    position    INTEGER     NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX idx_pages_space_id  ON pages (space_id)  WHERE deleted_at IS NULL;
CREATE INDEX idx_pages_parent_id ON pages (parent_id) WHERE deleted_at IS NULL;

CREATE TABLE page_revisions (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id     UUID        NOT NULL REFERENCES pages (id),
    version     INTEGER     NOT NULL,
    title       TEXT        NOT NULL,
    content     TEXT        NOT NULL,
    author_id   UUID        NOT NULL REFERENCES users (id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (page_id, version)
);

CREATE INDEX idx_page_revisions_page_id ON page_revisions (page_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS page_revisions;
DROP TABLE IF EXISTS pages;
-- +goose StatementEnd
