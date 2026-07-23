-- +goose Up
-- +goose StatementBegin

-- ── Vector item types ────────────────────────────────────────────────────────
-- Org-scoped, admin-editable item types (task, story, bug, epic by default).
-- Per ADR-0003 the type stays a discriminator column on project_items rather
-- than a joined entity: project_items.kind holds the type's slug (its immutable
-- identity). item_types carries the mutable display name, ordering, and archive
-- state. Renames change item_types.name only — never the slug, so item rows are
-- never rewritten.
CREATE TABLE item_types (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    slug        TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    position    INT         NOT NULL DEFAULT 0,
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT item_types_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9_]*$'),
    UNIQUE (org_id, slug)
);

CREATE INDEX idx_item_types_org ON item_types (org_id) WHERE archived_at IS NULL;

-- Seed the default set for every existing org. New orgs are seeded at
-- provisioning time (adapters.ItemTypeAdapter.SeedDefaultTypes / the
-- SeedDefaultItemTypes query), which is idempotent.
INSERT INTO item_types (org_id, slug, name, position)
SELECT o.id, d.slug, d.name, d.position
FROM organizations o
CROSS JOIN (VALUES
    ('task',  'Task',  1),
    ('story', 'Story', 2),
    ('bug',   'Bug',   3),
    ('epic',  'Epic',  4)
) AS d(slug, name, position);

-- project_items.kind is the item's type identity (an immutable slug). It was a
-- fixed 4-value CHECK; item types are now org-editable, so the CHECK is dropped
-- and the vocabulary is governed by item_types. Existing kind values already
-- match seeded slugs, so no item rows are rewritten (kind is PRESERVED, not
-- reset to 'task'). Referential integrity and the "a referenced type cannot be
-- hard-deleted" rule are enforced in the item-types service rather than a DB FK,
-- so that ordinary item inserts are not coupled to per-org type seeding.
ALTER TABLE project_items DROP CONSTRAINT IF EXISTS project_items_kind_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE project_items ADD CONSTRAINT project_items_kind_check
    CHECK (kind IN ('task', 'story', 'epic', 'bug'));
DROP TABLE IF EXISTS item_types;
-- +goose StatementEnd
