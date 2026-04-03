-- name: CreateComment :one
INSERT INTO comments (id, item_id, page_id, parent_id, author_id, body)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCommentByID :one
SELECT * FROM comments WHERE id = $1 AND deleted_at IS NULL;

-- name: ListCommentsByItem :many
SELECT c.id, c.item_id, c.page_id, c.parent_id, c.author_id, c.body, c.created_at, c.updated_at, c.deleted_at,
       u.display_name AS author_name, u.avatar_url AS author_avatar
FROM comments c
JOIN users u ON u.id = c.author_id
WHERE c.item_id = $1 AND c.parent_id IS NULL AND c.deleted_at IS NULL
ORDER BY c.created_at ASC;

-- name: ListCommentsByPage :many
SELECT c.id, c.item_id, c.page_id, c.parent_id, c.author_id, c.body, c.created_at, c.updated_at, c.deleted_at,
       u.display_name AS author_name, u.avatar_url AS author_avatar
FROM comments c
JOIN users u ON u.id = c.author_id
WHERE c.page_id = $1 AND c.parent_id IS NULL AND c.deleted_at IS NULL
ORDER BY c.created_at ASC;

-- name: ListCommentReplies :many
SELECT c.id, c.item_id, c.page_id, c.parent_id, c.author_id, c.body, c.created_at, c.updated_at, c.deleted_at,
       u.display_name AS author_name, u.avatar_url AS author_avatar
FROM comments c
JOIN users u ON u.id = c.author_id
WHERE c.parent_id = $1 AND c.deleted_at IS NULL
ORDER BY c.created_at ASC;

-- name: UpdateComment :one
UPDATE comments SET body = $2 WHERE id = $1 AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteComment :exec
UPDATE comments SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;
