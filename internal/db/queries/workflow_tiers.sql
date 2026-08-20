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
-- Scoped to the transition, not just the id. {transitionID} and the child id
-- are separate path segments and nothing ties them together, so an unscoped
-- delete lets an admin naming one of their OWN transitions remove a row
-- belonging to any other — including another organisation's. Zero rows is the
-- adapter's ErrNotFound, which the handler answers as 404.
DELETE FROM workflow_transition_guards WHERE id = $1 AND transition_id = $2;


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
-- Scoped to the transition, not just the id. {transitionID} and the child id
-- are separate path segments and nothing ties them together, so an unscoped
-- delete lets an admin naming one of their OWN transitions remove a row
-- belonging to any other — including another organisation's. Zero rows is the
-- adapter's ErrNotFound, which the handler answers as 404.
DELETE FROM workflow_transition_post_functions WHERE id = $1 AND transition_id = $2;

-- ─── Tier 2: approver configuration ───────────────────────────────────────────

-- name: ListTransitionApprovers :many
-- The subject display name is resolved by the same LEFT JOIN-on-discriminator
-- shape space_grants uses (internal/db/queries/space_grants.sql), so an
-- approver and a grant render a deleted subject identically. subject_id carries
-- no foreign key — it is polymorphic — so a missing subject is a real state and
-- subject_missing reports it rather than hiding it.
--
-- The subject joins are ORG-SCOPED, against the org the approver's transition
-- belongs to (transition → workflow → org). Without that scope a subject_id
-- naming a real user or team in ANOTHER org resolved here — subject_missing came
-- back false and the foreign name rendered — even though such a subject can
-- never approve anything in this org. The scope mirrors the create-time check
-- exactly: a user must have a membership in the org (IsOrgMember), a team must
-- belong to it (TeamExistsInOrg), so what this read calls "present" is precisely
-- what CreateApprover will accept.
SELECT
    a.*,
    COALESCE(CASE WHEN a.subject_type = 'user' THEN u.display_name ELSE t.name END, '')::text AS subject_name,
    (CASE WHEN a.subject_type = 'user' THEN u.id IS NULL ELSE t.id IS NULL END)::boolean AS subject_missing
FROM workflow_transition_approvers a
JOIN workflow_transitions wt ON wt.id = a.transition_id
JOIN workflows w ON w.id = wt.workflow_id
LEFT JOIN users u ON a.subject_type = 'user' AND u.id = a.subject_id AND u.deleted_at IS NULL
    AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.org_id = w.org_id)
LEFT JOIN teams t ON a.subject_type = 'team' AND t.id = a.subject_id AND t.deleted_at IS NULL
    AND t.org_id = w.org_id
WHERE a.transition_id = $1
ORDER BY a.subject_type, subject_name, a.id;

-- name: ListWorkflowApprovers :many
-- Org-scoped subject resolution, exactly as ListTransitionApprovers above: the
-- workflow names the org, and a subject outside it renders as missing rather
-- than resolving a foreign name.
SELECT
    a.*,
    COALESCE(CASE WHEN a.subject_type = 'user' THEN u.display_name ELSE tm.name END, '')::text AS subject_name,
    (CASE WHEN a.subject_type = 'user' THEN u.id IS NULL ELSE tm.id IS NULL END)::boolean AS subject_missing
FROM workflow_transition_approvers a
JOIN workflow_transitions t ON t.id = a.transition_id
JOIN workflows w ON w.id = t.workflow_id
LEFT JOIN users u  ON a.subject_type = 'user' AND u.id  = a.subject_id AND u.deleted_at IS NULL
    AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.org_id = w.org_id)
LEFT JOIN teams tm ON a.subject_type = 'team' AND tm.id = a.subject_id AND tm.deleted_at IS NULL
    AND tm.org_id = w.org_id
WHERE t.workflow_id = $1
ORDER BY a.transition_id, a.subject_type, subject_name, a.id;

-- name: CreateTransitionApprover :one
INSERT INTO workflow_transition_approvers (transition_id, subject_type, subject_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteTransitionApprover :execrows
-- Scoped to the transition, not just the id. {transitionID} and the child id
-- are separate path segments and nothing ties them together, so an unscoped
-- delete lets an admin naming one of their OWN transitions remove a row
-- belonging to any other — including another organisation's. Zero rows is the
-- adapter's ErrNotFound, which the handler answers as 404.
DELETE FROM workflow_transition_approvers WHERE id = $1 AND transition_id = $2;

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

-- name: GetApprovalInSpace :one
-- One request, reconciled against the space the caller's URL named.
--
-- The space predicate is the whole authorisation. ListPendingApprovalsForSpace
-- below already states the rule — "a decision is authorised against the space
-- the request was made in" — and this is the read that has to enforce it,
-- because the decide route's other check structurally cannot. Approvers are
-- configured per TRANSITION; a transition belongs to a workflow, which is an
-- ORG object shared by every space that assigns it. So "is the actor a
-- configured approver" is org-wide by construction and says nothing about which
-- space the approved entity lives in.
--
-- A miss is a miss: wrong space and no such row both return zero rows, the
-- adapter maps both to ErrNotFound, and the route answers 404 either way. That
-- matters more here than on a plain read, because the branches downstream of
-- this one carry distinguishable statuses — already decided is 409, a deleted
-- edge is 409, not an approver is 403 — so reaching them at all would let a
-- caller outside the space learn that an approval exists and what state it is
-- in.
SELECT
    sqlc.embed(a),
    COALESCE(r.display_name, '')::text AS requested_by_name,
    COALESCE(d.display_name, '')::text AS decided_by_name
FROM workflow_approvals a
LEFT JOIN users r ON r.id = a.requested_by
LEFT JOIN users d ON d.id = a.decided_by
WHERE a.id = @approval_id AND a.space_id = @space_id;

-- name: DecideApproval :one
-- The WHERE clause carries `decided_at IS NULL`, so a second approver deciding
-- concurrently updates zero rows rather than overwriting the first decision.
-- The adapter turns zero rows into ErrApprovalAlreadyDecided after a follow-up
-- read, the way RevokeEntityShare distinguishes already-revoked from not-found.
--
-- reason is written in the SAME statement as the decision, never in a follow-up
-- UPDATE. migration 050's workflow_approvals_reason_requires_decision refuses a
-- reason without a decision, so a two-statement version would have to write the
-- decision first — and a failure between the two would leave a decline standing
-- with no reason, which is exactly the unexplained decline the column exists to
-- prevent.
--
-- space_id is in the WHERE for the same reason GetApprovalInSpace carries it.
-- The service reads through that query first, so a wrong-space id cannot reach
-- this statement today; the predicate is here so that stays true of the next
-- caller as well. Zero rows for a space mismatch lands in the same arm as zero
-- rows for a concurrent decision, and the arm's follow-up read is scoped too,
-- so it reports not-found rather than already-decided.
UPDATE workflow_approvals
SET decided_by = @decided_by, decided_at = now(), decision = @decision, reason = @reason
WHERE id = @approval_id AND space_id = @space_id AND decided_at IS NULL
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
--
-- Reconciled against the space the request named. The route is
-- /orgs/{orgID}/spaces/{spaceID}/workflow/entities/{entityType}/{entityID}/approvals
-- and its guards prove the caller may read {spaceID}; they prove nothing at all
-- about {entityID}. Keyed on the entity id alone, this handed back another
-- space's approval history by id — who asked, who decided, when, and the
-- decline reason.
--
-- space_id was already on the row. Migration 047 denormalised it onto
-- workflow_approvals deliberately, and ListPendingApprovals immediately above
-- filters on it; this query simply never consulted it.
SELECT
    sqlc.embed(a),
    COALESCE(r.display_name, '')::text AS requested_by_name,
    COALESCE(d.display_name, '')::text AS decided_by_name
FROM workflow_approvals a
LEFT JOIN users r ON r.id = a.requested_by
LEFT JOIN users d ON d.id = a.decided_by
WHERE a.entity_type = @entity_type
  AND a.entity_id = @entity_id
  AND a.space_id = @space_id
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
-- Post-function effects commit with the status write or not at all, so this
-- carries the same space predicate UpdateTicketWorkflowState does. Without it a
-- transition could be refused the status change and still rewrite the far
-- entity's assignee and due date. The set_field:tags effect is not a column on
-- this row at all any more — the applier replaces the entity's entity_tags
-- associations inside the same transaction, through the same predicate-scoped
-- statements every tag write uses.
UPDATE tickets SET
    assignee_id = CASE WHEN @set_assignee::boolean THEN sqlc.narg(assignee_id)::uuid ELSE assignee_id END,
    due_at      = CASE WHEN @set_due_at::boolean   THEN sqlc.narg(due_at)::timestamptz ELSE due_at END,
    updated_at  = now()
WHERE id = @id AND space_id = @space_id AND deleted_at IS NULL;

-- name: ApplyProjectItemEffects :exec
-- See ApplyTicketEffects.
UPDATE project_items SET
    assignee_id = CASE WHEN @set_assignee::boolean THEN sqlc.narg(assignee_id)::uuid ELSE assignee_id END,
    due_at      = CASE WHEN @set_due_at::boolean   THEN sqlc.narg(due_at)::timestamptz ELSE due_at END,
    updated_at  = now()
WHERE id = @id AND space_id = @space_id AND deleted_at IS NULL;

-- ─── Approval notification recipients ─────────────────────────────────────────

-- name: ListEffectiveTeamMemberIDs :many
-- Who counts as a member of one team, for the approval-requested notification.
--
-- This is the INVERSE of effective_team_ids(). That function is subject-side —
-- given a user, which teams do they effectively belong to — and every
-- authorisation read in the product asks it in that direction. Notifying a team
-- approver needs the other direction, and the two must agree: a person the
-- guard would accept as an approver but the notifier never told is an approval
-- that waits on somebody who was never asked.
--
-- So rather than re-derive the ancestry rule (teams.path overlap, migration
-- 038), this asks the SAME function once per candidate and keeps whoever it
-- answers for. It cannot drift from the authorisation rule because it IS the
-- authorisation rule — the exact reasoning migration 038's header gives for
-- extracting the function rather than letting callers copy the expansion.
--
-- The candidate set is team_members, not users: somebody in no team cannot be
-- in one team's effective set, so the correlated call runs over the members of
-- the org rather than its whole roster. This runs once per approval REQUEST —
-- a rare write, never a read path — so the correlated shape buys consistency
-- at a cost nothing hot pays.
SELECT DISTINCT tm.user_id
FROM team_members tm
JOIN users u ON u.id = tm.user_id AND u.deleted_at IS NULL
WHERE tm.org_id = @org_id
  AND @team_id::uuid IN (
      SELECT e.team_id FROM effective_team_ids(@org_id, tm.user_id) AS e(team_id)
  );
