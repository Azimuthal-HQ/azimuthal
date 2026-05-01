-- name: CreatePage :one
INSERT INTO pages (id, space_id, parent_id, title, content, author_id, position, path)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetPageByID :one
SELECT * FROM pages WHERE id = $1 AND deleted_at IS NULL;

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

-- name: UpdatePagePosition :exec
UPDATE pages SET parent_id = $2, position = $3, path = $4 WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdatePageDescendantPaths :exec
UPDATE pages
SET path = REPLACE(path, $2, $3)
WHERE space_id = $1
  AND path LIKE $2 || '.%'
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

-- name: CreatePageRevision :one
INSERT INTO page_revisions (id, page_id, version, title, content, author_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

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
