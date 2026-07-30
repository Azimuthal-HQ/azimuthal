-- Dashboards and gadgets (ADR-0009, migration 048).
--
-- Everything here is ordinary org-scoped CRUD over two tables the caller owns
-- or has been shared. There is no cross-space read in this file and there must
-- never be one: a gadget's DATA comes from the saved-view fan-outs in
-- saved_views.sql, which are the sanctioned ADR-0008 exception and the only
-- place that exception is taken. A dashboard row grants nothing to anybody.

-- name: CreateDashboard :one
INSERT INTO dashboards (
    org_id, owner_id, name, description, module,
    is_default, is_seeded, visibility, visibility_team_id
) VALUES (
    @org_id, @owner_id, @name, @description, @module,
    @is_default, @is_seeded, @visibility, @visibility_team_id
)
RETURNING *;

-- name: GetDashboard :one
SELECT d.*,
       u.display_name AS owner_name,
       tm.name        AS team_name
FROM dashboards d
JOIN users u ON u.id = d.owner_id
LEFT JOIN teams tm ON tm.id = d.visibility_team_id
WHERE d.id = @id AND d.org_id = @org_id AND d.deleted_at IS NULL;

-- name: UpdateDashboard :one
-- The whole mutable surface in one statement, for migration 038's reason: a
-- single nullable pointer for visibility_team_id would collapse "absent" and
-- "null" into "clear it", which is the defect that silently wiped every item's
-- due_at. The service reads the row, applies only what the request set, and
-- writes the whole result back.
UPDATE dashboards
SET name               = @name,
    description        = @description,
    module             = @module,
    is_default         = @is_default,
    visibility         = @visibility,
    visibility_team_id = @visibility_team_id
WHERE id = @id AND org_id = @org_id AND deleted_at IS NULL
RETURNING *;

-- name: ClearDefaultDashboard :execrows
-- Stand every other dashboard of this owner+module down from default.
--
-- Run immediately before promoting one, inside the same transaction.
-- dashboards_one_default is a plain (non-deferrable) partial unique index, so
-- the demotion must land first; doing it the other way round collides on the
-- index rather than replacing the row.
UPDATE dashboards
SET is_default = FALSE
WHERE org_id = @org_id
  AND owner_id = @owner_id
  AND module = @module
  AND id <> @keep_id
  AND is_default
  AND deleted_at IS NULL;

-- name: SoftDeleteDashboard :execrows
UPDATE dashboards
SET deleted_at = now()
WHERE id = @id AND org_id = @org_id AND deleted_at IS NULL;

-- name: ListDashboardsForViewer :many
-- Every dashboard the caller may see: their own, plus org-audience, plus
-- team-audience whose team is in the caller's EFFECTIVE team set (already
-- subject-side expanded by the resolver, ADR-0007).
--
-- visibility_team_id IS NOT NULL is load-bearing on the team branch, exactly
-- as it is on ListSavedViewsForViewer. Migration 048 nulls the column when the
-- team is deleted rather than cascading the row away, so a dashboard whose
-- audience team is gone must match nobody but its owner. Without the explicit
-- test that would rest on `= ANY('{}')` being false, which is SQL trivia
-- rather than intent.
--
-- An empty @module means every module; the cardinality-style guard is written
-- as an equality against '' because module is a scalar rather than an array.
SELECT d.*,
       u.display_name AS owner_name,
       tm.name        AS team_name
FROM dashboards d
JOIN users u ON u.id = d.owner_id
LEFT JOIN teams tm ON tm.id = d.visibility_team_id
WHERE d.org_id = @org_id
  AND d.deleted_at IS NULL
  AND (@module::text = '' OR d.module = @module::text)
  AND (
        d.owner_id = @viewer_id
     OR d.visibility = 'org'
     OR (d.visibility = 'team'
         AND d.visibility_team_id IS NOT NULL
         AND d.visibility_team_id = ANY(@effective_team_ids::uuid[]))
  )
ORDER BY d.module ASC, d.is_default DESC, lower(d.name) ASC, d.id ASC;

-- name: GetDefaultDashboard :one
-- The caller's default dashboard for one module. At most one row can match:
-- dashboards_one_default is unique on exactly this predicate.
SELECT d.*,
       u.display_name AS owner_name,
       tm.name        AS team_name
FROM dashboards d
JOIN users u ON u.id = d.owner_id
LEFT JOIN teams tm ON tm.id = d.visibility_team_id
WHERE d.org_id = @org_id
  AND d.owner_id = @owner_id
  AND d.module = @module
  AND d.is_default
  AND d.deleted_at IS NULL;

-- name: CreateStarterDashboard :one
-- The lazily-seeded Home dashboard.
--
-- IDEMPOTENT BY CONSTRUCTION, not by a check the handler performs. ON CONFLICT
-- against dashboards_one_default means two tabs opening Home at the same
-- moment cannot produce two starters, and a person whose default already
-- exists gets no row at all — which is what "seeding runs exactly once" means
-- in practice. A check-then-insert would have that race, and the race is
-- silent: the loser's extra dashboard would simply appear in their list.
--
-- :one with ON CONFLICT DO NOTHING returns pgx.ErrNoRows when nothing was
-- inserted. The caller treats that as "somebody already has one" and re-reads,
-- rather than as an error.
INSERT INTO dashboards (
    org_id, owner_id, name, description, module,
    is_default, is_seeded, visibility
) VALUES (
    @org_id, @owner_id, @name, '', @module,
    TRUE, TRUE, 'private'
)
ON CONFLICT (owner_id, module) WHERE is_default AND deleted_at IS NULL
DO NOTHING
RETURNING *;

-- name: ListDashboardGadgets :many
-- One dashboard's gadgets in display order.
SELECT * FROM dashboard_gadgets
WHERE dashboard_id = @dashboard_id
ORDER BY position ASC;

-- name: DeleteDashboardGadgets :execrows
-- The first half of a whole-collection layout write. Run inside the same
-- transaction as the inserts that follow it: spec §6 says layout "saves as a
-- whole collection, never per gadget, to avoid partial states", and a delete
-- that committed on its own would be exactly such a state — somebody's
-- dashboard, empty, because the second half failed.
DELETE FROM dashboard_gadgets WHERE dashboard_id = @dashboard_id;

-- name: CreateDashboardGadget :one
INSERT INTO dashboard_gadgets (
    dashboard_id, gadget_key, position, col_span, saved_view_id, config
) VALUES (
    @dashboard_id, @gadget_key, @position, @col_span, @saved_view_id, @config
)
RETURNING *;
