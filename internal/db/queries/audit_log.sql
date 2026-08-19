-- name: CreateAuditEvent :one
-- batch_id and ticket_ref (migration 025) are nullable: NULL for ordinary
-- single events; a bulk grant change writes its events with one shared
-- batch_id (and optional operator ticket_ref) inside the same transaction
-- as the grants themselves.
INSERT INTO audit_log (id, org_id, actor_id, action, entity_kind, entity_id, payload, ip_address, user_agent, batch_id, ticket_ref)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListAuditEventsByOrg :many
SELECT * FROM audit_log WHERE org_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAuditEventsByEntity :many
SELECT * FROM audit_log WHERE entity_kind = $1 AND entity_id = $2 ORDER BY created_at DESC;

-- name: ListAuditEventsByActor :many
SELECT * FROM audit_log WHERE actor_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListEntityHistory :many
-- The per-entity History surface (D5): the audit trail for ONE ticket or item,
-- for a space reader. This is NOT the org-admin audit viewer — the raw log is an
-- org-admin surface (see ListAuditLogEntries, mounted behind RequireOrgAdmin404
-- on /audit-log). This read is space-read-guarded, and the vocabulary it may
-- show is narrower: the caller passes @actions, and only rows whose action is in
-- that set come back, so an org-admin-only event (a grant, a role change) that
-- happens to carry this entity's id can never reach a space member through here.
-- The filter is server-side on purpose — the client is not trusted to omit what
-- it must not see.
--
-- entity_kind is the AUDIT kind, which for a project item is "item" (not the
-- "project_item" the comments/scoping code uses); the handler passes the right
-- literal. No ip_address / user_agent: those are forensic columns even the admin
-- viewer does not surface, and they have no place on a contributor-visible
-- history. actor_name is joined the same way the admin viewer joins it, so a
-- deleted actor renders as empty rather than a bare id.
SELECT a.id, a.actor_id, COALESCE(u.display_name, '')::text AS actor_name,
       a.action, a.payload, a.created_at
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_id
WHERE a.entity_kind = @entity_kind
  AND a.entity_id = @entity_id
  AND a.action = ANY(@actions::text[])
ORDER BY a.created_at DESC, a.id DESC;

-- name: ListAuditLogEntries :many
-- The admin audit viewer (P2.5 W7). One query, constant cost: events
-- sharing a batch_id collapse to a single representative row (the newest
-- event of the batch) carrying the batch size; singleton events pass
-- through with batch_size 1. Keyset cursor over (created_at, id) of the
-- representative rows. All filters are optional.
SELECT id, actor_id, actor_name, action, entity_kind, entity_id, payload,
       batch_id, ticket_ref, created_at, batch_size
FROM (
    SELECT a.id, a.actor_id, a.action, a.entity_kind, a.entity_id,
           a.payload, a.batch_id, a.ticket_ref, a.created_at,
           COALESCE(u.display_name, '')::text AS actor_name,
           (COUNT(*) OVER (PARTITION BY COALESCE(a.batch_id, a.id)))::int AS batch_size,
           ROW_NUMBER() OVER (PARTITION BY COALESCE(a.batch_id, a.id)
                              ORDER BY a.created_at DESC, a.id DESC) AS rn
    FROM audit_log a
    LEFT JOIN users u ON u.id = a.actor_id
    WHERE a.org_id = sqlc.arg(org_id)
      AND (sqlc.narg(actor_id)::uuid IS NULL OR a.actor_id = sqlc.narg(actor_id))
      AND (sqlc.narg(entity_kind)::text IS NULL OR a.entity_kind = sqlc.narg(entity_kind))
      AND (sqlc.narg(action)::text IS NULL OR a.action = sqlc.narg(action))
      AND (sqlc.narg(created_from)::timestamptz IS NULL OR a.created_at >= sqlc.narg(created_from))
      AND (sqlc.narg(created_to)::timestamptz IS NULL OR a.created_at <= sqlc.narg(created_to))
) grouped
WHERE rn = 1
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: ListAuditLogBatchEvents :many
-- Expanding one batch row into its constituent events.
SELECT a.id, a.actor_id, a.action, a.entity_kind, a.entity_id, a.payload,
       a.batch_id, a.ticket_ref, a.created_at,
       COALESCE(u.display_name, '')::text AS actor_name
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_id
WHERE a.org_id = $1 AND a.batch_id = $2
ORDER BY a.created_at ASC, a.id ASC;
