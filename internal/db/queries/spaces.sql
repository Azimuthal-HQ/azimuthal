-- name: CreateSpace :one
INSERT INTO spaces (id, org_id, slug, name, description, type, icon, is_private, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetSpaceByID :one
SELECT * FROM spaces WHERE id = $1 AND deleted_at IS NULL;

-- name: GetSpaceBySlug :one
SELECT * FROM spaces WHERE org_id = $1 AND slug = $2 AND deleted_at IS NULL;

-- name: ListSpacesByOrg :many
SELECT * FROM spaces WHERE org_id = $1 AND deleted_at IS NULL ORDER BY name ASC;

-- name: ListSpacesByType :many
SELECT * FROM spaces WHERE org_id = $1 AND type = $2 AND deleted_at IS NULL ORDER BY name ASC;

-- name: UpdateSpace :one
UPDATE spaces
SET name = $2, description = $3, icon = $4, is_private = $5
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteSpace :exec
UPDATE spaces SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: AddSpaceMember :one
INSERT INTO space_members (id, space_id, user_id, role)
VALUES ($1, $2, $3, $4)
ON CONFLICT (space_id, user_id) DO UPDATE SET role = EXCLUDED.role
RETURNING *;

-- name: GetSpaceMember :one
SELECT * FROM space_members WHERE space_id = $1 AND user_id = $2;

-- name: ListSpaceMembers :many
SELECT sm.id, sm.space_id, sm.user_id, sm.role, sm.created_at,
       u.email, u.display_name, u.avatar_url
FROM space_members sm
JOIN users u ON u.id = sm.user_id
WHERE sm.space_id = $1
ORDER BY u.display_name ASC;

-- name: RemoveSpaceMember :exec
DELETE FROM space_members WHERE space_id = $1 AND user_id = $2;
