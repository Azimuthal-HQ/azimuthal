-- +goose Up
-- +goose StatementBegin

-- Credential links: one possession-based, single-use, self-expiring token that
-- backs three internal-user credential handoffs — an admin-issued sign-in link
-- (the account is created, the user sets their own password), a password reset
-- for deployments with no SSO/LDAP, and the confirmation step that closes the
-- authenticated email-change vector (security finding C.2-c: the old path
-- rebound an account's email with no reauthentication and no token_generation
-- bump).
--
-- Shaped on requester_magic_links (migration 044) VERBATIM, because it is the
-- same problem this repository has already solved once, correctly: a credential
-- delivered out of band, hashed at rest, redeemed once, expiring on its own.
-- Same construction — 32 bytes of crypto/rand as URL-safe base64, only the
-- SHA-256 hex digest stored, the raw token returned exactly once and never
-- persisted or logged.
--
-- Three lifecycle columns, not one, for the reason 044 gives: "why did my link
-- stop working" has three different answers and collapsing them makes the audit
-- trail unreadable —
--
--   consumed_at      redeemed (the single-use terminus)
--   invalidated_at   superseded, because a newer link was issued for the same
--                    user and purpose (issue-supersedes-outstanding)
--   expires_at       ran out of time
--
-- SINGLE USE IS ENFORCED BY A GUARDED UPDATE, NOT BY A PRE-CHECK. The redemption
-- statement (ConsumeCredentialLink) carries `WHERE consumed_at IS NULL AND
-- invalidated_at IS NULL AND expires_at > now()` and returns the row only when it
-- wins; zero rows means somebody, or time, got there first. Checking then
-- updating would let two concurrent redemptions of one link both succeed — the
-- race 044 and invites (migration 024) were both written to close.
CREATE TABLE credential_links (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The account the link acts on. ON DELETE CASCADE: a deleted user's
    -- outstanding links are meaningless and go with them.
    user_id        UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose        TEXT        NOT NULL,
    token_hash     TEXT        NOT NULL,
    -- The email_change payload — the address to bind on consume. NULL for the
    -- other two purposes; the CHECK below makes that structural rather than a
    -- convention a handler has to remember.
    new_email      TEXT,
    expires_at     TIMESTAMPTZ NOT NULL,
    consumed_at    TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ,
    -- The admin who minted the link (sign-in / reset issuance), or the user
    -- themselves (email change). NULL for the unauthenticated forgot-password
    -- path, which has no actor. Nullable, and ON no cascade: keep the audit
    -- fact even if the issuing admin is later removed. SET NULL on delete.
    created_by     UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT credential_links_purpose_valid
        CHECK (purpose IN ('signin', 'password_reset', 'email_change')),
    -- email_change carries a new address; the other two never do.
    CONSTRAINT credential_links_payload_shape CHECK (
        (purpose = 'email_change' AND new_email IS NOT NULL AND new_email <> '')
        OR (purpose <> 'email_change' AND new_email IS NULL)
    )
);

-- Global unique index on the hash: only the digest is stored, and a redemption
-- resolves a link by its hash alone (ConsumeCredentialLink WHERE token_hash).
CREATE UNIQUE INDEX credential_links_token_hash_idx ON credential_links (token_hash);

-- Answers "this user's outstanding links of this purpose" by prefix, which is
-- exactly the predicate InvalidateOutstandingCredentialLinks filters on, and
-- keeps the supersede UPDATE cheap.
CREATE INDEX credential_links_user_purpose_idx ON credential_links (user_id, purpose)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE credential_links;

-- +goose StatementEnd
