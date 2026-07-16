-- name: CreateOrganization :one
INSERT INTO organizations (id, slug, name, description)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetOrganizationByID :one
SELECT * FROM organizations WHERE id = $1 AND deleted_at IS NULL;

-- name: GetOrganizationBySlug :one
SELECT * FROM organizations WHERE slug = $1 AND deleted_at IS NULL;

-- name: ListOrganizations :many
SELECT * FROM organizations WHERE deleted_at IS NULL ORDER BY name ASC;

-- name: UpdateOrganization :one
UPDATE organizations
SET name = $2, description = $3
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteOrganization :exec
UPDATE organizations SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;
