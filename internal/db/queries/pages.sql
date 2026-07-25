-- name: CreatePage :one
INSERT INTO pages (id, space_id, parent_id, title, content, author_id, position, path)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetPageByID :one
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

-- name: SoftDeletePage :exec
UPDATE pages SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

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
SELECT id, page_id, version, title, author_id, created_at
FROM page_revisions WHERE page_id = $1 ORDER BY version DESC;

-- name: UpsertPageLock :one
INSERT INTO page_locks (page_id, user_id, user_name, acquired_at, expires_at)
VALUES ($1, $2, $3, now(), $4)
ON CONFLICT (page_id) DO UPDATE
  SET user_id = EXCLUDED.user_id,
      user_name = EXCLUDED.user_name,
      acquired_at = now(),
      expires_at = EXCLUDED.expires_at
RETURNING *;

-- name: GetPageLock :one
SELECT * FROM page_locks WHERE page_id = $1 AND expires_at > now();

-- name: DeletePageLock :exec
DELETE FROM page_locks WHERE page_id = $1 AND user_id = $2;

-- name: DeleteExpiredPageLocks :exec
DELETE FROM page_locks WHERE expires_at <= now();
