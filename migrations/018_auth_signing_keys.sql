-- +goose Up
-- +goose StatementBegin

-- RS256 JWT signing key, persisted in the database so a process or container
-- restart never regenerates the key and never invalidates live tokens.
-- Singleton row (id is always 1): concurrent first boots race safely via
-- INSERT ... ON CONFLICT DO NOTHING followed by a re-read.
CREATE TABLE auth_signing_keys (
    id              SMALLINT    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    private_key_pem TEXT        NOT NULL,
    algorithm       TEXT        NOT NULL DEFAULT 'RS256',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS auth_signing_keys;
-- +goose StatementEnd
