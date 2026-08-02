-- CreateComment writes an AGENT comment.
--
-- THE EMPTY STRING MEANS INTERNAL, and the COALESCE is what makes that true
-- rather than a convention. sqlc turns visibility into a required Go
-- parameter, so migration 045's column DEFAULT can never fire on this
-- statement — every insert sends a value, including the zero value. Without
-- the COALESCE a caller that simply forgot the field would send "", which
-- satisfies no branch of comments_visibility_valid and fails the write.
--
-- Failing loudly would be defensible; defaulting to the safe value is better,
-- because the two mistakes are not symmetric. A forgotten field that becomes
-- 'internal' is a comment the customer has to wait for. A forgotten field
-- that became 'public' would be a disclosure. The zero value must therefore
-- be the private one, and this is where that is enforced for every caller at
-- once instead of in each handler.
--
-- A requester's own reply does NOT come through here — see
-- CreateRequesterComment in portal.sql, which is a separate statement so that
-- the author columns cannot be mixed up.
-- name: CreateComment :one
INSERT INTO comments (id, entity_type, entity_id, parent_id, author_id, body, visibility)
VALUES ($1, $2, $3, $4, $5, $6, COALESCE(NULLIF(sqlc.arg(visibility)::text, ''), 'internal'))
RETURNING *;

-- name: GetCommentByID :one
SELECT * FROM comments WHERE id = $1 AND deleted_at IS NULL;

-- ListCommentsByEntity is the AGENT-side thread: every comment on the entity,
-- internal and public alike, with the visibility carried so the UI can mark
-- which ones the customer can read.
--
-- THE AUTHOR JOINS ARE LEFT, AND MUST STAY LEFT. Migration 045 made
-- author_id nullable so a requester's reply can be attributed to a
-- requesters row instead of a users row. The INNER JOIN this replaced would
-- have dropped every requester message from the agent's view of the
-- conversation — silently, with the agent seeing a thread that appeared to
-- have no customer in it.
--
-- ITS SIBLING ListCommentReplies WAS DELETED RATHER THAN SCOPED. That query
-- selected a comment's children with `WHERE c.parent_id = $1` and nothing else:
-- no space, no org, no entity, no visibility filter. It was the one comment read
-- the cross-space read pass did not give a @space_id, missed because that sweep
-- followed callers and this had none - no adapter, no domain interface, no
-- service, no handler, no route, no OpenAPI schema, no frontend type. Its only
-- caller was three lines of TestComments in internal/db/queries_test.go.
--
-- Wiring it as it stood would have reintroduced exactly the disclosure the
-- EXISTS below closes, and added a second one on top: with no visibility
-- predicate it returned internal comments, which the portal read deliberately
-- withholds. A primitive one call site away from two disclosures is not a head
-- start on the feature; it is a trap that reads as ready.
--
-- Threaded replies ARE commissioned work - replies are written and never read
-- back today, because of the `c.parent_id IS NULL` filter below. Whoever builds
-- that read writes it then, with this query's @space_id + EXISTS shape AND a
-- visibility predicate. Neither is a modification of what stood here; both are
-- the query it should have been.
-- name: ListCommentsByEntity :many
SELECT c.id, c.entity_type, c.entity_id, c.item_id, c.page_id, c.parent_id,
       c.author_id, c.body, c.created_at, c.updated_at, c.deleted_at,
       c.visibility, c.author_requester_id,
       COALESCE(u.display_name, r.display_name, '') AS author_name,
       u.avatar_url AS author_avatar
--
-- The entity is reconciled against the space the request named. Comments carry
-- no space_id of their own — they are readable exactly when the thing they are
-- attached to is — so the test is an EXISTS against whichever table entity_type
-- names. Without it a bare entity id returned every comment body on any item,
-- ticket or page in the installation, including internal-visibility notes that
-- the customer-facing surface deliberately withholds.
--
-- The three arms are mutually exclusive on entity_type, so exactly one can
-- match; writing it as a union rather than three OR'd branches keeps each arm's
-- id comparison bound to its own table.
FROM comments c
LEFT JOIN users u ON u.id = c.author_id
LEFT JOIN requesters r ON r.id = c.author_requester_id
WHERE c.entity_type = @entity_type::text
  AND c.entity_id = @entity_id
  AND c.parent_id IS NULL
  AND c.deleted_at IS NULL
  AND EXISTS (
      SELECT 1 FROM project_items pi
       WHERE @entity_type::text = 'project_item'
         AND pi.id = @entity_id AND pi.space_id = @space_id AND pi.deleted_at IS NULL
      UNION ALL
      SELECT 1 FROM tickets t
       WHERE @entity_type::text = 'ticket'
         AND t.id = @entity_id AND t.space_id = @space_id AND t.deleted_at IS NULL
      UNION ALL
      SELECT 1 FROM pages p
       WHERE @entity_type::text = 'page'
         AND p.id = @entity_id AND p.space_id = @space_id AND p.deleted_at IS NULL
  )
ORDER BY c.created_at ASC;

-- name: UpdateComment :one
UPDATE comments SET body = $2, updated_at = now() WHERE id = $1 AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteComment :exec
UPDATE comments SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: CommentBelongsToEntity :one
-- Is this comment a reply target on this exact entity?
--
-- parent_id arrives in the REQUEST BODY and reached the INSERT unchecked. Its
-- only constraint is migration 006's `parent_id UUID REFERENCES comments (id)`,
-- a bare foreign key to the whole table — nothing ties a reply to the thread it
-- claims to be part of, to the entity, to the space, or to the organisation.
--
-- Two things follow, and the second is the one that makes this a disclosure
-- rather than a data-integrity nit. A parent naming no comment violates the
-- foreign key and answers 500; a parent naming a real comment ANYWHERE in the
-- installation answers 201. That difference is an existence oracle over every
-- comment id in every organisation. Reconciling the parent against the entity
-- collapses both to one refusal.
--
-- entity_type and entity_id rather than a space: comments carry no space column
-- of their own, and the entity has already been reconciled against the caller's
-- space by the time this runs — so agreeing with the entity is exactly as
-- strong as agreeing with the space, and needs no second join.
SELECT EXISTS (
    SELECT 1 FROM comments
     WHERE id = @comment_id
       AND entity_type = @entity_type::text
       AND entity_id = @entity_id
       AND deleted_at IS NULL
) AS belongs;
