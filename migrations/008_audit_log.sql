-- +goose Up
-- +goose StatementBegin
CREATE TABLE audit_log (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID        NOT NULL REFERENCES organizations (id),
    actor_id     UUID        REFERENCES users (id),
    action       TEXT        NOT NULL,
    entity_kind  TEXT        NOT NULL,
    entity_id    UUID        NOT NULL,
    payload      JSONB       NOT NULL DEFAULT '{}',
    ip_address   INET,
    user_agent   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_org_id   ON audit_log (org_id, created_at DESC);
CREATE INDEX idx_audit_log_actor_id ON audit_log (actor_id) WHERE actor_id IS NOT NULL;
CREATE INDEX idx_audit_log_entity   ON audit_log (entity_kind, entity_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_log;
-- +goose StatementEnd
