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

-- name: UpdateUserProfile :one
UPDATE users
SET display_name = $2, email = $3
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateUserPasswordHash :exec
-- The token_generation bump is built into the statement so no password
-- change path can forget it: changing a password signs out every other
-- session (P2.5 session control).
UPDATE users SET password_hash = $2, token_generation = token_generation + 1
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: GetUserAuthState :one
-- The per-request auth check (P2.5 session control): one primary-key read
-- comparing the JWT's token_generation claim against the live column and
-- rejecting deactivated accounts. Constant cost — TestMatrixAPI23 asserts
-- this statement runs exactly once per authenticated request, so it cannot
-- be silently optimised away.
SELECT token_generation, is_active FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: BumpTokenGeneration :execrows
-- Force logout: instantly invalidates every token the user holds. The user
-- stays active and simply signs in again.
UPDATE users SET token_generation = token_generation + 1
WHERE id = $1 AND deleted_at IS NULL;

-- name: DeactivateUserAccount :execrows
-- Deactivation always terminates sessions: the generation bump rides in the
-- same statement so there is no code path that deactivates without it.
UPDATE users SET is_active = false, token_generation = token_generation + 1
WHERE id = $1 AND deleted_at IS NULL AND is_active;

-- name: ReactivateUserAccount :execrows
-- No generation bump: the old tokens died at deactivation; the user signs
-- in fresh.
UPDATE users SET is_active = true
WHERE id = $1 AND deleted_at IS NULL AND NOT is_active;

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

-- name: ListOrgPeople :many
-- The admin People page: every member with org role, primary team, status,
-- and last sign-in, in one query (matrix case 23 — constant cost regardless
-- of member count). Search and status filtering happen client-side over
-- this single fetch.
SELECT m.user_id, m.role AS org_role, m.created_at AS joined_at,
       u.email, u.display_name, u.avatar_url, u.is_active, u.last_login_at,
       pt.team_id AS primary_team_id, ptt.name AS primary_team_name
FROM memberships m
JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL
LEFT JOIN team_members pt ON pt.user_id = m.user_id AND pt.org_id = m.org_id AND pt.is_primary
LEFT JOIN teams ptt ON ptt.id = pt.team_id AND ptt.deleted_at IS NULL
WHERE m.org_id = $1
ORDER BY u.display_name ASC, u.id ASC;

-- name: SearchOrgMembers :many
-- The person picker: name-or-email search over active org members. Bounded
-- result set; one query.
SELECT u.id, u.email, u.display_name, u.avatar_url
FROM memberships m
JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL
WHERE m.org_id = $1 AND u.is_active
  AND (u.display_name ILIKE '%' || sqlc.arg(query)::text || '%'
       OR u.email ILIKE '%' || sqlc.arg(query)::text || '%')
ORDER BY u.display_name ASC, u.id ASC
LIMIT 20;

-- name: CountOtherActiveOrgAdmins :one
-- Last-admin protection: how many OTHER members of the org hold an
-- admin-class role on an active, non-deleted account. The admin-class role
-- names are passed in from the one Go site that interprets org role names
-- (rbac) — this query never hardcodes them.
SELECT count(*) FROM memberships m
JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL AND u.is_active
WHERE m.org_id = $1 AND m.user_id <> $2
  AND m.role = ANY(sqlc.arg(admin_roles)::text[]);

-- name: ListOrgsWhereUserIsLastAdmin :many
-- Global deactivation guard: is_active is a user-level column, so
-- deactivating someone must be blocked if it would leave ANY org they
-- administer with zero active admins — not just the org the action was
-- taken from.
SELECT m.org_id FROM memberships m
WHERE m.user_id = $1 AND m.role = ANY(sqlc.arg(admin_roles)::text[])
  AND NOT EXISTS (
    SELECT 1 FROM memberships m2
    JOIN users u2 ON u2.id = m2.user_id AND u2.deleted_at IS NULL AND u2.is_active
    WHERE m2.org_id = m.org_id AND m2.user_id <> m.user_id
      AND m2.role = ANY(sqlc.arg(admin_roles)::text[])
  );

-- name: DeleteTeamMembershipsInOrg :exec
-- Removal from an org drops the user's team rows in that org only.
DELETE FROM team_members WHERE user_id = $1 AND org_id = $2;

-- name: LockAdminMembershipsInOrg :many
-- Serialises concurrent admin-lifecycle operations in one org so two
-- simultaneous demotions of the two last admins cannot both pass the
-- last-admin check. Deterministic ORDER BY prevents deadlock.
SELECT m.user_id FROM memberships m
WHERE m.org_id = $1 AND m.role = ANY(sqlc.arg(admin_roles)::text[])
ORDER BY m.user_id
FOR UPDATE;

-- name: LockAdminMembershipsForUserOrgs :many
-- The deactivation variant: locks the admin-class membership rows of every
-- org the target user administers, since deactivation is global.
SELECT m.org_id, m.user_id FROM memberships m
WHERE m.org_id IN (
        SELECT m2.org_id FROM memberships m2
        WHERE m2.user_id = sqlc.arg(target_user_id)
          AND m2.role = ANY(sqlc.arg(admin_roles)::text[])
      )
  AND m.role = ANY(sqlc.arg(admin_roles)::text[])
ORDER BY m.org_id, m.user_id
FOR UPDATE;
