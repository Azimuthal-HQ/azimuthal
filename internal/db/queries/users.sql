-- name: CreateUser :one
INSERT INTO users (id, org_id, email, display_name, avatar_url, password_hash, role)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1 AND deleted_at IS NULL;

-- name: GetUserByEmailAndOrg :one
SELECT * FROM users WHERE org_id = $1 AND email = $2 AND deleted_at IS NULL;

-- name: ListMembershipsByUser :many
SELECT m.id, m.org_id, m.user_id, m.role, m.invited_by, m.created_at, m.updated_at,
       o.slug AS org_slug, o.name AS org_name
FROM memberships m
JOIN organizations o ON o.id = m.org_id
WHERE m.user_id = $1
ORDER BY m.role = 'owner' DESC, m.created_at ASC;

-- name: ListUsersByOrg :many
SELECT * FROM users WHERE org_id = $1 AND deleted_at IS NULL ORDER BY display_name ASC;

-- name: UpdateUser :one
UPDATE users
SET display_name = $2, avatar_url = $3, role = $4, is_active = $5
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateUserPasswordHash :exec
UPDATE users SET password_hash = $2 WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, token_hash, ip_address, user_agent, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT s.id, s.user_id, s.token_hash, s.ip_address, s.user_agent,
       s.created_at, s.expires_at, s.revoked_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > now()
  AND u.deleted_at IS NULL;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAllUserSessions :exec
UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < now();

-- name: CreateMembership :one
INSERT INTO memberships (id, org_id, user_id, role, invited_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetMembership :one
SELECT * FROM memberships WHERE org_id = $1 AND user_id = $2;

-- name: ListMembershipsByOrg :many
SELECT m.id, m.org_id, m.user_id, m.role, m.invited_by, m.created_at, m.updated_at,
       u.email, u.display_name, u.avatar_url
FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1
ORDER BY u.display_name ASC;

-- name: UpdateMembershipRole :exec
UPDATE memberships SET role = $3 WHERE org_id = $1 AND user_id = $2;

-- name: DeleteMembership :exec
DELETE FROM memberships WHERE org_id = $1 AND user_id = $2;
