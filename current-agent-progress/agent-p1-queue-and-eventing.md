# Agent P1 — Background Queue + Audit + Notifications Wiring

## Context

You are working on **Azimuthal**, a fully open-source replacement for Jira + Jira Service Management + Confluence. Single Go binary, React frontend embedded, PostgreSQL, MinIO. Apache 2.0.

This is **Phase 1 of 6** in a sequential series leading to a fully functioning application before a v0.2 rebrand. Five more phases follow this one — do not anticipate them or design for them. Build only what this phase specifies.

**Read these first, in order, before any code:**
1. `CLAUDE.md` (repo root) — agent rules are non-negotiable
2. `docs/project-state.md` — current feature state
3. `docs/known-issues.md` — context, especially items #4, #5, #9
4. `internal/jobs/queue.go` — the queue infrastructure that exists but doesn't run
5. `cmd/server/main.go` — where the wiring is missing
6. `migrations/007_notifications.sql` and `migrations/008_audit_log.sql` — tables waiting for producers

**Your branch:** `feat/p1-queue-and-eventing` from `main`.

---

## Why this phase

Three pieces of infrastructure exist in the codebase but don't run:

1. **River queue** is registered (`internal/jobs/queue.go`) but never started in `cmd/server/main.go`. Nothing async runs.
2. **`notifications` table** exists with zero `INSERT` callers anywhere in `internal/`.
3. **`audit_log` table** exists; the default `audit.Logger` impl literally discards events.

Wiring all three because they share lifecycle and testing surface. This phase unblocks everything async that comes later.

---

## Hard rules

- **CLAUDE.md compliance is non-negotiable.** Test plan, real-DB tests for write paths, no agent-name file suffixes.
- **All three commands must exit 0 for ready-to-merge:** `make test-live`, `make e2e-test`, `make verify-api`. If any fails, PR stays **DRAFT** with body explaining why.
- **No assertion weakening to make CI green.** If a test fails after a fix, the fix is wrong — stop and document.
- **No drive-by refactors.** Every commit references P1 in the message.
- **Migrations are immutable.** No new migrations needed in this phase — the tables exist.
- **No new dependencies without a license check** in the PR body (Apache-2.0 / MIT / BSD only).
- **No stubbed handlers.** If a route is added, it does the thing.
- **PR body includes a "Test integrity statement"** — explicit confirmation no assertion was weakened.
- **Windows / PowerShell environment.**

---

## Tasks

### P1.1 — Start the River queue

In `cmd/server/main.go`, instantiate the River queue from `internal/jobs/queue.go` after the DB pool is up and add it to the graceful-shutdown sequence (drain in-flight jobs before pool close).

- Add a config flag (env: `AZIMUTHAL_QUEUE_ENABLED`, default `true`). When `false`, log a single warning and continue without starting the queue. This is for self-hosters who explicitly disable it — not a feature flag for incomplete work.
- Confirm the existing `internal/jobs/email.go` `EmailWorker` and `internal/jobs/notification.go` worker are registered with the queue.
- Extend the existing health endpoint to include `queue: ok|disabled|error`.

### P1.2 — Wire the audit producer

Replace the discarding default `audit.Logger` with a real implementation that `INSERT`s into `audit_log` synchronously. Audit must be reliable even if the queue is down — do not route audit through River.

Audit emission points (this phase only):
- **Auth:** login success, login failure, logout, token issue
- **Tickets:** create, update, status change, assign, unassign, delete
- **Wiki:** page create, update, move, delete
- **Project items:** create, update, status change, sprint move, delete
- **Sprints:** create, start, complete
- **Comments:** create, delete

Each event records `actor_id` (from JWT), `action`, `entity_type`, `entity_id`, and a small `details JSONB` (e.g. `{"from": "open", "to": "in_progress"}` for status). Do not log full row contents.

Adapter pattern: an `audit.Recorder` interface injected into each service. The existing `audit.Logger` interface can be the same thing or a thin wrapper. Don't redesign the interface — make it work, don't refactor.

All adapter wiring happens in `cmd/server/main.go`. Services receive the recorder via constructor.

### P1.3 — Wire notifications

Replace `// TODO(phase-2)` at `internal/jobs/notification.go:48` with a producer that `INSERT`s into `notifications`.

Notification triggers (this phase only):
- **`assigned`** — when an item or ticket's `assignee_id` changes to a non-null value (skip if assignee == actor)
- **`mentioned`** — placeholder kind only; mention parsing is later, but reserve the kind enum value now so the schema doesn't need re-migration later
- **`commented`** — when a comment is created on an entity the recipient is the assignee or reporter of (skip if commenter == recipient)

Add these routes:
- `GET /api/v1/notifications` — current user, unread first, paginated
- `POST /api/v1/notifications/{id}/read`
- `POST /api/v1/notifications/read-all`

Owner-scoped — a user can only see their own.

**Frontend:**
- Bell icon in the shell header
- Badge count from `notifications` filter `read_at IS NULL`
- Clicking opens a panel listing recent notifications
- Clicking a notification navigates to the entity (URL computed from `entity_type` + `entity_id`)

**Email delivery is NOT in this phase.** In-app surface only. Outbound email replies still go through the existing `EmailWorker`; notification-to-email fan-out is a later phase.

---

## Tests required

- Integration tests with real DB asserting `INSERT` into `audit_log` for at least one event per category in P1.2
- Integration test for notification creation when assignee changes; assert the row exists, then `POST /read` clears it
- Playwright test: log in, assign a ticket to yourself, see bell badge increment, open panel, click notification, navigate, verify badge decrement
- River queue unit test: enqueue a no-op job in test mode, assert it executes within timeout

---

## Definition of Done — every item must be verifiably true for ready-to-merge

1. River queue starts at boot, drains on shutdown
2. `/api/v1/health` (or current health endpoint) reports queue status
3. `audit_log` rows are inserted for the listed events; assertion in integration tests
4. `notifications` rows are inserted for assignment and comment events
5. `GET /notifications`, `POST /notifications/{id}/read`, `POST /notifications/read-all` routes work; owner-scoped
6. Frontend bell icon shows unread count and navigates on click
7. `make test-live` exits 0
8. `make e2e-test` exits 0
9. `make verify-api` exits 0
10. New routes have swag annotations and `make docs` updates `docs/api/openapi.yaml`
11. PR body contains "Test integrity statement" confirming no assertion was weakened

If any of those is false, PR stays **DRAFT** and the body says which.

---

## Out of scope — do NOT do these in this phase

- Mention parsing in comment/page bodies (kind enum value reserved only)
- Email fan-out from notifications
- Watchers (per-page or per-item)
- Audit log UI / browser
- Per-event configurability of audit/notify
- Any roadmap edits
- Any refactor not directly serving the tasks above

---

## PR body required structure

1. **Summary** — one paragraph, what was wired
2. **PR state** — "Ready-to-merge" or "DRAFT — reason: <which DoD item failed>"
3. **Phase task checklist** — ✅ for each P1.1, P1.2, P1.3 sub-task with commit SHA
4. **Test results** — full output of `make test-live`, `make e2e-test`, `make verify-api`
5. **Test integrity statement** — explicit confirmation no assertion was weakened; if any was changed at all, list the change and rationale
6. **Out-of-scope findings** — anything noticed but not fixed
7. **Coverage delta** — before/after percentages
8. **License notes** — none expected (no new deps), but confirm
