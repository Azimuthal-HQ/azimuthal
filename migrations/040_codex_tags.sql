-- +goose Up
-- +goose StatementBegin

-- ── Codex tags ───────────────────────────────────────────────────────────────
-- Org-scoped tags, and the page↔tag association.
--
--
-- Identity: id-referenced, slug-immutable
-- --------------------------------------
-- This follows the item-types decision (D49, migration 032) in spirit and goes
-- one step further. There, `project_items.kind` stores the type's *slug*, so a
-- rename must never touch the slug or every item row would have to be
-- rewritten. Here the association is a real join table, so it stores the tag's
-- **id** — and a rename cannot rewrite anything even in principle.
--
-- The slug is still immutable and still the org-unique key, because it is the
-- textual identity: it is what `#foo` in a document body resolves to at publish
-- time, and it is what a tag URL carries. `name` is the mutable display form.
-- So both properties hold at once:
--
--   rename → UPDATE tags SET name = $2         (zero join rows touched)
--   `#foo` → slugify → tags.slug → tags.id     (stable across renames)
--
-- Slugs are derived with `itemtypes.Slugify` — the repository's one slug
-- helper, and the one whose output this CHECK is written for. Reusing it rather
-- than writing a second is the shared-surfaces rule; the consequence worth
-- knowing is that Codex tag slugs are underscore-separated (`#design_docs`),
-- like item-type and custom-field slugs, not hyphenated like space and team
-- slugs.
--
--
-- Why no archived_at, no position, no updated_at
-- ----------------------------------------------
-- Tags are created by use, not administered: there is no tag-management
-- surface in this phase, so there is nothing yet that archives, reorders or
-- renames one. Columns for those operations would be columns nothing writes,
-- which is worse than absent — a reader cannot tell an unused column from a
-- broken one. Adding them later is an ALTER; adding them now is a promise.
--
--
-- Why no FK from a document to a tag
-- ----------------------------------
-- An inline `#foo` token in a page's stored document carries its label as
-- text, never a tag id. The document is content and must stay self-describing
-- and portable; a UUID embedded in prose is neither. Publish resolves the label
-- to a tag row (creating it if it is new) and writes the association here,
-- which is the only place the two are joined.

CREATE TABLE tags (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    slug       TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tags_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9_]*$'),
    UNIQUE (org_id, slug)
);

-- No separate (org_id) index: the UNIQUE above is backed by an index on
-- (org_id, slug), and every read here is either "this org's tags" or "this
-- org's tag with this slug". A second index on the leading column alone would
-- be redundant with it.

-- The page↔tag association.
--
-- Composite primary key rather than a surrogate id, following team_members
-- (migration 022): the pair IS the row's identity, an association carries no
-- audit of its own, and the key is what makes "tag a page twice" impossible
-- without application code being careful.
--
-- No org_id column, for the reason attachments (027) and page_drafts (036)
-- have none: the org derives from the page, and a denormalised copy here could
-- go stale if a page ever moved between orgs. Both sides cascade — deleting a
-- tag removes its associations rather than leaving rows pointing at nothing.
CREATE TABLE page_tags (
    page_id    UUID        NOT NULL REFERENCES pages (id) ON DELETE CASCADE,
    tag_id     UUID        NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (page_id, tag_id)
);

-- "Which pages carry this tag" — the tag browse page. The primary key already
-- answers the other direction (a page's tags), so this is the one index the
-- pair does not get for free.
CREATE INDEX page_tags_tag_idx ON page_tags (tag_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS page_tags;
DROP TABLE IF EXISTS tags;

-- +goose StatementEnd
