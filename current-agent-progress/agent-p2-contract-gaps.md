# Agent P2 — FE↔BE Contract Gap Closure

## Context

You are working on **Azimuthal**, a fully open-source replacement for Jira + Jira Service Management + Confluence. Single Go binary, React frontend embedded, PostgreSQL, MinIO. Apache 2.0.

This is **Phase 2 of 6** in a sequential series leading to a fully functioning application before a v0.2 rebrand. **Phase 1 must already be merged** before you start. Four more phases follow this one — do not anticipate them.

**Read these first, in order, before any code:**
1. `CLAUDE.md` (repo root) — agent rules are non-negotiable
2. `docs/project-state.md` — current feature state
3. `docs/known-issues.md`
4. The three stock-take reports for cited references:
   - `C:\Users\Kitsune\OneDrive\Documents\Claude\azimuthal-reviews\2026-04-19\service-desk-vs-jsm.md`
   - `C:\Users\Kitsune\OneDrive\Documents\Claude\azimuthal-reviews\2026-04-19\wiki-vs-confluence.md`
   - `C:\Users\Kitsune\OneDrive\Documents\Claude\azimuthal-reviews\2026-04-19\projects-vs-jira.md`

**Your branch:** `fix/p2-contract-gaps` from `main`.

---

## Why this phase

Across the three stock-take reports, there are roughly a dozen specific FE↔BE contract bugs. Each looks small in isolation but together they account for most of the "this just doesn't work when I click around" experience. They're cited and citable, so the work is mechanical, not judgmental.

---

## Hard rules

- **CLAUDE.md compliance is non-negotiable.**
- **All three commands must exit 0 for ready-to-merge:** `make test-live`, `make e2e-test`, `make verify-api`. If any fails, **DRAFT** with body explaining why.
- **No assertion weakening to make CI green.** If a test fails after a fix, the fix is wrong — stop and document.
- **No drive-by refactors.** Every commit references the P2 sub-ID and the source stock-take section.
- **Migrations are immutable.** This phase adds exactly one new migration: `011_items_number.sql`.
- **No new dependencies without a license check** (Apache-2.0 / MIT / BSD only).
- **PR body includes a "Test integrity statement"** — explicit confirmation no assertion was weakened.
- **Windows / PowerShell environment.**

---

## Tasks (each is a separate commit, citing the stock-take section)

### P2.1 — Wiki body/content field-name mismatch

Per `wiki-vs-confluence.md` §3.4 and §12 row 2: TypeScript `WikiPage` declares `body: string`; backend returns `content`. The list endpoint returns no body or content at all. `useWikiPage` is defined but unreferenced.

- Fix `web/src/lib/api.ts` `WikiPage` to declare `content: string` (matching backend snake_case at `internal/db/generated/pages.sql.go`)
- Switch `WikiPage.tsx` to call `useWikiPage(spaceId, pageId)` for the active page rather than reading from the list response
- Verify: Playwright creates a page with content, navigates away, returns, sees the content rendered

### P2.2 — Project item priority `critical` rejection

Per `projects-vs-jira.md` §3 row "Item priority" and §13 row 2: frontend submits `critical`; backend rejects with `ErrInvalidPriority`. Backend uses `urgent`.

- Frontend dialog must submit `urgent` for the highest priority. Display label can stay "Critical"; wire value must be `urgent`. This matches what tickets already does in v0.1.12.

### P2.3 — Project item PATCH silently drops `status`

Per `projects-vs-jira.md` §3 row "Item update (PATCH)" and §13 row 7: handler's `updateItemRequest` does not include `status`. Frontend POSTs it; backend ignores it.

- Status changes go through the dedicated `POST /items/{itemID}/status` endpoint (consistent with tickets) — remove `status` from the frontend's PATCH payload
- Update `useUpdateProjectItem` and any caller. Add `useUpdateItemStatus` if it doesn't exist

### P2.4 — Sprint create field names

Per `projects-vs-jira.md` §3 mismatch row 4: frontend sends `start_date` / `end_date`; backend expects `starts_at` / `ends_at`.

- Align frontend with backend (changing backend names would be a migration; not in scope)
- Update `web/src/lib/api.ts` `Sprint` type and any sprint create call site

### P2.5 — Sprint board pulls all space items

Per `projects-vs-jira.md` §3 row "Sprint board" and §13 row 4: `SprintBoardPage.tsx` calls `useProjectItems(spaceId)` (all items) instead of the active sprint's items.

- Use `GET /sprints/active` for the active sprint
- Then `GET /sprints/{id}/items` for the items
- If no active sprint exists, render empty-state with "Start a sprint" CTA (functionality lands in P3)

### P2.6 — Sprint board kanban drag does not persist

Per `projects-vs-jira.md` §3 row "Sprint board" and §13 row 1: `handleDragEnd` is a no-op. Drag changes local state, doesn't POST.

- On drop, call the status transition endpoint (`POST /items/{itemID}/status`)
- Invalidate sprint items query on success
- Optimistic update OK; on error, revert

### P2.7 — Sprint board / backlog status label gaps

Per `projects-vs-jira.md` §3 row "Item status — frontend labels": frontend `STATUS_LABEL` covers `todo, in_progress, in_review, done` but not `open` (the backend default). Newly created items render their raw status string and don't appear in any board column.

- Add `open` to `STATUS_LABEL`; treat `open` as the leftmost column on the sprint board (or merge with `todo` visually)
- Add a TODO referencing P6 in the same file (workflow engine will replace this map later)

### P2.8 — Project item TS↔Go type drift

Per `projects-vs-jira.md` §3 mismatches 1, 2, 3:

- **`ProjectItem.number`** referenced in TS but not returned by backend. **Add `number INT` column on items** (sequence per space, like Jira's `PROJ-42`). Migration `011_items_number.sql` adds the column with a backfill and a sequence.
- **`ProjectItem.sort_order` (TS) vs `Rank` (Go).** Align TS to `rank: string`.
- **`ProjectItem.label_ids` (TS) vs `labels: string[]` (Go).** Align TS to `labels: string[]`.

### P2.9 — Labels: text array vs. labels table inconsistency

Per `service-desk-vs-jsm.md` §8 ("two parallel label stores exist") and `projects-vs-jira.md` §13 row 8: `items.labels TEXT[]` and the `labels(id, org_id, name, color)` table are not linked.

- **For P2: do not redesign.** Document the inconsistency in `docs/known-issues.md`. Add a stub follow-up note. Frontend shows label text from the array; the labels admin table exists for color/grouping but is not joined.
- The proper fix lands after P5.

### P2.10 — Wiki edit button has no onClick

Per `wiki-vs-confluence.md` §3.4 row "Edit button": button exists, no handler.

- For P2: wire the button to a placeholder action that POSTs the page back unchanged via `PUT /wiki/{pageID}` with the current `expected_version`. This confirms the round-trip works end-to-end and removes the `test.fixme` on `wiki.spec.ts:39`.
- Does **not** ship a real editor — that's P4.
- After this, `wiki.spec.ts:39` is no longer fixme'd; assertion verifies the round-trip.

### P2.11 — Ticket assignment notifier dropped

Per `service-desk-vs-jsm.md` §3 row "Assignee": handler always passes `nil` for the notifier in `h.svc.Assign(..., nil)`.

- Now that P1 has wired notifications, plumb the assignment notifier so assignment events emit a notification
- This is the smallest test that proves P1's pipeline works for tickets, not just project items

---

## Migration

This phase adds exactly **one** new migration: `migrations/011_items_number.sql`.

- Adds `number INT` column on `items`
- Adds a per-space sequence (one sequence per space, populated via trigger on insert OR via a service-side allocator — pick the one that's simpler to test)
- Backfills existing rows with sequential numbers per space, ordered by `created_at`
- Adds `UNIQUE (space_id, number)` constraint after backfill

---

## Tests required

- P2.1, P2.2, P2.5, P2.6, P2.10: Playwright tests asserting the new behavior. If a test was previously `test.fixme` on the same scenario, un-fixme it. **Assertion must match the corrected behavior, not be weakened.**
- P2.3, P2.4, P2.8: API integration tests sending the new field names and asserting the response
- P2.11: integration test asserting a notification row exists after assigning a ticket to another user

Previously fixme'd tests that should already be live (do not regress them):
- `auth.spec.ts:52`, `auth.spec.ts:91`, `dashboard.spec.ts:31`, `service-desk.spec.ts:33`, `service-desk.spec.ts:48`, `projects.spec.ts:33`

---

## Definition of Done — every item must be verifiably true for ready-to-merge

1. All eleven P2 items merged with cited fix
2. No new `test.fixme` introduced
3. Previously-live tests above remain live and passing
4. Migration `011_items_number.sql` (only) added; succeeds on a fresh DB
5. `make test-live` exits 0
6. `make e2e-test` exits 0
7. `make verify-api` exits 0
8. Modified routes have updated swag annotations; `make docs` regenerates `docs/api/openapi.yaml`
9. PR body contains "Test integrity statement" confirming no assertion was weakened
10. P2.9 documented in `docs/known-issues.md` (not fixed, deferred)

If any of those is false, PR stays **DRAFT** and body says which.

---

## Out of scope — do NOT do these in this phase

- Real wiki editor (P4)
- Items table split (P5)
- Workflow engine (P6)
- Email notifications fan-out
- Watchers
- Mentions parsing
- Labels join table redesign
- Any roadmap edits
- Any refactor not directly serving the tasks above

---

## PR body required structure

1. **Summary** — one paragraph, what was fixed
2. **PR state** — "Ready-to-merge" or "DRAFT — reason: <which DoD item failed>"
3. **Phase task checklist** — ✅ for each P2.1 through P2.11 with commit SHA
4. **Test results** — full output of all three commands
5. **Test integrity statement** — explicit confirmation no assertion was weakened; list any assertion changes with rationale
6. **Out-of-scope findings**
7. **Coverage delta** — before/after
8. **License notes** — none expected
