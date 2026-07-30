-- +goose Up
-- +goose StatementBegin

-- Customer portal, part 2 of 2: comment visibility and requester authorship.
-- Migration 044 carries the requester identity this one references.
--
-- ── Why the column lands on a polymorphic table ──────────────────────────
--
-- `comments` serves three entity types (migration 015:
-- 'ticket' | 'project_item' | 'page'), and only tickets have a portal. A
-- visibility flag that is meaningful for one third of a table's rows is a
-- smell worth answering rather than ignoring, and there are two honest
-- answers: a separate portal-comment table, or one column with a constraint
-- that says where it applies.
--
-- A separate table was rejected. ADR-0003 lists comments among the things
-- shared "deliberately, and built for both from the start", and a ticket
-- conversation split across two tables would give the agent thread and the
-- portal thread different sources of truth for one exchange — the exact
-- shape that makes "the customer says they replied and I can't see it"
-- unanswerable. So: one column, and constraints that make its scope
-- explicit rather than conventional.
--
-- ── The safe direction ───────────────────────────────────────────────────
--
-- DEFAULT 'internal' is doing real work on the backfill. Every comment that
-- exists today was written by an agent, in a product that had no external
-- reader, with the reasonable expectation that no customer would ever see
-- it. NOT NULL DEFAULT 'internal' settles all of them in the direction that
-- cannot leak. A default of 'public', or a nullable column read as "not yet
-- classified", would make the migration itself the disclosure event.
ALTER TABLE comments ADD COLUMN visibility TEXT NOT NULL DEFAULT 'internal';

ALTER TABLE comments ADD CONSTRAINT comments_visibility_valid
    CHECK (visibility IN ('internal', 'public'));

-- Where the flag applies, as a constraint rather than a convention. A page
-- or project-item comment may only ever be 'internal', so a future surface
-- that reads `visibility` on the wrong entity type cannot find a row
-- claiming to be publishable. Note the asymmetry is deliberate: this does
-- not say "ticket comments are public", it says "only ticket comments may
-- be".
ALTER TABLE comments ADD CONSTRAINT comments_public_ticket_only
    CHECK (visibility = 'internal' OR entity_type = 'ticket');

-- ── Requester authorship ─────────────────────────────────────────────────
--
-- `author_id` becomes nullable and gains an exclusive-or partner, on the
-- same reasoning as tickets.reporter_id in migration 044: a requester has
-- no `users` row, and attributing their reply to a stand-in user would put
-- an agent's name on a customer's words.
--
-- This restores a shape the table used to have. Migration 006 carried
-- `comments_must_have_target`, an XOR over (item_id, page_id), until 015
-- replaced it with the entity_type/entity_id pair. The author side now needs
-- the same treatment for the same reason, so the constraint is named in the
-- same spirit.
ALTER TABLE comments ADD COLUMN author_requester_id UUID REFERENCES requesters (id);
ALTER TABLE comments ALTER COLUMN author_id DROP NOT NULL;

ALTER TABLE comments ADD CONSTRAINT comments_author_identity CHECK (
    (author_id IS NOT NULL AND author_requester_id IS NULL)
    OR (author_id IS NULL AND author_requester_id IS NOT NULL)
);

-- "A requester's own words are always public" as a database constraint.
--
-- This one earns its place. The rule is trivially true at the only call
-- site that writes requester comments today, so a reviewer could reasonably
-- ask why it needs enforcing. The answer is what it prevents rather than
-- what it asserts: an internal-by-default comment box, a shared create
-- path, and one future caller that forgets to override the default would
-- silently file the customer's own reply where the customer cannot read it.
-- That failure is invisible from both sides — the agent sees the reply, the
-- requester sees their message vanish — and it is unfixable after the fact
-- because nothing records that the default was wrong. The constraint turns
-- it into a write that fails loudly.
ALTER TABLE comments ADD CONSTRAINT comments_requester_public CHECK (
    author_requester_id IS NULL OR visibility = 'public'
);

-- The portal's only comment read, served without touching the internal rows.
-- Partial on visibility so the index itself cannot return an internal
-- comment even if a query forgot the predicate — defence in depth behind
-- the hard-coded literal in ListPortalTicketComments.
CREATE INDEX comments_entity_public_idx ON comments (entity_type, entity_id, created_at)
    WHERE deleted_at IS NULL AND visibility = 'public';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS comments_entity_public_idx;

-- Requester-authored comments cannot survive the restored NOT NULL on
-- author_id, and they are exactly the rows this migration made possible.
-- Migration 044's Down takes the same line for portal-originated tickets.
DELETE FROM comments WHERE author_requester_id IS NOT NULL;

ALTER TABLE comments DROP CONSTRAINT IF EXISTS comments_requester_public;
ALTER TABLE comments DROP CONSTRAINT IF EXISTS comments_author_identity;
ALTER TABLE comments DROP COLUMN IF EXISTS author_requester_id;
ALTER TABLE comments ALTER COLUMN author_id SET NOT NULL;

ALTER TABLE comments DROP CONSTRAINT IF EXISTS comments_public_ticket_only;
ALTER TABLE comments DROP CONSTRAINT IF EXISTS comments_visibility_valid;
ALTER TABLE comments DROP COLUMN IF EXISTS visibility;

-- +goose StatementEnd
