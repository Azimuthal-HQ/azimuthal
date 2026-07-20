-- +goose Up
-- +goose StatementBegin

-- Invites and session control (P2.5, v0.3.2).
--
-- invites: the only way into an org besides open registration (which
-- defaults off from this release). The raw invite token is generated with
-- crypto/rand, shown to the admin exactly once, and never persisted —
-- token_hash stores its SHA-256, exactly as sessions.token_hash does, so a
-- database leak yields no usable invites.
CREATE TABLE invites (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    email            TEXT        NOT NULL,
    token_hash       TEXT        NOT NULL,
    org_role         TEXT        NOT NULL DEFAULT 'member',
    -- Optional initial team. NULL means the org default team. SET NULL on
    -- team deletion: the invite survives and falls back to the default team,
    -- which ADR-0006 guarantees always exists.
    team_id          UUID        REFERENCES teams (id) ON DELETE SET NULL,
    invited_by       UUID        NOT NULL REFERENCES users (id),
    expires_at       TIMESTAMPTZ NOT NULL,
    accepted_at      TIMESTAMPTZ,
    accepted_user_id UUID        REFERENCES users (id),
    revoked_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT invites_org_role_valid CHECK (org_role IN ('member', 'admin'))
);

-- One active invite per email per org. Active = neither accepted nor
-- revoked; an expired-but-active invite still holds the slot and is
-- refreshed in place by resend. Emails are normalised to lowercase at the
-- API boundary; lower() here backstops that.
CREATE UNIQUE INDEX invites_one_active_per_email
    ON invites (org_id, lower(email))
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- Acceptance looks up by hash; unique because a duplicate hash would mean a
-- duplicate token.
CREATE UNIQUE INDEX invites_token_hash_idx ON invites (token_hash);

CREATE INDEX invites_org_idx ON invites (org_id);

-- Session control: RS256 access tokens are stateless, so without this a
-- deactivated user stays authenticated until their token expires. The JWT
-- carries token_generation as a claim; the auth middleware compares it to
-- this column in a single indexed read (primary key) and rejects any
-- mismatch. Incrementing the column instantly kills every token the user
-- holds. Incremented on deactivation, force logout, and password change.
-- Existing users start at 0 — matching the claim on any token minted after
-- this release, and pre-existing tokens carry no claim (decoded as 0), so
-- the upgrade itself disrupts no session.
ALTER TABLE users ADD COLUMN token_generation INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE users DROP COLUMN token_generation;
DROP TABLE invites;

-- +goose StatementEnd
