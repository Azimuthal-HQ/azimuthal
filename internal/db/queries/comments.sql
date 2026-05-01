-- name: CreateComment :one
INSERT INTO comments (id, entity_type, entity_id, parent_id, author_id, body)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCommentByID :one
SELECT * FROM comments WHERE id = $1 AND deleted_at IS NULL;

-- name: ListCommentsByEntity :many
SELECT c.id, c.entity_type, c.entity_id, c.item_id, c.page_id, c.parent_id,
       c.author_id, c.body, c.created_at, c.updated_at, c.deleted_at,
       u.display_name AS author_name, u.avatar_url AS author_avatar
FROM comments c
JOIN users u ON u.id = c.author_id
WHERE c.entity_type = $1 AND c.entity_id = $2 AND c.parent_id IS NULL AND c.deleted_at IS NULL
ORDER BY c.created_at ASC;

-- name: ListCommentReplies :many
SELECT c.id, c.entity_type, c.entity_id, c.item_id, c.page_id, c.parent_id,
       c.author_id, c.body, c.created_at, c.updated_at, c.deleted_at,
       u.display_name AS author_name, u.avatar_url AS author_avatar
FROM comments c
JOIN users u ON u.id = c.author_id
WHERE c.parent_id = $1 AND c.deleted_at IS NULL
ORDER BY c.created_at ASC;

-- name: UpdateComment :one
UPDATE comments SET body = $2, updated_at = now() WHERE id = $1 AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteComment :exec
UPDATE comments SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;
