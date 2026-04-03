-- name: CreatePage :one
INSERT INTO pages (id, space_id, parent_id, title, content, author_id, position)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetPageByID :one
SELECT * FROM pages WHERE id = $1 AND deleted_at IS NULL;

-- name: ListPagesBySpace :many
SELECT id, space_id, parent_id, title, version, author_id, position, created_at, updated_at
FROM pages WHERE space_id = $1 AND deleted_at IS NULL ORDER BY position ASC, title ASC;

-- name: ListRootPagesBySpace :many
SELECT id, space_id, parent_id, title, version, author_id, position, created_at, updated_at
FROM pages WHERE space_id = $1 AND parent_id IS NULL AND deleted_at IS NULL ORDER BY position ASC;

-- name: ListChildPages :many
SELECT id, space_id, parent_id, title, version, author_id, position, created_at, updated_at
FROM pages WHERE parent_id = $1 AND deleted_at IS NULL ORDER BY position ASC;

-- name: UpdatePageContent :one
UPDATE pages
SET title = $3, content = $4, version = version + 1
WHERE id = $1 AND version = $2 AND deleted_at IS NULL
RETURNING *;

-- name: UpdatePagePosition :exec
UPDATE pages SET parent_id = $2, position = $3 WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeletePage :exec
UPDATE pages SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: SearchPages :many
SELECT id, space_id, parent_id, title, version, author_id, position, created_at, updated_at
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
