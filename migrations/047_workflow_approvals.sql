-- +goose Up
-- +goose StatementBegin

-- Workflow tier 2 (ADR-0011): approvals.
--
-- "A transition that blocks pending approval from a named user, team, or role,
-- with the pending state visible and the decision recorded." The ADR justifies
-- this tier on its own merits — "Beacon cannot credibly replace JSM without
-- this" — rather than on migration parity.
--
-- Two tables, because configuration and instance have different lifetimes:
-- workflow_transition_approvers says WHO must approve a given edge, and lives
-- as long as the edge; workflow_approvals is one request about one item, and
-- outlives both the edge and the approver list.
--
--
-- "or role" is not implemented, and is not silently approximated
-- ---------------------------------------------------------------
-- The ADR names three approver kinds. Two exist in this product and the third
-- does not:
--
--   user   — users(id).
--   team   — teams(id), with ADR-0007 effective membership.
--   role   — NOT REPRESENTABLE. There are two things "role" could mean here
--            and neither is available. A SPACE role (access.Role: viewer /
--            contributor / agent / space_admin) has no user-set resolution
--            query — the access model resolves subject-side, from a user to
--            their capabilities, and there is no inverse. A TEAM role
--            (team_members.role) is explicitly metadata and forbidden as a
--            permission input: internal/core/teams/service.go states "Metadata
--            only — the capability model never reads them."
--
-- Adding either would change the access model, which CLAUDE.md §5 makes a
-- stop-and-raise decision rather than a phase decision. So the CHECK below
-- admits two values, and the gap is reported rather than approximated. That is
-- the same discipline ADR-0011 requires of the migration assessor: report what
-- cannot be represented instead of quietly representing something else.
--
--
-- The subject columns mirror space_grants, deliberately
-- ----------------------------------------------------
-- migration 023 established the polymorphic subject in this codebase:
-- `subject_type TEXT` + `subject_id UUID` with a CHECK on the discriminator and
-- NO foreign key on the id, because a column cannot reference two tables. Its
-- header states the consequence — "the store layer owns that integrity" — and
-- that obligation transfers here unchanged: the API validates the subject
-- exists in the org before insert, using the same IsOrgMember / TeamExistsInOrg
-- checks grants use.
--
-- entity_shares (026) models the OTHER polymorphism — an audience where one arm
-- carries no id — with a real FK and ON DELETE CASCADE. It is the wrong mirror
-- here: both approver arms carry an id, and cascading a team deletion into an
-- approver list would silently remove an approval requirement, which is the
-- same silent-permit failure migration 046 rejects CASCADE for.
CREATE TABLE workflow_transition_approvers (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    transition_id UUID        NOT NULL REFERENCES workflow_transitions (id) ON DELETE CASCADE,

    subject_type  TEXT        NOT NULL,
    subject_id    UUID        NOT NULL,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT workflow_transition_approvers_subject_valid
        CHECK (subject_type IN ('user', 'team')),

    UNIQUE (transition_id, subject_type, subject_id)
);

-- "Who approves this edge" — the evaluation read.
CREATE INDEX workflow_transition_approvers_transition_idx
    ON workflow_transition_approvers (transition_id);

-- "Which edges is this subject an approver for" — the admin surface's
-- impact view, and the shape space_grants_subject_idx established.
CREATE INDEX workflow_transition_approvers_subject_idx
    ON workflow_transition_approvers (subject_type, subject_id);


-- One approval request about one item.
--
--
-- The item does not move while approval is pending
-- ------------------------------------------------
-- There are two ways to build a blocking approval and only one of them is
-- safe. The rejected one moves the item to the target status immediately and
-- moves it back on decline; it produces an item that is *closed pending
-- approval*, which defeats the gate — every report, board, queue and saved view
-- reads status, and status would already say the transition happened.
--
-- So the item stays in its source status and the transition commits only when
-- the approval is granted. "Decline returns the item to the source status" is
-- satisfied because it never left: a decline records the decision and the item
-- is exactly where the requester found it. This also means approvals introduce
-- no new status vocabulary — which matters, because tickets.status and
-- project_items.status are unconstrained TEXT that boards (migration 035) and
-- saved-view filters (038) enumerate. A synthetic "pending" status would have
-- appeared as a board column and broken board validation for every space.
--
--
-- Why the source status is stored as text as well as an id
-- -------------------------------------------------------
-- from_state_id can be renamed out from under a pending approval:
-- workflow_states carries UNIQUE (workflow_id, name) but nothing makes a name
-- immutable, and renaming a state does NOT rewrite the status text on items.
-- Recomputing the source status at decision time from the state row would
-- therefore restore a name the item never had. Both are captured at request
-- time; the text is what a decline restores and the id is what the engine
-- re-validates against.
--
--
-- Entity polymorphism follows audit_log, not a foreign key
-- -------------------------------------------------------
-- (entity_type, entity_id) with no FK, because tickets and project_items are
-- and stay separate tables (ADR-0003) and one column cannot reference both.
-- entity_type's CHECK matches the audit log's vocabulary for the same two
-- things — 'ticket' and 'item' — so one word means one thing across the
-- product.
--
-- space_id IS a real FK. It is denormalised onto the row rather than joined
-- through the entity because the "awaiting my decision" read has no entity
-- table to join until it knows entity_type, and because a decision must be
-- authorised against the space the request was made in.
CREATE TABLE workflow_approvals (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),

    -- SET NULL rather than CASCADE: deleting an edge must not destroy the
    -- record that someone once asked to traverse it. A pending approval whose
    -- edge is gone becomes unresolvable and is surfaced as such, rather than
    -- vanishing along with the process state.
    transition_id  UUID        REFERENCES workflow_transitions (id) ON DELETE SET NULL,

    entity_type    TEXT        NOT NULL,
    entity_id      UUID        NOT NULL,
    space_id       UUID        NOT NULL REFERENCES spaces (id) ON DELETE CASCADE,

    -- Captured at request time; see the header.
    from_state_id  UUID        REFERENCES workflow_states (id) ON DELETE SET NULL,
    to_state_id    UUID        REFERENCES workflow_states (id) ON DELETE SET NULL,
    from_status    TEXT        NOT NULL,
    to_status      TEXT        NOT NULL,

    requested_by   UUID        NOT NULL REFERENCES users (id),
    requested_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The decision triple. All three are NULL together or set together.
    decided_by     UUID        REFERENCES users (id),
    decided_at     TIMESTAMPTZ,
    decision       TEXT,

    CONSTRAINT workflow_approvals_entity_type_valid
        CHECK (entity_type IN ('ticket', 'item')),

    CONSTRAINT workflow_approvals_decision_valid
        CHECK (decision IS NULL OR decision IN ('approved', 'declined')),

    -- The biconditional from entity_shares_audience_id_present (026), applied
    -- to the decision triple: a decided_at with no decision, or a decision with
    -- no decided_at, is a half-recorded decision and must not be
    -- representable.
    CONSTRAINT workflow_approvals_decision_complete
        CHECK ((decided_at IS NULL) = (decision IS NULL)
           AND (decided_at IS NULL) = (decided_by IS NULL))
);

-- At most one PENDING approval per item.
--
-- A partial unique index and not a read-then-write. Migration 034 wrote the
-- reasoning out for sprints: "that read-then-write has a TOCTOU window under
-- concurrency … A partial unique index closes the window". Two people pressing
-- the same guarded transition at the same moment is the ordinary case here, not
-- the exotic one, so the second INSERT must lose on a constraint the database
-- enforces. The adapter maps the violation back to a 409, exactly as the sprint
-- adapter does.
--
-- Decided rows are excluded, so an item may accumulate any number of historical
-- approvals and still be able to request a new one.
CREATE UNIQUE INDEX workflow_approvals_one_pending_per_entity
    ON workflow_approvals (entity_type, entity_id)
    WHERE decided_at IS NULL;

-- "What is pending in this space" — the space-scoped list, and the read the
-- board uses to mark items blocked.
CREATE INDEX workflow_approvals_space_pending_idx
    ON workflow_approvals (space_id)
    WHERE decided_at IS NULL;

-- "What is pending on this edge" — asked before an administrator deletes a
-- transition, so the delete can refuse rather than orphan in-flight requests.
CREATE INDEX workflow_approvals_transition_pending_idx
    ON workflow_approvals (transition_id)
    WHERE decided_at IS NULL AND transition_id IS NOT NULL;


-- Nothing is seeded, and no default workflow gains an approval requirement.
-- The seeded workflows from migrations 016/019 and their reverse edges from 029
-- are untouched, so a space nobody has configured behaves exactly as it did
-- before this migration. Migrations 019 and 029 both exist because an earlier
-- seed change reached only new orgs; this migration adds no seed content, so it
-- needs no backfill counterpart.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS workflow_approvals;
DROP TABLE IF EXISTS workflow_transition_approvers;

-- +goose StatementEnd
