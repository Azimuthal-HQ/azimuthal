-- +goose Up
-- +goose StatementBegin

-- Space type rebrand (v0.3 spec §4, migration "006" in the spec's table —
-- numbered 021 here because the shipped scaffold already ends at 020):
-- service_desk → beacon, wiki → codex, project → vector.
-- spaces_type_check is the auto-generated name of the inline CHECK from
-- 003_spaces.sql, confirmed against pg_constraint on a live database.
ALTER TABLE spaces DROP CONSTRAINT IF EXISTS spaces_type_check;

UPDATE spaces SET type = CASE type
    WHEN 'service_desk' THEN 'beacon'
    WHEN 'wiki'         THEN 'codex'
    WHEN 'project'      THEN 'vector'
    ELSE type
END;

ALTER TABLE spaces ADD CONSTRAINT spaces_type_valid
    CHECK (type IN ('beacon', 'codex', 'vector'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE spaces DROP CONSTRAINT spaces_type_valid;

UPDATE spaces SET type = CASE type
    WHEN 'beacon' THEN 'service_desk'
    WHEN 'codex'  THEN 'wiki'
    WHEN 'vector' THEN 'project'
    ELSE type
END;

-- Restore the original constraint under its original auto-generated name so
-- a down migration returns the schema to the exact post-020 state.
ALTER TABLE spaces ADD CONSTRAINT spaces_type_check
    CHECK (type IN ('project', 'wiki', 'service_desk'));

-- +goose StatementEnd
