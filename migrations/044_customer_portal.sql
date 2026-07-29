-- +goose Up
-- +goose StatementBegin

-- Customer portal, part 1 of 2: the requester identity and its credential.
-- Migration 045 carries the comment-visibility half.
--
-- WHAT A REQUESTER IS, AND WHAT IT DELIBERATELY IS NOT. A requester is an
-- external person who raises and tracks requests against one service desk
-- without holding an account. It is NOT a user: there is no row in `users`,
-- no row in `memberships`, no row in `space_members`, no grant in
-- `space_grants`, and no team enrolment. That is not an omission to be
-- tidied up later — it is the security boundary. ADR-0007 resolves every
-- authorisation question from the subject's org membership and team
-- expansion, so a subject with no membership resolves to no access at all,
-- and `access.Can` can never return true for a requester no matter which
-- capability is asked about. The portal routes therefore carry their own
-- guard and never call `access.Can`; a requester is outside the capability
-- model rather than at the bottom of it.
--
-- The mechanical consequence is the one worth stating, because it is what
-- stops a portal credential from becoming an internal one: because there is
-- no `users` row, a portal token presented to the internal auth middleware
-- fails its live-state lookup (`GetUserAuthState` finds nothing) even before
-- the audience check added alongside this migration rejects it. Two
-- independent barriers, and the schema is the first.
--
-- ADR-0003 anticipated exactly this. It justifies the tickets/project_items
-- split partly on "a requester who is frequently an external customer with
-- no organisation membership at all"; this table is that sentence made real.

CREATE TABLE requesters (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    email              TEXT        NOT NULL,
    display_name       TEXT        NOT NULL DEFAULT '',
    is_active          BOOLEAN     NOT NULL DEFAULT TRUE,
    -- Mirrors users.token_generation (migration 024) and exists for the same
    -- reason: portal tokens are stateless RS256, so without a generation to
    -- compare against, deactivating a requester would leave every token they
    -- hold valid until it expired. The portal guard reads this column on
    -- every request and rejects a mismatch. Bumping it kills every session
    -- that requester holds, instantly.
    session_generation INTEGER     NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at       TIMESTAMPTZ
);

-- Identity is (org, email) case-insensitively. lower(email) rather than a
-- normalised column because that is what `invites_one_active_per_email`
-- (migration 024) already does for the same question, and two spellings of
-- the same rule drift.
CREATE UNIQUE INDEX requesters_org_email_key ON requesters (org_id, lower(email));
CREATE INDEX requesters_org_idx ON requesters (org_id);

CREATE TRIGGER trg_requesters_updated_at BEFORE UPDATE ON requesters
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── The portal a Beacon space opts into ──────────────────────────────────
--
-- Absence means off. This follows migration 035's rule ("a space with no
-- rows here has no custom board") rather than a flag column on `spaces`,
-- for the reason 035 states: the invariants are expressible relationally,
-- so a blob or a nullable flag would throw away real constraints. Here the
-- invariants are that a portal has exactly one space, that its public
-- identifier is unique org-wide, and that the identifier is opaque.
--
-- WHY portal_key IS RANDOM AND NOT DERIVED. This is the identifier that
-- appears in the URL an external requester visits, so it is the single
-- largest opportunity to leak container context. `spaces.slug`,
-- `spaces.key` and `spaces.name` all describe the internal organisation of
-- the product — "supportdesk", "SUP", "Customer Support" — and any of them
-- in a URL tells an outsider what the internal space is called. The format
-- CHECK enforces the shape rather than trusting the generator: 16-32
-- lowercase alphanumerics, which no derivation from a human-chosen name
-- could satisfy by accident.
CREATE TABLE service_desk_portals (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id   UUID        NOT NULL UNIQUE REFERENCES spaces (id) ON DELETE CASCADE,
    portal_key TEXT        NOT NULL UNIQUE,
    name       TEXT        NOT NULL,
    intro      TEXT        NOT NULL DEFAULT '',
    enabled    BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by UUID        NOT NULL REFERENCES users (id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT service_desk_portals_key_format CHECK (portal_key ~ '^[a-z0-9]{16,32}$'),
    CONSTRAINT service_desk_portals_name_present CHECK (name <> '')
);

CREATE TRIGGER trg_service_desk_portals_updated_at BEFORE UPDATE ON service_desk_portals
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── The magic link ───────────────────────────────────────────────────────
--
-- Shaped on the invite token (migration 024 + internal/core/invites), which
-- is the same problem: a possession-based credential delivered out of band,
-- redeemed once, expiring on its own. Same construction — 32 bytes of
-- crypto/rand as URL-safe base64, only the SHA-256 hex digest stored, the
-- raw value returned exactly once and never persisted.
--
-- Three lifecycle columns rather than one, because "why did my link stop
-- working" has three different answers and collapsing them makes the audit
-- trail unreadable:
--
--   consumed_at      redeemed for a session (the single-use terminus)
--   invalidated_at   superseded, because a newer link was requested
--   expires_at       ran out of time
--
-- SINGLE USE IS ENFORCED BY A GUARDED UPDATE, NOT BY A PRE-CHECK. The
-- redemption statement carries `WHERE consumed_at IS NULL AND
-- invalidated_at IS NULL AND expires_at > now()` and reports rows affected;
-- zero means somebody else won. Checking first and updating second lets two
-- concurrent redemptions of one link both succeed, which is precisely the
-- race migration 024's `MarkInviteAccepted` was written to close, and the
-- same reasoning as shared-surfaces §10: the guard belongs in the UPDATE.
CREATE TABLE requester_magic_links (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    requester_id   UUID        NOT NULL REFERENCES requesters (id) ON DELETE CASCADE,
    portal_id      UUID        NOT NULL REFERENCES service_desk_portals (id) ON DELETE CASCADE,
    token_hash     TEXT        NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    consumed_at    TIMESTAMPTZ,
    invalidated_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX requester_magic_links_token_hash_idx ON requester_magic_links (token_hash);
CREATE INDEX requester_magic_links_requester_idx ON requester_magic_links (requester_id, portal_id)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

-- ── Tickets gain a requester origin ──────────────────────────────────────
--
-- `reporter_id` becomes nullable and gains an exclusive-or partner. A ticket
-- raised through the portal has no internal reporter, and the alternative —
-- pointing reporter_id at some stand-in user row — would render a real
-- agent's name as the person who raised the request. That is a lie the UI
-- would faithfully display.
--
-- THE XOR IS WHAT MAKES PROVENANCE STRUCTURAL. "This ticket came from the
-- portal" is `requester_id IS NOT NULL`, derived from the identity column
-- itself, so the provenance chip cannot disagree with the data. A separate
-- `origin` enum would be a second source of truth for one fact, and the two
-- would eventually diverge. If a later phase wants "an agent raises a
-- request on behalf of a requester", it relaxes this constraint
-- deliberately and re-decides what provenance means — which is the right
-- moment to have that argument, not now by leaving the door ajar.
ALTER TABLE tickets ADD COLUMN requester_id UUID REFERENCES requesters (id);
ALTER TABLE tickets ALTER COLUMN reporter_id DROP NOT NULL;

ALTER TABLE tickets ADD CONSTRAINT tickets_origin_identity CHECK (
    (reporter_id IS NOT NULL AND requester_id IS NULL)
    OR (reporter_id IS NULL AND requester_id IS NOT NULL)
);

-- "The requests this requester raised in this space", the portal's only
-- list. Partial on deleted_at to match every other tickets index (014).
CREATE INDEX tickets_requester_idx ON tickets (requester_id, space_id)
    WHERE deleted_at IS NULL AND requester_id IS NOT NULL;

-- NOTE: this migration seeds NOTHING and enables NOTHING, on migration 035's
-- reasoning. No existing space gains a portal, no existing ticket gains a
-- requester, and every existing ticket keeps the reporter it had — the
-- nullability widening cannot invalidate a row that already satisfies the
-- XOR's left branch. A portal is created through the guarded, audited API
-- path, one space at a time, by somebody who decided to expose it.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Portal-originated tickets cannot survive the restored NOT NULL, and they
-- are exactly the rows this migration made possible. Remove them before
-- restoring the constraint rather than letting the ALTER fail on data this
-- migration is responsible for — migration 039's Down takes the same line.
-- This is destructive and says so: a down-migration that silently invented a
-- reporter for them would be worse.
DELETE FROM tickets WHERE requester_id IS NOT NULL;

ALTER TABLE tickets DROP CONSTRAINT IF EXISTS tickets_origin_identity;
DROP INDEX IF EXISTS tickets_requester_idx;
ALTER TABLE tickets DROP COLUMN IF EXISTS requester_id;
ALTER TABLE tickets ALTER COLUMN reporter_id SET NOT NULL;

DROP TABLE IF EXISTS requester_magic_links;

DROP TRIGGER IF EXISTS trg_service_desk_portals_updated_at ON service_desk_portals;
DROP TABLE IF EXISTS service_desk_portals;

DROP TRIGGER IF EXISTS trg_requesters_updated_at ON requesters;
DROP TABLE IF EXISTS requesters;

-- +goose StatementEnd
