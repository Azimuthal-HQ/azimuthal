-- +goose Up
-- +goose StatementBegin
CREATE TABLE spaces (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID        NOT NULL REFERENCES organizations (id),
    slug        TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    description TEXT,
    type        TEXT        NOT NULL CHECK (type IN ('project', 'wiki', 'service_desk')),
    icon        TEXT,
    is_private  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_by  UUID        NOT NULL REFERENCES users (id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    UNIQUE (org_id, slug)
);

CREATE INDEX idx_spaces_org_id ON spaces (org_id) WHERE deleted_at IS NULL;

CREATE TABLE space_members (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id   UUID        NOT NULL REFERENCES spaces (id),
    user_id    UUID        NOT NULL REFERENCES users (id),
    role       TEXT        NOT NULL DEFAULT 'member',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (space_id, user_id)
);

CREATE INDEX idx_space_members_space_id ON space_members (space_id);
CREATE INDEX idx_space_members_user_id  ON space_members (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_members;
DROP TABLE IF EXISTS spaces;
-- +goose StatementEnd
