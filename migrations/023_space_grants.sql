-- +goose Up
-- +goose StatementBegin

-- Space grants (v0.3 spec §4, migration 023): explicit access, expanded on
-- the subject side (ADR-0007). subject_id is polymorphic (user or team) so
-- it carries no FK — the store layer owns that integrity: a user's grants
-- are deleted in the same transaction as the user, and a grant to a
-- non-org-member is rejected with 400.
CREATE TABLE space_grants (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    space_id     UUID NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL,
    subject_id   UUID NOT NULL,
    role         TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by   UUID REFERENCES users(id),
    UNIQUE (space_id, subject_type, subject_id),
    CONSTRAINT space_grants_subject_valid CHECK (subject_type IN ('user','team')),
    CONSTRAINT space_grants_role_valid
        CHECK (role IN ('viewer','contributor','agent','space_admin'))
);

CREATE INDEX space_grants_space_idx   ON space_grants (space_id);
CREATE INDEX space_grants_subject_idx ON space_grants (subject_type, subject_id);

ALTER TABLE spaces ADD COLUMN owner_team_id UUID REFERENCES teams(id) ON DELETE RESTRICT;
ALTER TABLE spaces ADD COLUMN visibility TEXT NOT NULL DEFAULT 'discoverable';

-- Backfill before the NOT NULL applies: every existing space is owned by its
-- org's default team, seeded in 022. owner_team_id grants nothing by itself —
-- it drives picker grouping and guarantees no orphaned space.
UPDATE spaces s SET owner_team_id = (
    SELECT t.id FROM teams t WHERE t.org_id = s.org_id AND t.is_default LIMIT 1
) WHERE owner_team_id IS NULL;

ALTER TABLE spaces ALTER COLUMN owner_team_id SET NOT NULL;
ALTER TABLE spaces ADD CONSTRAINT spaces_visibility_valid
    CHECK (visibility IN ('hidden','discoverable','org'));

CREATE INDEX spaces_owner_team_idx ON spaces (owner_team_id) WHERE deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 023's up drops no pre-existing constraint, so the down only removes what
-- it added: the two spaces columns (their FK, CHECK, and index go with them)
-- and the space_grants table. This restores the exact post-022 state.
ALTER TABLE spaces DROP CONSTRAINT spaces_visibility_valid;
ALTER TABLE spaces DROP COLUMN visibility;
ALTER TABLE spaces DROP COLUMN owner_team_id;
DROP TABLE space_grants;

-- +goose StatementEnd
