-- Customer portal queries (migrations 044 and 045).
--
-- READ THIS BEFORE ADDING A QUERY HERE. Every statement in this file may be
-- reached by an external requester who is not a member of the organisation,
-- holds no grant, and must learn nothing about the internal shape of the
-- product. Two rules follow, and both are structural rather than advisory:
--
--   1. NO CONTAINER COLUMNS IN A PORTAL SELECT LIST. No spaces.key, no
--      spaces.name, no spaces.slug, no tickets.number, no assignee, no
--      labels, no rank, no workflow state. The select list is the enforcement
--      point — a column that is never selected cannot be leaked by a
--      serialiser bug downstream.
--
--   2. THE VISIBILITY PREDICATE IS A LITERAL, NEVER A PARAMETER. See
--      ListPortalTicketComments. A parameterised visibility is one wrong call
--      site away from handing an internal note to a customer; a literal in a
--      portal-only statement cannot be passed the wrong value.

-- ── Requesters ───────────────────────────────────────────────────────────

-- UpsertRequester finds or creates the identity for (org, email).
--
-- ON CONFLICT rather than select-then-insert because two simultaneous
-- first-time link requests from one address must not produce two identities.
-- The DO UPDATE is a no-op write on email that exists solely so RETURNING
-- yields the row in both the insert and the conflict case; display_name is
-- filled in only when the row has none, so a later blank submission cannot
-- erase a name the requester already gave.
-- name: UpsertRequester :one
INSERT INTO requesters (org_id, email, display_name)
VALUES ($1, $2, $3)
ON CONFLICT (org_id, lower(email)) DO UPDATE
SET display_name = CASE
        WHEN requesters.display_name = '' THEN EXCLUDED.display_name
        ELSE requesters.display_name
    END,
    last_seen_at = now()
RETURNING *;

-- name: GetRequesterByID :one
SELECT * FROM requesters WHERE id = $1;

-- name: GetRequesterByEmail :one
SELECT * FROM requesters WHERE org_id = $1 AND lower(email) = lower($2);

-- GetRequesterState is the portal guard's per-request revocation read — the
-- requester-side counterpart of GetUserAuthState, and it must stay exactly
-- one indexed primary-key lookup for the same reason (spec §2.5 case 23).
-- name: GetRequesterState :one
SELECT is_active, session_generation FROM requesters WHERE id = $1;

-- BumpRequesterSessions invalidates every session the requester holds,
-- instantly, by moving the generation the guard compares against.
-- name: BumpRequesterSessions :execrows
UPDATE requesters SET session_generation = session_generation + 1 WHERE id = $1;

-- name: SetRequesterActive :execrows
UPDATE requesters
SET is_active = $2,
    session_generation = CASE WHEN $2 THEN session_generation ELSE session_generation + 1 END
WHERE id = $1;

-- ── Portals ──────────────────────────────────────────────────────────────

-- name: CreatePortal :one
INSERT INTO service_desk_portals (space_id, portal_key, name, intro, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- GetPortalByKey resolves the public identifier. It joins spaces only to
-- recover org_id and to exclude a portal whose space was deleted — no space
-- column reaches the select list.
-- name: GetPortalByKey :one
SELECT p.*, s.org_id
FROM service_desk_portals p
JOIN spaces s ON s.id = p.space_id AND s.deleted_at IS NULL
WHERE p.portal_key = $1 AND p.enabled;

-- GetPortalByID is used when rebuilding a session, and requires the portal to
-- still be enabled — so disabling a portal ends its outstanding sessions on
-- their next request rather than at their next expiry.
-- name: GetPortalByID :one
SELECT p.*, s.org_id
FROM service_desk_portals p
JOIN spaces s ON s.id = p.space_id AND s.deleted_at IS NULL
WHERE p.id = $1 AND p.enabled;

-- GetPortalBySpace is the AGENT-side read and deliberately does NOT filter on
-- enabled — the settings screen has to show a disabled portal in order to
-- offer to re-enable it.
-- name: GetPortalBySpace :one
SELECT p.*, s.org_id
FROM service_desk_portals p
JOIN spaces s ON s.id = p.space_id AND s.deleted_at IS NULL
WHERE p.space_id = $1;

-- name: SetPortalEnabled :one
UPDATE service_desk_portals SET enabled = $2 WHERE space_id = $1 RETURNING *;

-- ── Magic links ──────────────────────────────────────────────────────────

-- InvalidateOutstandingLinks supersedes a requester's unconsumed links for
-- one portal. Called immediately before issuing a new one, in the same
-- transaction, so that "request another link" cannot leave two live
-- credentials in one inbox.
-- name: InvalidateOutstandingLinks :execrows
UPDATE requester_magic_links
SET invalidated_at = now()
WHERE requester_id = $1 AND portal_id = $2
  AND consumed_at IS NULL AND invalidated_at IS NULL;

-- name: CreateMagicLink :one
INSERT INTO requester_magic_links (requester_id, portal_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- ConsumeMagicLink redeems a link.
--
-- THE SINGLE-USE GUARD IS IN THIS UPDATE'S WHERE CLAUSE, not in a SELECT
-- before it. Two concurrent redemptions of one link both pass a pre-check and
-- both proceed; only one can win a guarded UPDATE. Zero rows returned means
-- the link was unknown, already consumed, superseded, or expired — the caller
-- collapses all four into one indistinguishable refusal.
-- name: ConsumeMagicLink :one
UPDATE requester_magic_links
SET consumed_at = now()
WHERE token_hash = $1
  AND consumed_at IS NULL
  AND invalidated_at IS NULL
  AND expires_at > now()
RETURNING requester_id, portal_id;

-- ── Requests (the portal's view of tickets) ──────────────────────────────

-- CreatePortalRequest raises a portal-originated ticket.
--
-- reporter_id is left NULL and requester_id set, which migration 044's
-- tickets_origin_identity XOR requires; that is what makes "came from the
-- portal" derivable from the data rather than from a separate origin flag
-- that could disagree with it.
--
-- The number is computed INSIDE the insert rather than by a preceding SELECT.
-- The existing agent path (GetTicketMaxNumber then CreateTicket, two round
-- trips with no lock) can interleave and collide on UNIQUE (space_id, number);
-- one statement narrows that window to the statement itself, and the adapter
-- retries the unique violation that remains. Project items avoid the problem
-- entirely with a counter row (migration 031); tickets never got one, and
-- fixing that properly is a change to the shared agent create path rather
-- than to this feature.
-- name: CreatePortalRequest :one
INSERT INTO tickets (space_id, number, title, description, priority, status, requester_id)
SELECT $1, COALESCE(MAX(t.number), 0) + 1, $2, $3, 'medium', 'open', $4
FROM tickets t WHERE t.space_id = $1
RETURNING id, title, description, status, created_at, updated_at;

-- ListPortalRequests returns ONLY this requester's own requests.
--
-- Scoped by requester_id in the query itself, not filtered afterwards: rows
-- belonging to another requester are never in the result set, so there is no
-- filtered-out remainder for a bug downstream to reveal.
-- name: ListPortalRequests :many
SELECT id, title, description, status, created_at, updated_at
FROM tickets
WHERE space_id = $1 AND requester_id = $2 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- GetPortalRequest resolves one request under the requester's own scope. A
-- request belonging to somebody else returns zero rows and is therefore
-- indistinguishable from one that does not exist — §2.6's "never 403, do not
-- leak existence", applied to an external reader.
-- name: GetPortalRequest :one
SELECT id, title, description, status, created_at, updated_at
FROM tickets
WHERE id = $1 AND space_id = $2 AND requester_id = $3 AND deleted_at IS NULL;

-- name: GetTicketAssignee :one
SELECT assignee_id FROM tickets WHERE id = $1 AND deleted_at IS NULL;

-- ── Messages (the portal's view of public comments) ──────────────────────

-- ListPortalTicketComments returns the PUBLIC comments on a request.
--
-- `c.visibility = 'public'` IS A LITERAL AND MUST STAY ONE. This is the query
-- that decides whether a customer sees an agent's internal note, and the
-- whole point of writing it here rather than reusing ListCommentsByEntity
-- with a visibility parameter is that a literal cannot be passed the wrong
-- value. If this ever needs to serve both audiences, add a second statement;
-- do not parameterise this one.
--
-- The author join is LEFT on both sides because migration 045 made author_id
-- nullable — a requester's own message has author_requester_id instead, and
-- an INNER JOIN on users would silently drop exactly the messages the
-- requester most expects to see.
-- name: ListPortalTicketComments :many
SELECT c.id,
       c.body,
       c.created_at,
       (c.author_requester_id IS NOT NULL)::bool AS from_requester,
       COALESCE(u.display_name, r.display_name, '')::text AS author_label
FROM comments c
LEFT JOIN users u ON u.id = c.author_id
LEFT JOIN requesters r ON r.id = c.author_requester_id
WHERE c.entity_type = 'ticket'
  AND c.entity_id = $1
  AND c.deleted_at IS NULL
  AND c.visibility = 'public'
ORDER BY c.created_at ASC;

-- CreateRequesterComment writes a requester's reply.
--
-- visibility is written 'public' explicitly even though migration 045's
-- comments_requester_public constraint would refuse anything else. The
-- redundancy is deliberate: the constraint is the backstop that catches a
-- future caller, and this literal is the statement of intent at the only
-- caller that exists today.
-- name: CreateRequesterComment :one
INSERT INTO comments (entity_type, entity_id, author_requester_id, body, visibility)
VALUES ('ticket', $1, $2, $3, 'public')
RETURNING id, body, created_at;
