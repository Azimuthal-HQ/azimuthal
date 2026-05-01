-- name: CreateTicket :one
INSERT INTO tickets (id, space_id, number, title, description, status, priority,
                     reporter_id, assignee_id, labels, due_at, rank)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetTicketByID :one
SELECT * FROM tickets WHERE id = $1 AND deleted_at IS NULL;

-- name: ListTicketsBySpace :many
SELECT * FROM tickets
WHERE space_id = $1 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListTicketsByStatus :many
SELECT * FROM tickets
WHERE space_id = $1 AND status = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListTicketsByAssignee :many
SELECT * FROM tickets
WHERE space_id = $1 AND assignee_id = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: UpdateTicket :one
UPDATE tickets
SET title = $2, description = $3, status = $4, priority = $5,
    assignee_id = $6, labels = $7, due_at = $8, rank = $9,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateTicketStatus :one
UPDATE tickets
SET status = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteTicket :exec
UPDATE tickets SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: SearchTickets :many
SELECT * FROM tickets
WHERE space_id = $1
  AND deleted_at IS NULL
  AND search_vector @@ plainto_tsquery('english', $2)
ORDER BY ts_rank(search_vector, plainto_tsquery('english', $2)) DESC
LIMIT $3;

-- name: GetTicketMaxNumber :one
SELECT COALESCE(MAX(number), 0)::bigint AS max_number FROM tickets WHERE space_id = $1;
