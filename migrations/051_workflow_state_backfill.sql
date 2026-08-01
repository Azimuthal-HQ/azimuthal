-- +goose Up
-- +goose StatementBegin

-- Reconcile workflow_state_id with the status text it should have agreed with
-- all along (D71).
--
-- ── What went wrong ──────────────────────────────────────────────────────────
--
-- An entity's position in its state machine is recorded in two columns:
-- `status` (TEXT, what the user sees) and `workflow_state_id` (the FK the
-- workflow engine reads). Until this migration's release, only two statements
-- in the product wrote both — UpdateTicketWorkflowState and
-- UpdateProjectItemWorkflowState — and neither had a caller the shipped
-- frontend could reach. Every route the frontend actually used
-- (POST .../{id}/status) wrote `status` alone.
--
-- So workflow_state_id did not merely drift: for any installation driven by the
-- shipped UI it was never written after creation at all. CreateTicket,
-- CreateProjectItem and CreatePortalRequest all omit the column from their
-- INSERT lists and it carries no DEFAULT, so it is born NULL. Migrations 016
-- and 019 populated it once, for rows that existed at the time, and nothing has
-- maintained it since.
--
-- That is why the engine-backed routes carry a "fall back to the initial state
-- when workflow_state_id is not set" branch: it is the only way they could work
-- at all, and it means an entity that has moved four times still validates its
-- fifth move as though it had never left the start.
--
-- ── What this migration does, and deliberately does not do ───────────────────
--
-- It sets workflow_state_id from the state whose NAME equals the entity's
-- status, resolving the workflow through the entity's own space
-- (`spaces.workflow_id`) rather than through the org's seeded default. That
-- distinction matters and is why this is not a copy of 016's block: 016 could
-- use the org's default because it had just written both, whereas today a space
-- can be pointed at any workflow and administrators create their own.
-- `UNIQUE (workflow_id, name)` on workflow_states guarantees the join yields at
-- most one row.
--
-- It does NOT rewrite `status`, and it does NOT fall back to the initial state
-- for rows whose status names no state. Both were considered and both are
-- refused here:
--
--   * Rewriting status is a product decision about user-visible data — a
--     project item sitting at "open" against a backlog/todo/in_progress/
--     in_review/done vocabulary would have to become "backlog", and that is a
--     judgement about what those items MEAN. known-issues #30 records it as a
--     maintainer's call, and a migration is the worst possible place to make one
--     quietly.
--
--   * Falling back to the initial state is what 016 and 019 did, and it is the
--     move known-issues #30 rejects: it writes a position the entity was never
--     in. Doing it per-request is a guess that the next real transition
--     corrects; doing it in a backfill makes the guess permanent.
--
-- Rows this leaves NULL are handled at read time instead, by
-- TierService.ResolveFromState, which consults the status text, then the stored
-- state id, then the workflow's initial state — a guess only when neither
-- recorded position resolves, and one that the entity's next transition
-- replaces with a written value.
--
-- Scoped to `workflow_state_id IS NULL` so a row that already has one is never
-- overwritten: a state can be RENAMED, which leaves a correct state id beside a
-- status text that matches nothing, and matching on name would be exactly wrong
-- there.

UPDATE tickets t
SET workflow_state_id = ws.id
FROM spaces sp
JOIN workflow_states ws ON ws.workflow_id = sp.workflow_id
WHERE t.space_id = sp.id
  AND ws.name = t.status
  AND t.workflow_state_id IS NULL
  AND t.deleted_at IS NULL
  AND sp.workflow_id IS NOT NULL;

UPDATE project_items pi
SET workflow_state_id = ws.id
FROM spaces sp
JOIN workflow_states ws ON ws.workflow_id = sp.workflow_id
WHERE pi.space_id = sp.id
  AND ws.name = pi.status
  AND pi.workflow_state_id IS NULL
  AND pi.deleted_at IS NULL
  AND sp.workflow_id IS NOT NULL;

-- workflow_state_id has been unindexed since migration 016, which made the two
-- statements above sequential scans and, more to the point, makes every
-- ResolveFromState fallback and every "which entities are in this state" read a
-- scan for the life of the table. It is a foreign key that is now written on
-- every transition, so it earns an index.
--
-- Partial on NOT NULL: the NULL rows are the ones no query filters BY state,
-- and excluding them keeps the index proportional to the entities actually
-- inside a state machine rather than to the table.
CREATE INDEX idx_tickets_workflow_state
    ON tickets (workflow_state_id) WHERE workflow_state_id IS NOT NULL;
CREATE INDEX idx_project_items_workflow_state
    ON project_items (workflow_state_id) WHERE workflow_state_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- The indexes drop cleanly.
DROP INDEX IF EXISTS idx_project_items_workflow_state;
DROP INDEX IF EXISTS idx_tickets_workflow_state;

-- The backfill does NOT, and this is flagged rather than faked.
--
-- The Up statements set a column that was NULL. Reversing them means setting it
-- back to NULL — but only for the rows THIS migration touched, and nothing
-- distinguishes those from rows that migration 016 or 019 populated, or that a
-- transition has written since. `UPDATE ... SET workflow_state_id = NULL` would
-- destroy all three populations to undo one.
--
-- So the down is deliberately a no-op on the data. Re-running Up is safe and
-- idempotent (the IS NULL predicate makes it so), and leaving a correct state id
-- in place is harmless in the older schema, which simply reads the column less
-- often. A reversal that loses data it did not write would be worse than not
-- reversing.

-- +goose StatementEnd
