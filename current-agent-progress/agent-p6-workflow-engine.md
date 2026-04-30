# Agent P6 — Workflow Engine

## Context

You are working on **Azimuthal**, a fully open-source replacement for Jira + Jira Service Management + Confluence.

This is **Phase 6 of 6** — the final phase before the path-to-functional series is complete. **Phases 1–5 must be merged** before you start.

**Read these first, in order:**
1. `CLAUDE.md`
2. `docs/project-state.md`
3. `docs/known-issues.md`
4. `internal/core/tickets/status.go` — the hardcoded ticket state machine that goes away
5. `internal/core/projects/item.go` around line 131-138 — the no-op status update that goes away
6. `migrations/014_split_items_phase1.sql` and `015_polymorphic_comments_relations.sql` — the post-split schema this phase builds on
7. `projects-vs-jira.md` §5 (workflow engine decision framing) for context

**Your branch:** `feat/p6-workflow-engine` from `main`.

---

## Locked decisions (do not deviate)

- **Engine + minimal admin UI.** Admin UI permits: rename states, reorder states, add states, remove states, add transitions, remove transitions. **No** conditions, validators, or post-functions in this phase.
- **Both tickets and project_items adopt the engine from day one.** Single engine, two consumers.
- **Default workflow per space, seeded on space create.** Existing entities backfilled in the migration.
- **Existing transitions become workflow-driven.** The hardcoded ticket state machine and the projects no-op both go away. The engine is the only code path that mutates status.
- **`status TEXT` column on both tables remains** as a denormalization of the current state's name — kept in sync with `workflow_state_id` on every transition. Existing queries (`WHERE status = 'open'`) keep working; canonical truth is the FK.

---

## Hard rules

- **CLAUDE.md compliance is non-negotiable.**
- **All three commands must exit 0:** `make test-live`, `make e2e-test`, `make verify-api`. If any fails, **DRAFT**.
- **No assertion weakening.** Stop and document.
- **No drive-by refactors.**
- **Migrations:** this phase adds `016_workflow_engine.sql`. No others.
- **No new dependencies without a license check.**
- **PR body includes a "Test integrity statement."**
- **Windows / PowerShell environment.**

---

## Tasks

### P6.1 — Schema

**Migration `016_workflow_engine.sql`:**

```sql
CREATE TABLE workflows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    applies_to TEXT NOT NULL CHECK (applies_to IN ('tickets','project_items','both')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE TABLE workflow_states (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    category TEXT NOT NULL CHECK (category IN ('todo','in_progress','done')),
    color TEXT NOT NULL DEFAULT '#6b7280',
    position INT NOT NULL,
    is_initial BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workflow_id, name),
    UNIQUE (workflow_id, position)
);

-- Exactly one initial state per workflow
CREATE UNIQUE INDEX idx_workflow_initial ON workflow_states (workflow_id) WHERE is_initial;

CREATE TABLE workflow_transitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    from_state_id UUID NOT NULL REFERENCES workflow_states(id) ON DELETE CASCADE,
    to_state_id UUID NOT NULL REFERENCES workflow_states(id) ON DELETE CASCADE,
    name TEXT NOT NULL,        -- e.g. "Start Progress", "Resolve"
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workflow_id, from_state_id, to_state_id)
);

-- Items reference a workflow state
ALTER TABLE tickets ADD COLUMN workflow_state_id UUID REFERENCES workflow_states(id);
ALTER TABLE project_items ADD COLUMN workflow_state_id UUID REFERENCES workflow_states(id);
-- workflow_id columns were added in P5; populate them in this migration
```

The migration also:

- **Seeds two default workflows per org** (existing orgs get them via the migration; new orgs get them via the org create service):
  - **"Default Service Desk"** — `applies_to = 'tickets'`. States: `open` (initial, todo) → `in_progress` (in_progress) → `resolved` (done) → `closed` (done). Transitions match today's hardcoded set.
  - **"Default Project"** — `applies_to = 'project_items'`. States: `backlog` (todo, initial) → `todo` (todo) → `in_progress` (in_progress) → `in_review` (in_progress) → `done` (done). Sensible default transitions.
- **Backfills `workflow_id` and `workflow_state_id`** for every existing ticket and project_item, mapping their current `status` text to the matching state's name in the default workflow.
- **Assigns the appropriate default workflow** to existing service-desk and project spaces.

### P6.2 — Engine code

`internal/core/workflow/`:

- Types: `Workflow`, `State`, `Transition`
- Interface: `Engine` with methods:
  - `AvailableTransitions(workflowID, fromStateID) -> []Transition`
  - `Validate(workflowID, fromStateID, toStateID) -> error`
  - `Apply(entityType, entityID, transitionID, actorID) -> error`
- The engine writes to the entity table, updates `workflow_state_id` and `status`, emits an audit event (P1's audit producer carries this for free)
- **The engine is the only code path that updates status.** All previous status-change paths (handlers, services) call into the engine.

### P6.3 — Adapt tickets and project_items

- `internal/core/tickets/status.go`: hardcoded transitions deleted. Status moves through the engine.
- `internal/core/projects/item.go`: `UpdateItemStatus` deleted. Status moves through the engine.
- Both modules' handlers no longer have `POST /status` endpoints — replaced with `POST /transitions/{transitionID}` which references a workflow transition by ID. Returns the new state.
- **Existing routes return a deprecation header** for one release pointing to the new endpoint. Frontend updates accordingly.
  - `POST /items/{itemID}/status` (project items)
  - `POST /tickets/{ticketID}/status` (tickets)

### P6.4 — Sprint board uses workflow states

- Sprint board columns are derived from the workflow assigned to the project space (default: backlog / todo / in_progress / in_review / done)
- Drag-drop targets a transition, not a status string
- Items with no `workflow_state_id` (defensive — shouldn't happen post-backfill) render in a "Needs triage" column at the leftmost position
- The `STATUS_LABEL` map from P2.7 is removed — workflow state names are used directly

### P6.5 — Minimal admin UI

Admin route: `/admin/workflows` (org admin only — `space_members.role` check, with fallback to `memberships.role`).

- **List workflows** with name, applies_to, default flag, count of attached spaces
- **Edit workflow:**
  - Rename
  - Add a state (name, category, color, position)
  - Remove a state (only allowed if no entity references it; otherwise show count of references and refuse)
  - Reorder states (drag to set `position`)
  - Add a transition (from-state → to-state, name)
  - Remove a transition

- New spaces of type `servicedesk` or `project` get their default workflow assigned on create.

The admin UI is minimal: a table of states, a table of transitions, in/out edit. **No graph view.** That's a wishlist item for later.

### P6.6 — Authorization

- Workflow editing requires org-admin or space-admin
- The `space_members.role` system has been documented but not enforced — this phase is the first place where role enforcement actually gates a UI surface
- Status transitions on entities continue to be available to anyone with edit access on the space

---

## Tests required

- Integration: default workflows seeded on org create
- Integration: cannot remove a state with referenced entities (returns count, refuses)
- Integration: a transition not defined in the workflow is rejected
- Integration: existing tickets and project items have `workflow_state_id` populated correctly post-backfill
- Integration: status transition emits an audit event with `from_state` and `to_state` in `details`
- Playwright: edit a workflow as an admin (rename a state, add a state, add a transition, remove an unused transition)
- Playwright: as a non-admin, the workflow admin UI is gated and shows a permission error
- Playwright: drag a card on the sprint board, status persists, audit log records it
- Playwright: existing E2E tests still pass — `POST /status` deprecation works through the engine

---

## Definition of Done — every item must be verifiably true for ready-to-merge

1. Migration `016_workflow_engine.sql` applied; defaults seeded; existing entities backfilled
2. Engine is the only code path mutating status on tickets and project_items (verified by code inspection — no other path remains)
3. Both old `POST /status` routes work via deprecation but call into the engine
4. New `POST /transitions/{transitionID}` route works for both entity types
5. Sprint board columns are workflow-state-driven; `STATUS_LABEL` map removed
6. Admin UI: rename, add, reorder, remove states; add, remove transitions — all working
7. `space_members.role` enforcement on the admin UI works; non-admin sees permission error
8. New spaces get appropriate default workflow on create
9. `make test-live` exits 0
10. `make e2e-test` exits 0
11. `make verify-api` exits 0
12. New routes have swag annotations; `make docs` regenerates `docs/api/openapi.yaml`
13. PR body contains "Test integrity statement"

If any is false, PR stays **DRAFT**.

---

## Out of scope — do NOT do these

- Conditions on transitions
- Validators on transitions
- Post-functions on transitions
- Workflow graph view in admin UI
- Per-issue-type workflows (one workflow per space type for now)
- Workflow versioning / history
- Migration tooling for moving existing entities between workflows mid-project
- Email notification of transitions (P1's audit captures the events; email fan-out is later)
- Any roadmap edit
- Any refactor not directly serving the tasks above

---

## PR body required structure

1. **Summary**
2. **PR state** — "Ready-to-merge" or "DRAFT — reason: <which>"
3. **Phase task checklist** — ✅ each P6.1 through P6.6 with SHA
4. **Backfill verification** — counts of tickets and project_items mapped to each default-workflow state
5. **Test results** — full output of all three commands
6. **Test integrity statement**
7. **Out-of-scope findings**
8. **Coverage delta**
9. **License notes** — none expected
