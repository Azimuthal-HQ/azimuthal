-- +goose Up
-- +goose StatementBegin

-- Saved views (ADR-0009, spec §3/§4): a named, reusable, cross-container query
-- over Beacon tickets and Vector project items. A view stores a QUERY, never
-- results — it is the substrate P5's dashboards and gadgets will reference.
--
-- The spec sketch for this table is numbered 029 and has been overtaken four
-- times: 029–037 were consumed by phases that were not in the plan, so saved
-- views ship as 038. Numbering is immutable once shipped; the sketch's number
-- was never shipped, so nothing here renumbers anything.
--
--
-- Why the query column is `jsonb` and not `json`
-- ----------------------------------------------
-- The opposite choice from migration 036, for the opposite reason, and the
-- distinction is worth stating so neither rule gets cargo-culted onto the other.
--
-- `pages.doc` is `json` because it holds USER CONTENT that must survive a
-- read-modify-write byte-identically (ADR-0012). `jsonb` would reorder keys,
-- rewrite number literals and drop duplicates — silent data loss.
--
-- A filter document is not user content. It is NORMALISED CONFIGURATION that
-- the server defines, validates field by field against a closed whitelist, and
-- re-serialises from its own Go structs on every write. Nothing here is
-- byte-preserved because nothing here is the user's bytes: an unknown field is
-- rejected at the API boundary rather than stored, so there is no unrecognised
-- content to preserve in the first place. Key order carries no meaning, and
-- containment queries over the document are useful (P5 will want "which views
-- reference this space"). `jsonb` is correct here and `json` would be the
-- cargo-cult.
--
--
-- Why the filter is a document at all, rather than columns
-- -------------------------------------------------------
-- The field set is closed but genuinely optional and multi-valued — a view may
-- name three statuses, no priorities and one space. As columns that is a dozen
-- mostly-null arrays; the invariant that actually matters (only known fields,
-- only known values) is not expressible as a column constraint either way, and
-- lives in the validator. This is not migration 035's rejected case: there the
-- invariant ("every status is mapped, to exactly one column") WAS expressible
-- relationally, so a blob would have thrown away a real constraint. Here there
-- is no such constraint to throw away.
--
-- It is deliberately NOT a query language. There are no operator nodes, no
-- boolean tree, no user-supplied field names — ADR-0011's reasoning about
-- arbitrary scripting applies with equal force to a JQL analogue: a query
-- language you cannot reason about is a query language you cannot migrate,
-- index, or explain. The document is a fixed record of named, typed fields.
-- The one place its semantics are written down is
-- internal/core/views/filter.go, which the future Jira importer maps JQL onto.
--
--
-- Visibility, and the constraint that is deliberately absent
-- ---------------------------------------------------------
-- private            only the owner (and the org-admin bypass) sees it
-- team               plus the members of visibility_team_id, subject-side
--                    expanded exactly like grants (ADR-0007)
-- org                plus every member of the org
--
-- A share of a view shares the DEFINITION, never the results: every read
-- re-resolves against the viewer's own access, so two people opening one view
-- legitimately see different rows. That is the design, not a leak.
--
-- Note what is NOT here: the spec sketch's
--
--     CHECK (visibility <> 'team' OR visibility_team_id IS NOT NULL)
--
-- together with ON DELETE CASCADE on the team reference. Both are wrong for
-- this table, and the second is the more dangerous. Under CASCADE, deleting a
-- team DELETES every view shared to it — someone else's saved work, destroyed
-- as a side effect of an unrelated administrative action. ADR-0009's
-- degradation rule (decision log C1) requires the opposite: a view whose scope
-- team was deleted is *marked invalid*, renders "scope unavailable", and
-- prompts its owner to re-scope. It never errors and it never vanishes.
--
-- So the reference is ON DELETE SET NULL, and the CHECK is omitted because the
-- (visibility = 'team', visibility_team_id IS NULL) state must be
-- REPRESENTABLE — it is precisely the degraded state C1 describes. Leaving the
-- constraint in would turn a team deletion into a constraint violation, which
-- is the "it errors" outcome C1 forbids.
--
-- The invariant still holds on every write, one layer up: the API refuses to
-- create or update a view into that state (ErrTeamRequired), and resolution
-- requires visibility_team_id IS NOT NULL before a team audience can match, so
-- a degraded view is visible to nobody but its owner. Fail closed, then
-- prompt. Both halves are tested, in both directions.
--
-- Validity is DERIVED, never stored. The sketch carried an `is_valid BOOLEAN`,
-- which needs a writer — some sweeper or cross-domain hook that remembers to
-- flip it — and a stored copy of derivable state is a stored copy that can go
-- stale. A view is invalid iff its team audience lost its team, or every space
-- it names is gone; both are answerable at read time, for a whole page of
-- views, in one query.
CREATE TABLE saved_views (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    owner_id           UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name               TEXT        NOT NULL CHECK (name <> ''),
    description        TEXT        NOT NULL DEFAULT '',
    -- {"v":1,"filter":{...},"sort":{...}} — validated field by field before it
    -- is ever written. See internal/core/views/filter.go.
    query              JSONB       NOT NULL,
    visibility         TEXT        NOT NULL DEFAULT 'private',
    visibility_team_id UUID        REFERENCES teams (id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    CONSTRAINT saved_views_visibility_valid
        CHECK (visibility IN ('private', 'team', 'org'))
);

-- "My views" — the owner half of the list endpoint.
CREATE INDEX saved_views_owner_idx ON saved_views (owner_id)
    WHERE deleted_at IS NULL;

-- "Shared with me, org audience" — the org half. Visibility leads because the
-- org-audience scan is the selective one; an org's full view list is not.
CREATE INDEX saved_views_org_visibility_idx ON saved_views (org_id, visibility)
    WHERE deleted_at IS NULL;

-- "Shared with me, team audience" — probed with the caller's effective team
-- set, which is already subject-side expanded by the resolver.
CREATE INDEX saved_views_team_idx ON saved_views (visibility_team_id)
    WHERE deleted_at IS NULL AND visibility_team_id IS NOT NULL;

-- set_updated_at() is the shared trigger function from migration 009; the
-- naming follows the trg_<table>_updated_at convention established there.
CREATE TRIGGER trg_saved_views_updated_at
    BEFORE UPDATE ON saved_views FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- saved_view_sort_key collapses whichever field a view sorts by into ONE
-- comparable text value.
--
-- It exists so the two result fan-outs (internal/db/queries/saved_views.sql)
-- can be static SQL. Without it each would need dynamic ORDER BY — or the same
-- CASE expression pasted four times per query, once in the cursor predicate
-- and once in the ORDER BY, for each of two directions, in each of two
-- modules. Sixteen copies of one rule is sixteen chances for one of them to
-- drift, and a drifted copy shows up as rows silently missing from page two.
--
-- Three properties the callers depend on:
--
--   * ONE TYPE FOR EVERY SORT FIELD. ADR-0009 requires cross-module results to
--     be merged in the API layer, and a merge needs the two halves' sort
--     positions to be comparable. Text is the only type all six fields
--     collapse into.
--
--   * BYTE-COMPARABLE. Callers order with COLLATE "C" so PostgreSQL's order
--     and Go's bytewise string comparison are the SAME order. A title sort
--     under a linguistic collation would order differently in the database
--     than in the merge, interleaving two correctly-sorted halves incorrectly.
--
--   * FIXED WIDTH PER FIELD. Timestamps render as YYYYMMDDHH24MISSUS, so
--     lexicographic order is chronological order. Priority renders as a single
--     ordinal digit, because the stored values sort alphabetically as
--     high < low < medium < urgent, which is meaningless.
--
-- A NULL timestamp collapses to the empty string, sorting before every real
-- value ascending and after every real value descending. Parameters are
-- p_-prefixed so no name can be captured by a column of the calling query.
CREATE FUNCTION saved_view_sort_key(
    p_field       TEXT,
    p_updated_at  TIMESTAMPTZ,
    p_created_at  TIMESTAMPTZ,
    p_due_at      TIMESTAMPTZ,
    p_resolved_at TIMESTAMPTZ,
    p_priority    TEXT,
    p_title       TEXT
) RETURNS TEXT
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT CASE p_field
        WHEN 'updated_at'  THEN COALESCE(to_char(p_updated_at  AT TIME ZONE 'UTC', 'YYYYMMDDHH24MISSUS'), '')
        WHEN 'created_at'  THEN COALESCE(to_char(p_created_at  AT TIME ZONE 'UTC', 'YYYYMMDDHH24MISSUS'), '')
        WHEN 'due_at'      THEN COALESCE(to_char(p_due_at      AT TIME ZONE 'UTC', 'YYYYMMDDHH24MISSUS'), '')
        WHEN 'resolved_at' THEN COALESCE(to_char(p_resolved_at AT TIME ZONE 'UTC', 'YYYYMMDDHH24MISSUS'), '')
        WHEN 'priority'    THEN CASE p_priority
                                    WHEN 'urgent' THEN '0'
                                    WHEN 'high'   THEN '1'
                                    WHEN 'medium' THEN '2'
                                    WHEN 'low'    THEN '3'
                                    ELSE '9'
                                END
        WHEN 'title'       THEN lower(COALESCE(p_title, ''))
        ELSE ''
    END
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION IF EXISTS saved_view_sort_key(TEXT, TIMESTAMPTZ, TIMESTAMPTZ, TIMESTAMPTZ, TIMESTAMPTZ, TEXT, TEXT);
DROP TRIGGER IF EXISTS trg_saved_views_updated_at ON saved_views;
DROP TABLE IF EXISTS saved_views;

-- +goose StatementEnd
