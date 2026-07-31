-- name: CreateProjectItem :one
-- Assigns number and item_key atomically. The data-modifying seq CTE bumps the
-- per-space counter with an ON CONFLICT upsert, which row-locks the counter so
-- concurrent creators serialise (no duplicate or reused numbers); sp resolves
-- the space's org_id and key for the item_key (<SPACE_KEY>-<n>). Because it is a
-- single statement, the counter bump and the item insert commit or roll back
-- together — a failed insert leaves no gap.
WITH seq AS (
    INSERT INTO project_item_sequences (space_id, last_number)
    VALUES ($2, 1)
    ON CONFLICT (space_id) DO UPDATE
        SET last_number = project_item_sequences.last_number + 1
    RETURNING last_number
),
sp AS (
    SELECT org_id, key FROM spaces WHERE id = $2
)
INSERT INTO project_items (id, space_id, org_id, parent_id, number, item_key, kind,
                           title, description, status, priority, reporter_id,
                           assignee_id, sprint_id, labels, due_at, rank)
SELECT $1, $2, sp.org_id, $3, seq.last_number, sp.key || '-' || seq.last_number,
       $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
FROM seq, sp
RETURNING *;

-- name: GetProjectItemInSpace :one
-- An item, reconciled against the space the request named.
--
-- This is the read every space-scoped route must use. The routes are shaped
-- /orgs/{orgID}/spaces/{spaceID}/projects/items/{itemID}, and the middleware
-- proves the caller may read {spaceID} — but nothing proved {itemID} lives
-- there, so an authorised member of any one space could read any item in the
-- installation by id, across hidden spaces and across organizations. Adding
-- space_id here rather than comparing item.SpaceID in each handler is what
-- makes the guarantee structural: a handler that forgets cannot get the row.
--
-- A miss is indistinguishable from an absent item, deliberately: both return no
-- rows and become ErrNotFound, so the endpoint is not an existence oracle.
SELECT * FROM project_items
WHERE id = @item_id AND space_id = @space_id AND deleted_at IS NULL;

-- name: GetProjectItemByID :one
-- UNSCOPED. Reaches an item without reference to any space, so it must only be
-- used where authorisation has already been established by other means.
--
-- The one legitimate caller is the entity-share read path (ADR-0008), where
-- access is granted by share coverage precisely so that it can bypass space
-- access — CoversForCaller has already answered before this runs. Every other
-- caller wants GetProjectItemInSpace.
SELECT * FROM project_items WHERE id = $1 AND deleted_at IS NULL;

-- name: GetProjectItemByOrgKey :one
-- Resolves a human-readable key (e.g. VEC-123) to an item within an org.
SELECT * FROM project_items
WHERE org_id = $1 AND item_key = $2 AND deleted_at IS NULL;

-- name: ListProjectItemsBySpace :many
SELECT * FROM project_items
WHERE space_id = $1 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListProjectItemsByStatus :many
SELECT * FROM project_items
WHERE space_id = $1 AND status = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListProjectItemsByAssignee :many
SELECT * FROM project_items
WHERE space_id = $1 AND assignee_id = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListProjectItemsBySprint :many
-- A sprint's items, reconciled against the space the request named.
--
-- The sprint id alone used to be enough, which made this a bulk disclosure:
-- one guessed or leaked sprint id returned every item on it regardless of
-- whose space it was. Items and sprints both carry space_id, and both are
-- checked — the item test is the one that authorises, and the sprint test
-- stops a sprint from another space matching through items that happen to
-- share this one.
SELECT pi.* FROM project_items pi
JOIN sprints s ON s.id = pi.sprint_id
WHERE pi.sprint_id = @sprint_id
  AND pi.space_id = @space_id
  AND s.space_id = @space_id
  AND pi.deleted_at IS NULL
ORDER BY pi.rank ASC;

-- name: UpdateProjectItem :one
-- kind is appended last so every existing parameter position stays stable.
-- It is written on every update, not only when the client asked to change it:
-- the handler's applyItemPatch leaves the stored slug in place when the PATCH
-- body omits "kind", so an omitting request rewrites the same value. Type
-- integrity is the item-types service's job and not this statement's —
-- migration 032 dropped the CHECK constraint (D49), so whatever reaches here
-- is stored verbatim.
UPDATE project_items
SET title = $2, description = $3, status = $4, priority = $5,
    assignee_id = $6, labels = $7, due_at = $8, rank = $9, kind = $10,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateProjectItemStatus :one
UPDATE project_items
SET status = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateProjectItemWorkflowState :one
-- The space predicate is what makes ApplyInput.SpaceID load-bearing. It was
-- carried all the way into the applier and then never used — the caller's space
-- reached the audit row and nothing else — so a transition released by an
-- approval in another space wrote the far entity by bare id. The approval is now
-- reconciled upstream and cannot arrive here mismatched, so this is the seam
-- being closed rather than the hole; a miss is zero rows and the transaction
-- rolls back rather than committing a status the caller had no claim on.
UPDATE project_items
SET status = @status, workflow_state_id = @workflow_state_id, updated_at = now()
WHERE id = @item_id AND space_id = @space_id AND deleted_at IS NULL
RETURNING *;

-- name: AssignProjectItemToSprintInSpace :exec
-- Move an item into a sprint, both of which must live in the caller's space.
--
-- TWO unreconciled ids arrive at this statement and both are closed here, in
-- ONE atomic UPDATE rather than a lookup followed by a write. The item comes
-- from the URL on the item route and from the request BODY on the backlog move
-- pair; the sprint always comes from the body. The predecessor carried neither
-- predicate, so an editor in one space could re-point the sprint of any project
-- item in the installation, in any organisation.
--
-- Checking the sprint with a separate SELECT would be the same gap in slower
-- motion: it opens a window in which the sprint moves or is deleted between the
-- check and the write, and it makes the refusal a second decision some future
-- caller can skip. Here there is one decision and no window.
--
-- The sprint must live in the SAME space as the item, not merely in some space
-- the caller can read. A sprint is one space's board; an item pointing at
-- another space's sprint would render on a board it does not belong to and
-- vanish from its own.
--
-- Clearing is ClearProjectItemSprintInSpace, deliberately a separate statement.
-- Folding it in would need a nullable parameter whose NULL had to short-circuit
-- the EXISTS, which is the partial-PATCH tri-state confusion in another costume
-- — and an un-assign has no sprint to validate, so it has no business carrying
-- the predicate at all.
--
-- Both tables are aliased because sqlc's analyser flattens the EXISTS into the
-- outer scope, where an unqualified `id` is ambiguous against sprints.
--
-- :exec, so the route answers as it always has whether or not a row matched. A
-- foreign item and an absent one stay indistinguishable — as they were before,
-- except that the write used to LAND in both cases rather than in neither.
UPDATE project_items pi
SET sprint_id = @sprint_id, updated_at = now()
WHERE pi.id = @item_id
  AND pi.space_id = @space_id
  AND pi.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM sprints s
       WHERE s.id = @sprint_id
         AND s.space_id = @space_id
  );

-- name: ClearProjectItemSprintInSpace :exec
-- Return an item in this space to the unassigned backlog.
--
-- The un-assign half of AssignProjectItemToSprintInSpace, split out because it
-- names no sprint and so has nothing to reconcile beyond the item itself. Same
-- space predicate, same silence on a miss.
UPDATE project_items
SET sprint_id = NULL, updated_at = now()
WHERE id = @item_id
  AND space_id = @space_id
  AND deleted_at IS NULL;

-- name: UpdateProjectItemRank :exec
UPDATE project_items
SET rank = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteProjectItemInSpace :exec
-- Scoped to the space, not just the id.
--
-- The route above this is reconciled, but it is the only thing that was: the
-- transactional deleter took an entity id alone, so the refusal lived in a
-- handler rather than in the write. That is the shape this whole class is made
-- of — a convention the next caller (a bulk operation, a job, a new route)
-- inherits nothing of. The delete handler's own comment said as much: "a
-- {entity} outside {spaceID} has to be refused here or nowhere."
--
-- :exec, so a mismatch deletes nothing and says nothing, exactly as an id that
-- named nothing already did.
UPDATE project_items SET deleted_at = now()
WHERE id = @item_id AND space_id = @space_id AND deleted_at IS NULL;

-- name: SearchProjectItems :many
SELECT * FROM project_items
WHERE space_id = $1
  AND deleted_at IS NULL
  AND search_vector @@ plainto_tsquery('english', $2)
ORDER BY ts_rank(search_vector, plainto_tsquery('english', $2)) DESC
LIMIT $3;
