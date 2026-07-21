-- Entity shares (v0.3 spec §4 migration 026, §5, §6, ADR-0008). Shares
-- widen, never narrow. (entity_type, entity_id) is polymorphic with no FK;
-- the store layer owns integrity — shares are revoked in the same
-- transaction that deletes or cross-space-moves their entity.

-- name: CreateEntityShare :one
INSERT INTO entity_shares (id, org_id, space_id, entity_type, entity_id,
                           audience, audience_id, cascade, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetEntityShare :one
SELECT * FROM entity_shares WHERE id = $1;

-- name: RevokeEntityShare :one
-- Revocation sets revoked_at; rows are never hard-deleted (ADR-0008). The
-- revoked_at IS NULL guard makes double-revocation detectable (no rows).
UPDATE entity_shares SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: ListSharesByEntity :many
-- Active (unrevoked) shares for one entity, expired included — the dialog
-- shows expired rows as expired instead of silently hiding them. Resolution
-- (ResolveShareRows) is where expiry actually denies access.
SELECT * FROM entity_shares
WHERE org_id = $1 AND entity_type = $2 AND entity_id = $3 AND revoked_at IS NULL
ORDER BY created_at ASC;

-- name: ResolveShareRows :many
-- The once-per-request share resolution (spec §5 readable_entity_ids).
-- One round trip returning every ACTIVE share whose audience includes the
-- caller: org-audience rows, plus team-audience rows whose team is in the
-- caller's effective set (direct teams plus all descendants — the same
-- subject-side expansion as ResolveAccessRows). Expiry is evaluated HERE,
-- so an expired share stops granting access on the very next request with
-- no sweeper involved. Cascade roots carry their page's current path so
-- subtree coverage is a prefix check computed per request — a moved root
-- covers its new subtree immediately.
WITH direct_teams AS (
    SELECT tm.team_id
    FROM team_members tm
    JOIN teams dt ON dt.id = tm.team_id AND dt.deleted_at IS NULL
    WHERE tm.user_id = sqlc.arg(user_id) AND tm.org_id = sqlc.arg(org_id)
),
effective_teams AS (
    SELECT t.id
    FROM teams t
    WHERE t.org_id = sqlc.arg(org_id)
      AND t.deleted_at IS NULL
      AND t.path && (SELECT COALESCE(array_agg(team_id), '{}')::uuid[] FROM direct_teams)
)
SELECT s.entity_type, s.entity_id, s.cascade,
       p.path AS root_path, p.space_id AS root_space_id
FROM entity_shares s
LEFT JOIN pages p ON s.entity_type = 'page' AND p.id = s.entity_id AND p.deleted_at IS NULL
WHERE s.org_id = sqlc.arg(org_id)
  AND s.revoked_at IS NULL
  AND (s.expires_at IS NULL OR s.expires_at > now())
  AND (s.audience = 'org'
       OR (s.audience = 'team' AND s.audience_id IN (SELECT id FROM effective_teams)));

-- name: RevokeSharesByEntity :many
-- Revoke-on-delete (ADR-0008 rule 10): run through the entity delete's own
-- transaction via WithTx. RETURNING feeds the in-transaction audit rows.
UPDATE entity_shares SET revoked_at = now()
WHERE entity_type = $1 AND entity_id = $2 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeSharesByPageSubtree :many
-- Revoke-on-move (ADR-0008 rule 9) for a page and every descendant: any of
-- them may carry its own share, and after a cross-space move each one would
-- otherwise stay visible from inside the new space. path_pattern is the
-- LIKE-escaped old path plus '.%' — built by the caller with EscapeLike so
-- a stored '%' or '_' can never widen the match (defence in depth: paths
-- are dotted UUIDs today, but the column is unconstrained TEXT).
UPDATE entity_shares s SET revoked_at = now()
WHERE s.entity_type = 'page'
  AND s.revoked_at IS NULL
  AND s.entity_id IN (
      SELECT p.id FROM pages p
      WHERE p.space_id = sqlc.arg(space_id)
        AND p.deleted_at IS NULL
        AND (p.id = sqlc.arg(page_id) OR p.path LIKE sqlc.arg(path_pattern))
  )
RETURNING *;

-- name: CountActiveSharesForPageSubtree :one
-- Backs the move-confirmation warning: how many live (unrevoked, unexpired)
-- shares would a cross-space move revoke. Same subtree shape as the
-- revocation query so the warning counts exactly what the move would kill.
SELECT count(*) FROM entity_shares s
WHERE s.entity_type = 'page'
  AND s.revoked_at IS NULL
  AND (s.expires_at IS NULL OR s.expires_at > now())
  AND s.entity_id IN (
      SELECT p.id FROM pages p
      WHERE p.space_id = sqlc.arg(space_id)
        AND p.deleted_at IS NULL
        AND (p.id = sqlc.arg(page_id) OR p.path LIKE sqlc.arg(path_pattern))
  );

-- name: CountPageSubtree :one
-- Backs the cascade-share confirmation (ADR-0008 rule 7): the affected page
-- count, root included, served by the API — never counted client-side.
SELECT count(*) FROM pages p
WHERE p.space_id = sqlc.arg(space_id)
  AND p.deleted_at IS NULL
  AND (p.id = sqlc.arg(page_id) OR p.path LIKE sqlc.arg(path_pattern));

-- name: ListActiveSharesForSpacePages :many
-- Backs the ShareBadge annotation for a whole space in ONE query (matrix
-- case 23: constant queries regardless of page count): every active page
-- share rooted in the space, with the root's current path so cascade
-- coverage is a prefix check in the handler.
SELECT s.entity_id, s.cascade, p.path AS root_path
FROM entity_shares s
JOIN pages p ON p.id = s.entity_id AND p.deleted_at IS NULL
WHERE s.entity_type = 'page'
  AND p.space_id = $1
  AND s.revoked_at IS NULL
  AND (s.expires_at IS NULL OR s.expires_at > now());

-- name: LookupSharedPage :one
-- Entity → container resolution for share management and the shared read
-- guard: one query, space join for the org (tenancy is always re-checked
-- against the URL org).
SELECT p.id, p.space_id, p.path, s.org_id
FROM pages p
JOIN spaces s ON s.id = p.space_id AND s.deleted_at IS NULL
WHERE p.id = $1 AND p.deleted_at IS NULL;

-- name: LookupSharedTicket :one
SELECT t.id, t.space_id, s.org_id
FROM tickets t
JOIN spaces s ON s.id = t.space_id AND s.deleted_at IS NULL
WHERE t.id = $1 AND t.deleted_at IS NULL;

-- name: LookupSharedProjectItem :one
SELECT i.id, i.space_id, s.org_id
FROM project_items i
JOIN spaces s ON s.id = i.space_id AND s.deleted_at IS NULL
WHERE i.id = $1 AND i.deleted_at IS NULL;

-- name: HasActiveShareForEntity :one
-- Backs the ShareBadge on ticket and project-item detail (flat entities:
-- direct shares only, no cascade).
SELECT EXISTS(
    SELECT 1 FROM entity_shares
    WHERE entity_type = $1 AND entity_id = $2 AND revoked_at IS NULL
      AND (expires_at IS NULL OR expires_at > now())
) AS shared;
