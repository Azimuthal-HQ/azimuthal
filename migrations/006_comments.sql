-- +goose Up
-- +goose StatementBegin
CREATE TABLE comments (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id     UUID        REFERENCES items (id),
    page_id     UUID        REFERENCES pages (id),
    parent_id   UUID        REFERENCES comments (id),
    author_id   UUID        NOT NULL REFERENCES users (id),
    body        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    CONSTRAINT comments_must_have_target CHECK (
        (item_id IS NOT NULL)::INT + (page_id IS NOT NULL)::INT = 1
    )
);

CREATE INDEX idx_comments_item_id   ON comments (item_id)   WHERE deleted_at IS NULL AND item_id IS NOT NULL;
CREATE INDEX idx_comments_page_id   ON comments (page_id)   WHERE deleted_at IS NULL AND page_id IS NOT NULL;
CREATE INDEX idx_comments_parent_id ON comments (parent_id) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS comments;
-- +goose StatementEnd
