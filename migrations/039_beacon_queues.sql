-- +goose Up
-- +goose StatementBegin

-- Beacon queues (P4 PR-B), built on the saved-view model from migration 038.
--
-- A QUEUE IS A SAVED VIEW. It is not a second model with its own results
-- path: it is a row in saved_views that carries a space binding and a position
-- among that space's queues. Everything else — the filter vocabulary, the
-- per-viewer resolution, the `me` token, the ADR-0008 share union — is the
-- same code. If a second results-resolution path ever appears, that is the
-- drift docs/design/shared-surfaces.md exists to prevent, and this table is
-- shaped the way it is specifically to make the second path unnecessary.
--
-- Three columns, and the constraints that make the two kinds of row
-- distinguishable at the storage layer rather than by convention:
--
--   space_id IS NULL      an ordinary saved view (migration 038)
--   space_id IS NOT NULL  a queue, in that space, at that position
--
-- saved_views_queue_shape ties the three together so a half-queue cannot
-- exist: a row either has all of (space_id, position, visibility='space') or
-- none of them. Without it a queue could be written with no position and
-- would sort arbitrarily, or an ordinary view could claim a space and quietly
-- disappear from the views list.
--
-- WHY 'space' IS A VISIBILITY RATHER THAN AN IMPLICIT RULE. A queue is
-- readable by everyone who can read its space, which is neither 'private',
-- 'team' nor 'org'. Naming it keeps the audience in the same column as every
-- other audience, so the "who may see this view" question has exactly one
-- answer site. The generic /views list excludes space-bound rows entirely —
-- queues are listed by the space-scoped queue endpoint, whose middleware has
-- already established that the caller can read the space — so the audience is
-- enforced by the route rather than re-derived per row.

ALTER TABLE saved_views
    ADD COLUMN space_id UUID REFERENCES spaces (id) ON DELETE CASCADE,
    ADD COLUMN position INT;

-- Replace 038's visibility vocabulary to admit the space audience.
ALTER TABLE saved_views DROP CONSTRAINT saved_views_visibility_valid;
ALTER TABLE saved_views ADD CONSTRAINT saved_views_visibility_valid
    CHECK (visibility IN ('private', 'team', 'org', 'space'));

ALTER TABLE saved_views ADD CONSTRAINT saved_views_queue_shape
    CHECK (
        (space_id IS NULL AND position IS NULL AND visibility <> 'space')
        OR (space_id IS NOT NULL AND position IS NOT NULL AND visibility = 'space')
    );

-- Ordering among a space's queues.
--
-- DEFERRABLE INITIALLY DEFERRED for the reason migration 035 gives for
-- board_columns: a reorder renumbers several rows inside one transaction and
-- must not have to shuffle through temporary positions to dodge the
-- constraint. The uniqueness is still enforced — just at COMMIT.
--
-- Deliberately NOT partial on deleted_at. A soft-deleted queue keeps its
-- position slot, exactly as a soft-deleted space keeps its slug (migration
-- 028); reusing a dead queue's position would make an undelete ambiguous.
ALTER TABLE saved_views ADD CONSTRAINT saved_views_space_position_key
    UNIQUE (space_id, position) DEFERRABLE INITIALLY DEFERRED;

-- One live queue per name per space. This is what makes "create the default
-- queues" idempotent BY CONSTRUCTION rather than by the handler checking
-- first: the seeding insert is ON CONFLICT DO NOTHING against this index, so
-- pressing the button twice, or two agents pressing it at once, cannot
-- produce duplicates. Partial on deleted_at so a deleted queue's name can be
-- reused, which is the opposite of the position rule above and deliberate —
-- a name is a label, a position is an identity within an ordering.
CREATE UNIQUE INDEX saved_views_space_name_key ON saved_views (space_id, name)
    WHERE space_id IS NOT NULL AND deleted_at IS NULL;

-- "The queues of this space, in order" — the only read this table adds.
CREATE INDEX saved_views_space_position_idx ON saved_views (space_id, position)
    WHERE space_id IS NOT NULL AND deleted_at IS NULL;

-- NOTE: this migration seeds NOTHING, on migration 035's reasoning. A space
-- with no queue rows has no queues, and the UI offers a one-click "create the
-- default set" that runs through the same guarded, audited API path a manual
-- create takes. A backfill would touch every existing space in one
-- unreviewable step and would have to reproduce the default filter documents
-- in SQL, where they could drift from the Go that produces them everywhere
-- else. Absence-means-none cannot drift.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS saved_views_space_position_idx;
DROP INDEX IF EXISTS saved_views_space_name_key;
ALTER TABLE saved_views DROP CONSTRAINT IF EXISTS saved_views_space_position_key;
ALTER TABLE saved_views DROP CONSTRAINT IF EXISTS saved_views_queue_shape;

-- Space-bound rows cannot survive the narrowed vocabulary, and they are
-- exactly the rows this migration created. Remove them before restoring the
-- 038 constraint, rather than letting the ALTER fail on data this migration
-- is responsible for.
DELETE FROM saved_views WHERE space_id IS NOT NULL;

ALTER TABLE saved_views DROP CONSTRAINT saved_views_visibility_valid;
ALTER TABLE saved_views ADD CONSTRAINT saved_views_visibility_valid
    CHECK (visibility IN ('private', 'team', 'org'));

ALTER TABLE saved_views DROP COLUMN IF EXISTS position;
ALTER TABLE saved_views DROP COLUMN IF EXISTS space_id;

-- +goose StatementEnd
