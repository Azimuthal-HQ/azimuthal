-- +goose Up
-- +goose StatementBegin

-- Workflow tier 1 (ADR-0011): per-transition conditions and validators.
--
-- ADR-0011 divides Jira's workflow surface into three tiers and admits two.
-- This migration is tier 1: "Declarative predicates evaluated at transition
-- time, with no side effects. A condition determines whether a transition is
-- offered; a validator determines whether it succeeds."
--
-- Numbering: 040-045 are held by tracks in flight (Codex tags took 040,
-- dashboards took 042). 046/047 are this phase's reservation. Nothing here
-- renumbers anything — migration numbering is immutable once shipped, and none
-- of 040-045 has shipped on main at the time of writing.
--
--
-- One predicate vocabulary, two classes
-- -------------------------------------
-- The ADR names the two classes by what they DO, not by what they can ask:
-- a condition hides the transition, a validator refuses the commit. The
-- question each asks is the same question, so `guard_class` is a column on one
-- table rather than the discriminator between two tables. `field_required` as a
-- condition hides "Close" until a due date exists; the same predicate as a
-- validator offers "Close" and refuses it with a named reason. Both readings
-- are useful and neither needs its own vocabulary.
--
-- The vocabulary is closed and lives in exactly one place in Go —
-- internal/core/workflow/guard.go — which this table's CHECK constraints
-- mirror. Two copies of a closed vocabulary drift; the mitigation is that the
-- Go side is the definition, the SQL side refuses anything the Go side would
-- not have written, and TestGuardKinds_MatchDatabaseCheckConstraint fails if
-- they ever disagree.
--
--
-- Why typed columns and not a `config` jsonb
-- -----------------------------------------
-- Migration 038 stored the saved-view filter as one jsonb document and wrote
-- down the test for that choice: a document is right when "the invariant that
-- actually matters is not expressible as a column constraint either way", and
-- wrong when it "would throw away a real constraint" that IS expressible —
-- 038's own contrast with migration 035.
--
-- Guards fall on the other side of that test, so the same rule produces the
-- opposite answer:
--
--   * `team_id` is a real foreign key to `teams`. Buried in jsonb it has no
--     referential integrity at all, and a deleted team leaves a guard pointing
--     at nothing with no database-level signal that it happened.
--   * "which parameter belongs to which kind" IS expressible, as the shape
--     CHECK below. In a document it is a promise the validator makes.
--
-- 038's filter had neither property: its values are free-form text and id
-- lists with no single FK target, and its optionality is genuinely multi-valued
-- ("three statuses, no priorities, one space"). A guard is one predicate with
-- one parameter. Columns are correct here and a document would be the
-- cargo-cult — the same sentence 038 wrote about `jsonb` vs `json`, pointing
-- the other way.
--
--
-- ON DELETE SET NULL, and why CASCADE would be a security defect
-- -------------------------------------------------------------
-- `team_id` references teams ON DELETE SET NULL. The tempting choice is
-- CASCADE — the guard names a team, the team is gone, drop the guard — and it
-- is exactly wrong. Deleting the guard row makes the transition UNGUARDED:
-- an administrative action on an unrelated object silently REMOVES a
-- restriction, and nothing anywhere reports it. That is a silent permit, the
-- one failure mode this whole tier exists to prevent.
--
-- So the reference SET NULLs and the degraded state (kind = 'actor_in_team'
-- with team_id IS NULL) is deliberately REPRESENTABLE — the same reasoning
-- 038 applied to `visibility_team_id`, and the reason the shape CHECK below
-- does NOT require team_id to be non-null. A guard in that state is
-- unsatisfiable, so it fails CLOSED: the transition is never offered and never
-- commits, and the admin surface renders the guard as needing re-scoping.
-- Fail closed, then prompt.
--
-- The narrower `capability` and `field_key` parameters are plain text over
-- vocabularies this build defines, so they have no such degraded state and are
-- required by shape.
--
--
-- Deletion of the transition itself DOES cascade. A guard has no meaning
-- without the edge it guards, and removing the edge removes the permission it
-- carried rather than granting one.
--
--
-- No uniqueness constraint, deliberately
-- --------------------------------------
-- Two identical guards on one transition are harmless: every predicate here is
-- pure and idempotent, so evaluating one twice answers what evaluating it once
-- answers. A unique key over (transition, class, kind, parameters) would need
-- NULLS NOT DISTINCT to work at all, and would buy nothing but a constraint
-- violation on a double-click. The admin surface de-duplicates for tidiness;
-- the database does not need to.
CREATE TABLE workflow_transition_guards (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    transition_id UUID        NOT NULL REFERENCES workflow_transitions (id) ON DELETE CASCADE,

    -- Which half of ADR-0011 tier 1 this row is.
    guard_class   TEXT        NOT NULL,
    -- The predicate. Closed vocabulary; see internal/core/workflow/guard.go.
    kind          TEXT        NOT NULL,

    -- Evaluation order, so a transition refused by two validators names the
    -- same one every time. Ties break on id.
    position      INT         NOT NULL DEFAULT 0,

    -- Exactly one of these is used, chosen by `kind` and enforced by the shape
    -- CHECK. All are NULL for the parameterless kinds.
    capability    TEXT,
    team_id       UUID        REFERENCES teams (id) ON DELETE SET NULL,
    field_key     TEXT,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT workflow_transition_guards_class_valid
        CHECK (guard_class IN ('condition', 'validator')),

    CONSTRAINT workflow_transition_guards_kind_valid
        CHECK (kind IN (
            'actor_is_assignee',
            'actor_in_team',
            'actor_has_capability',
            'field_required'
        )),

    -- `field_required` names a field that exists, and is nullable-or-emptiable,
    -- on BOTH tickets and project_items. `priority` is deliberately absent: it
    -- is NOT NULL with a default on both tables, so requiring it would assert
    -- something that cannot be false — a check that reads as coverage and is
    -- not.
    CONSTRAINT workflow_transition_guards_field_key_valid
        CHECK (field_key IS NULL OR field_key IN (
            'assignee_id',
            'due_at',
            'description',
            'labels'
        )),

    -- One parameter shape per kind. `actor_in_team` permits a NULL team_id
    -- because ON DELETE SET NULL must be able to produce that state; see the
    -- header.
    CONSTRAINT workflow_transition_guards_shape_valid
        CHECK (
               (kind = 'actor_is_assignee'
                    AND capability IS NULL AND team_id IS NULL AND field_key IS NULL)
            OR (kind = 'actor_in_team'
                    AND capability IS NULL AND field_key IS NULL)
            OR (kind = 'actor_has_capability'
                    AND capability IS NOT NULL AND team_id IS NULL AND field_key IS NULL)
            OR (kind = 'field_required'
                    AND field_key IS NOT NULL AND capability IS NULL AND team_id IS NULL)
        )
);

-- The evaluation read: every guard on one transition, in order. This is on the
-- hot path of both the availability query and the commit path, so it is the
-- index that matters.
CREATE INDEX workflow_transition_guards_transition_idx
    ON workflow_transition_guards (transition_id, guard_class, position);

-- "Which guards would this team's deletion degrade" — the admin surface's
-- re-scope prompt, and the only query that starts from a team.
CREATE INDEX workflow_transition_guards_team_idx
    ON workflow_transition_guards (team_id)
    WHERE team_id IS NOT NULL;

-- set_updated_at() is the shared trigger function from migration 009; the
-- trg_<table>_updated_at naming follows the convention established there.
CREATE TRIGGER trg_workflow_transition_guards_updated_at
    BEFORE UPDATE ON workflow_transition_guards
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- Workflow tier 3 (ADR-0011): the fixed post-function set.
--
-- The ADR names the permitted actions and closes the set in the same breath:
-- "Permitted actions: set a field, assign to a user or team, add a comment,
-- transition a linked item. That set is defined in code. It is extended only
-- by a deliberate release decision, never by configuration, and never by
-- anything a user supplies."
--
-- So `kind` below is a CHECK over four values and there is no mechanism —
-- none, at any layer — for a deployment to add a fifth. Adding one is a code
-- change, a migration, and a release.
--
-- Two of the four ship, and two are deliberately absent from the CHECK. All
-- four dispositions are recorded here because "the set is fixed in code" only
-- means something if the reason each member is in or out is written down.
--
--   set a field           SHIPS, narrowed. See the field vocabulary below.
--   assign to a user      SHIPS.
--   ...or team            NOT REPRESENTABLE. Both entity tables declare
--                         `assignee_id UUID REFERENCES users (id)` (migration
--                         014, lines 14 and 55). A team cannot be an assignee
--                         in this product, so the column that would hold one
--                         is not created — a column that can never be read is
--                         worse than an absent one, because it reads as a
--                         feature.
--   add a comment         NOT BUILT HERE. `comments` exists (migration 006)
--                         but the ticket comment surface is owned by a track
--                         in flight; a second writer would collide with it.
--   transition a linked   NOT MODELLED. There is no link table in this schema.
--   item                  The only structural link is project_items.parent_id
--                         (Vector-only, self-referencing), which is a
--                         hierarchy rather than a link, and traversing it
--                         would need a cycle guard the ADR does not describe.
--
-- The last three are ADR-sanctioned and are deliberate omissions, not refusals.
-- The CHECK widens by migration when each gains a target, which is exactly the
-- "extended only by a deliberate release decision" the ADR requires.
CREATE TABLE workflow_transition_post_functions (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    transition_id UUID        NOT NULL REFERENCES workflow_transitions (id) ON DELETE CASCADE,

    kind          TEXT        NOT NULL,
    position      INT         NOT NULL DEFAULT 0,

    -- `assign_to`: the user who becomes the assignee, or NULL, which means
    -- unassign.
    --
    -- ON DELETE SET NULL collapses "assign to this deleted user" into
    -- "unassign", and that is the one place in this migration where a deleted
    -- referent changes behaviour rather than stopping it. It is safe in the
    -- direction that matters: the outcome removes an assignment, never grants
    -- one, and an assignment is not an access right (ADR-0007 grants access;
    -- assignment records responsibility). The alternative, CASCADE, would
    -- delete the post-function row and silently stop the workflow doing
    -- something an administrator configured.
    assignee_user_id UUID     REFERENCES users (id) ON DELETE SET NULL,

    -- `set_field`: which field, and the literal value written into it.
    field_key     TEXT,
    field_value   TEXT,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT workflow_transition_post_functions_kind_valid
        CHECK (kind IN ('assign_to', 'set_field')),

    -- Narrower than the guard vocabulary, and narrower on purpose.
    --
    -- `description` is readable by a guard and NOT writable by a post-function:
    -- a post-function that sets it would overwrite author-written prose on
    -- every transition, which is silent data loss dressed as automation.
    -- `assignee_id` is absent because `assign_to` already owns it, and two ways
    -- to write one column is how they disagree.
    CONSTRAINT workflow_transition_post_functions_field_key_valid
        CHECK (field_key IS NULL OR field_key IN ('due_at', 'labels')),

    CONSTRAINT workflow_transition_post_functions_shape_valid
        CHECK (
               (kind = 'assign_to'
                    AND field_key IS NULL AND field_value IS NULL)
            OR (kind = 'set_field'
                    AND field_key IS NOT NULL
                    AND assignee_user_id IS NULL)
        )
);

CREATE INDEX workflow_transition_post_functions_transition_idx
    ON workflow_transition_post_functions (transition_id, position);

CREATE TRIGGER trg_workflow_transition_post_functions_updated_at
    BEFORE UPDATE ON workflow_transition_post_functions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- Nothing is seeded. The default workflows from migrations 016/019 and their
-- reverse edges from 029 gain no guards and no post-functions, so a space that
-- nobody has configured behaves byte-identically to the build before this
-- migration. That is asserted directly by
-- TestUntouchedWorkflow_BehavesIdenticallyToPreGuardBuild.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_workflow_transition_post_functions_updated_at ON workflow_transition_post_functions;
DROP TABLE IF EXISTS workflow_transition_post_functions;

DROP TRIGGER IF EXISTS trg_workflow_transition_guards_updated_at ON workflow_transition_guards;
DROP TABLE IF EXISTS workflow_transition_guards;

-- +goose StatementEnd
