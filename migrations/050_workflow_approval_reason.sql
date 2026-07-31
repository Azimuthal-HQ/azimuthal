-- +goose Up
-- +goose StatementBegin

-- The decline reason (ADR-0011 tier 2, PR-B).
--
-- Migration 047 recorded WHO decided and WHEN, but not WHY. That was enough
-- while approvals had no surface; it is not enough now that one exists. An
-- approver who declines a transition is telling the requester something, and a
-- decline whose reason cannot be displayed is the silent no-op this tier was
-- built to prevent — the requester sees an item that did not move and no
-- statement of what would make it move.
--
--
-- Nullable, and deliberately not CHECKed against 'declined'
-- --------------------------------------------------------
-- The obvious constraint — `decision <> 'declined' OR reason IS NOT NULL` —
-- must NOT be written. It would refuse any already-declined row, and a live
-- database can hold those: migration 047 shipped and this column did not exist
-- when they were written. A migration that cannot apply to a database with
-- ordinary history is a migration that fails at boot, which is where D73
-- already showed this project has no safety net (internal/db/migrate.go calls
-- goose.UpContext with no AllowMissing, and CI's databases are always fresh, so
-- no job would catch it).
--
-- "A decline must carry a reason" is therefore enforced one layer up, in
-- TierService.Decide, which refuses a bare decline with ErrDeclineReasonRequired
-- before anything is written. That is the same division migration 047 already
-- states for approver subjects: the CHECK admits the shape, and the layer that
-- can see the whole request owns the rule.
--
--
-- What IS constrained
-- -------------------
-- A reason with no decision is not representable. It mirrors
-- workflow_approvals_decision_complete (047) and entity_shares_audience_id_present
-- (026): a pending approval has not been reasoned about, so a row carrying a
-- reason and no decision is a half-recorded decision, not a state the product
-- can be in. This one IS safe on existing data — every pre-existing row has
-- reason NULL, so the CHECK is satisfied by construction.
ALTER TABLE workflow_approvals
    ADD COLUMN reason TEXT;

ALTER TABLE workflow_approvals
    ADD CONSTRAINT workflow_approvals_reason_requires_decision
        CHECK (reason IS NULL OR decision IS NOT NULL);

-- No index. The reason is read only as part of a row already located by id,
-- entity or space, and it is never a search or filter key — adding one would
-- cost writes to serve a query nothing makes.

-- Nothing is backfilled and no seed changes. A space that has never configured
-- an approval has no approval rows, so this migration is invisible to it.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE workflow_approvals
    DROP CONSTRAINT IF EXISTS workflow_approvals_reason_requires_decision;

ALTER TABLE workflow_approvals
    DROP COLUMN IF EXISTS reason;

-- +goose StatementEnd
