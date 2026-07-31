-- Codex tags (migration 040). Org-scoped tags and the page↔tag association.

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

-- name: ListTagsForPage :many
-- A page's tags, reconciled against the space the request named. A tag set
-- describes what a page is about, so reading one across a space boundary
-- discloses the subject matter of a page the caller cannot open.
SELECT t.id, t.org_id, t.slug, t.name, t.created_at
FROM page_tags pt
JOIN tags t ON t.id = pt.tag_id
JOIN pages p ON p.id = pt.page_id
WHERE pt.page_id = @page_id
  AND p.space_id = @space_id
  AND p.deleted_at IS NULL
ORDER BY t.name;

-- name: AddPageTag :exec
INSERT INTO page_tags (page_id, tag_id)
VALUES ($1, $2)
ON CONFLICT (page_id, tag_id) DO NOTHING;

-- name: DeletePageTagsExcept :exec
-- Removes every association this page holds that is not in the incoming set.
-- Paired with AddPageTag, this is a whole-set replacement without dropping and
-- re-adding the rows that did not change — so created_at stays true for a tag
-- that was already there.
DELETE FROM page_tags
WHERE page_id = $1 AND NOT (tag_id = ANY(@keep_ids::uuid[]));

-- name: ListPagesWithTag :many
-- The tag browse. Cross-space by nature (a tag is org-scoped), so it filters
-- against the caller's resolved readable set — ADR-0010's rule for every
-- cross-space endpoint. An empty readable set matches nothing, which is the
-- correct fail-closed answer rather than "no filter".
SELECT p.id, p.space_id, p.title, p.path, p.updated_at, s.name AS space_name, s.key AS space_key
FROM page_tags pt
JOIN pages  p ON p.id = pt.page_id
JOIN spaces s ON s.id = p.space_id
WHERE pt.tag_id = $1
  AND p.deleted_at IS NULL
  AND s.deleted_at IS NULL
  AND p.space_id = ANY(@readable_space_ids::uuid[])
ORDER BY p.updated_at DESC
-- One more than the page size the service returns. The extra row is how the
-- caller learns the answer was cut short: a bare LIMIT returns a truncated list
-- that is indistinguishable from a complete one, and the pages that vanish are
-- the oldest, so a reader is told nothing and shown the wrong nothing.
LIMIT 201;
