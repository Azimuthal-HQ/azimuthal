-- Invites (P2.5, migration 024). token_hash is the SHA-256 of the raw
-- invite token; the raw token is generated with crypto/rand, returned to the
-- admin exactly once at creation (and again on resend), and never stored.
-- An invite is active while accepted_at and revoked_at are both NULL; the
-- partial unique index invites_one_active_per_email holds the slot.

-- name: CreateInvite :one
INSERT INTO invites (id, org_id, email, token_hash, org_role, team_id, invited_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetInviteByTokenHash :one
SELECT * FROM invites WHERE token_hash = $1;

-- name: GetInviteByID :one
SELECT * FROM invites WHERE id = $1 AND org_id = $2;

-- name: ListActiveInvitesByOrg :many
-- The pending set: not accepted, not revoked. Expired rows stay listed so
-- the admin can resend (refreshing the token in place) or revoke them;
-- accepted and revoked rows are history and live in the audit log.
SELECT i.id, i.org_id, i.email, i.org_role, i.team_id, i.invited_by,
       i.expires_at, i.created_at,
       u.display_name AS invited_by_name,
       t.name AS team_name
FROM invites i
JOIN users u ON u.id = i.invited_by
LEFT JOIN teams t ON t.id = i.team_id AND t.deleted_at IS NULL
WHERE i.org_id = $1 AND i.accepted_at IS NULL AND i.revoked_at IS NULL
ORDER BY i.created_at DESC;

-- name: GetActiveInviteByEmail :one
SELECT * FROM invites
WHERE org_id = $1 AND lower(email) = lower(sqlc.arg(email))
  AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: RevokeInvite :execrows
UPDATE invites SET revoked_at = now()
WHERE id = $1 AND org_id = $2 AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: RefreshInviteToken :one
-- Resend: a fresh token replaces the old one in place — the previous link
-- stops working the moment this commits — and the expiry window restarts.
UPDATE invites SET token_hash = $3, expires_at = $4
WHERE id = $1 AND org_id = $2 AND accepted_at IS NULL AND revoked_at IS NULL
RETURNING *;

-- name: MarkInviteAccepted :execrows
-- Guarded so a revoked, expired, or already-accepted invite cannot be
-- consumed: 0 rows means the invite was not acceptable at commit time.
UPDATE invites SET accepted_at = now(), accepted_user_id = $2
WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now();
