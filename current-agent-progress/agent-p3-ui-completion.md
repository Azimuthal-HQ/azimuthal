# Agent P3 — UI Completion Against Existing Backend

## Context

You are working on **Azimuthal**, a fully open-source replacement for Jira + Jira Service Management + Confluence.

This is **Phase 3 of 6** in a sequential series. **Phases 1 and 2 must be merged** before you start. Three more phases follow — do not anticipate them.

**Read these first, in order:**
1. `CLAUDE.md`
2. `docs/project-state.md`
3. `docs/known-issues.md`
4. `internal/core/api/router.go` — to confirm which routes already exist (this phase consumes existing routes; it does **not** add new backend functionality except where specifically noted)
5. The three stock-take reports in `C:\Users\Kitsune\OneDrive\Documents\Claude\azimuthal-reviews\2026-04-19\`

**Your branch:** `feat/p3-ui-completion` from `main`.

---

## Why this phase

Six wiki backend routes are unreachable from any UI. Sprint admin endpoints have no UI. Roadmap endpoints have no UI. Item relations have no UI on tickets. None of this needs new backend work — it consumes routes that already exist. This phase produces the most visible "the app actually works now" change of the entire series.

---

## Hard rules

- **CLAUDE.md compliance is non-negotiable.**
- **All three commands must exit 0:** `make test-live`, `make e2e-test`, `make verify-api`. If any fails, **DRAFT**.
- **No assertion weakening to make CI green.** Stop and document instead.
- **No drive-by refactors.** Every commit references P3 and the sub-task ID.
- **Migrations are immutable.** This phase adds **no migrations** (no new backend schema).
- **No new dependencies without a license check** (Apache-2.0 / MIT / BSD only).
- **PR body includes a "Test integrity statement."**
- **Windows / PowerShell environment.**

---

## Tasks

### P3.1 — Wiki tree, search, revisions

Per `wiki-vs-confluence.md` §3.4 and §12 rows 4, 5, 6.

**Wiki tree (sidebar):**
- New hook `useWikiTree(spaceId)` calling `GET /spaces/{spaceId}/wiki/tree`
- Sidebar renders the tree with collapse / expand
- Click a node navigates to that page
- Drag-to-reorder calls `POST /{pageID}/move` with new `parent_id` and `position`
- Replaces the current flat list at `WikiPage.tsx:96-120`
- Each tree node shows title only; small badge for child count if > 0

**Wiki search:**
- Search box in the wiki page header, debounced 300ms
- Calls `GET /spaces/{spaceId}/wiki/search?q=`
- Renders dropdown of results below the box
- Results show title (snippet/highlight is deferred to a later phase)
- Click a result navigates to the page

**Wiki revisions:**
- "History" button on a wiki page header opens a side panel listing revisions with timestamp and author
- Click a revision shows the page at that version (`GET /{pageID}/revisions/{version}`)
- "Compare" mode: pick two revisions, hit Compare, render the existing `GET /{pageID}/diff?from=&to=` (returns ANSI-style colored text — render as styled HTML in panel)
- **No restore button** in this phase — restore is a P4 follow-up

### P3.2 — Sprint admin UI

Per `projects-vs-jira.md` §3 rows "Sprint create / state machine / list" and §13 row 5.

- New page: `/spaces/{spaceId}/projects/sprints` listing sprints with state, dates, item count
- "New Sprint" dialog: name, goal, optional `starts_at` / `ends_at`. POSTs `/sprints`
- Each sprint row: "Start" button (active sprint constraint enforced backend-side; show error toast on conflict), "Complete" button when active
- Active sprint indicator on the sprint board page

### P3.3 — Roadmap UI

Per `projects-vs-jira.md` §3 row "Roadmap" and §13 row 6.

- New page: `/spaces/{spaceId}/projects/roadmap`
- Default view: list grouped by month, using `due_at` (Option C from projects report §11)
- Two filter chips: "All items" / "Sprint timeline". The latter calls `GET /roadmap/sprints` and renders sprint bars
- "Overdue" link in sidebar shows items from `GET /roadmap/overdue`
- **Fix the sidebar "Roadmap" link** — currently points to `/backlog`; should point to the new roadmap page

This is **not** a Gantt view — that's a separate later decision. This is the smallest UI that consumes the existing roadmap endpoints.

### P3.4 — Item relations UI

Per `projects-vs-jira.md` §13 row 14.

**Project items:**
- On the project item detail page, add a "Relations" section listing existing relations (`GET /items/{itemID}/relations`)
- Each row shows kind (`blocks`, `is_blocked_by`, `duplicates`, `relates_to`, `wiki_link`), target title, target status, delete button (`DELETE /relations/{relationID}`)
- "Add relation" form: kind dropdown, search box for target item (uses `GET /items/search?q=`)

**Tickets:**
- **Same UI on tickets.** The ticket handler does not currently expose relations endpoints — add them now using the same SQL queries that already exist (`internal/db/queries/items.sql:53-66`)
- Ticket relations and project relations share the underlying `item_relations` table; preserve that
- This is the **only** place P3 adds new backend routes (handler-only; no schema change)

The `wiki_link` relation kind is exposed in the dropdown but linking to a wiki page works only if the target search returns a page — acceptable interim behavior; first-class wiki↔item linking comes after P5.

### P3.5 — Sprint board status persistence

Already covered by P2.6 (which depends on this phase's ancestors having merged). **P3 verifies** that drag-drop on the sprint board persists. If it regressed during P3 work, fix.

### P3.6 — Backlog drag-to-reorder

Per `projects-vs-jira.md` §13 row 13: `Rank` column and `ReorderItem` service exist; no HTTP route is mounted.

- **Add new route** `POST /items/{itemID}/rank` with body `{ before_id?, after_id? }` (one or the other). Uses existing `ReorderItem` service.
- Frontend backlog page: drag-to-reorder, optimistic update, on error revert.

### P3.7 — Profile page

Per the testing-audit fix PR: this should already work. P3 verifies it stayed fixed; if it regressed during P1 or P2, fix it again. No new code expected.

---

## Tests required

- Playwright: wiki tree renders, click navigates, drag reorders
- Playwright: wiki search returns results, click navigates
- Playwright: wiki revisions panel shows history; compare shows diff
- Playwright: create a sprint, start it, see active indicator, complete it
- Playwright: create an item with a due date, see it on the roadmap
- Playwright: add a `blocks` relation on a project item, see it appear, delete it, see it gone
- Playwright: same relation flow on a ticket
- Integration: `POST /items/{itemID}/rank` reorders correctly
- Integration: new ticket relations endpoints work end-to-end

---

## Definition of Done — every item must be verifiably true for ready-to-merge

1. P3.1 — wiki tree, search, revisions all working with E2E coverage
2. P3.2 — sprint admin works end-to-end
3. P3.3 — roadmap page works; sidebar link fixed
4. P3.4 — item relations UI on both project items and tickets; new ticket relation endpoints exist with swag annotations
5. P3.5 — sprint board drag persists (verified, not regressed)
6. P3.6 — backlog drag reorders; new `/rank` route works
7. P3.7 — profile page works (verified, not regressed)
8. `make test-live` exits 0
9. `make e2e-test` exits 0
10. `make verify-api` exits 0
11. New routes have swag annotations; `make docs` regenerates `docs/api/openapi.yaml`
12. PR body contains "Test integrity statement"

If any is false, PR stays **DRAFT**.

---

## Out of scope — do NOT do these

- Real wiki editor (P4)
- Items split (P5)
- Workflow engine (P6)
- Wiki search snippet/highlight (`ts_headline` SQL — defer)
- Restoring a wiki revision (P4)
- Cross-space relations (only same-space relations work for now)
- Mentions
- Gantt view of roadmap
- Any roadmap document edit
- Any refactor not directly serving the tasks above

---

## PR body required structure

1. **Summary**
2. **PR state** — "Ready-to-merge" or "DRAFT — reason: <which>"
3. **Phase task checklist** — ✅ each P3.1 through P3.7 with SHA
4. **Test results** — full output of all three commands
5. **Test integrity statement**
6. **Out-of-scope findings**
7. **Coverage delta**
8. **License notes** — likely no new deps; confirm
