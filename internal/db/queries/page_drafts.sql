-- Per-user unpublished page edits (issue #15, migration 036). A draft is
-- visible only to its author; every query here is keyed by author_id for that
-- reason, not as an optimisation.

-- name: UpsertPageDraft :one
-- Autosave. The (page_id, author_id) primary key makes this an upsert rather
-- than a duplicate when the same author saves from two tabs. updated_at is
-- maintained by trg_page_drafts_updated_at, which ON CONFLICT DO UPDATE fires.
INSERT INTO page_drafts (page_id, author_id, title, doc, base_version)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (page_id, author_id) DO UPDATE
  SET title        = EXCLUDED.title,
      doc          = EXCLUDED.doc,
      base_version = EXCLUDED.base_version
RETURNING *;

-- name: GetPageDraft :one
SELECT * FROM page_drafts WHERE page_id = $1 AND author_id = $2;

-- name: DeletePageDraft :execrows
-- Discard, and the same statement publish uses to clear the draft it just
-- published. execrows so the caller can tell "discarded" from "there was
-- nothing to discard" without a preceding read.
DELETE FROM page_drafts WHERE page_id = $1 AND author_id = $2;

-- name: ListPageDraftsForAuthorInSpace :many
-- The Codex Drafts view and the unpublished-changes indicator: every page in
-- one space on which the caller holds a draft. One index scan on
-- page_drafts_author_idx joined to the page — the count does not grow the
-- number of queries (spec §2.5 case 23).
SELECT d.page_id,
       d.title        AS draft_title,
       d.base_version,
       d.updated_at,
       p.title        AS page_title,
       p.version      AS page_version,
       p.path
FROM page_drafts d
JOIN pages p ON p.id = d.page_id
WHERE d.author_id = $1
  AND p.space_id = $2
  AND p.deleted_at IS NULL
ORDER BY d.updated_at DESC;
