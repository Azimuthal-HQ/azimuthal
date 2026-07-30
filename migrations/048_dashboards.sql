-- +goose Up
-- +goose StatementBegin

-- Dashboards and gadgets (P5, ADR-0009, spec §4/§6), built on the saved-view
-- model from migration 038.
--
-- A DASHBOARD OWNS LAYOUT AND NOTHING ELSE. ADR-0009 decision 3 is the whole
-- design: the query lives on the saved view, the arrangement lives here, and a
-- gadget is the join between them plus a render mode. There is no query column
-- on either table and there must never be one — a gadget that embedded a query
-- would be a saved view wearing a gadget's clothes, unreachable from /views,
-- unreachable from the filter builder, and a second place for the closed
-- vocabulary of internal/core/views/filter.go to live.
--
--
-- Migration number: 048
-- ---------------------
-- The spec's §4 table says "039+ unassigned — Dashboards — P5". 039 shipped as
-- Beacon queues while this phase was being planned, and 040/041 were reserved
-- for a Codex phase running concurrently, so this phase was assigned 042 and
-- 043 and this file was written as 042.
--
-- IT COULD NOT STAY THERE, and the reason is worth recording because the same
-- trap is waiting for the next phase that runs long. While this branch was
-- open, a customer-portal phase merged 044 and 045 to main. goose refuses a
-- migration numbered BELOW the current version — `found 1 missing migrations
-- before current version 45` — and internal/db/migrate.go runs goose.UpContext
-- at BOOT. So a 042 landing after 045 would not have produced a failed
-- migration; it would have produced a server that does not start, on every
-- deployment already carrying the portal.
--
-- Nothing in CI would have caught it. Every CI database is built fresh from an
-- empty schema, where numbering gaps are irrelevant and out-of-order does not
-- arise. The failure only exists on a database with history — which is to say,
-- only in production.
--
-- Numbering is immutable ONCE SHIPPED. This migration had not shipped, so it
-- moves: 048, the first free number (main is at 045; a workflow phase holds
-- 046 and 047 on an open branch). 042 and 043 were reserved for this phase and
-- neither is used; both stay free.
--
-- The rule this leaves behind: a pre-assigned migration number is a claim on a
-- position in a SEQUENCE, and it expires the moment a higher number merges
-- first. Re-check it against main before you open the PR, not when you plan
-- the phase.
--
--
-- Visibility: the same three audiences as saved_views, and the same FK
-- ------------------------------------------------------------------
-- private            only the owner (and the org-admin bypass) sees it
-- team               plus the members of visibility_team_id, subject-side
--                    expanded exactly like grants (ADR-0007)
-- org                plus every member of the org
--
-- There is deliberately no 'space' audience here. That value exists on
-- saved_views because a queue is bound to a space and reached through a route
-- that has already established space-readability (migration 039); a dashboard
-- has no such binding and no such route.
--
-- SHARING SHARES THE DEFINITION, NEVER THE RESULTS. This is inherited whole
-- from migration 038 and it is the invariant the whole feature rests on: every
-- gadget re-resolves its saved view against the VIEWER's own access on every
-- render, so two people opening one shared dashboard legitimately see
-- different numbers and different rows. Nothing in either table records a
-- result, a count, or a snapshot, and nothing here consults the owner's
-- access.
--
-- The spec sketch for this table declares
--
--     visibility_team_id UUID REFERENCES teams(id) ON DELETE CASCADE
--
-- which is character-for-character the construct D57 condemned on the
-- saved-views sketch. On a NULLABLE column, CASCADE deletes the ROW: deleting
-- a team would delete every dashboard shared with it — somebody else's work,
-- destroyed as a side effect of an unrelated administrative action. As in
-- migration 038 the reference is ON DELETE SET NULL, and the
-- (visibility = 'team', visibility_team_id IS NULL) state is left
-- REPRESENTABLE rather than CHECKed away, because that state is precisely
-- ADR-0009's degradation case: a dashboard whose audience team is gone is
-- visible to nobody but its owner, who is prompted to re-scope. Fail closed,
-- then prompt.
--
-- Validity is DERIVED, never stored, for migration 038's reason: a stored copy
-- of derivable state is a stored copy that can go stale, and it needs a writer
-- that somebody has to remember to call. There is no is_valid column.
CREATE TABLE dashboards (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    owner_id           UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name               TEXT        NOT NULL CHECK (name <> ''),
    description        TEXT        NOT NULL DEFAULT '',
    -- Which product surface this dashboard belongs to. Matches the spec
    -- sketch. Deliberately NOT the saved-view module vocabulary: a saved view
    -- names which TABLES it reads ('beacon', 'vector'), while this names which
    -- part of the product lists the dashboard, and 'home' is a real answer to
    -- the second question and not to the first. Codex is absent from both, for
    -- different reasons — pages are not queryable by a saved view at all.
    module             TEXT        NOT NULL DEFAULT 'home',
    is_default         BOOLEAN     NOT NULL DEFAULT FALSE,
    -- is_seeded records that this row came from the starter layout rather than
    -- from a person. It exists so seeding can run EXACTLY ONCE per user per
    -- module: a customised starter dashboard must never be re-seeded, because
    -- re-seeding destroys the customisation. See the NOTE at the bottom on why
    -- there is no backfill.
    is_seeded          BOOLEAN     NOT NULL DEFAULT FALSE,
    visibility         TEXT        NOT NULL DEFAULT 'private',
    visibility_team_id UUID        REFERENCES teams (id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    CONSTRAINT dashboards_module_valid
        CHECK (module IN ('home', 'beacon', 'vector')),
    CONSTRAINT dashboards_visibility_valid
        CHECK (visibility IN ('private', 'team', 'org'))
);

-- Decision log C3: "Home dashboards per user — many, one marked default."
-- Partial and unique, so the "one default" half is a database fact rather than
-- a handler convention, and so a soft-deleted dashboard releases its claim.
--
-- It is also what makes lazy seeding idempotent BY CONSTRUCTION: the starter
-- insert is ON CONFLICT DO NOTHING against this index, so two tabs opening
-- Home at the same moment cannot produce two starter dashboards. A
-- check-then-insert would have that race.
CREATE UNIQUE INDEX dashboards_one_default ON dashboards (owner_id, module)
    WHERE is_default AND deleted_at IS NULL;

-- "My dashboards" — the owner half of the list endpoint.
CREATE INDEX dashboards_owner_idx ON dashboards (owner_id)
    WHERE deleted_at IS NULL;

-- "Shared with me, org audience" — visibility leads because the org-audience
-- scan is the selective one, exactly as in migration 038.
CREATE INDEX dashboards_org_visibility_idx ON dashboards (org_id, visibility)
    WHERE deleted_at IS NULL;

-- "Shared with me, team audience" — probed with the caller's effective team
-- set, which effective_team_ids() (migration 038) expands.
CREATE INDEX dashboards_team_idx ON dashboards (visibility_team_id)
    WHERE deleted_at IS NULL AND visibility_team_id IS NOT NULL;

-- set_updated_at() is the shared trigger function from migration 009; the
-- naming follows the trg_<table>_updated_at convention established there.
CREATE TRIGGER trg_dashboards_updated_at
    BEFORE UPDATE ON dashboards FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- Gadgets: the ordered arrangement, and the join to the saved view.
--
--
-- Why config is `jsonb`, and why it is a document at all
-- -----------------------------------------------------
-- The same reasoning migration 038 gives for saved_views.query, and for the
-- same reason it is NOT the reasoning migration 036 gives for pages.doc.
--
-- pages.doc is `json` because it holds USER CONTENT that must survive a
-- read-modify-write byte-identically (ADR-0012). A gadget config is not user
-- content: it is NORMALISED CONFIGURATION that the server defines, validates
-- key by key against the gadget registry's closed vocabulary, and
-- re-serialises from its own Go structs on every write. An unknown key is
-- rejected at the API boundary rather than stored, so there is no
-- unrecognised content to preserve. Key order carries no meaning, and
-- containment queries over the document are useful. `jsonb` is correct here
-- and `json` would be the cargo-cult.
--
-- It is a document rather than columns because the key set differs PER GADGET
-- KIND and is genuinely sparse — a note has body text and no limit, a
-- breakdown has a group-by field and no body text. As columns that is a
-- handful of mostly-null fields whose only real invariant ("only the keys this
-- gadget kind defines, with only the values it allows") is not expressible as
-- a column constraint anyway and lives in the registry. This is not migration
-- 035's rejected case: there the invariant WAS relational.
--
-- The one thing config must never hold is a QUERY. ADR-0009 decision 2 —
-- "a gadget references a saved view plus a render mode; it never embeds a
-- query" — is enforced in internal/core/dashboards/registry.go, which names
-- every key each gadget kind may carry, and none of them is a filter document.
--
--
-- gadget_key is deliberately unconstrained text
-- --------------------------------------------
-- No CHECK, no enum, no FK to a registry table. The registry is a closed set
-- IN CODE (spec §7, ADR-0009 decision 5) and validated on every write, so a
-- key this build does not know cannot be written through the API. But a key
-- this build does not know can still be READ — from a row an older or newer
-- build wrote — and decision log C5 requires that to render a placeholder tile
-- rather than crash the dashboard. A CHECK constraint would turn that
-- degradation case into a failed migration or an unreadable row: strict on
-- write, tolerant on read, and the tolerance has to be in the schema.
--
--
-- saved_view_id: SET NULL, and what NULL means
-- --------------------------------------------
-- ADR-0009's fourth degradation rule: "a gadget whose saved_view_id was
-- deleted renders a recoverable empty state offering to pick another view."
-- CASCADE would delete the gadget — and with it the position, the span and
-- everything the author arranged — as a side effect of somebody deleting a
-- view. SET NULL keeps the tile, and the API reports the gadget as needing a
-- view rather than as broken. NULL is also the ordinary state of a gadget kind
-- that takes no saved view at all (a note, or one whose query the registry
-- supplies), which is why there is no NOT NULL variant and no CHECK tying the
-- column to the key: the registry answers "does this kind need a view", and
-- the answer changes with the code, not with the data.
CREATE TABLE dashboard_gadgets (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    dashboard_id  UUID        NOT NULL REFERENCES dashboards (id) ON DELETE CASCADE,
    gadget_key    TEXT        NOT NULL CHECK (gadget_key <> ''),
    position      INTEGER     NOT NULL,
    col_span      SMALLINT    NOT NULL DEFAULT 1,
    saved_view_id UUID        REFERENCES saved_views (id) ON DELETE SET NULL,
    config        JSONB       NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT dashboard_gadgets_span_valid
        CHECK (col_span IN (1, 2, 4)),
    CONSTRAINT dashboard_gadgets_position_nonneg
        CHECK (position >= 0)
);

-- "The gadgets of this dashboard, in order" — the only read this table serves.
CREATE INDEX dashboard_gadgets_dash_idx ON dashboard_gadgets (dashboard_id, position);

-- One gadget per slot. Layout is written as a WHOLE COLLECTION (spec §6:
-- "gadget layout saves as a whole collection, never per gadget, to avoid
-- partial states"), so the write is DELETE-then-INSERT inside one transaction
-- and never passes through a state where two rows claim one position. That
-- makes an ordinary unique index sufficient — unlike migration 039's queue
-- positions, which are renumbered in place and therefore need DEFERRABLE.
-- Stated rather than assumed, because the two tables look alike and are not.
CREATE UNIQUE INDEX dashboard_gadgets_position_key
    ON dashboard_gadgets (dashboard_id, position);

-- "Which gadgets point at this view" — used when a view is deleted or listed,
-- and the containment read migration 038 anticipated when it chose jsonb.
CREATE INDEX dashboard_gadgets_view_idx ON dashboard_gadgets (saved_view_id)
    WHERE saved_view_id IS NOT NULL;

-- NOTE: this migration seeds NOTHING, on migration 035's and 039's reasoning,
-- and the spec agrees ("on first login a user's Home dashboard is seeded").
-- A backfill would touch every existing user in one unreviewable step, would
-- have to reproduce the starter layout's filter documents in SQL where they
-- could drift from the Go that produces them everywhere else, and would create
-- rows for users who never open Home. The starter layout is created lazily on
-- first visit through the same guarded API path a manual create takes, and
-- dashboards_one_default makes that idempotent. Absence-means-none cannot
-- drift.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS dashboard_gadgets_view_idx;
DROP INDEX IF EXISTS dashboard_gadgets_position_key;
DROP INDEX IF EXISTS dashboard_gadgets_dash_idx;
DROP TABLE IF EXISTS dashboard_gadgets;

DROP TRIGGER IF EXISTS trg_dashboards_updated_at ON dashboards;
DROP INDEX IF EXISTS dashboards_team_idx;
DROP INDEX IF EXISTS dashboards_org_visibility_idx;
DROP INDEX IF EXISTS dashboards_owner_idx;
DROP INDEX IF EXISTS dashboards_one_default;
DROP TABLE IF EXISTS dashboards;

-- +goose StatementEnd
