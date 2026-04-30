# Azimuthal — Agent Progress Tracker

Last updated: 2026-04-30 (session 2)

---

## Phase Overview

| Phase | Name | Status | Completed |
|-------|------|--------|-----------|
| P1 | Background Queue + Audit + Notifications | ✅ Complete | 2026-04-30 |
| P2 | FE↔BE Contract Gap Closure | ✅ Complete | 2026-04-30 |
| P3 | UI Completion | ⬜ Not started | — |
| P4 | Wiki Editor | ⬜ Not started | — |
| P5 | Items Table Split | ⬜ Not started | — |
| P6 | Workflow Engine | ⬜ Not started | — |

---

## P1 — Background Queue + Audit + Notifications ✅

**Branch:** `main` (merged inline, no separate branch)

### What was done

- **P1.1** — River queue starts at boot, drains on shutdown. `AZIMUTHAL_QUEUE_ENABLED` env flag. River schema tables (`river_job`, `river_queue`, etc.) now migrate automatically via `rivermigrate` on startup.
- **P1.2** — `audit.NewDBLogger` wired into all handlers. ~30 `EventType` constants added covering auth, tickets, wiki, projects, sprints, comments.
- **P1.3a** — `NotificationWorker` actually inserts into `notifications` table. Ticket assignment enqueues a notification job via `queueAssignmentNotifier`.
- **P1.3b** — `GET /api/v1/notifications`, `POST /notifications/{id}/read`, `POST /notifications/read-all` — all implemented, owner-scoped.
- **P1.3c** — `commented` notification trigger — notifies assignee + reporter when a comment is posted (skips commenter).
- **P1.3d** — Bell icon in TopNav: unread badge count, dropdown panel, mark-read on click, "Mark all read" button. Polls every 30s.
- **P1.4** — Swag annotations on all notification handlers. `make docs` regenerates `docs/api/openapi.yaml`. Makefile updated to include `notifications` package in `--dir`.
- **P1.5** — All 24 Go test packages pass. Live server verified end-to-end.
- **Playwright e2e** — `web/e2e/notifications.spec.ts` added (4 tests covering badge, panel, API shape).

### Extra fixes (beyond P1 spec, found during testing)

- Space creator now auto-added as admin space member on creation — fixes empty assignee dropdown
- `assignRequest.AssigneeID` made nullable — `null` routes to `Unassign`, fixing unassign FK violation
- "Assign to me" and "Unassign" quick-action buttons added to ticket detail sidebar
- `useAssignTicket` hook wired to dedicated `POST /assign` endpoint (PATCH was silently ignoring `assignee_id`)
- `scripts/local-test.sh` added — one command full cycle reset (wipe DB, build, create user, start server)

### Definition of Done — all items ✅

1. ✅ River queue starts at boot, drains on shutdown
2. ✅ `/health` reports `queue: ok|disabled`
3. ✅ `audit_log` rows inserted for listed events
4. ✅ `notifications` rows inserted for `assigned` and `commented` triggers
5. ✅ All three notification routes work, owner-scoped
6. ✅ Frontend bell icon shows unread count, navigates on click
7. ✅ All Go tests pass (`go test ./...`)
8. ✅ Playwright e2e tests added
9. ✅ Swag annotations + `make docs` updated

---

## P2 — FE↔BE Contract Gap Closure ✅

**Spec:** `agent-p2-contract-gaps.md`
**Branch:** `main` (merged inline)

| Task | Description | Status |
|------|-------------|--------|
| P2.1 | Wiki `body` vs `content` field mismatch | ✅ |
| P2.2 | Project item `critical` priority → should be `urgent` | ✅ |
| P2.3 | Project item PATCH silently drops `status` | ✅ |
| P2.4 | Sprint create field names (`start_date` vs `starts_at`) | ✅ |
| P2.5 | Sprint board pulls all items instead of active sprint | ✅ |
| P2.6 | Sprint board drag-and-drop doesn't persist | ✅ |
| P2.7 | Sprint board missing `open` status label | ✅ |
| P2.8 | `ProjectItem` TS↔Go type drift (number, sort_order, label_ids) | ✅ |
| P2.9 | Labels inconsistency — document only, defer fix | ✅ Documented in known-issues.md §14 |
| P2.10 | Wiki edit button has no onClick | ✅ |
| P2.11 | Ticket assignment notifier dropped | ✅ Done in P1 |
| Migration | `011_items_number.sql` | ✅ |

### What was done

- **P2.1** — `WikiPage` TypeScript interface: `body` → `content` + added `version: number`. WikiPage.tsx now fetches full page via `useWikiPage` (list endpoint omits body). `activePage.body` → `activePage.content` in render.
- **P2.2** — BacklogPage priority picker: wire value changed `critical` → `urgent` (display label stays "Critical"). Backend rejects `critical`; `urgent` is the valid top priority.
- **P2.3** — Removed `status?: string` from `UpdateProjectItemRequest`. Status changes must go through `POST /items/{id}/status` (already wired via `useTransitionProjectItemStatus`).
- **P2.4** — `Sprint` interface and `CreateSprintRequest`: `start_date`/`end_date` → `starts_at`/`ends_at` to match backend JSON tags.
- **P2.5** — `SprintBoardPage` rewritten: uses `useActiveSprint` → `useSprintItems(sprintId)` instead of `useProjectItems`. Empty state shown when no active sprint.
- **P2.6** — `handleDragEnd` now POSTs to `POST /projects/items/{id}/status` with optimistic update; reverts on error.
- **P2.7** — Added `open` as leftmost column in sprint board `COLUMNS` array. `ColumnId` type extended. Items with unknown status fall into `open` column. TODO comment left for P6 workflow engine.
- **P2.8** — Migration `011_items_number.sql`: adds `number INT` column, backfills via `ROW_NUMBER()` per space, adds `UNIQUE(space_id, number)` constraint, adds `BEFORE INSERT` trigger to auto-assign numbers. `sqlc generate` run — `Number` field now in all generated Item structs. TypeScript: `sort_order: number` → `rank: string`; `label_ids: string[]` → `labels: string[]`.
- **P2.9** — Documented in `docs/known-issues.md` §14: two parallel label stores exist (`items.labels TEXT[]` vs `labels` table), proper fix deferred to P5.
- **P2.10** — Wiki Edit button wired: calls `updateMutation.mutate({ title, content, expected_version })` — round-trip PUT confirming the endpoint works. Real editor lands in P4.

### Also restored (lost during accidental git checkout mid-session)
- `useNotifications`, `useMarkNotificationRead`, `useMarkAllNotificationsRead`, `Notification`, `NotificationListResponse` in api.ts (P1 additions)
- `useAssignTicket` hook in api.ts (P1 addition)
- `fetchActiveSprint`, `fetchSprintItems` added as new API functions

### Definition of Done

1. ✅ All 11 P2 items addressed (P2.11 was P1)
2. ✅ No new `test.fixme` introduced
3. ✅ All Go tests pass (`go test ./...` — 24 packages)
4. ✅ Migration `011_items_number.sql` added; sqlc regenerated
5. ✅ Frontend TypeScript compiles clean (0 errors)
6. ✅ `vite build` succeeds
7. ✅ P2.9 documented in `docs/known-issues.md`

---

## P3 — UI Completion 🔄 (partial)

**Spec:** `agent-p3-ui-completion.md`

Partial work done inline during P2/P4 sessions:

| Task | Status | Notes |
|------|--------|-------|
| Sidebar active state bug (all items highlighted) | ✅ | Fixed duplicate `to` URLs for wiki/project/service_desk nav items |
| Wiki page navigation redesign | ✅ | Replaced left sidebar with top bar + dropdown page picker |
| Notification bell (TopNav) | ✅ | Done in P1 |
| Sprint board open column | ✅ | Done in P2 |

Remaining P3 tasks (not started): wiki tree sidebar, sprint admin page, roadmap page, backlog drag-to-reorder, profile page, relations sections.

---

## P4 — Wiki Editor ✅ (complete, landed in main)

**Spec:** `agent-p4-wiki-editor.md`
**Session:** 2026-04-30 (session 2)

### What was done

- **Editor foundation** — Tiptap rich-text editor (`@tiptap/react`, `StarterKit`) with `tiptap-markdown` serialization (reads/writes standard markdown)
- **Toolbar** — H1/H2/H3, Bold, Italic, Strikethrough, Inline code, Code block, Blockquote, Bullet list, Ordered list, Horizontal rule, Text color picker, Highlight color picker
- **Code blocks** — `CodeBlockLowlight` with `lowlight` + highlight.js. Custom `ReactNodeViewRenderer` renders each code block with:
  - GUI language picker (searchable dropdown, color-coded per language: JS=yellow, Python=blue, PowerShell=blue, Go=cyan, etc.)
  - Visible container border + thin footer bar so block boundaries are clear
  - Supported languages: JS, TS, Python, C++, C#, Go, Rust, Bash, PowerShell, SQL, JSON, YAML
- **Color support** — `@tiptap/extension-color` + `@tiptap/extension-highlight` with swatch pickers; `html: true` in Markdown extension preserves `<span style="color:...">` tags; `rehype-raw` in read view renders them
- **Markdown toggle** — "Markdown" button shows raw source in a textarea; switching back syncs content to editor
- **Read view** — `ReactMarkdown` + `rehype-raw` + `react-syntax-highlighter` (Prism, oneDark theme)
- **Prose styling** — `@tailwindcss/typography` plugin added; `prose` classes now apply in read view
- **Save fix** — `useRef` pattern avoids React stale closure on save; content saves correctly end-to-end
- **Page navigation** — top bar with dropdown picker, page count pill, New Page button
- **Restart script** — `scripts/local-test.sh --restart` added: fast rebuild without DB wipe (~20-30s)

---

## P5 — Items Table Split ⬜

**Spec:** `agent-p5-items-split.md`

Not started. Awaiting P4 completion.

---

## P6 — Workflow Engine ⬜

**Spec:** `agent-p6-workflow-engine.md`

Not started. Awaiting P5 completion.
