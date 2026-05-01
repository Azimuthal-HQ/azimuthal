-- name: CreateAuditEvent :one
INSERT INTO audit_log (id, org_id, actor_id, action, entity_kind, entity_id, payload, ip_address, user_agent)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListAuditEventsByOrg :many
SELECT * FROM audit_log WHERE org_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAuditEventsByEntity :many
SELECT * FROM audit_log WHERE entity_kind = $1 AND entity_id = $2 ORDER BY created_at DESC;

-- name: ListAuditEventsByActor :many
SELECT * FROM audit_log WHERE actor_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;
