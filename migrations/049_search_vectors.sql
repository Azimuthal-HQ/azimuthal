-- +goose Up
-- +goose StatementBegin

-- ── Cross-module search: the indexing substrate (P6, spec §4 "Full-text search") ──
--
-- Brings Beacon and Vector onto the scheme Codex has used since 009: a
-- GENERATED STORED tsvector, weighted title over body. Two things change, and
-- one deliberately does not.
--
--
-- WHY GENERATED RATHER THAN FIXING THE TRIGGERS
-- --------------------------------------------
-- 014 gave tickets and project_items plain TSVECTOR columns maintained by
-- `update_tickets_search_vector()` / `update_project_items_search_vector()`.
-- Those triggers are not broken: both are `BEFORE INSERT OR UPDATE ... FOR EACH
-- ROW` with no column list, so an edit to title or description does refresh the
-- vector. There is no staleness defect to fix here, and this migration should
-- not be read as claiming one.
--
-- What the triggers cannot do is be weighted without a second class of bug. The
-- trigger body is `to_tsvector('english', title || ' ' || description)` — one
-- call, one weight class (D). Replacing only the function body leaves every
-- PRE-EXISTING row unweighted while new rows are weighted: correct for new,
-- wrong for old, and invisible on any fresh test database. A generated column
-- has no such state — `ADD COLUMN ... GENERATED` computes the expression for
-- every existing row as part of the ALTER, so old and new rows cannot disagree.
--
-- The trigger also silently wins against anything else: because it assigns
-- `NEW.search_vector` unconditionally in a BEFORE hook, an UPDATE that writes
-- the column directly reports success and changes nothing. A backfill written
-- that way is a no-op that looks like a migration.
--
--
-- WHY THE WEIGHTS ARE THE POINT, AND WHY A QUERY-TIME WEIGHT ARRAY IS NOT A
-- SUBSTITUTE
-- -------------------------------------------------------------------------
-- Search fans out over three tables and merges by ts_rank. Before this
-- migration the same word scores ten times higher on a page than on a ticket,
-- because ts_rank's default weight array is {D,C,B,A} = {0.1,0.2,0.4,1.0} and
-- pages label their title `A` while tickets label nothing at all:
--
--     ts_rank(setweight(to_tsvector('english','widget'),'A'), ...)  -- 0.6079271
--     ts_rank(          to_tsvector('english','widget'),      ...)  -- 0.06079271
--
-- So a merged ranking would place every Codex page above every equally
-- relevant Beacon ticket, and no per-module test could see it.
--
-- Passing an explicit weight array at query time looks like it fixes this
-- without a migration. It does not. It scales a ticket's title and its
-- description by the same factor, because the distinction was destroyed at
-- index time — one to_tsvector call cannot carry two weights. The array raises
-- a ticket's DESCRIPTION match to the level of a page's TITLE match: a
-- cross-module error traded for a within-module one. The distinction has to be
-- made where the lexemes are produced, which is here.
--
--
-- WHAT THIS MIGRATION DOES NOT TOUCH
-- ----------------------------------
-- `pages` already is the target form (009: title A, content B, GENERATED
-- STORED, partial GIN `idx_pages_search`). Nothing to do, and nothing safe to
-- do — a generated column cannot be converted by dropping a trigger, because
-- there is no trigger.
--
-- The legacy table is NOT called `items`: 015 renamed it to `items_archive`,
-- and PostgreSQL does not rename a table's indexes with the table, so its
-- generated `search_vector` is still indexed as `idx_items_search` — on
-- `items_archive`. Neither that index name nor the identifier `items` is free,
-- and neither is part of search.
--
--
-- TEXT SEARCH CONFIGURATION: 'english', AND WHY NOT 'simple'
-- ----------------------------------------------------------
-- 'english' throughout, matching 009. This is not a fresh choice: pages'
-- generated column has been 'english' since 009 and the spec's own sketch for
-- this migration specifies 'english'. Consistency across all three columns is
-- what makes ts_rank comparable between them at all, so a mixed corpus would
-- defeat the merge this migration exists to enable.
--
-- 'simple' (no stemming, no stopwords) is the defensible alternative for a
-- self-hosted product whose corpus language is unknown, and it pairs naturally
-- with prefix matching. Adopting it is a product decision about recall, not a
-- refactor: it would mean rewriting pages' generated column — a full rewrite
-- and reindex of the largest text table — and changing the behaviour of three
-- shipped endpoints. It is recorded as the open option it is, not taken here.
--
--
-- ONE ASYMMETRY, DELIBERATE
-- -------------------------
-- project_items' vector includes `item_key` (weight A); tickets' does not,
-- because tickets HAVE no key column — a ticket's reference is composed from
-- its space key and number by tickets.ComposeRef, and a generated column may
-- only reference columns of its own row, so `spaces.key` is unreachable from
-- here. Ticket references stay served by the existing /ticketref resolver,
-- which is an exact lookup and a better answer than a ranked match anyway.
-- Both vectors still carry title `A` and body `B`, so cross-type rank
-- comparability is unaffected.
--
--
-- COLUMN ORDINALS MOVE, SO sqlc MUST BE REGENERATED
-- ------------------------------------------------
-- DROP COLUMN + ADD COLUMN moves search_vector to the end of the table. sqlc
-- derives column order by replaying migrations, and both tickets.sql and
-- project_items.sql contain `RETURNING *` / `SELECT *` statements whose scan
-- order is generated from it. Skipping `make sqlc` here is a RUNTIME
-- scan-order mismatch, not a compile error.
--
-- And DROP COLUMN takes the column's indexes with it silently. If the CREATE
-- INDEX statements below were ever dropped, search would keep working by
-- sequential scan and report no error at all.

-- ── Beacon ───────────────────────────────────────────────────────────────────
DROP TRIGGER  tickets_search_vector_update ON tickets;
DROP FUNCTION update_tickets_search_vector();
DROP INDEX    idx_tickets_search;
ALTER TABLE   tickets DROP COLUMN search_vector;

ALTER TABLE tickets ADD COLUMN search_vector TSVECTOR
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B')
    ) STORED;

CREATE INDEX idx_tickets_search ON tickets USING GIN (search_vector)
    WHERE deleted_at IS NULL;

-- ── Vector ───────────────────────────────────────────────────────────────────
DROP TRIGGER  project_items_search_vector_update ON project_items;
DROP FUNCTION update_project_items_search_vector();
DROP INDEX    idx_project_items_search;
ALTER TABLE   project_items DROP COLUMN search_vector;

ALTER TABLE project_items ADD COLUMN search_vector TSVECTOR
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(item_key, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B')
    ) STORED;

CREATE INDEX idx_project_items_search ON project_items USING GIN (search_vector)
    WHERE deleted_at IS NULL;

-- +goose StatementEnd

-- +goose StatementBegin

-- ── The merge sort key ───────────────────────────────────────────────────────
--
-- Search fans out per module and merges in the API layer (ADR-0009). For that
-- merge to be correct, the order PostgreSQL returns each half in and the order
-- Go compares them in must be the SAME order — the lesson 038's
-- saved_view_sort_key exists to encode, and the reason its callers apply
-- COLLATE "C".
--
-- ts_rank returns `real`. A float is the wrong cursor key: page two resumes
-- from a value the client round-tripped through JSON, and float equality after
-- that trip is not something to bet a "rows silently missing from page two"
-- bug on. So the key is fixed-width zero-padded TEXT, which is byte-comparable
-- and therefore compares identically in SQL under COLLATE "C" and in Go:
--
--     rank 0.6079271 → '0000607927'
--     rank 0.0       → '0000000000'
--
-- Ten digits is not arbitrary. Callers pass ts_rank's normalization flag 32
-- (rank/(rank+1)), which bounds the value to [0,1) while being strictly
-- monotonic — it changes no relative order, it only makes the domain finite so
-- a fixed width cannot overflow. Six decimal places is the resolution; ranks
-- closer than 1e-6 tie and fall through to the id tiebreaker, which is
-- deterministic. Length normalization (flags 1/2, divide by document length) is
-- deliberately NOT used: it would penalise a long page against a short ticket
-- and make cross-type comparison worse, not better.
--
-- IMMUTABLE (not STABLE) so the expression can be used in an index later if
-- ranking ever needs one; it reads no tables and depends on nothing but its
-- arguments.
CREATE FUNCTION search_sort_key(rank REAL) RETURNS TEXT
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    RETURN lpad((round(LEAST(GREATEST(rank, 0.0::real), 1.0::real)::numeric * 1000000))::bigint::text, 10, '0');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restores 014's arrangement VERBATIM: plain TSVECTOR columns, both trigger
-- functions with the original unweighted body, both triggers as
-- BEFORE INSERT OR UPDATE ... FOR EACH ROW with no column list, and both
-- partial GIN indexes under their 014 names.
--
-- A Down that merely dropped the generated columns would leave tickets and
-- project_items with NO search vector at all — not the pre-search state — and
-- TestMigrateDown would still pass, because it asserts the rollback runs, not
-- that the schema came back.

DROP FUNCTION search_sort_key(REAL);

-- ── Beacon ───────────────────────────────────────────────────────────────────
DROP INDEX  idx_tickets_search;
ALTER TABLE tickets DROP COLUMN search_vector;
ALTER TABLE tickets ADD COLUMN search_vector TSVECTOR;

CREATE INDEX idx_tickets_search ON tickets USING GIN (search_vector)
    WHERE deleted_at IS NULL;

CREATE OR REPLACE FUNCTION update_tickets_search_vector()
RETURNS TRIGGER AS $$
BEGIN
    NEW.search_vector := to_tsvector('english', coalesce(NEW.title, '') || ' ' || coalesce(NEW.description, ''));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER tickets_search_vector_update
    BEFORE INSERT OR UPDATE ON tickets
    FOR EACH ROW EXECUTE FUNCTION update_tickets_search_vector();

-- 014 populated the column through the trigger as rows were written. Rows that
-- already exist at rollback time have no trigger event of their own, so a
-- no-op UPDATE is what fires it for them. Unlike the Up direction this really
-- is necessary: a plain column is not computed for existing rows by anything.
UPDATE tickets SET title = title;

-- ── Vector ───────────────────────────────────────────────────────────────────
DROP INDEX  idx_project_items_search;
ALTER TABLE project_items DROP COLUMN search_vector;
ALTER TABLE project_items ADD COLUMN search_vector TSVECTOR;

CREATE INDEX idx_project_items_search ON project_items USING GIN (search_vector)
    WHERE deleted_at IS NULL;

CREATE OR REPLACE FUNCTION update_project_items_search_vector()
RETURNS TRIGGER AS $$
BEGIN
    NEW.search_vector := to_tsvector('english', coalesce(NEW.title, '') || ' ' || coalesce(NEW.description, ''));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER project_items_search_vector_update
    BEFORE INSERT OR UPDATE ON project_items
    FOR EACH ROW EXECUTE FUNCTION update_project_items_search_vector();

UPDATE project_items SET title = title;

-- +goose StatementEnd
