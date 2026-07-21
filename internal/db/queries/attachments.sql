-- Attachments (P3, ADR-0008 rule 3). Reads are authorised through the
-- OWNING ENTITY: handlers look the row up by id (scoped to the entity in
-- the URL), decide access from the entity's space or an active share, and
-- derive the object key from the row — a client-supplied key never reaches
-- the object store.

-- name: CreateAttachment :one
INSERT INTO attachments (id, org_id, entity_type, entity_id, filename,
                         content_type, size_bytes, object_key, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetAttachment :one
SELECT * FROM attachments WHERE id = $1 AND deleted_at IS NULL;

-- name: ListAttachmentsByEntity :many
SELECT id, org_id, entity_type, entity_id, filename, content_type,
       size_bytes, created_by, created_at
FROM attachments
WHERE entity_type = $1 AND entity_id = $2 AND deleted_at IS NULL
ORDER BY created_at ASC;

-- name: SoftDeleteAttachment :exec
UPDATE attachments SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;
