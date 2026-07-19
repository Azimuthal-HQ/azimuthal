-- +goose Up
-- +goose StatementBegin

-- Teams (v0.3 spec §4, migration 022): materialised path by ID, consistent
-- with the Codex page hierarchy. Descendant expansion is a single GIN-indexed
-- query (path && ARRAY[...]) and the depth limit is a database constraint,
-- not application trust. path is always parent.path || id — the store layer
-- owns that construction and reparenting rewrites whole subtrees in one
-- transaction.
CREATE TABLE teams (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    parent_id   UUID REFERENCES teams(id) ON DELETE RESTRICT,
    path        UUID[] NOT NULL,
    slug        TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_default  BOOLEAN NOT NULL DEFAULT false,
    source      TEXT NOT NULL DEFAULT 'manual',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,
    UNIQUE (org_id, slug),
    CONSTRAINT teams_depth_max      CHECK (array_length(path, 1) BETWEEN 1 AND 5),
    CONSTRAINT teams_path_ends_self CHECK (path[array_length(path, 1)] = id),
    CONSTRAINT teams_source_valid   CHECK (source IN ('manual','scim','oidc'))
);

CREATE INDEX teams_path_gin ON teams USING GIN (path);
CREATE INDEX teams_org_idx  ON teams (org_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX teams_one_default ON teams (org_id)
    WHERE is_default AND deleted_at IS NULL;

CREATE TABLE team_members (
    team_id    UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member',
    is_primary BOOLEAN NOT NULL DEFAULT false,
    source     TEXT NOT NULL DEFAULT 'manual',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id),
    CONSTRAINT team_members_role_valid   CHECK (role   IN ('member','lead')),
    CONSTRAINT team_members_source_valid CHECK (source IN ('manual','scim','oidc'))
);

CREATE UNIQUE INDEX team_members_one_primary ON team_members (user_id, org_id)
    WHERE is_primary;
CREATE INDEX team_members_user_idx ON team_members (user_id, org_id);

-- Bootstrap ordering (ADR-0006 point 4): every org needs a default team
-- before 023 can make spaces.owner_team_id NOT NULL, and every existing
-- member must not be left teamless. Soft-deleted orgs are included — their
-- space rows still exist, and 023's constraint applies to every row.
INSERT INTO teams (id, org_id, parent_id, path, slug, name, description, is_default)
SELECT seed.id, seed.org_id, NULL, ARRAY[seed.id], 'default', 'Default',
       'Org default team. Every member belongs here until assigned elsewhere.',
       true
FROM (SELECT gen_random_uuid() AS id, o.id AS org_id FROM organizations o) AS seed;

-- Existing org members join their org's default team as their primary team.
INSERT INTO team_members (team_id, user_id, org_id, is_primary)
SELECT t.id, m.user_id, m.org_id, true
FROM memberships m
JOIN teams t ON t.org_id = m.org_id AND t.is_default;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE team_members;
DROP TABLE teams;
-- +goose StatementEnd
