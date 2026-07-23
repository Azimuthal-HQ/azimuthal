-- name: CreateLabel :one
INSERT INTO labels (id, org_id, name, color) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListLabelsByOrg :many
SELECT * FROM labels WHERE org_id = $1 ORDER BY name ASC;

-- name: DeleteLabel :exec
DELETE FROM labels WHERE id = $1;

-- name: CreateSprint :one
INSERT INTO sprints (id, space_id, name, goal, status, starts_at, ends_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetSprintByID :one
SELECT * FROM sprints WHERE id = $1;

-- name: ListSprintsBySpace :many
SELECT * FROM sprints WHERE space_id = $1 ORDER BY created_at DESC;

-- name: GetActiveSprintBySpace :one
SELECT * FROM sprints WHERE space_id = $1 AND status = 'active' LIMIT 1;

-- name: UpdateSprintStatus :one
UPDATE sprints SET status = $2 WHERE id = $1 RETURNING *;

-- name: UpdateSprint :one
UPDATE sprints SET name = $2, goal = $3, starts_at = $4, ends_at = $5 WHERE id = $1 RETURNING *;

-- name: ReassignIncompleteSprintItems :execrows
-- Reassigns every not-yet-done item in a sprint to a target sprint, or to the
-- backlog when next_sprint_id is NULL. Items whose status is in the supplied
-- done set are left on the completing sprint (they belong to its record).
-- Used by sprint completion to empty the sprint of unfinished work.
UPDATE project_items
SET sprint_id = sqlc.narg('next_sprint_id'), updated_at = now()
WHERE sprint_id = @sprint_id
  AND deleted_at IS NULL
  AND NOT (status = ANY(@done_statuses::text[]));

-- name: CreateEntityRelation :one
INSERT INTO entity_relations (id, from_id, from_type, to_id, to_type, kind, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListEntityRelationsByEntity :many
SELECT er.id, er.from_id, er.from_type, er.to_id, er.to_type, er.kind, er.created_by, er.created_at,
       COALESCE(t.title,  pi.title)  AS to_title,
       COALESCE(t.status, pi.status) AS to_status
FROM entity_relations er
LEFT JOIN tickets      t  ON er.to_id = t.id  AND er.to_type = 'ticket'
LEFT JOIN project_items pi ON er.to_id = pi.id AND er.to_type = 'project_item'
WHERE er.from_id = $1 AND er.from_type = $2;

-- name: DeleteEntityRelation :exec
DELETE FROM entity_relations WHERE id = $1;

-- name: CountItemsArchiveTickets :one
SELECT COUNT(*) FROM items_archive WHERE kind = 'ticket' AND deleted_at IS NULL;

-- name: CountItemsArchiveProjectItems :one
SELECT COUNT(*) FROM items_archive WHERE kind IN ('task','story','epic','bug') AND deleted_at IS NULL;
