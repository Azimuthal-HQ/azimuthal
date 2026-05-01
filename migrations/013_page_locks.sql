-- +goose Up
-- +goose StatementBegin

CREATE TABLE page_locks (
    page_id     UUID        PRIMARY KEY REFERENCES pages(id) ON DELETE CASCADE,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_name   TEXT        NOT NULL DEFAULT '',
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_page_locks_expires ON page_locks (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS page_locks;
-- +goose StatementEnd
