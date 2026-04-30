# Agent P5 — Items Table Split

## Context

You are working on **Azimuthal**, a fully open-source replacement for Jira + Jira Service Management + Confluence.

This is **Phase 5 of 6** — the highest-risk phase in the series. **Phases 1, 2, 3, and 4 must be merged** before you start. One more phase follows — do not anticipate it.

**Read these first, in order:**
1. `CLAUDE.md`
2. `docs/project-state.md`
3. `docs/known-issues.md`
4. `migrations/` — all existing migrations to understand the current `items`, `comments`, `item_relations` schema
5. `internal/core/tickets/`, `internal/core/projects/`, `internal/core/wiki/` to understand current adapter shape
6. `service-desk-vs-jsm.md`, `projects-vs-jira.md` for context on why this split is happening

**Your branch:** `feat/p5-items-split` from `main`.

---

## Locked decisions (do not deviate)

- **Full split:** `items` becomes two tables, `tickets` and `project_items`. Each has only its own fields.
- **Comments and item_relations become polymorphic:** `entity_type TEXT CHECK IN (...)` + `entity_id UUID`. This is the only way cross-entity linking works without two parallel relation tables.
- **`items` is renamed to `items_archive`, not dropped.** Kept for one release as belt-and-suspenders. A future migration in v0.3 drops it.

---

## Why this is the highest-risk phase

- Schema migration on production data with no rollback once merged
- Touches every ticket, every project item, every comment, every relation
- Polymorphic dispatch in handlers requires careful test coverage to avoid silent miscategorization
- The route shape changes for comments — existing clients break unless deprecation header is honored

**Read this carefully:** P5.1 below requires you to produce a written migration plan in the PR body **before** any code is written. This is the owner's checkpoint to review before risk is incurred. Do not skip it.

---

## Hard rules

- **CLAUDE.md compliance is non-negotiable.**
- **All three commands must exit 0:** `make test-live`, `make e2e-test`, `make verify-api`. If any fails, **DRAFT**.
- **No assertion weakening.** Stop and document.
- **No drive-by refactors.**
- **Migrations:** this phase adds `014_split_items_phase1.sql` and `015_polymorphic_comments_relations.sql`. No others.
- **No new dependencies without a license check.**
- **PR body includes a "Test integrity statement."**
- **Windows / PowerShell environment.**

---

## Tasks

### P5.1 — Schema migration plan (READ-ONLY: do this first)

**Before any code change**, produce a written migration plan in the PR body:

- Source rows per `kind` count, taken from a fresh DB after running existing migrations
- Destination rows per table
- A backfill SQL script that copies rows
- A verification query that asserts row counts match before / after
- Order of operations: which tables get created first; when adapters cut over; when the old `items` is dropped (answer: **not in this phase** — renamed to `items_archive` and kept for one release)

**Do not start implementation until this plan is in the PR body and the PR exists in DRAFT state.** Owner reviews the plan before risk is incurred.

### P5.2 — Migrations

**Migration `014_split_items_phase1.sql`:**

```sql
-- Tickets table — service-desk specific
CREATE TABLE tickets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id UUID NOT NULL REFERENCES spaces(id),
    number INT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    priority TEXT NOT NULL DEFAULT 'medium' CHECK (priority IN ('urgent','high','medium','low')),
    reporter_id UUID NOT NULL REFERENCES users(id),
    assignee_id UUID REFERENCES users(id),
    labels TEXT[] NOT NULL DEFAULT '{}',
    due_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    rank TEXT NOT NULL DEFAULT '0|aaaaaa:',
    workflow_id UUID,             -- FK added in P6
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (space_id, number)
);
-- Add search vector column, indexes, sequence-per-space for `number` (pattern matches existing items)

-- Project items table
CREATE TABLE project_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id UUID NOT NULL REFERENCES spaces(id),
    parent_id UUID REFERENCES project_items(id),
    number INT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('task','story','epic','bug')),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    priority TEXT NOT NULL DEFAULT 'medium' CHECK (priority IN ('urgent','high','medium','low')),
    reporter_id UUID NOT NULL REFERENCES users(id),
    assignee_id UUID REFERENCES users(id),
    sprint_id UUID REFERENCES sprints(id) ON DELETE SET NULL,
    labels TEXT[] NOT NULL DEFAULT '{}',
    due_at TIMESTAMPTZ,
    start_at TIMESTAMPTZ,         -- new — for roadmap
    resolved_at TIMESTAMPTZ,
    rank TEXT NOT NULL DEFAULT '0|aaaaaa:',
    workflow_id UUID,             -- FK added in P6
    story_points NUMERIC,         -- new
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (space_id, number)
);

-- Backfill from items
INSERT INTO tickets (id, space_id, number, title, description, status, priority,
                     reporter_id, assignee_id, labels, due_at, resolved_at, rank,
                     created_at, updated_at, deleted_at)
SELECT id, space_id, number, title, description, status, priority,
       reporter_id, assignee_id, labels, due_at, resolved_at, rank,
       created_at, updated_at, deleted_at
FROM items WHERE kind = 'ticket';

INSERT INTO project_items (id, space_id, parent_id, number, kind, title, description,
                           status, priority, reporter_id, assignee_id, sprint_id,
                           labels, due_at, resolved_at, rank, created_at, updated_at, deleted_at)
SELECT id, space_id, parent_id, number, kind, title, description,
       status, priority, reporter_id, assignee_id, sprint_id,
       labels, due_at, resolved_at, rank, created_at, updated_at, deleted_at
FROM items WHERE kind IN ('task','story','epic','bug');

-- Rename old table for one-release safety
ALTER TABLE items RENAME TO items_archive;
```

**Migration `015_polymorphic_comments_relations.sql`:**

```sql
-- Comments: replace dual-FK + CHECK pattern with polymorphic
ALTER TABLE comments ADD COLUMN entity_type TEXT;
ALTER TABLE comments ADD COLUMN entity_id UUID;

UPDATE comments SET entity_type = 'ticket', entity_id = item_id
  WHERE item_id IS NOT NULL AND item_id IN (SELECT id FROM tickets);
UPDATE comments SET entity_type = 'project_item', entity_id = item_id
  WHERE item_id IS NOT NULL AND item_id IN (SELECT id FROM project_items);
UPDATE comments SET entity_type = 'page', entity_id = page_id
  WHERE page_id IS NOT NULL;

ALTER TABLE comments ALTER COLUMN entity_type SET NOT NULL;
ALTER TABLE comments ALTER COLUMN entity_id SET NOT NULL;
ALTER TABLE comments ADD CONSTRAINT comments_entity_type_check
  CHECK (entity_type IN ('ticket','project_item','page'));
ALTER TABLE comments DROP CONSTRAINT comments_polymorphism;  -- the old XOR
-- Keep item_id, page_id columns for one release as belt-and-suspenders; drop later.

CREATE INDEX idx_comments_entity ON comments (entity_type, entity_id) WHERE deleted_at IS NULL;

-- Item relations: same treatment, two endpoints (from + to)
ALTER TABLE item_relations RENAME TO entity_relations;
ALTER TABLE entity_relations ADD COLUMN from_type TEXT;
ALTER TABLE entity_relations ADD COLUMN to_type TEXT;

-- Backfill from_type and to_type for all 9 combinations of (ticket, project_item, page)
-- (write each UPDATE individually for clarity)

ALTER TABLE entity_relations ALTER COLUMN from_type SET NOT NULL;
ALTER TABLE entity_relations ALTER COLUMN to_type SET NOT NULL;
ALTER TABLE entity_relations ADD CONSTRAINT entity_relations_from_type_check
  CHECK (from_type IN ('ticket','project_item','page'));
ALTER TABLE entity_relations ADD CONSTRAINT entity_relations_to_type_check
  CHECK (to_type IN ('ticket','project_item','page'));

CREATE INDEX idx_entity_relations_from ON entity_relations (from_type, from_id);
CREATE INDEX idx_entity_relations_to ON entity_relations (to_type, to_id);
```

The `wiki_link` relation kind no longer needs a separate carve-out — pages are full citizens of the polymorphic relation graph.

### P5.3 — Adapter and service updates

- `internal/db/adapters/projects.go` reads/writes from `project_items`. Old code path deleted.
- New `internal/db/adapters/tickets.go` (per `docs/known-issues.md`, the tickets module previously had no adapter — wiki module style; here we add it cleanly)
- Service code in `internal/core/projects/` and `internal/core/tickets/` continues to take the same domain types — adapters do the table mapping. Domain code does not need to know about the split.
- `comments.entity_type` / `entity_id` propagates through the comment service. Polymorphic dispatch happens in the handler.

**Route reshape:**
- Old: `/orgs/{orgID}/spaces/{spaceID}/items/{itemID}/comments`
- New: `/orgs/{orgID}/spaces/{spaceID}/{entityType}/{entityID}/comments` where `entityType` is one of `tickets`, `project-items`, `wiki`
- Old route returns deprecation header for one release pointing to new path

### P5.4 — Frontend updates

- `useProjectItems` hits the new project-items routes. Tests verify it sees only project items, not tickets.
- `useTickets` similarly.
- Comments hooks dispatch on entity type. Detail page passes `entityType`.
- **Cross-entity linking UI:** in the relations dropdown on a project item or ticket, the search dropdown can return all three entity types. Result rows show the entity type badge.

### P5.5 — Wiki page comments end-to-end

The polymorphic comments table now supports pages cleanly. Add the wiki page comments UI:

- Comments section at the bottom of a wiki page
- Same component as ticket / project item comments (reused)
- Test: post a comment on a page, see it. Post a reply (`parent_id`), see threading at one level (deep threading is a later concern).

### P5.6 — Cross-entity linking

- Relations form on any entity (ticket, project item, page) can target any other entity
- The `wiki_link` kind is now redundant — `relates_to` between an item and a page covers it. Keep `wiki_link` as a synonym in the kind enum for one release; new relations should use `relates_to` between heterogeneous types.
- A relation between a ticket in space A and a project item in space B works. Tested.

### P5.7 — `items_archive` retention

- Old `items` table renamed `items_archive`, not dropped
- Future migration in v0.3 drops it after a release-and-rollback window
- Add `azimuthal admin verify-split` command that compares row counts and key columns between `items_archive` and the union of `tickets` + `project_items`. Logs any divergence.

---

## Tests required

- Integration: backfill verification — every row from `items_archive` lands in exactly one of `tickets` / `project_items`. Row count assertion.
- Integration: post a comment on a ticket, on a project item, on a page; assert each has correct `entity_type` and `entity_id`.
- Integration: create a relation between a ticket and a project item across spaces. Assert it appears on both endpoints.
- Integration: deprecation header on the old `/items/{itemID}/comments` route.
- Playwright: full ticket, project, wiki flows still work after the split. The same E2E suite that passed in P4 must pass here.

---

## Definition of Done — every item must be verifiably true for ready-to-merge

1. Migration plan in PR body, owner-reviewable
2. Both new tables populated correctly, old `items` renamed to `items_archive` (not dropped)
3. Polymorphic `comments.entity_type` / `entity_id` populated for all existing comments
4. `entity_relations` has `from_type` / `to_type` populated for all existing relations
5. Adapters for tickets and project_items both in place
6. Frontend hooks use the new routes
7. `azimuthal admin verify-split` reports zero divergence
8. All E2E tests from P4 still pass (no regression)
9. New polymorphic comment routes work for all three entity types
10. Cross-space ticket↔project_item relation works end-to-end
11. `make test-live` exits 0
12. `make e2e-test` exits 0
13. `make verify-api` exits 0
14. New routes have swag annotations; `make docs` regenerates `docs/api/openapi.yaml`
15. PR body contains "Test integrity statement"

If any is false, PR stays **DRAFT**.

---

## Out of scope — do NOT do these

- Drop `items_archive` (a future migration after v0.2 ships and stabilizes)
- Story points UI (column added but no UI yet)
- `start_at` UI on items (column added; consumed in roadmap later)
- Custom fields infrastructure (now possible because each table has its own surface, but not built here)
- Workflow engine (next phase)
- Labels join table redesign
- Any roadmap edit
- Any refactor not directly serving the tasks above

---

## PR body required structure

1. **Migration plan** (Step P5.1) — full, before any other content
2. **Summary**
3. **PR state** — "Ready-to-merge" or "DRAFT — reason: <which>"
4. **Phase task checklist** — ✅ each P5.1 through P5.7 with SHA
5. **Test results** — full output of all three commands
6. **Test integrity statement**
7. **Backfill verification output** — row counts before/after; `verify-split` output
8. **Out-of-scope findings**
9. **Coverage delta**
10. **License notes** — none expected
