-- +goose Up
-- +goose StatementBegin

-- The Codex document model (issue #15, ADR-0012 "Content fidelity and unknown
-- nodes"). Pages gain a ProseMirror-native document alongside the markdown
-- they have carried since migration 005, plus a per-user draft.
--
--
-- Why the column type is `json` and not `jsonb`
-- ---------------------------------------------
-- This is load-bearing, not a style choice. ADR-0012 requires that a page
-- containing content the editor cannot represent survives an edit-and-save
-- cycle byte-identically. `jsonb` cannot honour that: it stores a parsed,
-- normalised value, so it reorders object keys, rewrites number literals, and
-- silently drops duplicate keys. Verified against this database:
--
--   '{"zzz":1,"a":{"n":1e2,"dup":1,"dup":2}}'::jsonb
--     -> {"a": {"n": 100, "dup": 2}, "zzz": 1}
--   '{"zzz":1,"a":{"n":1e2,"dup":1,"dup":2}}'::json
--     -> {"zzz":1,"a":{"n":1e2,"dup":1,"dup":2}}
--
-- The `"dup":1` member is gone in the jsonb form. That is silent data loss at
-- the storage layer — the exact failure ADR-0012 exists to prevent — so a
-- document column has to be the type that stores the text verbatim. `json`
-- still validates syntax, which is all the validation a document needs here:
-- nothing queries inside it. Full-text search reads the projected `content`
-- column (below), never the document, so the GIN indexing `jsonb` would buy
-- has no consumer. `wiki/doc` has a test that asserts this round-trip
-- property against the real database, and it fails if the type is changed.
--
-- Migration 035's reasoning does not conflict. It rejected a JSONB blob
-- standing in for data that is really relational. A rich-text document is
-- really a document.
--
--
-- How the two content columns relate
-- ----------------------------------
--   doc IS NULL      the page predates the document editor (or was never
--                    opened in it). `content` is the source of truth and is
--                    markdown, exactly as before. The old renderer keeps
--                    working, unchanged.
--
--   doc IS NOT NULL  `doc` is the source of truth. `content` holds a markdown
--                    projection of it, derived on every publish, and is kept
--                    for two consumers and no others: the generated
--                    `search_vector` (migration 009 spans title + content, so
--                    dropping the projection would silently empty the wiki's
--                    search index), and any legacy reader that has only ever
--                    known `content`. Nothing reads `content` back INTO the
--                    editor when `doc` is present — that direction is where a
--                    lossy projection would become data loss.
--
-- Conversion is per-page and on first edit. There is deliberately no bulk
-- migration of existing markdown: a backfill would rewrite every page in one
-- unreviewable step, and any conversion defect would land on all of them at
-- once instead of on the one page an author is looking at.

ALTER TABLE pages          ADD COLUMN doc JSON;
ALTER TABLE page_revisions ADD COLUMN doc JSON;

-- Per-user unpublished edits. Confluence semantics: readers see the last
-- published version until the author publishes.
--
-- The primary key is (page_id, author_id) — one draft per person per page,
-- enforced by the key rather than by application code, so a concurrent
-- autosave from two tabs upserts instead of duplicating.
--
-- No space_id column, for the same reason `attachments` (migration 027) has
-- none: authorisation derives from the page the draft hangs off. A copy of the
-- container here could go stale when a page moves between spaces, and a stale
-- authorisation input is worse than an extra join.
--
-- base_version records which published version the author started from. It is
-- what publish compares against to detect that the page moved on underneath a
-- draft, and what resolves the draft's preserved-unknown references back to
-- the document they were captured from.
CREATE TABLE page_drafts (
    page_id      UUID        NOT NULL REFERENCES pages (id) ON DELETE CASCADE,
    author_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title        TEXT        NOT NULL,
    doc          JSON        NOT NULL,
    base_version INTEGER     NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, author_id),
    CONSTRAINT page_drafts_base_version_positive CHECK (base_version > 0)
);

-- "Which pages do I hold a draft on" — the unpublished-changes indicator and
-- the Codex Drafts view. Author-leading so the lookup is one index scan.
CREATE INDEX page_drafts_author_idx ON page_drafts (author_id);

-- set_updated_at() is the shared trigger function from migration 009; the
-- naming follows the trg_<table>_updated_at convention established there.
CREATE TRIGGER trg_page_drafts_updated_at
    BEFORE UPDATE ON page_drafts FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_page_drafts_updated_at ON page_drafts;
DROP TABLE IF EXISTS page_drafts;
ALTER TABLE page_revisions DROP COLUMN IF EXISTS doc;
ALTER TABLE pages          DROP COLUMN IF EXISTS doc;

-- +goose StatementEnd
