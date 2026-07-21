-- +goose Up
-- +goose StatementBegin

-- Entity shares (v0.3 spec §4, ADR-0008; the spec's sketch is numbered 024
-- but P2.5 took 024–025, so shares ship as 026). Shares widen, never
-- narrow: an entity may be MORE visible than its space, never less.
--
-- Polymorphic (entity_type, entity_id) with no FK — the store layer owns
-- integrity, and revoke-on-delete/revoke-on-move run in the same
-- transaction as the entity mutation. Revocation sets revoked_at; rows are
-- never hard-deleted, so the audit trail survives. Expiry is evaluated in
-- the resolution query — a share past expires_at stops granting access on
-- the very next request, with no sweeper involved.
CREATE TABLE entity_shares (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    space_id    UUID NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    entity_id   UUID NOT NULL,
    audience    TEXT NOT NULL,
    audience_id UUID REFERENCES teams(id) ON DELETE CASCADE,
    cascade     BOOLEAN NOT NULL DEFAULT false,
    expires_at  TIMESTAMPTZ,
    created_by  UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ,
    CONSTRAINT entity_shares_type_valid
        CHECK (entity_type IN ('page','ticket','project_item')),
    CONSTRAINT entity_shares_audience_valid CHECK (audience IN ('org','team')),
    CONSTRAINT entity_shares_audience_id_present
        CHECK ((audience = 'team') = (audience_id IS NOT NULL)),
    CONSTRAINT entity_shares_cascade_pages_only
        CHECK (cascade = false OR entity_type = 'page'),
    CONSTRAINT entity_shares_expiry_future
        CHECK (expires_at IS NULL OR expires_at > created_at)
);

-- One ACTIVE share per (entity, audience) cell; revoked rows do not block
-- re-sharing. audience_id is NULL for org audience, hence the COALESCE.
CREATE UNIQUE INDEX entity_shares_unique_active
    ON entity_shares (entity_type, entity_id, audience,
                      COALESCE(audience_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE revoked_at IS NULL;

CREATE INDEX entity_shares_entity_idx   ON entity_shares (entity_type, entity_id)
    WHERE revoked_at IS NULL;
CREATE INDEX entity_shares_audience_idx ON entity_shares (audience, audience_id)
    WHERE revoked_at IS NULL;
CREATE INDEX entity_shares_expiry_idx   ON entity_shares (expires_at)
    WHERE revoked_at IS NULL AND expires_at IS NOT NULL;
-- Not in the spec sketch: the per-request resolution query and the org
-- share listing both filter on org_id first; without this every resolution
-- is a full-table scan once several orgs share an instance.
CREATE INDEX entity_shares_org_idx ON entity_shares (org_id)
    WHERE revoked_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE entity_shares;

-- +goose StatementEnd
