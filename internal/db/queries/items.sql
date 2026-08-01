-- name: CreateLabel :one
INSERT INTO labels (id, org_id, name, color) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListLabelsByOrg :many
SELECT * FROM labels WHERE org_id = $1 ORDER BY name ASC;

-- name: DeleteLabelInOrg :exec
-- Delete a label belonging to this organisation, and no other.
--
-- Labels are org-wide metadata that any member may manage, so the route carries
-- no space and no admin guard — which left the org itself as the only boundary,
-- and the predecessor did not carry it. `WHERE id = $1` meant any authenticated
-- member of any organisation could hard-delete any label row in the
-- installation, and labels have no soft delete to recover from.
--
-- Its sibling ListLabelsByOrg above has always been org-scoped. This is the
-- same predicate on the write.
--
-- :exec, so a label in another organisation and a label that never existed are
-- the same 204 and nothing is disclosed either way.
DELETE FROM labels WHERE id = @label_id AND org_id = @org_id;

-- name: CreateSprint :one
INSERT INTO sprints (id, space_id, name, goal, status, starts_at, ends_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetSprintInSpace :one
-- A sprint, reconciled against the space the request named. See the note on
-- GetProjectItemInSpace: the route proves the caller may read {spaceID} and
-- proved nothing at all about {sprintID}, so sprint names, goals and dates were
-- readable across every space boundary by id.
SELECT * FROM sprints WHERE id = @sprint_id AND space_id = @space_id;

-- name: GetSprintByID :one
-- UNSCOPED. No space reconciliation — only for callers that have established
-- authorisation another way. Prefer GetSprintInSpace.
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

-- name: ListEntityRelationsForEntity :many
-- Every relation touching one entity, in BOTH directions, with the far side
-- resolved only when the caller may read the space it lives in.
--
-- The predecessor (ListEntityRelationsByEntity) joined tickets and project_items
-- on `er.to_id = t.id` alone — no space predicate, no org predicate — and
-- returned COALESCE(title)/COALESCE(status) for whatever it found. The route is
-- space-scoped, and that was the trap rather than the mitigation: the {spaceID}
-- in the URL authorises the FROM item, and the far side's space was never
-- checked at all. Any item's title and status could be read by id, including
-- across a hidden space in the same org and across organizations entirely.
--
-- Two things make this version correct, and both are structural rather than
-- conventions a later caller has to remember:
--
--  1. The readable-space predicate lives in the JOIN, not in a Go filter after
--     the fact. An unreadable title is never SELECTed, so it cannot reach a log
--     line, an error string or a debug dump either.
--  2. There is exactly ONE join pair, applied to a far side the CTE has already
--     normalised. The outgoing and incoming directions cannot drift apart,
--     because after the CTE there is only one direction left.
--
-- far_id is NULL in precisely three cases — the far side does not exist, is
-- soft-deleted, or sits in a space the caller cannot read — which is what makes
-- the response carry no existence oracle (D82: no container identity). far_type
-- comes from the same joined row, so an unreadable relation does not even
-- disclose what KIND of thing it points at.
--
-- entity_relations.created_by is deliberately absent from the projection. No
-- caller rendered it, and on an incoming relation from a space the viewer
-- cannot read it would name a user who acted inside that space. Dropping the
-- column answers the question instead of gating it.
--
-- `= ANY(@readable_space_ids::uuid[])` is false for every row when the array is
-- empty, and NULL — equally non-matching in a JOIN — when it is nil. A caller
-- who can read nothing therefore resolves nothing. That is fail-closed by
-- accident of SQL semantics rather than by intent, so the adapter also passes
-- the set through nonNilUUIDs and an explicit empty-set test pins the behaviour
-- rather than leaving it resting on the trivia.
WITH rel AS (
    SELECT er.id, er.kind, er.created_at,
           TRUE       AS is_outgoing,
           er.to_id   AS target_id,
           er.to_type AS target_type
    FROM entity_relations er
    WHERE er.from_id = @entity_id AND er.from_type = @entity_type::text

    UNION ALL

    -- The reverse direction. Without it a "blocks" link is invisible to the very
    -- item it blocks: CreateEntityRelation writes one row and no inverse, and
    -- the predecessor matched on from_id only, so the blocked item's Relations
    -- panel was empty. The NOT(...) guard keeps a self-relation from appearing
    -- twice; validateRelation refuses those on the way in, but this query is
    -- read-side and does not get to assume the writer was well-behaved.
    SELECT er.id, er.kind, er.created_at,
           FALSE        AS is_outgoing,
           er.from_id   AS target_id,
           er.from_type AS target_type
    FROM entity_relations er
    WHERE er.to_id = @entity_id AND er.to_type = @entity_type::text
      AND NOT (er.from_id = @entity_id AND er.from_type = @entity_type::text)
),
-- Every item the caller may actually read, as one relation. Building the
-- readable set FIRST and LEFT JOINing it is what makes the nullability real
-- rather than notional: there is no COALESCE over two outer joins for a later
-- reader to mistake for a total function, and no branch where a title exists
-- but the space test was skipped. Postgres inlines this CTE (referenced once,
-- no side effects), so the join still resolves through each table's primary key.
readable_target AS (
    SELECT t.id, t.title, t.status, 'ticket'::text AS entity_type
    FROM tickets t
    WHERE t.deleted_at IS NULL
      AND t.space_id = ANY(@readable_space_ids::uuid[])

    UNION ALL

    SELECT pi.id, pi.title, pi.status, 'project_item'::text
    FROM project_items pi
    WHERE pi.deleted_at IS NULL
      AND pi.space_id = ANY(@readable_space_ids::uuid[])
)
SELECT rel.id,
       rel.kind,
       rel.is_outgoing,
       rel.created_at,
       tgt.id          AS far_id,
       tgt.title       AS far_title,
       tgt.status      AS far_status,
       tgt.entity_type AS far_type
FROM rel
LEFT JOIN readable_target tgt
       ON tgt.id = rel.target_id
      AND tgt.entity_type = rel.target_type
ORDER BY rel.created_at ASC, rel.id ASC;

-- name: EntityRelationTargetIsReadable :one
-- One boolean: may this caller link to this target?
--
-- CreateRelation used to validate only that the kind was known and that from
-- and to differed, so any UUID at all was accepted and stored — migration 015
-- deliberately DROPPED the from_id/to_id foreign keys ("so any UUID (ticket,
-- project_item, page) can be linked"), which means nothing below this check
-- constrains the write either.
--
-- The shape matters as much as the predicate. This returns a single bool over
-- an EXISTS, so "no such entity" and "exists but you may not read it" are not
-- two values that a caller might accidentally report differently — they are the
-- same false. A version that resolved the target's space and then compared it
-- would produce two distinguishable failures, and a 404-vs-403 difference is the
-- same disclosure in a different shape. RequireSpaceReadable already made this
-- choice for space-scoped routes ("404, never 403: unreadable spaces do not
-- exist as far as the caller can tell"); this is that rule, one level down.
--
-- All three entity types the to_type CHECK constraint permits are covered.
-- Pages have no reader in ListEntityRelationsForEntity and never had one, so a
-- page target still renders as unresolvable — but refusing the WRITE outright
-- would narrow behaviour the schema allows, and this is a security fix, not a
-- feature removal.
SELECT EXISTS (
    SELECT 1 FROM tickets t
     WHERE t.id = @target_id
       AND @target_type::text = 'ticket'
       AND t.deleted_at IS NULL
       AND t.space_id = ANY(@readable_space_ids::uuid[])
    UNION ALL
    SELECT 1 FROM project_items pi
     WHERE pi.id = @target_id
       AND @target_type::text = 'project_item'
       AND pi.deleted_at IS NULL
       AND pi.space_id = ANY(@readable_space_ids::uuid[])
    UNION ALL
    SELECT 1 FROM pages pg
     WHERE pg.id = @target_id
       AND @target_type::text = 'page'
       AND pg.deleted_at IS NULL
       AND pg.space_id = ANY(@readable_space_ids::uuid[])
) AS readable;

-- name: DeleteEntityRelationInSpace :exec
-- Remove a relation, but only one the caller's space actually touches.
--
-- The READ side of this table was reshaped so that no ungated method existed to
-- call by mistake — see RelationRepository's header. The DELETE was left behind
-- taking a bare id, which made it a cross-organisation delete: there are no
-- foreign keys underneath it, because migration 015 dropped from_id/to_id on
-- purpose so that any entity kind could be linked.
--
-- EITHER endpoint may match, not just from_id. ListEntityRelationsForEntity
-- unions the reverse direction precisely so a "blocks" link is visible to the
-- item it blocks, and a relation a caller can see from one side but delete only
-- from the other would be an affordance that fails for no reason they could
-- infer.
--
-- It stays :exec, and so still answers 204 whether or not a row matched. That
-- is deliberate, and it is the whole no-oracle story here: a relation in
-- another space and a relation that never existed produce byte-identical
-- responses, because both delete nothing and neither is counted. Answering 404
-- on a miss is the more conventional shape and would introduce exactly the
-- existence signal this predicate exists to remove. It also leaves the route as
-- idempotent as it already was, which known-issues #24 explicitly left to a
-- maintainer rather than something to settle in passing.
DELETE FROM entity_relations er
 WHERE er.id = @relation_id
   AND EXISTS (
       SELECT 1 FROM tickets t
        WHERE t.space_id = @space_id
          AND t.deleted_at IS NULL
          AND ((er.from_type = 'ticket' AND t.id = er.from_id)
            OR (er.to_type = 'ticket' AND t.id = er.to_id))
       UNION ALL
       SELECT 1 FROM project_items pi
        WHERE pi.space_id = @space_id
          AND pi.deleted_at IS NULL
          AND ((er.from_type = 'project_item' AND pi.id = er.from_id)
            OR (er.to_type = 'project_item' AND pi.id = er.to_id))
       UNION ALL
       SELECT 1 FROM pages pg
        WHERE pg.space_id = @space_id
          AND pg.deleted_at IS NULL
          AND ((er.from_type = 'page' AND pg.id = er.from_id)
            OR (er.to_type = 'page' AND pg.id = er.to_id))
   );

-- name: CountItemsArchiveTickets :one
SELECT COUNT(*) FROM items_archive WHERE kind = 'ticket' AND deleted_at IS NULL;

-- name: CountItemsArchiveProjectItems :one
SELECT COUNT(*) FROM items_archive WHERE kind IN ('task','story','epic','bug') AND deleted_at IS NULL;
