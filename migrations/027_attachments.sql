-- +goose Up
-- +goose StatementBegin

-- Attachments (P3, ADR-0008 rule 3: "attachments follow the entity").
-- Until now the ObjectStore had no production caller (known-issues #16);
-- the shared read path forces the issue — a shared page with an image must
-- render for a viewer with no space access, and that read path must not be
-- a way to fetch arbitrary object keys. Every read therefore goes through
-- this table: the handler looks the attachment up by id under its entity,
-- derives the object key from the ROW (orgs/{org_id}/attachments/{id}),
-- and never accepts a key from the client.
--
-- No space_id column on purpose: authorisation always derives from the
-- entity the attachment hangs off, so a cross-space entity move cannot
-- strand a stale copy of the container here.
CREATE TABLE attachments (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    entity_type  TEXT NOT NULL,
    entity_id    UUID NOT NULL,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    object_key   TEXT NOT NULL,
    created_by   UUID NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    CONSTRAINT attachments_type_valid
        CHECK (entity_type IN ('page','ticket','project_item')),
    CONSTRAINT attachments_filename_present CHECK (length(filename) > 0),
    CONSTRAINT attachments_size_nonnegative CHECK (size_bytes >= 0),
    CONSTRAINT attachments_object_key_unique UNIQUE (object_key)
);

CREATE INDEX attachments_entity_idx ON attachments (entity_type, entity_id)
    WHERE deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE attachments;

-- +goose StatementEnd
