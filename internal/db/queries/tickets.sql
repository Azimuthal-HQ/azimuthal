-- name: CreateTicket :one
INSERT INTO tickets (id, space_id, number, title, description, status, priority,
                     reporter_id, assignee_id, labels, due_at, rank)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetTicketInSpace :one
-- A ticket, reconciled against the space the request named. See the note on
-- GetProjectItemInSpace in project_items.sql — this is the same missing
-- predicate on the Beacon side, and it disclosed the whole ticket including
-- its description.
SELECT * FROM tickets
WHERE id = @ticket_id AND space_id = @space_id AND deleted_at IS NULL;

-- name: GetTicketByID :one
-- UNSCOPED. The legitimate callers are the entity-share read path (ADR-0008,
-- where share coverage authorises instead of space access) and the customer
-- portal, whose requester holds no space membership at all and is authorised
-- by its own token audience. Every space-scoped route wants GetTicketInSpace.
SELECT * FROM tickets WHERE id = $1 AND deleted_at IS NULL;

-- name: ListTicketsBySpace :many
SELECT * FROM tickets
WHERE space_id = $1 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListTicketsByStatus :many
SELECT * FROM tickets
WHERE space_id = $1 AND status = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: ListTicketsByAssignee :many
SELECT * FROM tickets
WHERE space_id = $1 AND assignee_id = $2 AND deleted_at IS NULL
ORDER BY rank ASC, created_at DESC;

-- name: UpdateTicket :one
UPDATE tickets
SET title = $2, description = $3, status = $4, priority = $5,
    assignee_id = $6, labels = $7, due_at = $8, rank = $9,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateTicketStatus :one
UPDATE tickets
SET status = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateTicketWorkflowState :one
-- The space predicate is what makes ApplyInput.SpaceID load-bearing. It was
-- carried all the way into the applier and then never used — the caller's space
-- reached the audit row and nothing else — so a transition released by an
-- approval in another space wrote the far entity by bare id. The approval is now
-- reconciled upstream and cannot arrive here mismatched, so this is the seam
-- being closed rather than the hole; a miss is zero rows and the transaction
-- rolls back rather than committing a status the caller had no claim on.
UPDATE tickets
SET status = @status, workflow_state_id = @workflow_state_id, updated_at = now()
WHERE id = @ticket_id AND space_id = @space_id AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteTicket :exec
UPDATE tickets SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: SearchTickets :many
SELECT * FROM tickets
WHERE space_id = $1
  AND deleted_at IS NULL
  AND search_vector @@ plainto_tsquery('english', $2)
ORDER BY ts_rank(search_vector, plainto_tsquery('english', $2)) DESC
LIMIT $3;

-- name: GetTicketMaxNumber :one
SELECT COALESCE(MAX(number), 0)::bigint AS max_number FROM tickets WHERE space_id = $1;

-- name: SuggestTicketRefs :many
-- Backs the ticket_ref typeahead. The readable_space_ids filter IS the access
-- control: for an org admin the resolver fills that set with every live space
-- in the org, for everyone else with exactly their granted set, so one ANY()
-- serves both personas and no ticket outside the caller's read access can
-- appear. The caller never runs this with an empty set — the service
-- short-circuits first.
--
-- Matching is ILIKE, deliberately not the search_vector GIN index: a typeahead
-- needs substring behaviour on partial words ("BEA-4", "logi") and tsvector
-- matching gives neither prefix nor infix. Tickets have no key column of their
-- own, so the human-readable reference is composed here from the space key and
-- the ticket number; matching that composed string is what lets an operator
-- type "BEA-42", "bea-42" or just "42".
--
-- COALESCE around the assignment test is load-bearing twice over. In the
-- select list it keeps assigned_to_me a plain bool rather than a tri-state,
-- and in ORDER BY it makes an unassigned ticket (assignee_id NULL, so the
-- comparison is NULL) sort *after* the caller's own — DESC defaults to
-- NULLS FIRST, which would otherwise float every unassigned ticket to the top.
SELECT t.id, t.number, t.title, t.space_id, t.status,
       s.key AS space_key,
       COALESCE(t.assignee_id = sqlc.arg(caller_id)::uuid, false)::boolean AS assigned_to_me
FROM tickets t
JOIN spaces s ON s.id = t.space_id AND s.deleted_at IS NULL
WHERE t.deleted_at IS NULL
  AND t.space_id = ANY(sqlc.arg(readable_space_ids)::uuid[])
  -- The caller's text is a literal substring, not a pattern. It is already a
  -- bound parameter, so this is not an injection guard — it stops a bare `%`
  -- or `_` in a legitimate query from acting as a wildcard and quietly
  -- widening the match. Backslash is PostgreSQL's default LIKE escape, so the
  -- escape character itself is doubled first.
  AND (sqlc.arg(query)::text = ''
       OR t.title ILIKE '%' || replace(replace(replace(sqlc.arg(query)::text, '\', '\\'), '%', '\%'), '_', '\_') || '%'
       OR (s.key || '-' || t.number::text) ILIKE '%' || replace(replace(replace(sqlc.arg(query)::text, '\', '\\'), '%', '\%'), '_', '\_') || '%')
ORDER BY COALESCE(t.assignee_id = sqlc.arg(caller_id)::uuid, false) DESC,
         t.updated_at DESC
LIMIT 20;

-- name: UserIsMemberOfSpaceOrg :one
-- Is this user a member of the organisation that owns this space?
--
-- The assignment write needs it and had nothing like it. tickets.assignee_id
-- references the GLOBAL users table, so a uuid naming any user in the
-- installation satisfies the foreign key and the write lands 200 — the ticket
-- then names somebody with no membership in the org and no access to the space,
-- and the notification enqueuer carries the ticket's TITLE to them
-- (known-issues #23c).
--
-- Membership is resolved THROUGH the space rather than taken from the caller's
-- token. The org that matters is the one owning the ticket, not the one the
-- actor is logged into, and on this route those are already proven to be the
-- same by RequireSpaceInOrg — but only the URL's ids were ever compared, so
-- deriving it here keeps the check true of the entity rather than of the
-- request.
--
-- Single bool over an EXISTS, for the same reason EntityRelationTargetIsReadable
-- is: "no such user" and "a user in another org" must not be two answers a
-- caller could tell apart.
SELECT EXISTS (
    SELECT 1
      FROM spaces s
      JOIN memberships m ON m.org_id = s.org_id
     WHERE s.id = @space_id
       AND m.user_id = @user_id
) AS is_member;
