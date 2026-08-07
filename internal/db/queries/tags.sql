-- Entity tags (migrations 040, 055). Org-scoped tags and the entity↔tag
-- association, carried by pages, tickets and project items alike.

-- name: ListTagsByOrg :many
SELECT id, org_id, slug, name, created_at
FROM tags WHERE org_id = $1 ORDER BY name;

-- name: GetTagByOrgSlug :one
SELECT id, org_id, slug, name, created_at
FROM tags WHERE org_id = $1 AND slug = $2;

-- name: UpsertTag :one
-- Create-on-use. Tags have no administration surface: they come into existence
-- because somebody typed one.
--
-- ON CONFLICT DO UPDATE rather than DO NOTHING, because DO NOTHING returns no
-- row on conflict and the caller needs the id either way. The SET is a no-op
-- write of the existing name — deliberately NOT the incoming one: the first
-- spelling wins, so "Design Docs" typed once does not become "design docs"
-- the next time somebody types the slug in lower case.
INSERT INTO tags (org_id, slug, name)
VALUES ($1, $2, $3)
ON CONFLICT (org_id, slug) DO UPDATE SET name = tags.name
RETURNING id, org_id, slug, name, created_at;

-- name: ListTagsForEntity :many
-- An entity's tags, reconciled against the space the request named. A tag set
-- describes what an entity is about, so reading one across a space boundary
-- discloses the subject matter of a thing the caller cannot open.
--
-- The reconciliation is a three-arm EXISTS rather than a join because the
-- association table's entity side is polymorphic (no FK, migration 055). The
-- arm shape copies EntityRelationTargetIsReadable in items.sql — the one
-- established spelling of "resolve a polymorphic entity to its space".
SELECT t.id, t.org_id, t.slug, t.name, t.created_at
FROM entity_tags et
JOIN tags t ON t.id = et.tag_id
WHERE et.entity_type = @entity_type::text
  AND et.entity_id = @entity_id::uuid
  AND EXISTS (
      SELECT 1 FROM tickets tk
       WHERE @entity_type::text = 'ticket'
         AND tk.id = @entity_id::uuid
         AND tk.space_id = @space_id::uuid
         AND tk.deleted_at IS NULL
      UNION ALL
      SELECT 1 FROM project_items pi
       WHERE @entity_type::text = 'project_item'
         AND pi.id = @entity_id::uuid
         AND pi.space_id = @space_id::uuid
         AND pi.deleted_at IS NULL
      UNION ALL
      SELECT 1 FROM pages pg
       WHERE @entity_type::text = 'page'
         AND pg.id = @entity_id::uuid
         AND pg.space_id = @space_id::uuid
         AND pg.deleted_at IS NULL
  )
ORDER BY t.name;

-- name: EntityTagTargetInSpace :one
-- One boolean: is this entity really in the space the route authorised?
--
-- The write path asks this before it resolves or writes anything, and the
-- single bool is the point — "no such entity" and "an entity in a space the
-- caller cannot reach" are the same false, so the answer carries no existence
-- oracle. Same shape and same reasoning as EntityRelationTargetIsReadable in
-- items.sql, narrowed to the one space the route proved readable.
SELECT EXISTS (
    SELECT 1 FROM tickets tk
     WHERE @entity_type::text = 'ticket'
       AND tk.id = @entity_id::uuid
       AND tk.space_id = @space_id::uuid
       AND tk.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM project_items pi
     WHERE @entity_type::text = 'project_item'
       AND pi.id = @entity_id::uuid
       AND pi.space_id = @space_id::uuid
       AND pi.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM pages pg
     WHERE @entity_type::text = 'page'
       AND pg.id = @entity_id::uuid
       AND pg.space_id = @space_id::uuid
       AND pg.deleted_at IS NULL
) AS in_space;

-- name: AddEntityTag :exec
-- The INSERT carries the space reconciliation in the same statement, so an
-- association can never be written for an entity outside the space the caller
-- was authorised against — even by a caller that skipped the probe above. The
-- page-only predecessor was a bare INSERT whose safety was an unenforced
-- calling convention in the handler; entity-generic, the predicate is part of
-- the write itself.
INSERT INTO entity_tags (entity_type, entity_id, tag_id)
SELECT @entity_type::text, @entity_id::uuid, @tag_id::uuid
WHERE EXISTS (
    SELECT 1 FROM tickets tk
     WHERE @entity_type::text = 'ticket'
       AND tk.id = @entity_id::uuid
       AND tk.space_id = @space_id::uuid
       AND tk.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM project_items pi
     WHERE @entity_type::text = 'project_item'
       AND pi.id = @entity_id::uuid
       AND pi.space_id = @space_id::uuid
       AND pi.deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM pages pg
     WHERE @entity_type::text = 'page'
       AND pg.id = @entity_id::uuid
       AND pg.space_id = @space_id::uuid
       AND pg.deleted_at IS NULL
)
ON CONFLICT (entity_type, entity_id, tag_id) DO NOTHING;

-- name: DeleteEntityTagsExcept :exec
-- Removes every association this entity holds that is not in the incoming set.
-- Paired with AddEntityTag, this is a whole-set replacement without dropping
-- and re-adding the rows that did not change — so created_at stays true for a
-- tag that was already there.
--
-- Same in-statement reconciliation as AddEntityTag, for the same reason: a
-- DELETE keyed on a polymorphic id alone would strip the tags of any entity in
-- the installation.
DELETE FROM entity_tags AS et
WHERE et.entity_type = @entity_type::text
  AND et.entity_id = @entity_id::uuid
  AND NOT (et.tag_id = ANY(@keep_ids::uuid[]))
  AND EXISTS (
      SELECT 1 FROM tickets tk
       WHERE @entity_type::text = 'ticket'
         AND tk.id = @entity_id::uuid
         AND tk.space_id = @space_id::uuid
         AND tk.deleted_at IS NULL
      UNION ALL
      SELECT 1 FROM project_items pi
       WHERE @entity_type::text = 'project_item'
         AND pi.id = @entity_id::uuid
         AND pi.space_id = @space_id::uuid
         AND pi.deleted_at IS NULL
      UNION ALL
      SELECT 1 FROM pages pg
       WHERE @entity_type::text = 'page'
         AND pg.id = @entity_id::uuid
         AND pg.space_id = @space_id::uuid
         AND pg.deleted_at IS NULL
  );

-- name: ListEntitiesWithTag :many
-- The tag browse. Cross-space by nature (a tag is org-scoped), so every arm
-- filters against the caller's resolved readable set — ADR-0010's rule for
-- every cross-space endpoint. An empty readable set matches nothing, which is
-- the correct fail-closed answer rather than "no filter".
--
-- Each arm returns its kind's own raw ref parts — pages their path, tickets
-- their number, project items their item_key — and the API layer composes the
-- human reference at each kind's one existing composition site
-- (tickets.ComposeRef; item_key is already the composed spelling, per the
-- saved_views.sql precedent). Refs are never composed in SQL here.
SELECT u.entity_type, u.id, u.space_id, u.title, u.updated_at,
       u.space_name, u.space_key, u.path, u.number, u.item_key
FROM (
    SELECT 'page'::text AS entity_type, p.id, p.space_id, p.title, p.updated_at,
           s.name AS space_name, s.key AS space_key,
           p.path AS path, NULL::int AS number, ''::text AS item_key
    FROM entity_tags et
    JOIN pages  p ON p.id = et.entity_id
    JOIN spaces s ON s.id = p.space_id
    WHERE et.tag_id = sqlc.arg(tag_id)
      AND et.entity_type = 'page'
      AND p.deleted_at IS NULL
      AND s.deleted_at IS NULL
      AND p.space_id = ANY(sqlc.arg(readable_space_ids)::uuid[])

    UNION ALL

    SELECT 'ticket'::text, tk.id, tk.space_id, tk.title, tk.updated_at,
           s.name, s.key, ''::text, tk.number, ''::text
    FROM entity_tags et
    JOIN tickets tk ON tk.id = et.entity_id
    JOIN spaces  s  ON s.id = tk.space_id
    WHERE et.tag_id = sqlc.arg(tag_id)
      AND et.entity_type = 'ticket'
      AND tk.deleted_at IS NULL
      AND s.deleted_at IS NULL
      AND tk.space_id = ANY(sqlc.arg(readable_space_ids)::uuid[])

    UNION ALL

    SELECT 'project_item'::text, pi.id, pi.space_id, pi.title, pi.updated_at,
           s.name, s.key, ''::text, NULL::int, pi.item_key
    FROM entity_tags et
    JOIN project_items pi ON pi.id = et.entity_id
    JOIN spaces s ON s.id = pi.space_id
    WHERE et.tag_id = sqlc.arg(tag_id)
      AND et.entity_type = 'project_item'
      AND pi.deleted_at IS NULL
      AND s.deleted_at IS NULL
      AND pi.space_id = ANY(sqlc.arg(readable_space_ids)::uuid[])
) u
ORDER BY u.updated_at DESC, u.id DESC
-- One more than the page size the service returns, applied to the UNION, not
-- per arm — a per-arm limit could return up to three pages' worth of rows and
-- a truncation signal that means nothing. The extra row is how the caller
-- learns the answer was cut short: a bare LIMIT returns a truncated list that
-- is indistinguishable from a complete one, and the entities that vanish are
-- the oldest, so a reader is told nothing and shown the wrong nothing.
LIMIT 201;
