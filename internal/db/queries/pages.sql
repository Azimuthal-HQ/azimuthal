-- name: CreatePage :one
INSERT INTO pages (id, space_id, parent_id, title, content, author_id, position, path)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetPageInSpace :one
-- A page, reconciled against the space the request named. See the note on
-- GetProjectItemInSpace in project_items.sql.
--
-- This one disclosed the most of any member of that family: the wiki read
-- routes return the page's full content and, through the document handler, its
-- whole ADR-0012 document body — not a title and a status.
SELECT * FROM pages
WHERE id = @page_id AND space_id = @space_id AND deleted_at IS NULL;

-- name: GetPageByID :one
-- UNSCOPED. The legitimate caller is the entity-share read path (ADR-0008),
-- where share coverage authorises instead of space access, plus the internal
-- parent/ancestor resolution inside a transaction that has already established
-- the space. Every space-scoped route wants GetPageInSpace.
SELECT * FROM pages WHERE id = $1 AND deleted_at IS NULL;

-- name: GetPageForUpdate :one
-- Row-locked read for the move transaction: serialises concurrent moves of
-- the same page so descendant path rewrites cannot interleave.
SELECT * FROM pages WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: ListPagesBySpace :many
SELECT id, space_id, parent_id, title, version, author_id, position, path, created_at, updated_at
FROM pages WHERE space_id = $1 AND deleted_at IS NULL ORDER BY path ASC;

-- name: ListRootPagesBySpace :many
SELECT id, space_id, parent_id, title, version, author_id, position, path, created_at, updated_at
FROM pages WHERE space_id = $1 AND parent_id IS NULL AND deleted_at IS NULL ORDER BY path ASC;

-- name: ListChildPages :many
SELECT id, space_id, parent_id, title, version, author_id, position, path, created_at, updated_at
FROM pages WHERE parent_id = $1 AND deleted_at IS NULL ORDER BY path ASC;

-- name: GetPageDescendants :many
SELECT p.id, p.space_id, p.parent_id, p.title, p.version, p.author_id, p.position, p.path, p.created_at, p.updated_at
FROM pages p
WHERE p.space_id = $1
  AND p.deleted_at IS NULL
  AND p.path LIKE (SELECT pp.path || '.%' FROM pages pp WHERE pp.id = $2 AND pp.deleted_at IS NULL)
ORDER BY p.path ASC;

-- name: GetPageTree :many
SELECT id, space_id, parent_id, title, version, author_id, position, path, created_at, updated_at
FROM pages WHERE space_id = $1 AND deleted_at IS NULL ORDER BY path ASC;

-- name: UpdatePageContent :one
UPDATE pages
SET title = $3, content = $4, version = version + 1
WHERE id = $1 AND version = $2 AND deleted_at IS NULL
RETURNING *;

-- name: MovePageToSpace :exec
-- Cross-space move, root row (P3, ADR-0008 rule 9). Runs inside the move
-- transaction together with MovePageDescendantsToSpace and the share
-- revocation for the subtree.
UPDATE pages SET space_id = $2, parent_id = $3, position = $4, path = $5
WHERE id = $1 AND deleted_at IS NULL;

-- name: MovePageDescendantsToSpace :exec
-- Rewrites subtree membership and paths in one statement. Exact prefix
-- surgery via substr — REPLACE would rewrite ANY occurrence of the old
-- prefix, not just the leading one. path_pattern is the LIKE-escaped old
-- path plus '.%', built by the caller with EscapeLike.
UPDATE pages
SET space_id = sqlc.arg(new_space_id),
    path = sqlc.arg(new_prefix)::text || substr(path, length(sqlc.arg(old_prefix)::text) + 1)
WHERE space_id = sqlc.arg(old_space_id)
  AND path LIKE sqlc.arg(path_pattern)
  AND deleted_at IS NULL;

-- name: SoftDeletePageInSpace :exec
-- Scoped to the space, not just the id.
--
-- The route above this is reconciled, but it is the only thing that was: the
-- transactional deleter took an entity id alone, so the refusal lived in a
-- handler rather than in the write. That is the shape this whole class is made
-- of — a convention the next caller (a bulk operation, a job, a new route)
-- inherits nothing of. The delete handler's own comment said as much: "a
-- {entity} outside {spaceID} has to be refused here or nowhere."
--
-- :exec, so a mismatch deletes nothing and says nothing, exactly as an id that
-- named nothing already did.
UPDATE pages SET deleted_at = now()
WHERE id = @page_id AND space_id = @space_id AND deleted_at IS NULL;

-- name: SearchPages :many
SELECT id, space_id, parent_id, title, version, author_id, position, path, created_at, updated_at
FROM pages
WHERE space_id = $1
  AND deleted_at IS NULL
  AND search_vector @@ plainto_tsquery('english', $2)
ORDER BY ts_rank(search_vector, plainto_tsquery('english', $2)) DESC
LIMIT $3;

-- name: PublishPageDocument :one
-- Optimistic publish of the document model (issue #15). Same version guard as
-- UpdatePageContent — zero rows means the page moved on under the author, which
-- the caller turns into the reload-or-overwrite conflict.
UPDATE pages
SET title = $3, content = $4, doc = $5, version = version + 1
WHERE id = $1 AND version = $2 AND deleted_at IS NULL
RETURNING *;

-- name: OverwritePageDocument :one
-- The explicit overwrite arm of the conflict dialogue: no version guard, so it
-- lands on whatever is current. Reachable only from a caller that has already
-- reported the conflict and been told, in those words, to overwrite it.
UPDATE pages
SET title = $2, content = $3, doc = $4, version = version + 1
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: CreatePageRevision :one
INSERT INTO page_revisions (id, page_id, version, title, content, author_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CreatePageRevisionWithDoc :one
-- History for a document-model publish. A revision without its `doc` would
-- make the base-version lookup that resolves preserved unknown content fall
-- back to markdown, which is exactly the lossy direction ADR-0012 forbids.
INSERT INTO page_revisions (id, page_id, version, title, content, doc, author_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetPageRevisionDocument :one
-- The document as of one specific version. Publish uses it to resolve a
-- draft's preserved-unknown references against the version the draft was
-- started from, which is not necessarily the current one.
SELECT version, title, content, doc
FROM page_revisions WHERE page_id = $1 AND version = $2;

-- name: GetPageRevision :one
SELECT * FROM page_revisions WHERE page_id = $1 AND version = $2;

-- name: ListPageRevisions :many
-- The revision ledger, with the author resolved.
--
-- page_revisions has carried author_id since migration 005, so "who published
-- this version" was always stored — it was only ever missing from the read.
--
-- LEFT JOIN, not JOIN, and deliberately with NO predicate on the user's state.
-- Today the join cannot miss: author_id is NOT NULL REFERENCES users (id), and
-- users are soft-deleted rather than removed, so every revision resolves to a
-- row and author_name is never NULL in practice. The outer join is there so
-- that a page's history stays readable if that ever stops being true — losing
-- the whole ledger because one account was hard-deleted would be a much worse
-- failure than showing "Unknown" beside one version.
--
-- Do NOT add `AND u.deleted_at IS NULL` to make the NULL branch reachable. It
-- would start blanking the author of every version published by anybody who has
-- since been deactivated, which is most of the history of a long-lived page —
-- and deactivating an account is not meant to rewrite what they wrote.
-- The space test is on the PAGE, not on the revision: page_revisions has no
-- space_id of its own, and the revision ledger is readable exactly when its
-- page is. Without it a page id alone returned the full historical title of
-- every version, across any space boundary.
SELECT r.id, r.page_id, r.version, r.title, r.author_id, r.created_at,
       u.display_name AS author_name
FROM page_revisions r
JOIN pages p ON p.id = r.page_id
LEFT JOIN users u ON u.id = r.author_id
WHERE r.page_id = @page_id
  AND p.space_id = @space_id
  AND p.deleted_at IS NULL
ORDER BY r.version DESC;

-- The page-lock queries were removed in S2 along with the page_locks table
-- (migration 037). The lock was advisory only — no write path consulted it.
