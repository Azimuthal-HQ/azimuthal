-- +goose Up
-- +goose StatementBegin

-- ── Vector custom fields ─────────────────────────────────────────────────────
-- No custom-field storage existed in the repository (the phase brief assumed an
-- `item_fields` table; there was none — see spec-repo-reconciliation.md D48), so
-- this migration introduces both the definition schema and the value storage.
-- (This comment cited D46 until v0.4.2; D46 is the cross-space query-shape
-- entry and says nothing about custom fields. Comment-only correction — goose
-- never re-runs an applied migration, and nothing checksums migration files.)
--
-- Definitions are org-scoped (matching item_types, and keeping the admin surface
-- in one place). A field's slug is its immutable identity; the display name is
-- mutable. Values are stored by slug, not by a FK to the definition row, so a
-- value SURVIVES the archival or deletion of its definition and can be surfaced
-- read-only as a "legacy field" — the project's zero-silent-data-loss principle.
CREATE TABLE custom_field_defs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    slug        TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    field_type  TEXT        NOT NULL CHECK (field_type IN ('text', 'number', 'date', 'single_select')),
    options     JSONB       NOT NULL DEFAULT '[]'::jsonb,
    position    INT         NOT NULL DEFAULT 0,
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT custom_field_defs_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9_]*$'),
    UNIQUE (org_id, slug)
);

CREATE INDEX idx_custom_field_defs_org ON custom_field_defs (org_id) WHERE archived_at IS NULL;

-- One value per (item, field slug). value is stored as text; the definition's
-- field_type governs interpretation and write-time validation. Storing by slug
-- (rather than a FK to custom_field_defs.id) is deliberate: an item's stored
-- values outlive their definitions.
CREATE TABLE item_field_values (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id    UUID        NOT NULL REFERENCES project_items (id) ON DELETE CASCADE,
    field_slug TEXT        NOT NULL,
    value      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (item_id, field_slug)
);

CREATE INDEX idx_item_field_values_item ON item_field_values (item_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS item_field_values;
DROP TABLE IF EXISTS custom_field_defs;
-- +goose StatementEnd
