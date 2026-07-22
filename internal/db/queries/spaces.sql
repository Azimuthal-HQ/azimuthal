-- name: CreateSpace :one
INSERT INTO spaces (id, org_id, slug, name, description, type, icon, is_private, created_by, key, owner_team_id, visibility)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetSpaceByID :one
SELECT * FROM spaces WHERE id = $1 AND deleted_at IS NULL;

-- name: GetSpaceBySlug :one
-- Slugs are unique per (org, module) since migration 028, so resolving a
-- space by slug requires the module type — slug alone is ambiguous.
SELECT * FROM spaces WHERE org_id = $1 AND type = $2 AND slug = $3 AND deleted_at IS NULL;

-- name: ListSpacesByOrg :many
SELECT * FROM spaces WHERE org_id = $1 AND deleted_at IS NULL ORDER BY name ASC;

-- name: ListSpacesByType :many
SELECT * FROM spaces WHERE org_id = $1 AND type = $2 AND deleted_at IS NULL ORDER BY name ASC;

-- name: UpdateSpace :one
UPDATE spaces
SET name = $2, description = $3, icon = $4, is_private = $5, key = $6
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteSpace :exec
UPDATE spaces SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: SetSpaceVisibility :one
UPDATE spaces SET visibility = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SetSpaceOwnerTeam :one
UPDATE spaces SET owner_team_id = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: CountSpaceContents :one
-- The delete-confirmation summary (P2.5 W8): what the space contains, in
-- one query.
SELECT
    (SELECT count(*) FROM tickets t WHERE t.space_id = sqlc.arg(space_id) AND t.deleted_at IS NULL)::int AS tickets,
    (SELECT count(*) FROM pages p WHERE p.space_id = sqlc.arg(space_id) AND p.deleted_at IS NULL)::int AS pages,
    (SELECT count(*) FROM project_items i WHERE i.space_id = sqlc.arg(space_id) AND i.deleted_at IS NULL)::int AS items;

-- name: ListSpaceIDsByOrg :many
SELECT id FROM spaces WHERE org_id = $1 AND deleted_at IS NULL;

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
