# Spec ↔ repository reconciliation — P1.5

**Date:** 2026-07-19
**Scope:** `docs/design/v0.3-ia-spec.md` verified claim-by-claim against the repository at
the P1 merge (`a0dbdf5`, PR #51). Facts corrected in place; decisions, capability models,
phase ordering, and sections 2 and 10 untouched.

**Why this document exists.** The v0.3 spec was written against a stale handoff document
rather than against the repository. P1 discovered three factual errors mid-flight (migration
numbering, the old space-type literal, the constraint name) and recorded them only in its PR
description and session memory. This is the durable record of the full sweep: every
discrepancy found, what the spec said, what the repository says, and what changed. Every
remaining phase (P2–P6) should be written against the corrected spec plus this document.

Method: every table name, column name, migration number, constraint name, route path,
frontend path, Makefile target, and testutil behaviour the spec asserts about **existing**
structures was checked against the source of truth — `migrations/`, `internal/`, `web/src/`,
`Makefile`, `.github/workflows/ci.yml`. Sketches for *future* structures (teams, grants,
shares, views, dashboards) were checked only for their claims about existing objects they
touch or reference.

---

## 1. Discrepancies found and corrected

### D1 — Scaffold end and assignable migration numbers (§4 header)

- **Spec said:** "The scaffold ends at `005`. Nothing below has shipped, so these numbers
  are still assignable."
- **Repo says:** the scaffold ends at `020_drop_org_plan.sql`; `021_space_type_rebrand.sql`
  shipped in P1 (PR #51). Twenty-one migrations exist.
- **Changed:** §4 header rewritten — shipped sequence ends at 021, numbers 022+ assignable.

### D2 — Migration table numbering (§4)

- **Spec said:** 006 rebrand / 007 teams+audit / 008 grants / 009 shares / 010 views /
  011 dashboards / 012 search.
- **Repo says:** 006–020 are already occupied by shipped scaffold migrations
  (006 comments, 007 notifications, 008 audit_log, 009 indexes, … 020 drop_org_plan).
- **Changed:** table renumbered — 021 (shipped) / 022 teams / 023 grants / 024 shares /
  025 views / 026 dashboards / 027 search. "Teams and audit log" retitled "Teams" (see D6).

### D3 — Old space-type literal (§4 rebrand sketch, §9 P1 DoD)

- **Spec said:** `WHEN 'servicedesk' THEN 'beacon'`; DoD grep list included `servicedesk`.
- **Repo says:** `003_spaces.sql` defines `CHECK (type IN ('project', 'wiki',
  'service_desk'))` — underscore.
- **Changed:** rebrand subsection and §9 P1 DoD now use `service_desk`.

### D4 — Pre-existing constraint name (§4 rebrand sketch)

- **Spec said:** `ALTER TABLE spaces DROP CONSTRAINT IF EXISTS spaces_type_valid;`
- **Repo says:** the original constraint is the auto-named inline CHECK
  `spaces_type_check` (from `003_spaces.sql`; confirmed against `pg_constraint` on a live
  database during P1). `spaces_type_valid` is the *new* name introduced by 021.
- **Changed:** subsection replaced with the shipped SQL, which drops `spaces_type_check`.

### D5 — Rebrand down migration left the column unconstrained (§4 rebrand sketch)

- **Spec said:** the down migration dropped `spaces_type_valid` and reverted the values —
  and stopped. No constraint was restored.
- **Repo says:** shipped `021` restores the original `spaces_type_check` with its original
  definition, so a rollback returns the schema to the exact post-020 state.
- **Changed:** subsection now carries the shipped up **and** down verbatim, with a note on
  why the down must restore the constraint.

### D6 — audit_log already exists, with a different schema (§4 teams subsection)

- **Spec said:** "If the v0.2 scaffold has no `audit_log` table, create it in this
  migration" — with a sketch: `entity_type TEXT` (nullable), `entity_id UUID` (nullable),
  `metadata JSONB`, no `user_agent`, index `audit_log_org_idx`.
- **Repo says:** `008_audit_log.sql` created the table long ago. Actual schema:
  `entity_kind TEXT NOT NULL`, `entity_id UUID NOT NULL`,
  `payload JSONB NOT NULL DEFAULT '{}'`, plus `user_agent TEXT`; indexes
  `idx_audit_log_org_id (org_id, created_at DESC)`, `idx_audit_log_actor_id`,
  `idx_audit_log_entity (entity_kind, entity_id)`.
- **Changed:** the conditional and sketch replaced with the real schema, marked
  "do not recreate". P2's audit events must write `entity_kind`/`payload`, and every event
  must name an entity (both columns NOT NULL — every event in §6's list does).
- **Decision impact:** none. D2 in the decision log ("audit logging, append-only") stands;
  only the create-vs-reuse premise and column names were wrong.

### D7 — All three search targets already have `search_vector` columns (§4 search subsection)

- **Spec said:** migration adds `search_vector` via `ADD COLUMN` to `pages`, `tickets`,
  `project_items`, plus full GIN indexes named `*_search_idx`.
- **Repo says:**
  - `pages.search_vector` **already exists in exactly the target form** —
    `009_indexes.sql` created it `GENERATED ALWAYS AS` with the identical weighted
    expression (title `A`, content `B`) and partial GIN index `idx_pages_search`.
  - `tickets.search_vector` / `project_items.search_vector` exist as **trigger-maintained,
    unweighted** columns from `014_split_items_phase1.sql`
    (`update_tickets_search_vector()` / `update_project_items_search_vector()`), with
    partial GIN indexes `idx_tickets_search` / `idx_project_items_search`.
  - The legacy `items` table (superseded by the ADR-0003 split, never dropped) also carries
    a generated vector from 009.
  - A plain `ADD COLUMN` fails on all three tables.
- **Changed:** 027 subsection rewritten — pages untouched; tickets/project_items get the
  trigger arrangement dropped and replaced with generated stored columns; the down must
  restore the 014 arrangement verbatim; legacy `items` untouched.
- **Decision impact:** none. C4 ("generated stored columns") stands — pages already
  implements it; 027 converges tickets/project_items onto the same decision.

### D8 — Pages subtree match sketched with an array operator (§5 read-query shape)

- **Spec said:** `OR path && $shared_subtrees -- pages only`.
- **Repo says:** `pages.path` is dot-separated **TEXT** (`"{root}.{parent}.{self}"`, from
  `012_pages_materialized_path.sql`), not an array. `&&` does not apply; subtree membership
  is a string prefix match.
- **Changed:** the query line now reads as a prefix match against the shared root pages'
  path prefixes, with a note distinguishing it from the future `teams.path UUID[]` design
  (which is new in 022 and legitimately an array).
- **Decision impact:** none. ADR-0008's stated rationale ("a subtree share is a prefix
  match, not a recursive walk") already described the TEXT reality; only the §5 operator
  sketch was wrong.

### D9 — Phase descriptions referenced the wrong migration numbers (§9)

- **Spec said:** P1 "Migration 006"; P2 "Migrations 007–008"; P3 "Migration 009";
  P4 "Migration 010"; P5 "Migration 011"; P6 "Migration 012".
- **Changed:** P1 → 021 (shipped); P2 → 022–023; P3 → 024; P4 → 025; P5 → 026; P6 → 027.

### D10 — Decision log rows carried the stale numbers (Appendix B)

- **Spec said:** A8 "migration 006"; C4 "migration 012".
- **Changed:** A8 → migration 021 (shipped in P1); C4 → migration 027. Decision content
  untouched.

**Total discrepancies corrected: 10** (D1–D10, spanning §4, §5, §9, Appendix B).

---

## 2. Claims verified accurate — no change needed

- **Tables exist as the spec names them:** `organizations` (001), `users` and
  `memberships` (002), `spaces` (003), `pages` (005), `comments` (006), `notifications`
  (007), `audit_log` (008), `tickets` and `project_items` (014, ADR-0003 split intact).
- **Search column names (§4):** `pages(title, content)`, `tickets(title, description)`,
  `project_items(title, description)` — all correct as the sketch assumed.
- **`internal/testutil.NewTestDB(t)` (§2.8):** behaves as described. Real PostgreSQL only:
  connects to `DATABASE_URL`, creates an isolated per-test schema
  (`test_<sanitized_test_name>`), applies all goose migrations into that schema, caps the
  pool at 3 connections, drops the schema on cleanup. No mocks anywhere. One behaviour
  worth knowing: when `DATABASE_URL` is unset it calls `t.Skip` ("Run 'make test-db-up'
  first") — in CI the variable is always set, and `make test-live` sets it from
  `.env.test`, so the skip only fires for a bare local `go test` without the test DB.
- **Makefile targets (§2.8, §2.9, §6):** `test-db-up` (postgres :5433, minio :9001 — the
  ports in the spec's comment are right), `test-db-down`, `test-live`, `verify-api`,
  `e2e-test`, `regression-test`, `docs`, `docs-check` all exist. The `docs-check` CI gate
  (§6) exists as ci.yml GATE 6.
- **Route scoping claim (§6):** "Space-scoped resources keep
  `/api/v1/orgs/{org_id}/spaces/{space_id}/...` unchanged" — confirmed against
  `internal/core/api/router.go` (`mountSpaceResources`, `RequireSpaceInOrg` guard).
  Existing org-scoped families (`/orgs/{orgID}`, `/orgs/{orgID}/labels`,
  `/orgs/{orgID}/workflows`) are consistent with the planned ADR-0010 route family; none of
  the planned paths collide. Notifications are user-scoped at `/api/v1/notifications`.
- **Frontend route nesting (§7):** matches `web/src/App.tsx` after P1 — `/`,
  `/home/:dashboardId`, `/:module/:spaceId` nested under `SpaceLayout` with
  board/backlog/sprints/roadmap/labels/settings plus module-specific sub-routes and
  branded-placeholder catch-alls. `views/:viewId`, `dashboards/:dashboardId`, and
  `/shared/...` are correctly future (P4/P5/P3).
- **Component paths (§7):** every listed component that should exist after P1 does, at the
  listed path under `web/src/shell/` — `TopBar`, `ProductTabs`, `SpacePicker`, `FocusChip`,
  `SpaceLayout`, `ModuleChip`, `EmptyState`, `sidebars/BeaconSidebar`,
  `sidebars/CodexSidebar`, `sidebars/VectorSidebar`. `ShareBadge` is P3-future. P1 added
  shell files beyond the list (`AppShell`, `HomeLayout`, `HomeSidebar`, `SidebarChrome`,
  `ShellUIContext`, `ModuleLandingRedirect`, `NotFoundPage`, `hooks/`) — the list is not
  exhaustive and nothing contradicts it.
- **Design tokens (§8):** `--module-beacon #E0806A`, `--module-codex #A99BEE`,
  `--module-vector #4A90D9`, `--module-chip-alpha 0.30`, `--module-chip-fg #A6AEBC`, and
  `--sidebar-width-collapsed` all present in `web/src/styles/tokens.css` with the spec's
  values.

---

## 3. Observed, out of scope for spec edits

- **§2.8 coverage floor vs CI:** the spec states an 80% gate; CI enforced 70%. Per the
  P1.5 charter, reality bends to the spec here: the CI floor is raised to 80 in this PR.
  Section 2 itself is untouched.
- **`CLAUDE.md` is absent from the repo root on `main`.** Stale copies exist only on old
  worktrees/deleted branches. Not a spec claim; flagged for maintainers.
- **Legacy `items` table still exists** (the ADR-0003 split backfilled `tickets` /
  `project_items` but never dropped `items`). No spec claim asserts otherwise; noted so a
  future phase can schedule the drop deliberately.
- **`WorkflowAdminPage` violated §7's api.ts-only rule** with a rogue fetch client reading
  a never-written localStorage key. Fixed in this PR (workstream D); see the PR body for
  runtime severity.

---

## 4. Standing instruction for later phases

When this spec's prose and the repository disagree about an **existing** structure, the
repository wins and the spec gets corrected in the same PR that discovers it — with an
entry appended here. When the disagreement would change a **decision** (ADRs, capability
model, §2, §10), stop and raise it instead. That is this spec's own §0 conflict rule; P1.5
merely applied it wholesale.
