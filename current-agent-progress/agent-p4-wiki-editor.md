# Agent P4 — Wiki Editor (TipTap), Materialized Path, File Lock

## Context

You are working on **Azimuthal**, a fully open-source replacement for Jira + Jira Service Management + Confluence.

This is **Phase 4 of 6** in a sequential series. **Phases 1, 2, and 3 must be merged** before you start. Two more phases follow — do not anticipate them.

**Read these first, in order:**
1. `CLAUDE.md`
2. `docs/project-state.md`
3. `docs/known-issues.md`
4. `internal/core/wiki/tree.go` and `migrations/004_pages.sql` (or wherever pages schema lives)
5. `internal/db/queries/pages.sql` and the corresponding generated code
6. `wiki-vs-confluence.md` §9 (hierarchy options) and §6 (collaboration ceiling) for context

**Your branch:** `feat/p4-wiki-editor-and-hierarchy` from `main`.

---

## Locked decisions (do not deviate)

- **Editor:** TipTap with **markdown canonicality**. Storage stays markdown. This preserves the existing goldmark renderer, tsvector search, and diff/revision system.
- **Hierarchy:** **materialized path**.
- **File lock:** any user opening a page in edit mode acquires a lock. Other users see a banner "X is editing this page" and the editor is read-only for them. Lock expires after inactivity (default 5 minutes, configurable). Lock released on save, on navigate-away, or explicitly.
- **No real-time collab.** Single editor at a time. Yjs / CRDT / OT explicitly deferred.

---

## Hard rules

- **CLAUDE.md compliance is non-negotiable.**
- **All three commands must exit 0:** `make test-live`, `make e2e-test`, `make verify-api`. If any fails, **DRAFT**.
- **No assertion weakening to make CI green.** Stop and document.
- **No drive-by refactors.**
- **Migrations:** this phase adds `012_pages_materialized_path.sql` and `013_page_locks.sql`. No others.
- **No new dependencies without a license check** in PR body. Required additions for this phase:
  - `@tiptap/react`, `@tiptap/starter-kit`, `tiptap-markdown` (and a markdown library it depends on)
  - Go-side: `github.com/microcosm-cc/bluemonday` for HTML sanitization
  - All must be Apache-2.0 / MIT / BSD compatible — confirm in PR body.
- **PR body includes a "Test integrity statement."**
- **Windows / PowerShell environment.**

---

## Tasks

### P4.1 — Materialized path migration

**Migration `012_pages_materialized_path.sql`:**

- Add `path TEXT NOT NULL DEFAULT ''` to `pages`
- Add index `CREATE INDEX idx_pages_path ON pages (space_id, path) WHERE deleted_at IS NULL`
- Backfill: one-time `UPDATE` walks the existing adjacency-list tree and writes the path
- **Path format:** full UUIDs separated by dots — `{root_id}.{parent_id}.{my_id}` (or just `{my_id}` for root pages). Dots are unambiguous because UUIDs contain no dots; depth is implicit from segment count.

**Update sqlc queries in `internal/db/queries/pages.sql`:**

- `GetPageTree`: `SELECT * FROM pages WHERE space_id = $1 AND deleted_at IS NULL ORDER BY path` — returns rows in tree order; Go side builds the nested structure with a single pass
- `GetPageDescendants(page_id)`: `WHERE space_id = $1 AND path LIKE (SELECT path || '.%' FROM pages WHERE id = $2)` — used for subtree operations
- `GetPageAncestors(page_id)`: parse the path and select pages where `id = ANY(parts)`

**Update `BuildTree` in `internal/core/wiki/tree.go`** to use the new query — same return type so handler code is unchanged.

**Path maintenance (service-side, not triggers — keeps migrations greppable):**

- On `INSERT`: compute path from parent's path
- On `UPDATE` of `parent_id` (move): update the moving node's path, then bulk-update all descendants' paths via `UPDATE pages SET path = REPLACE(path, old_prefix, new_prefix) WHERE space_id = $1 AND path LIKE old_prefix || '%'`
- On soft delete: descendants are not auto-deleted — they become orphans; `BuildTree` already handles orphans

**Consistency check:** Go function `VerifyPagePaths(spaceID)` walks the adjacency list and the path independently and asserts agreement. Used in tests and exposed as `azimuthal admin verify-page-paths` for self-hosters.

### P4.2 — File lock

**Migration `013_page_locks.sql`:**

```sql
CREATE TABLE page_locks (
    page_id UUID PRIMARY KEY REFERENCES pages(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_page_locks_user_id ON page_locks (user_id);
```

**Routes:**
- `POST /spaces/{spaceID}/wiki/{pageID}/lock` — acquires lock if free or owned by self; otherwise 409 with current owner's `display_name` and `acquired_at`
- `POST /spaces/{spaceID}/wiki/{pageID}/lock/heartbeat` — updates `last_heartbeat_at`, pushes `expires_at` forward (default lock length: 5 minutes from now)
- `DELETE /spaces/{spaceID}/wiki/{pageID}/lock` — releases lock if owned by caller

**Lock check on `PUT /spaces/{spaceID}/wiki/{pageID}` (update):** if a lock exists and is not owned by the caller, 409. Existing `expected_version` optimistic check still runs after.

**Lock auto-expiry:** River job sweeps `page_locks` every 60s, deletes rows where `expires_at < now()`. Cheap because of the indexed column.

**Backend enforces the lock on every write to the page** — not just a UI construct.

### P4.3 — TipTap editor

Replace the Edit button placeholder action (from P2.10) with the real editor.

**Dependencies to add:**
- `@tiptap/react` (MIT)
- `@tiptap/starter-kit` (MIT)
- `tiptap-markdown` (MIT) — markdown serializer/parser

Confirm all licenses in PR body.

**Editor extensions, batch one** (matches what GFM already supports server-side):
- StarterKit (paragraph, headings, bold, italic, code, lists, blockquote, hr)
- Tables (with header)
- Code block with language
- Task list / task item
- Strike
- Link
- Image (URL only — no upload yet; that's a later phase with `attachments`)
- Horizontal rule

**On Edit click:**
- Frontend acquires lock (`POST .../lock`)
- On 409: show banner with current editor's name; disable Edit
- On success: switch from rendered view to editor
- Heartbeat every 60 seconds while editor is open

**On Save:**
- Serialize the TipTap document to markdown
- `PUT .../wiki/{pageID}` with `{ title, content, expected_version }`
- On 409 (version conflict): show conflict UI — user's draft on left, current page on right, with copy-to-clipboard actions. **No automatic 3-way merge** in this phase.
- On success: release lock, switch back to rendered view

**On Cancel or navigate-away:**
- Prompt if dirty
- Release lock unconditionally

**On browser close:** `navigator.sendBeacon` to release the lock (best-effort; River sweep catches strays).

### P4.4 — Sanitization

Per `wiki-vs-confluence.md` §12 row 23: goldmark output is currently written directly in `/render`. Add sanitization.

- Use `bluemonday` (BSD-3, compatible). Confirm in PR body.
- Policy: `bluemonday.UGCPolicy()` extended to allow code blocks, tables, and task list checkboxes
- Sanitizer runs on goldmark HTML output before it leaves the server
- Tests: `<script>` tag in markdown source does not appear in rendered output; standard formatting passes through unchanged

### P4.5 — Wiki revision restore

Now that the editor exists, the "restore this version" button in the revisions panel from P3.1 becomes possible.

- "Restore" button on a revision: prompts confirmation, then loads that revision's title/content into the editor (does not save automatically — user reviews and saves)
- Alternative button "Restore directly": performs `PUT .../wiki/{pageID}` with the old content
- Both options visible

### P4.6 — Frontend types & hook gaps

- Audit `web/src/lib/api.ts` for any wiki-related type/hook drift introduced in P3 and align before this phase ships
- Confirm `Page.content`, `Page.path` (new column), `Page.version`, and any lock-related response types match the Go structs

---

## Tests required

- Integration: create a tree of pages, verify path values, move a subtree, verify all descendants' paths updated, soft-delete a parent, verify orphan handling
- Integration: lock acquired, second user attempts edit, gets 409. Lock expires after 5 minutes (use a test clock); second user can now edit
- Integration: PUT page while another user holds a lock → 409
- Integration: PUT page with stale `expected_version` while holding a lock → 409 (lock check passes, version check fails — both errors handled)
- Integration: render output passes through bluemonday — `<script>` source becomes neutralized
- Playwright: log in as user A, edit a page, log in as user B in another browser context, verify B sees the lock banner
- Playwright: edit, save, verify content rendered. Edit again, see new revision in panel.

---

## Definition of Done — every item must be verifiably true for ready-to-merge

1. Migration `012_pages_materialized_path.sql` applied; backfill correct
2. Consistency check (`VerifyPagePaths`) passes; admin command works
3. Migration `013_page_locks.sql` applied; lock routes work; lock enforcement on PUT works
4. River lock-sweep job runs and clears expired locks
5. TipTap editor edits and saves; conflict UI surfaces on version mismatch
6. Sanitization pass on render output; XSS test passes
7. Revision restore works (loads into editor; user explicitly saves)
8. All three test commands exit 0
9. PR body contains "Test integrity statement"
10. License audit in PR body confirms TipTap, tiptap-markdown, bluemonday compatible

If any is false, PR stays **DRAFT**.

---

## Out of scope — do NOT do these

- Image upload / attachments (no `attachments` table yet)
- Real-time collaborative editing
- Page comments end-to-end (still backend-table-only — comes in P5 alongside polymorphic comment refactor)
- Mentions
- Wiki↔ticket bidirectional embeds (e.g. `:::ticket id=X:::` goldmark extension)
- Templates
- Page-level permissions
- Closure-table or recursive-CTE alternative — owner picked materialized path
- Items table split (P5)
- Workflow engine (P6)
- Any roadmap edit

---

## PR body required structure

1. **Summary**
2. **PR state** — "Ready-to-merge" or "DRAFT — reason: <which>"
3. **Phase task checklist** — ✅ each P4.1 through P4.6 with SHA
4. **Test results** — full output of all three commands
5. **Test integrity statement**
6. **Out-of-scope findings**
7. **Coverage delta**
8. **License audit** — TipTap, tiptap-markdown, bluemonday: confirm Apache-2.0 / MIT / BSD
