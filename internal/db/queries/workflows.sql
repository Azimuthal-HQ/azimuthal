-- name: CreateWorkflow :one
INSERT INTO workflows (org_id, name, description, is_default, applies_to)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetWorkflow :one
SELECT * FROM workflows WHERE id = $1;

-- name: GetDefaultWorkflow :one
SELECT * FROM workflows
WHERE org_id = $1 AND applies_to = $2 AND is_default = TRUE
LIMIT 1;

-- name: ListWorkflows :many
SELECT * FROM workflows WHERE org_id = $1 ORDER BY name;

-- name: UpdateWorkflow :one
UPDATE workflows
SET name = $2, description = $3, is_default = $4, applies_to = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteWorkflow :exec
DELETE FROM workflows WHERE id = $1;

-- name: CreateWorkflowState :one
INSERT INTO workflow_states (workflow_id, name, category, color, position, is_initial)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetWorkflowState :one
SELECT * FROM workflow_states WHERE id = $1;

-- name: GetInitialWorkflowState :one
SELECT * FROM workflow_states WHERE workflow_id = $1 AND is_initial = TRUE;

-- name: ListWorkflowStates :many
SELECT * FROM workflow_states WHERE workflow_id = $1 ORDER BY position;

-- name: UpdateWorkflowState :one
UPDATE workflow_states
SET name = $2, category = $3, color = $4, position = $5, is_initial = $6
WHERE id = $1
RETURNING *;

-- name: DeleteWorkflowState :exec
DELETE FROM workflow_states WHERE id = $1;

-- name: CreateWorkflowTransition :one
INSERT INTO workflow_transitions (workflow_id, from_state_id, to_state_id, name)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetWorkflowTransition :one
SELECT * FROM workflow_transitions WHERE id = $1;

-- name: ListWorkflowTransitions :many
SELECT * FROM workflow_transitions WHERE workflow_id = $1;

-- name: ListAvailableTransitions :many
SELECT * FROM workflow_transitions WHERE workflow_id = $1 AND from_state_id = $2;

-- name: DeleteWorkflowTransition :exec
DELETE FROM workflow_transitions WHERE id = $1;

-- name: AssignWorkflowToSpace :exec
UPDATE spaces SET workflow_id = $1 WHERE id = $2;

-- name: GetSpaceWorkflowStates :many
SELECT ws.*
FROM workflow_states ws
JOIN spaces s ON s.workflow_id = ws.workflow_id
WHERE s.id = $1
ORDER BY ws.position;

-- name: GetSpaceWorkflow :one
SELECT w.*
FROM workflows w
JOIN spaces s ON s.workflow_id = w.id
WHERE s.id = $1;

-- name: BulkCreateWorkflowStates :exec
INSERT INTO workflow_states (id, workflow_id, name, category, color, position, is_initial)
SELECT
    unnest($1::uuid[]),
    unnest($2::uuid[]),
    unnest($3::text[]),
    unnest($4::text[]),
    unnest($5::text[]),
    unnest($6::int[]),
    unnest($7::boolean[]);

-- name: BulkCreateWorkflowTransitions :exec
INSERT INTO workflow_transitions (workflow_id, from_state_id, to_state_id, name)
SELECT
    unnest($1::uuid[]),
    unnest($2::uuid[]),
    unnest($3::uuid[]),
    unnest($4::text[]);
