-- Workflow tiers (ADR-0011): conditions and validators, approvals, and the
-- fixed post-function set. Schema in migrations 046 and 047.
--
-- The three "ForTransition" reads below are on the transition hot path — every
-- status change runs all three — and every one of them is a single index scan
-- on (transition_id, …). They are separate queries rather than one join
-- because a transition with no guards, no approvers and no post-functions (the
-- default, and the overwhelming majority) must cost three empty index probes
-- and nothing else.

-- ─── Tier 1: guards ───────────────────────────────────────────────────────────

-- name: ListTransitionGuards :many
-- Ordered so a transition refused by two guards names the same one every time.
SELECT * FROM workflow_transition_guards
WHERE transition_id = $1
ORDER BY position, id;

-- name: ListWorkflowGuards :many
-- Every guard in a workflow, for the admin surface. Joined through transitions
-- so one round trip populates the whole editor.
SELECT g.* FROM workflow_transition_guards g
JOIN workflow_transitions t ON t.id = g.transition_id
WHERE t.workflow_id = $1
ORDER BY g.transition_id, g.position, g.id;

-- name: CreateTransitionGuard :one
INSERT INTO workflow_transition_guards
    (transition_id, guard_class, kind, position, capability, team_id, field_key)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: DeleteTransitionGuard :execrows
DELETE FROM workflow_transition_guards WHERE id = $1;

-- name: GetTransitionGuard :one
SELECT * FROM workflow_transition_guards WHERE id = $1;

-- ─── Tier 3: post-functions ───────────────────────────────────────────────────

-- name: ListTransitionPostFunctions :many
-- Ordered because a later post-function writing the same field as an earlier
-- one wins, which is only meaningful if the order is stable.
SELECT * FROM workflow_transition_post_functions
WHERE transition_id = $1
ORDER BY position, id;

-- name: ListWorkflowPostFunctions :many
SELECT p.* FROM workflow_transition_post_functions p
JOIN workflow_transitions t ON t.id = p.transition_id
WHERE t.workflow_id = $1
ORDER BY p.transition_id, p.position, p.id;

-- name: CreateTransitionPostFunction :one
INSERT INTO workflow_transition_post_functions
    (transition_id, kind, position, assignee_user_id, field_key, field_value)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: DeleteTransitionPostFunction :execrows
DELETE FROM workflow_transition_post_functions WHERE id = $1;

-- ─── Tier 2: approver configuration ───────────────────────────────────────────

-- name: ListTransitionApprovers :many
-- The subject display name is resolved by the same LEFT JOIN-on-discriminator
-- shape space_grants uses (internal/db/queries/space_grants.sql), so an
-- approver and a grant render a deleted subject identically. subject_id carries
-- no foreign key — it is polymorphic — so a missing subject is a real state and
-- subject_missing reports it rather than hiding it.
SELECT
    a.*,
    COALESCE(CASE WHEN a.subject_type = 'user' THEN u.display_name ELSE t.name END, '')::text AS subject_name,
    (CASE WHEN a.subject_type = 'user' THEN u.id IS NULL ELSE t.id IS NULL END)::boolean AS subject_missing
FROM workflow_transition_approvers a
LEFT JOIN users u ON a.subject_type = 'user' AND u.id = a.subject_id AND u.deleted_at IS NULL
LEFT JOIN teams t ON a.subject_type = 'team' AND t.id = a.subject_id AND t.deleted_at IS NULL
WHERE a.transition_id = $1
ORDER BY a.subject_type, subject_name, a.id;

-- name: ListWorkflowApprovers :many
SELECT
    a.*,
    COALESCE(CASE WHEN a.subject_type = 'user' THEN u.display_name ELSE tm.name END, '')::text AS subject_name,
    (CASE WHEN a.subject_type = 'user' THEN u.id IS NULL ELSE tm.id IS NULL END)::boolean AS subject_missing
FROM workflow_transition_approvers a
JOIN workflow_transitions t ON t.id = a.transition_id
LEFT JOIN users u  ON a.subject_type = 'user' AND u.id  = a.subject_id AND u.deleted_at IS NULL
LEFT JOIN teams tm ON a.subject_type = 'team' AND tm.id = a.subject_id AND tm.deleted_at IS NULL
WHERE t.workflow_id = $1
ORDER BY a.transition_id, a.subject_type, subject_name, a.id;

-- name: CreateTransitionApprover :one
INSERT INTO workflow_transition_approvers (transition_id, subject_type, subject_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteTransitionApprover :execrows
DELETE FROM workflow_transition_approvers WHERE id = $1;

-- ─── Tier 2: approval instances ───────────────────────────────────────────────

-- name: CreateApproval :one
-- The partial unique index workflow_approvals_one_pending_per_entity is what
-- actually prevents two concurrent requests, not a preceding read; the adapter
-- maps its violation to ErrApprovalPending. from_status and to_status are
-- captured here rather than recomputed at decision time, because a state rename
-- does not rewrite the status text on items.
INSERT INTO workflow_approvals (
    transition_id, entity_type, entity_id, space_id,
    from_state_id, to_state_id, from_status, to_status, requested_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetPendingApprovalForEntity :one
SELECT * FROM workflow_approvals
WHERE entity_type = $1 AND entity_id = $2 AND decided_at IS NULL;

-- name: GetApproval :one
SELECT
    sqlc.embed(a),
    COALESCE(r.display_name, '')::text AS requested_by_name,
    COALESCE(d.display_name, '')::text AS decided_by_name
FROM workflow_approvals a
LEFT JOIN users r ON r.id = a.requested_by
LEFT JOIN users d ON d.id = a.decided_by
WHERE a.id = $1;

-- name: DecideApproval :one
-- The WHERE clause carries `decided_at IS NULL`, so a second approver deciding
-- concurrently updates zero rows rather than overwriting the first decision.
-- The adapter turns zero rows into ErrApprovalAlreadyDecided after a follow-up
-- read, the way RevokeEntityShare distinguishes already-revoked from not-found.
UPDATE workflow_approvals
SET decided_by = $2, decided_at = now(), decision = $3
WHERE id = $1 AND decided_at IS NULL
RETURNING *;

-- name: ListPendingApprovalsForSpace :many
-- Powers both the "awaiting a decision" surface and the board's blocked
-- markers. Space-scoped because a decision is authorised against the space the
-- request was made in.
SELECT
    sqlc.embed(a),
    COALESCE(r.display_name, '')::text AS requested_by_name,
    ''::text AS decided_by_name
FROM workflow_approvals a
LEFT JOIN users r ON r.id = a.requested_by
WHERE a.space_id = $1 AND a.decided_at IS NULL
ORDER BY a.requested_at DESC, a.id;

-- name: ListApprovalsForEntity :many
-- The item's own history: every request ever made about it, newest first. A
-- declined request is kept, so the record of who asked and who refused survives
-- the item moving on.
SELECT
    sqlc.embed(a),
    COALESCE(r.display_name, '')::text AS requested_by_name,
    COALESCE(d.display_name, '')::text AS decided_by_name
FROM workflow_approvals a
LEFT JOIN users r ON r.id = a.requested_by
LEFT JOIN users d ON d.id = a.decided_by
WHERE a.entity_type = $1 AND a.entity_id = $2
ORDER BY a.requested_at DESC, a.id;

-- name: CountPendingApprovalsForTransition :one
-- Asked before an administrator deletes a transition, so the delete can refuse
-- rather than orphan in-flight requests. migration 047 makes transition_id
-- ON DELETE SET NULL precisely so a delete that slips through does not destroy
-- the record — this count is what stops it slipping through in the first place.
SELECT COUNT(*)::bigint FROM workflow_approvals
WHERE transition_id = $1 AND decided_at IS NULL;

-- ─── Resolution helpers for the transition chokepoint ─────────────────────────

-- name: GetWorkflowStateByName :one
-- Maps a status string onto the workflow state it names. The legacy /status
-- routes speak status text, the tier model speaks state ids, and this is the
-- only place the two meet.
SELECT * FROM workflow_states
WHERE workflow_id = $1 AND name = $2;

-- name: GetTransitionByStates :one
-- The edge between two states, if the workflow defines one. UNIQUE
-- (workflow_id, from_state_id, to_state_id) from migration 016 guarantees at
-- most one row.
SELECT * FROM workflow_transitions
WHERE workflow_id = $1 AND from_state_id = $2 AND to_state_id = $3;

-- name: GetWorkflowInOrg :one
-- Workflow lookup scoped to an org.
--
-- The pre-existing org-scoped workflow routes resolve {workflowID} without
-- checking it belongs to {orgID}; that is recorded as a finding rather than
-- changed here, because changing it alters existing behaviour. Every route this
-- phase adds uses THIS query instead, so the new tier surface does not widen
-- the exposure.
SELECT * FROM workflows WHERE id = $1 AND org_id = $2;

-- ─── Applying post-function effects ───────────────────────────────────────────

-- The two queries below apply every planned effect in ONE statement, inside the
-- transaction that writes the status.
--
-- Each field carries an explicit `set_*` flag rather than relying on the value
-- being NULL, because for these columns NULL is a real value a post-function can
-- mean: `assign_to` with no user means UNASSIGN, and `set_field due_at` with no
-- value means CLEAR THE DUE DATE. Collapsing "do not touch" and "set to NULL"
-- into one nullable parameter is the partial-PATCH tri-state defect that
-- silently wiped every item's due_at in this repository once already. The flag
-- is the same {Set, Value} discipline optionalField encodes in Go.

-- name: ApplyTicketEffects :exec
UPDATE tickets SET
    assignee_id = CASE WHEN @set_assignee::boolean THEN sqlc.narg(assignee_id)::uuid ELSE assignee_id END,
    due_at      = CASE WHEN @set_due_at::boolean   THEN sqlc.narg(due_at)::timestamptz ELSE due_at END,
    labels      = CASE WHEN @set_labels::boolean   THEN @labels::text[] ELSE labels END,
    updated_at  = now()
WHERE id = @id AND deleted_at IS NULL;

-- name: ApplyProjectItemEffects :exec
UPDATE project_items SET
    assignee_id = CASE WHEN @set_assignee::boolean THEN sqlc.narg(assignee_id)::uuid ELSE assignee_id END,
    due_at      = CASE WHEN @set_due_at::boolean   THEN sqlc.narg(due_at)::timestamptz ELSE due_at END,
    labels      = CASE WHEN @set_labels::boolean   THEN @labels::text[] ELSE labels END,
    updated_at  = now()
WHERE id = @id AND deleted_at IS NULL;
