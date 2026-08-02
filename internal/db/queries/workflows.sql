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
-- Add an edge to a workflow, refusing endpoints that are not its own states.
--
-- from_state_id and to_state_id arrive in the REQUEST BODY. The only thing that
-- constrained them was migration 016's `REFERENCES workflow_states (id)` — a
-- bare foreign key to the whole table, satisfied by any state of any workflow in
-- any organisation. The handler's workflowInOrg establishes that the workflow in
-- the URL belongs to the caller's org and establishes nothing whatever about the
-- two ids in the body, so an org admin could stitch another workflow's states —
-- and so another space's, or another organisation's — into their own graph.
--
-- The predicate lives here rather than in the handler because a load-then-compare
-- has a window between the two: a state deleted (migration 016 cascades) or a
-- workflow re-pointed after the check and before the INSERT lands an edge the
-- check would have refused. One statement has no such window, and it is the
-- shape AssignProjectItemToSprintInSpace already uses for the same reason.
--
-- The refusal is one answer for two questions. A state id naming nothing at all
-- fails this predicate for exactly the same reason a state id naming another
-- workflow's state does — neither is a state of @workflow_id — so the route
-- cannot be used to ask whether some other workflow's state exists. Before this,
-- the two were distinguishable: an invented uuid violated the foreign key and
-- answered 500, a real state anywhere in the installation answered 201. That
-- difference was an existence oracle over every workflow state in every org,
-- which is the same defect CommentBelongsToEntity closed for comment parents.
--
-- ws is aliased in both subqueries because sqlc's analyser flattens EXISTS into
-- the outer scope, where an unqualified `id` is ambiguous.
INSERT INTO workflow_transitions (workflow_id, from_state_id, to_state_id, name)
SELECT @workflow_id::uuid, @from_state_id::uuid, @to_state_id::uuid, @name::text
WHERE EXISTS (
    SELECT 1 FROM workflow_states ws
     WHERE ws.id = @from_state_id::uuid
       AND ws.workflow_id = @workflow_id::uuid
)
  AND EXISTS (
    SELECT 1 FROM workflow_states ws
     WHERE ws.id = @to_state_id::uuid
       AND ws.workflow_id = @workflow_id::uuid
)
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
