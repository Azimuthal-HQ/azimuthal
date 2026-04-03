-- +goose Up
-- +goose StatementBegin
CREATE TABLE notifications (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID        NOT NULL REFERENCES users (id),
    kind         TEXT        NOT NULL,
    title        TEXT        NOT NULL,
    body         TEXT,
    entity_kind  TEXT,
    entity_id    UUID,
    is_read      BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at      TIMESTAMPTZ
);

CREATE INDEX idx_notifications_user_id ON notifications (user_id, is_read, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notifications;
-- +goose StatementEnd
