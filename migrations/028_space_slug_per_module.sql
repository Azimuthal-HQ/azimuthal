-- +goose Up
-- +goose StatementBegin

-- Space slug uniqueness is per (org, module), not per org. A team called
-- DevOps legitimately wants a Beacon desk, a Vector board, and a Codex wiki
-- all slugged "devops"; the org-wide constraint forced names like
-- devops-vector. Module (type) is part of a space's identity — routes are
-- /:module/:spaceId — so nothing downstream is ambiguous.
--
-- spaces_org_id_slug_key is the auto-generated name of the inline
-- UNIQUE (org_id, slug) from 003_spaces.sql, confirmed against pg_constraint
-- on a live database (the same verification 021 did for spaces_type_check).
-- Like the constraint it replaces, the new one is deliberately NOT partial on
-- deleted_at: soft-deleted spaces keep holding their slug within a module.
ALTER TABLE spaces DROP CONSTRAINT spaces_org_id_slug_key;

ALTER TABLE spaces ADD CONSTRAINT spaces_org_id_type_slug_key
    UNIQUE (org_id, type, slug);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- This down migration FAILS BY DESIGN on any database where the same slug
-- now exists in more than one module of an org — re-adding UNIQUE (org_id,
-- slug) cannot succeed while such rows exist. That is the correct outcome:
-- a migration must refuse loudly rather than silently rename or drop
-- someone's space. Rolling back on such a database requires a human to first
-- decide which spaces to re-slug:
--
--   SELECT org_id, slug, array_agg(type ORDER BY type)
--   FROM spaces GROUP BY org_id, slug HAVING count(*) > 1;
ALTER TABLE spaces DROP CONSTRAINT spaces_org_id_type_slug_key;

ALTER TABLE spaces ADD CONSTRAINT spaces_org_id_slug_key
    UNIQUE (org_id, slug);

-- +goose StatementEnd
