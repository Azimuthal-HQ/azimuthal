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

---
---

# Spec ↔ repository reconciliation — post-P3

**Date:** 2026-07-21
**Scope:** `docs/design/v0.3-ia-spec.md` re-verified against the repository at `92f60e1`
(after P3 / PR #56, the security and docs-CI fix / #58, the ADR directory / #57, and the space
slug fix / #60). Also covers `docs/known-issues.md` and the ADR index.
**Method:** unchanged from P1.5 — every migration number, table, column, constraint name, index
name, identifier, route, test name and tag asserted about an **existing** structure was checked
against `migrations/`, `internal/`, `web/src/`, `web/e2e/`, `Makefile`, `.github/workflows/ci.yml`
and `git` itself. Sketches for future structures were checked for their claims about existing
objects they touch.

**Why this second pass exists.** P1.5 reconciled the spec and was correct on the day it merged.
The spec drifted again within two days, and for a structural reason worth naming: **the spec's
§4 migration table is written as a plan but read as a record.** Two unplanned phases (P1.5, P2.5)
shipped, one of them taking migration numbers this document had already promised to P3 and P4.
Everything downstream shifted.

The rule from P1.5 applies here unchanged. Facts are corrected. Decisions are not. Where reality
and the plan disagree about the **future**, the disagreement is recorded and flagged — see D27
and D33, both deliberately left unresolved.

---

## 1. Discrepancies found and corrected

Numbering continues from P1.5's D1–D10.

### W1 — Migrations and schema (§4)

#### D11 — The §4 migration table was stale from row 024 down

- **Spec said:** 024 entity shares (P3) / 025 saved views (P4) / 026 dashboards (P5) /
  027 full-text search (P6). Header: "the shipped sequence ends at `021`".
- **Repo says:** 28 migrations exist, `001`–`028`. `024` and `025` are P2.5's
  (`024_invites_session_control.sql`, `025_audit_batch_correlation.sql`); entity shares shipped
  as `026`; attachments as `027`; the space slug fix as `028`.
- **Root cause:** P2.5 was not in the plan and took 024–025 two days after P1.5 renumbered the
  table. `026_entity_shares.sql:4-5` records the shift in-file — the code noticed, the spec did
  not.
- **Changed:** table rebuilt from the directory, with filenames and what each does, marking
  021–028 shipped and naming `029` as the next assignable number. Added a note on why it went
  wrong twice, so the next phase reads `migrations/` rather than the table.

#### D12 — The attachments table was absent from the specification entirely

- **Spec said:** nothing. `attachments` appears nowhere in the document.
- **Repo says:** `027_attachments.sql` created it in P3 — 11 columns, 4 named constraints, a
  partial index, soft delete. It is the first production consumer of `ObjectStore` and it closed
  known-issue #16.
- **Changed:** full subsection added to §4, including the two load-bearing design choices — the
  object key is derived from the row rather than accepted from the client (otherwise the shared
  read path becomes an arbitrary-object-read primitive), and there is deliberately no `space_id`
  column so a cross-space move cannot strand a stale container reference.
- **Decision impact:** none. It implements ADR-0008 rule 3, which already required it.

#### D13 — The entity-shares sketch was missing an index that shipped

- **Spec said:** four indexes.
- **Repo says:** five. `026` adds `entity_shares_org_idx ON entity_shares (org_id) WHERE
  revoked_at IS NULL`, flagged in-file as "Not in the spec sketch": the resolution query and the
  org share listing both filter `org_id` first, and without it every resolution full-scans once
  several orgs share an instance.
- **Changed:** index added to the §4 SQL block. Columns and all five constraint names were
  verified identical to the sketch — that part of the sketch was exactly right.

#### D14 — Migration 028 and the constraint it did *not* change

- **Spec said:** nothing; 028 postdates the document.
- **Repo says:** slug uniqueness moved from `(org_id, slug)` to `(org_id, type, slug)`. Its down
  migration deliberately fails on a database that has used the new freedom, rather than silently
  re-slugging someone's space.
- **Changed:** subsection added — including the non-obvious part: `idx_spaces_org_key` from
  `017_space_key.sql` was **not** touched and remains org-wide, so space *keys* still collide
  across modules even though slugs no longer do.

#### D15 — The "legacy `items` table" no longer exists under that name

- **Spec said:** "The legacy `items` table (superseded by the ADR-0003 split, never dropped)".
  P1.5's own record repeated this.
- **Repo says:** `015_polymorphic_comments_relations.sql` **renamed** `items` to `items_archive`.
  PostgreSQL does not rename a table's indexes with the table, so its GIN index is still called
  `idx_items_search` and now sits on `items_archive`.
- **Changed:** §4 search subsection corrected. Two traps recorded: `idx_items_search` does not
  refer to a live table, and the name `items` is not free.
- **Note:** this was wrong in P1.5's record too, and is corrected here rather than left standing.

#### D16 — The search sketch accounts for only two of the four search vectors

- **Spec said:** drop the `tickets` / `project_items` triggers, functions and indexes, then add
  generated columns.
- **Repo says:** all six identifiers the sketch names still exist at the exact places P1.5 cited,
  and **nothing in migrations 015–028 touched any search vector** — D7 survives intact. But
  `pages.search_vector` and `items_archive.search_vector` are *generated* columns whose indexes
  (`idx_pages_search`, `idx_items_search`) the sketch never names. A generated column cannot be
  converted by dropping a trigger, because it has none.
- **Changed:** recorded in the search subsection as an obligation for P6.

### W1 — Permission resolution (§5)

The `readable_space_ids` and `readable_entity_ids` sketches are substantially accurate and were
verified against P2's shipped `ResolveAccessRows` and P3's share resolution. The **cross-space
read-query shape** is a different matter: it was never implemented, and taken literally it is
both incorrect and unsafe. It is the direct input to P4 and P6.

#### D17 — The subtree term binds raw paths where LIKE patterns are required

- **Spec said:** `OR path LIKE ANY($shared_subtree_path_prefixes)`.
- **Repo says:** a stored path is not a pattern. P3 builds one via `SubtreeLikePattern(root)` =
  `EscapeLike(root) + ".%"`. `EscapeLike` neutralises `\`, `%` and `_` first, because
  `pages.path` is unconstrained `TEXT` and a metacharacter that ever landed there must match
  itself rather than widen the match.
- **Changed:** §5 corrected, with the escaping requirement stated explicitly.

#### D18 — The shared root page was excluded from its own coverage

- **Spec said:** `direct := entity_id for shares where cascade = false`, plus a subtree term.
- **Repo says:** the LIKE pattern matches *strict descendants only* — deliberately. P3 therefore
  puts **every** share row into the direct set regardless of `cascade`, which is how the root
  gets covered. The spec's two clauses together cover the descendants and not the root.
- **Changed:** both the pseudocode and the query shape corrected, with the interaction between
  them called out — this is the failure the two halves conceal when read separately.

#### D19 — The subtree match was not pinned to the root's space

- **Spec said:** a bare path-prefix match.
- **Repo says:** `pages.path` uniqueness is not enforced across spaces. P3's `CoversPage` skips
  any cascade root whose `spaceID` differs from the candidate's, and all three shipped subtree
  queries pin `p.space_id`. There is a regression test for the path-coincidence case.
- **Changed:** the space pin added to the query shape.
- **Severity note:** as written, this term could have widened share coverage across a space
  boundary. It is the most consequential correction in this pass.

#### D20 — The org-admin bypass condition was too narrow

- **Spec said:** `if org_role(user, org) == 'admin'`.
- **Repo says:** the adapter classifies **both** `owner` and `admin` as org admin.
- **Changed:** pseudocode reads `in ('admin', 'owner')`.

#### D21 — Two omissions in `readable_space_ids` that change results

- **Repo says:** the shipped query joins `spaces ... deleted_at IS NULL` on the grants leg, so a
  grant on a soft-deleted space does not resolve; and it wraps the direct-team array in
  `COALESCE(..., '{}')`, because a teamless user would otherwise produce `path && NULL` → NULL
  and silently lose the whole leg.
- **Changed:** both recorded in the pseudocode.

#### D22 — "Have every query filter against the result" describes an intent, not the mechanism

- **Spec said:** resolve once, then "have every query filter against the result".
- **Repo says:** no shipped query binds the readable set as a parameter. Space routes 404 in
  middleware via `RequireSpaceReadable`; the directory filters in the handler. The accessor that
  would make the sentence literally true has no production caller.
- **Changed:** §5 now says which mechanism is in force today and marks the bound-array shape as
  design for P4/P6 rather than a description of running code.

#### D23 — §5 does not say that share resolution is route-scoped

- **Repo says:** P3 mounts share resolution **only** on the `/shared` subtree, deliberately, so
  space-scoped routes keep P2's per-request query budget.
- **Changed:** recorded, with the instruction that P4 and P6 mount it explicitly on their
  cross-space routes and re-run the case-23 constancy tracer rather than hoisting it to org-wide
  middleware.

### W2 — Versions and phase history (§9, Appendix B)

#### D24 — P1.5 is absent from the phase history

- **Repo says:** P1.5 merged as PR #52 and shipped this reconciliation document, the
  `WorkflowAdminPage` fix, a Playwright locator-integrity audit, and the CI coverage floor raised
  from 70 to 80. It has no version number and no tag of its own; it rode into `v0.3.1`.
- **Changed:** recorded in §9 as history.

#### D25 — P2.5 is absent from the phase history

- **Repo says:** P2.5 merged as PR #54, took migrations 024–025, and delivered session control,
  the invite lifecycle, the people lifecycle, bulk grants and the entire `/admin` area —
  including the release's headline breaking change, open registration defaulting off. The string
  `P2.5` appears nowhere in the specification, while appearing across code, migrations and other
  docs.
- **Changed:** recorded in §9 as history, including that the `org-admin-404` guard class enters
  here.

#### D26 — §9's P3 entry named the wrong migration

- **Spec said:** "ADR-0008. Migration 024".
- **Repo says:** P3 shipped `026_entity_shares.sql` and `027_attachments.sql`.
- **Changed:** corrected to 026–027, with attachments noted as unplanned-for.

#### D27 — Two phases claim version v0.3.2 — **FLAGGED, NOT RESOLVED**

- **Repo says:** two merge subjects claim `v0.3.2` — `feab5b0` (P2.5, #54) and `6aaece7`
  (P3, #56). Established from git:
  - the `v0.3.2` tag's own annotation reads "administration, users, and the access matrix (P2.5)";
  - `git merge-base --is-ancestor` puts **P2.5 inside** the tag and **P3 outside** it;
  - `git tag --contains` on P3's merge returns **nothing**;
  - five further places map v0.3.2 to P2.5 (`docs/upgrade.md`, `docs/self-hosting.md`, the headers
    of migrations 024 and 025, `scripts/verify-api.sh`); exactly one — the spec's own P3 heading —
    maps it to P3.
- **Changed:** the conflict is stated plainly in §9 with the evidence, and **left open**. On the
  evidence the released v0.3.2 is P2.5 and P3 claimed a number already taken, but that is a
  statement of what the repository says, not a decision.
- **Consequence noted, not applied:** if the maintainer gives P3 its own version, P4–P6 all shift.
  **This pass did not renumber them.** Renumbering the roadmap is not a documentation correction.

#### D28 — The `v0.3.2` tag is one commit past its intended target

- **Repo says:** the tag points at `eab99b0` (#55, a 314-line prototype HTML file), not at
  `feab5b0` (#54, P2.5) which its annotation describes. It was created 35 seconds after #55
  merged, against whatever `main` then pointed at.
- **Changed:** recorded in §9 as a separable defect from D27. Content impact is one static file.

#### D29 — P3 and everything after it is untagged

- **Repo says:** `git describe origin/main` reads `v0.3.2-4-g92f60e1`. Four merged commits sit
  past the newest tag — P3 (#56, migrations 026–027), the security fix (#58), the ADR directory
  (#57), and migration 028 (#60). (Measured against `origin/main`; on a feature branch `describe`
  counts that branch's own commits too.)
- **Changed:** recorded in §9. Whether to cut a tag is a maintainer decision, untouched here.

#### D30 — Appendix B row C4 carried a consumed migration number

- **Spec said:** C4 "PostgreSQL FTS, generated stored columns, migration 027".
- **Changed:** number marked unassigned (027 was consumed by P3). Decision content untouched.

#### D31 — §9's P4, P5 and P6 entries carried consumed migration numbers

- **Spec said:** P4 "Migration 025", P5 "Migration 026", P6 "Migration 027".
- **Changed:** P4 → 029; P5 and P6 → unassigned. **Only the migration numbers changed. The
  version numbers in those headings were deliberately left alone** — see D27.

### W3 — ADR extraction

#### D32 — ADRs 0005–0010 lived inside the specification, not in `docs/adr/`

- **Repo said:** the ADR index (added in #57) listed all six with location
  `../design/v0.3-ia-spec.md §3`, and noted they should be extracted.
- **Changed:** each extracted to `docs/adr/000N-slug.md`. The body of every new file was verified
  mechanically to be a byte-identical substring of the specification; only a status and provenance
  header was added. §3 is now a pointer table. Every index row points at a file.
- **Reference check:** a sweep for `ADR[\s-]?00(0[3-9]|1[0-2])` across the tree at `92f60e1`
  returned 167 occurrences in 88 files. **Every one cites an ADR by number, never by location.**
  (Counts move with the pattern and the commit — a hyphen-only `ADR-00NN` at the same commit gives
  174/90 — so treat them as scale, not as a fixture.) The only location-bearing references were the
  six index rows in `docs/adr/README.md`, which this pass updated. **No live reference resolves to
  the old location**; the six extracted files each carry an `**Origin:**` line naming
  `v0.3-ia-spec.md` §3, but that is provenance, not a pointer, and §3 is now a pointer table.

### W5 — Rules

#### D33 — §10 forbids the git operations every phase performs — **FLAGGED, NOT RESOLVED**

- **Spec said:** §10, Repository: "Agents perform **no git operations** — no commits, pushes,
  tags, or branch changes."
- **Reality says:** every phase since P0 has branched, committed, pushed and opened its own PR,
  under explicit instruction in its prompt. The autonomy envelope in use is narrower and
  different: never `main`, never force-push, never self-merge, never tag.
- **Changed:** nothing in §10. `CLAUDE.md` records §10 as authoritative, documents the envelope
  actually in force, and **flags the conflict for a maintainer** rather than quietly writing
  practice down as the rule. This is exactly the "would change a decision → stop and raise it"
  case from §4 of this document.

### W6 — Known issues

Seven entries were resolved by work merged in P1–P3 and never struck; two more carried premises
that the repository has since falsified.

| # | Was | Repo says | Action |
|---|---|---|---|
| D34 | #2 "coverage below 60% floor" | CI enforces **80%**; P3 merged at 80.2% | Struck |
| D35 | #3 "ensure the CI runner has GCC" | CI installs build tooling and runs `-race` | Struck; local constraint moved to `CLAUDE.md` |
| D36 | #6 "partially mitigated — see CLAUDE.md" | Permanent fix shipped; the cited `CLAUDE.md` did not exist | Struck |
| D37 | #9 "audit logger discards all events" | `audit.NewDBLogger` persists; wired in `main.go`; six integration tests | Struck |
| D38 | #10 "no profile update endpoint" | `PATCH /api/v1/auth/me` exists end to end | Struck |
| D39 | #13 "smoke login_user fails" | Subtest mints a unique address | Struck |
| D40 | #16 "object storage not wired" | P3 wired it; migration 027; five integration tests | Struck (backend); UI gap noted in place |
| D41 | #14 "`items.labels`" | `items` was renamed `items_archive` in 015; the live columns are `tickets.labels` and `project_items.labels` — two arrays, not one | Premise corrected |
| D42 | #15 "blocked by `item_relations` FK to `items`" | **False, and already false when written.** Migration 015 dropped both FKs, added `from_type`/`to_type`, and renamed the table to `entity_relations`. Only the ticket *endpoints* are missing | Root cause corrected; deferral instruction withdrawn |
| D43 | #9 and #10 cite `docs/project-state.md` as a live reference | The file is `.gitignore`d as "private repo only — never push to public", so it is unreachable from this repository by design — not merely absent | References annotated as dead links |

#### D44 — `CLAUDE.md` has never existed because it is `.gitignore`d — **RESOLVED BY MAINTAINER DECISION**

- **Every phase prompt since P0** has opened with "read `CLAUDE.md`". P1.5 recorded its absence as
  an observation ("stale copies exist only on old worktrees/deleted branches") without finding the
  cause.
- **The cause:** `.gitignore` line 55 lists `CLAUDE.md`, inside a block headed
  **"CI progress tracking (private repo only — never push to public)"** — together with
  `docs/agent-briefs.md`, `docs/github-setup-checklist.md`, `docs/project-state.md`,
  `docs/regression-test-checklist.md` and the `push-private.sh` / `push-public.sh` scripts.
- **Consequence:** `CLAUDE.md` could not be created in this repository by writing the file.
  `git add` silently skipped it — no error, no warning. Every agent told to read it has been
  reading nothing, and every phase has therefore run on prompt-embedded rules alone, which is
  exactly the failure mode W5 was meant to end.
- **Caught in this branch's own history.** Commit `d30bce8` is titled "add shared-surfaces.md and
  CLAUDE.md" and its diffstat contains one file. That is the silent skip happening in real time,
  and it is left in the history rather than rewritten, because it is the clearest evidence of the
  defect.
- **Why this needed a decision:** un-ignoring it reverses a recorded decision that this path is
  private-repo-only, and publishes to a public repository a file the repository explicitly marked
  "never push to public". That is a decision, not a fact, so it was raised rather than taken
  unilaterally.
- **Decision taken:** the maintainer authorised removing `CLAUDE.md` from `.gitignore`. The line is
  removed, the reason is recorded in place in `.gitignore`, and the file is committed. The rest of
  the private-only block is untouched.
- **Side effect worth knowing:** because `.gitignore` is neither under `docs/` nor a `*.md` file,
  this PR is **not** classified docs-only, so the full CI pipeline runs rather than cascade-skipping.
- **Also explains D43:** `docs/project-state.md` is in the same block. The references to it from
  `known-issues.md` and from `internal/core/api/known_issues_test.go` are not sloppy — they point
  at a document that exists only in the private mirror.
- **What shipped:** a complete `CLAUDE.md`, assembled only from settled public material (spec §2
  and §10, the autonomy envelope, the real verification battery), audited to contain no secret and
  no unfixed-vulnerability detail. It must stay that way — it is now a public file.

### Found by adversarially verifying this pass's own output

The three below were found by fact-checking the corrections above against the repository, after
they were written. Two are defects in *this pass's own work*; they are recorded rather than
quietly fixed, because the failure mode is instructive.

#### D45 — §2.8's "no mocks exist" is false about the repository — **FLAGGED, NOT RESOLVED**

- **Spec says:** "**Real PostgreSQL only**, via `internal/testutil.NewTestDB(t)`. No mocks exist,
  none will be added." P1.5's record repeated it as "No mocks anywhere."
- **Repo says:** roughly thirty hand-written `mock*` types exist across eight Go test files —
  `internal/core/api/router_test.go` alone declares twelve — plus `vi.mock` usage in the frontend
  suite. They stub repository *interfaces* in handler and service unit tests; the real-database
  coverage lives in the `*_integration_test.go` files beside them.
- **Why it is not resolved here:** the sentence is half rule and half fact. The **rule** — never
  mock the database — is a §2 decision and is untouchable by a reconciliation pass. The **factual
  assertion** is simply wrong. Reconciling them means either deleting ~30 test doubles or amending
  §2, and both are decisions.
- **What changed:** nothing in §2. `CLAUDE.md` states the rule as a rule ("never mock the
  database") and carries a note recording the gap, so the rules file does not assert something
  the repository contradicts.

#### D46 — The corrected cross-space query shape was itself wrong on first writing

- **This pass first wrote:** `OR (space_id = ANY($shared_subtree_space_ids) AND path LIKE
  ANY($shared_subtree_like_patterns))`.
- **Why that is wrong:** two independent arrays match the **cartesian product**, not paired rows.
  With root A in space 1 and root B in space 2, a page in space 1 whose path sits under root B's
  subtree satisfies both halves. That is exactly the cross-space widening D19 was written to
  prevent — reintroduced by the sketch that claims to fix it, three lines above the paragraph
  forbidding it.
- **Changed:** the shape now uses `EXISTS (SELECT 1 FROM unnest(space_ids, patterns) AS
  root(space_id, pattern) WHERE pages.space_id = root.space_id AND pages.path LIKE root.pattern)`,
  which keeps each `(space_id, pattern)` bound together, plus an explicit warning that the pin must
  be per-root rather than per-query.
- **Also recorded:** `$shared_subtree_space_ids` cannot currently be populated —
  `CascadeRootPaths()` returns paths only and `cascadeRoot.spaceID` is unexported. P4 must add an
  accessor returning the pairs. Without that note, the obvious workaround is to bind paths alone,
  which is the defect again.
- **Lesson worth keeping:** a correction is not self-verifying. This one read as more rigorous than
  what it replaced while carrying the same class of bug.

#### D47 — §5's inventory of read paths was incomplete

- **This pass first wrote:** "everything else is single-space behind `RequireSpaceReadable`."
- **Repo says:** two shipped read paths are neither. The space directory
  (`GET /orgs/{org_id}/spaces`) is org-wide and filters per space in the handler, deliberately, so
  it can show locked `discoverable` rows a middleware 404 would hide. And `GET /notifications` is
  user-scoped, mounted outside the `/orgs/{orgID}` group, and consults no readable set at all —
  `notifications` carries no `space_id`.
- **Changed:** §5 now enumerates all three enforcement mechanisms. Whether a notification row
  should survive revocation of access to the entity it names is flagged as an open question, not
  answered.

**Total discrepancies found in this pass: 37** (D11–D47), spanning §4, §5, §9, Appendix B, the ADR
index, `known-issues.md` and `.gitignore`. Three are **flagged and deliberately unresolved**
because resolving them would change a decision — **D27** (version collision), **D33** (§10 vs the
autonomy envelope) and **D45** (§2.8 "no mocks exist"). A fourth, **D44**, was raised for the same
reason and resolved by an explicit maintainer decision recorded above. Two — **D46** and **D47** —
are defects in this pass's own corrections, caught by adversarial verification and fixed before
merge.

Cumulative across both passes: **47** (P1.5's D1–D10 plus these).

---

## 2. Claims re-verified accurate — no change needed

- **P1.5's D7 survives.** All six search identifiers still exist at the cited lines, and nothing
  in migrations 015–028 touched a search vector, index, function or trigger.
- **P1.5's D8 survives.** `pages.path` is still dot-separated `TEXT` from
  `012_pages_materialized_path.sql`; the array-overlap framing stays correctly retired. The
  corrections in D17–D19 refine the replacement, they do not reverse it.
- **P1.5's D6 survives.** `audit_log` still carries `entity_kind` / `entity_id` / `payload`.
  Migration 025 added only nullable `batch_id` and `ticket_ref`, with no backfill — the
  append-only contract is intact.
- **The entity-shares sketch was right about everything except one index.** All 12 columns and
  all five constraint names shipped exactly as sketched. `cascade` is literally the column name,
  unquoted — `CASCADE` is a non-reserved keyword and no workaround name exists anywhere.
- **The teams and grants sketches (022, 023) shipped with no contradictions.** `teams.path` is
  genuinely `UUID[]` with a GIN index; the depth-5 constraint, the polymorphic FK-less
  `subject_id`, and both vocabularies match.
- **No collision for the future sketches.** No `saved_views`, `dashboards` or `dashboard_gadgets`
  table exists in any migration.
- **§5's `effective_teams` via `path && $direct_team_ids`** is exactly what shipped, GIN-backed.
- **Share resolution filters `revoked_at IS NULL` and `(expires_at IS NULL OR expires_at > now())`
  in the resolution query**, so expiry and revocation deny on the next request with no sweeper —
  ADR-0008 rules 8 and 11 hold in code.
- **Highest-role-wins** is implemented as an ordered enum reduced in Go over the grant rows.
- **Per-request caching** is `context.WithValue` with an unexported key. No package-level cache
  exists, so the resolution genuinely cannot outlive the request.

---

## 3. Observed, out of scope for this pass

- **`internal/core/api/known_issues_test.go` contains four placeholder tests that assert
  nothing.** `TestAuditLog_PersistsEvents`, `TestProfileUpdate_SavesChanges`,
  `TestRSAKey_SurvivesRestart` and `TestCORS_RestrictedInProduction` are empty functions taking
  `_ *testing.T`. They are not skipped — they pass unconditionally. `known-issues.md` cited two of
  them as evidence of tracked defects. Real coverage for all four exists elsewhere, so this is
  misleading bookkeeping rather than a coverage hole, but it fails §2's negative-test question
  outright. This pass changes no code; flagged for a maintainer.
- **The regression test for known-issue #11 asserts `409 || 500`.** It documents the defect
  instead of catching it, and would pass if the bug were fixed *or* if it got worse. Same
  category as above.
- **Known-issue #11 remains genuinely open.** `UserAdapter.Create` still has no `23505` mapping,
  though the helper that would do it exists and is unused on that path.
- **Known-issue #4 remains genuinely open.** None of `memberships`, `space_members` or `sprints`
  has gained `deleted_at`.
- **`docs/project-state.md` is referenced from `internal/core/api/known_issues_test.go` as well**,
  and does not exist. Code comment, not documentation; not changed here.
- **`idx_spaces_org_key` remains org-wide** after migration 028 made slugs per-module. Whether
  space keys should also become per-module is a product question, not a documentation one.

---

## 4. Standing instruction — reaffirmed, with one addition

The P1.5 instruction stands unchanged: repository wins on **existing** structures, correct the
spec in the same PR and append here; a disagreement that would change a **decision** means stop
and raise it.

**Addition, learned from this pass:** the §4 migration table is a *plan*, and a plan is not a
record. It has now been wrong twice for the same reason — a phase that was not in the plan took
numbers first. **Read `migrations/` before assigning a number.** The same caution applies to
constraint and index names: PostgreSQL generates names, and it does not rename a table's indexes
when the table is renamed. Both facts have already produced defects here.

---

**Date:** 2026-07-23
**Session:** Vector Completion Part 1 — schema package (item keys, item types, custom fields)

## 1. Discrepancies found and corrected

### D48 — `item_fields` storage did not exist (phase brief premise, not the spec)

The phase brief for this work stated *"The `item_fields` storage already exists — build the UI
and admin surface over it."* It does not. There is no `item_fields` table, nor any custom-field
value or definition storage, anywhere in `migrations/` or `internal/` (grep for `item_field`,
`custom_field`, `field_value` returns nothing before this phase). The spec (`v0.3-ia-spec.md`)
does not mention `item_fields` at all, so there is no spec text to correct — the claim originated
in the prompt. Per the standing rule (repository wins on existing structure), custom fields were
built from scratch: migration 033 introduces **both** `custom_field_defs` (org-scoped
definitions) and `item_field_values` (per-item values). The brief anticipated the definitions
table ("if no definitions table exists, one is in scope") but assumed the value store already
existed; both were needed. Values are keyed by field slug rather than a FK to the definition, so
a value survives the archival or deletion of its definition and is surfaced read-only as a
"legacy field" — the zero-silent-data-loss principle.

### D49 — `project_items.kind` is the item-type identity; the CHECK is gone (§4 area, ADR-0003)

The brief's V2 said to add a `type` column with existing items "backfilled to `task`".
`project_items` already carries `kind TEXT` with a four-value CHECK (`task`/`story`/`epic`/`bug`)
since migration 014. Blanket-backfilling every item to `task` would have discarded that real
data. Repository wins: `kind` **is** the type discriminator (ADR-0003 keeps the type a column,
not a joined entity), so migration 032 repurposes it as the immutable type *slug*, drops the
fixed CHECK (types are now org-editable), and seeds `item_types` from the existing set — item
rows are preserved, not reset. The wire field stays `kind` to avoid a rename across the whole
API + frontend; the admin surface presents it as "Item types". Flagged, not a decision change:
ADR-0003 explicitly sanctions the column approach.

## 2. Decisions taken (justified in the phase report, recorded here)

- **Item key counter:** a per-space `project_item_sequences` row bumped by an atomic `ON CONFLICT`
  upsert inside the same `CreateProjectItem` statement, replacing the racy `MAX(number)+1`.
- **Item-key org-uniqueness:** `org_id` was denormalised onto `project_items` (it had none) to
  back a `UNIQUE (org_id, item_key)` index and give the future importer a single-lookup key map.
- **Type / custom-field scope:** both are **org-scoped** (like labels and item_types), keeping the
  admin surface in `/admin`. A future phase may make custom fields space-scoped if projects need
  divergent field sets; noted, not built.
- **Referential integrity for types** is enforced in the item-types service (a referenced type
  cannot be hard-deleted → 409), not a DB FK, so ordinary item inserts are not coupled to per-org
  type seeding — which would otherwise break every fixture that inserts an item after a raw org.

## 3. Observed, out of scope

- **Filters by type on the backlog/board were not added.** The type picker, chip, and admin CRUD
  ship; a type *filter* control is a small follow-up, flagged for the next Vector phase.
- **Custom-field values have no typed columns** — one `TEXT value`, interpreted by the
  definition's `field_type`, validated on write. Sufficient for text/number/date/single_select;
  a future phase adding richer types may want typed storage.

---

**Date:** 2026-07-25
**Session:** Codex editor phase 1 — the document model (issue #15, ADR-0012), PR-A

## 1. Discrepancies found and corrected

### D50 — the Codex edit button is not a dead control, and page locks are live (phase brief premise)

The phase brief for this work stated that *"the wiki edit button has been a dead control since
v0.1.x"* and listed **"No page locks"** among its scope decisions, as though locks were something
this phase might otherwise add. Neither is the repository.

`web/src/pages/codex/WikiPage.tsx` has a working Edit button (`startEdit`, line 302) that opens a
TipTap-based editor (`web/src/components/ui/MarkdownEditor.tsx`, ~437 lines, markdown round-trip via
`tiptap-markdown`) and saves through `useUpdateWikiPage` with `expected_version` — so **optimistic
concurrency on pages already shipped**, in `internal/db/queries/pages.sql`'s `UpdatePageContent`
(`WHERE id = $1 AND version = $2`), with a purpose-built 409 body
(`internal/core/wiki/conflict.go`'s `ConflictDetail`). `web/e2e/wiki.spec.ts` has covered the whole
journey since P1 under the name *"wiki edit button opens editor and edit persists"*, and its comment
already says "TipTap-based rich text editor".

`page_locks` (migration 013) is equally live: `internal/core/wiki/lock.go`, four sqlc queries, three
routes, wired in `cmd/server/main.go`. The lock is **advisory only** — `UpdatePage` never consults
it, so it has never protected a write; it drives the "X is editing" banner.

Per the standing rule (repository wins on existing structure) the phase was executed as a
*replacement* of a working editor rather than a build from zero, which is what set its blast radius.
Two consequences worth recording:

- The reusable conflict shape already existed, so publish reports version conflicts through
  `ConflictDetail` rather than a second format.
- Because the lock never guarded a write, the new document flow simply does not acquire one, and
  nothing about write safety changes. **The lock table, service and routes are left in place and
  untouched** — removing shipped API is a maintainer's decision, not a side effect of an editor
  phase. See section 3.

### D51 — ADR-0012 governs nodes and is silent on marks and inline content

ADR-0012 names one preservation primitive, `unknownContent`, and describes it as a node. It never
uses the word "mark". ProseMirror drops an unrecognised **mark** and an unrecognised **inline node**
exactly as silently as it drops a block, and a node type cannot be both block and inline, so one
primitive cannot cover all three positions content can occupy.

This phase implements three — `unknownContent`, `unknownInline`, `unknownMark` — through one
mechanism. That is an **interpretation of the ADR, not a quotation of it**, and it is flagged here
rather than resolved: the ADR's Decision heading is "Zero silent data loss", which the narrow
reading would contradict, and the inline case is not hypothetical — Codex's shipped markdown editor
serialises text colour and highlight as inline `<span>` HTML, so real pages in this repository
already contain content that the narrow reading would destroy on first edit.

**For a maintainer:** confirm ADR-0012 accordingly, or narrow it. If the narrow reading is intended,
`unknownInline` and `unknownMark` should be removed and the loss documented as accepted.

> **CLOSED — 2026-07-27, security & integrity pass (S1).** The maintainer confirmed the broad
> reading on 2026-07-25. ADR-0012's Decision section now names all three carriers
> (`unknownContent`, `unknownInline`, `unknownMark`) with the position each covers, states that the
> three-way split is the substance of the guarantee rather than an implementation detail, and
> extends the round-trip requirement and its Consequences bullet to marks. The three shipped
> carriers stand as-is; nothing was removed.
>
> One correction carried with it: this entry says, in the present tense, that Codex's *shipped*
> markdown editor serialises colour and highlight as inline `<span>`. That editor was deleted in
> PR #75. The justification is unaffected — the pages it wrote still hold that inline HTML — but
> the tense was wrong here and in three code comments, and all four are corrected.

### D52 — the section 4 migration table was stale for the third time

It said `029` was the next free number. Migrations 029–035 were already on disk. Corrected in the
same PR, with the shipped rows filled in from the directory and the pattern named.

### D53 — `shared-surfaces.md` listed five `assertNoErrors` strings; the helper asserts six

`web/e2e/helpers/setup.ts` has failed on `"could not be loaded"` since the interior restyle, and
section 2 of `shared-surfaces.md` did not list it. The repository wins; the doc is corrected in the
PR that discovered it (issue #15 PR-B).

Not a cosmetic omission. It is the string most easily written by accident, because it is the shape
almost every `friendlyErrorMessage` fallback takes, and a contributor checking the documented list
before writing new copy would have concluded it was safe. This phase's image-failure copy read
"This image could not be loaded" until the list was checked against the helper — a page with a
broken image would then have failed `assertNoErrors` on every spec that navigated past it, and
been reported as a broken *page*.

### D54 — the markdown save path leaves `doc` stale on a document-backed page

`PUT /orgs/{o}/spaces/{s}/wiki/{pageID}` writes `content` and bumps `version` through
`UpdatePageContent` (`internal/db/queries/pages.sql`), which does not touch `doc` — it predates the
column and PR #73 did not extend it. On a page that holds a document, that produces a row whose
`content` and `doc` disagree, with `doc` still authoritative for the editor: the next person to
open the page sees the *old* document, the markdown edit having vanished from the editor while
remaining in search. The `page_revisions` row written for that version also carries `doc IS NULL`,
so a later overwrite resolving against it falls back to `FromMarkdown` and reconstructs a document
from the lossy projection.

**This phase removes the UI that reached it.** The markdown editor is gone and every Codex edit now
goes through the document surface, so no user-reachable path in the application produces the
divergence. The route is unchanged and still reachable by an API client, which is why this is
recorded rather than treated as closed.

Distinct from the entry in section 3 about that path's non-transactional revision write: this is
about *which columns it writes*, not about atomicity. **For a maintainer:** either extend
`UpdatePageContent` to clear or update `doc`, or refuse a markdown save on a page where
`doc IS NOT NULL`. Refusing is the smaller change and the more honest one — a markdown PUT against
a document-backed page is a category error, not a partial update.

> **CLOSED — 2026-07-27, security & integrity pass (S3).** Refused, as recommended.
> `ContentTxAdapter.UpdatePageContentTx` returns `wiki.ErrPageIsDocumentBacked` — HTTP 409 — when
> the locked row has `doc IS NOT NULL`. The test is strictly that, so a page that has only ever
> held markdown, including one open in the Codex editor but never published, still takes markdown
> saves; `web/e2e/codex-editor.spec.ts` depends on that and stays green. Covered by
> `TestUpdatePageContentTx_RefusesPageThatHoldsADocument` (real database, fails with the guard
> removed), `TestMarkdownSave_RefusesPageThatHoldsADocument` (HTTP) and
> `TestUpdatePage_RefusesDocumentBackedPage` (unit).

### D55 — ADR-0012 was not amended, so D51 is still open

The brief for this phase stated that ADR-0012 had been amended to cover marks and inline content
and that D51 was confirmed. It has not been. `docs/adr/0012-content-fidelity-and-unknown-nodes.md`
is unchanged since it was added in PR #57 (`89c4915`), contains no occurrence of the word "mark",
and D51 above still reads as an open question for a maintainer.

Nothing was blocked by this and nothing was decided because of it. PR #73 already shipped all three
carriers, and this phase builds the UI onto what shipped; the editor would be identical under
either reading, except that a narrowed ADR would mean deleting `unknownInline` and `unknownMark`
rather than rendering them. **The ADR is not edited here** — amending an ADR is a decision, and
CLAUDE.md section 5 sends decision-level disagreements to a maintainer rather than resolving them
in passing. D51 stands as written and still wants an answer.

> **CLOSED — 2026-07-27, security & integrity pass (S1).** The amendment was made, under an
> explicit maintainer instruction recording the broad reading as confirmed on 2026-07-25. See the
> resolution note on D51.

## 2. Decisions taken (justified in the phase report, recorded here)

- **`pages.doc` is PostgreSQL `json`, not `jsonb`.** `jsonb` is a parsed, normalised value: it sorts
  object keys, rewrites number literals, and silently drops duplicate keys. Verified against the
  test database — `{"zzz":1,"a":{"n":1e2,"dup":1,"dup":2}}` loses the `"dup":1` member entirely.
  That is silent data loss at the storage layer, which is the failure ADR-0012 exists to prevent. A
  test in `internal/core/wiki/doc` asserts the round trip against the real database and fails if the
  type is changed, so the choice cannot be undone silently. Nothing queries inside the document —
  search reads the projected `content` column — so the GIN indexing `jsonb` would buy has no
  consumer here.
- **`pages.content` stays, stays markdown, and becomes derived for document-backed pages.** It feeds
  the generated `search_vector` (migration 009 spans title + content), so dropping it would silently
  empty the wiki's search index; and it keeps every legacy reader working. Nothing reads it back
  **into** the editor when `doc` is present — that direction is where a lossy projection would become
  data loss.
- **Conversion is per-page and on first edit, never a bulk migration.** A backfill would rewrite
  every page in one unreviewable step, so a conversion defect would land on all of them at once
  instead of on the one page an author is looking at.
- **The round-trip guarantee lives server-side.** The bytes written back are the bytes that were
  read; they never pass through the client. A placeholder carries a display copy of its original so
  the editor can label the block, and `Restore` ignores it.
- **Publishing refuses to drop preserved content unless the removal is acknowledged.** Deleting an
  inert block is a legitimate edit, so it must be possible — but it must be *said*, because
  otherwise a schema-level drop is indistinguishable from an intentional deletion. This is the one
  place the ADR-0012 catastrophe can be caught in the act.
- **`page_drafts` is keyed `(page_id, author_id)`** so one draft per person per page is a property of
  the key rather than of application code. No `space_id`, for migration 027's reason: authorisation
  derives from the page.
- **Publishing is one transaction** (page row + history row + draft clear), via a new
  `PublishPageTx` on the existing `ContentTxAdapter` — extending the established transactional seam
  rather than forking one. Shared-surfaces convention B: the atomicity is the contract.
- **No new capability.** Drafting and publishing are the same permission as editing —
  `access.CanEditEntity` against `pages.author_id`, the same call the markdown save path makes.
- **Image content types are sniffed, never taken from the client**, against the same allow-list as
  the avatar surface (PNG/JPEG/WebP/GIF). `image/svg+xml` is excluded deliberately: an SVG can carry
  script and attachments are streamed inline from our own origin.

## 3. Observed, out of scope

- ~~**The page-lock routes are now unused by the shipped editor and remain wired.**~~ **Closed by S2**
  (security & integrity pass, 2026-07-27) under an explicit maintainer instruction to retire them.
  Routes, service, queries, table (migration 037) and the three dead frontend hooks are gone.
- ~~**`internal/core/wiki/render.go` claims to produce "sanitised HTML" and no sanitiser exists.**~~
  **Closed by S4** (2026-07-27). The docstring states the real mechanism and
  `TestRenderHTML_RawHTMLIsNotPassedThrough` fails if anyone adds `html.WithUnsafe()`.
- **The markdown save path's revision write is still not transactional.**
  `internal/core/wiki/page.go` updates the page and then inserts the revision as two separate calls
  against a pool, so a failed revision insert leaves a committed page whose history skips a version.
  The new publish path is transactional; the old one is unchanged, and is a defect either way.
  > **CLOSED — 2026-07-27, security & integrity pass (S13).** Both writes now commit together in
  > `ContentTxStore.UpdatePageContentTx`, following `PublishPageTx` and shared-surfaces convention
  > B. `TestUpdatePageContentTx_RevisionFailureRollsBackThePageRow` injects the failure through
  > `page_revisions.author_id`'s foreign key and asserts the page row rolled back with it.
- **`internal/core/api/docs_test.go` skips `TestDocsSpec_InSyncWithCode`** with no `SKIP:` marker, no
  issue number and no re-enable condition — a section 2 skip-discipline violation — and its stated
  reason ("handled by ... CI pipeline") is not true: CI's `docs-check` job only greps the committed
  YAML for a few required keys. The real check is the local `make docs-check`, which this PR ran.
  > **CLOSED — 2026-07-27, security & integrity pass (T2, then T1).** The skip became
  > `TestDocsSpec_EveryRouterPathIsDocumented` in the first integrity PR, which walks the live
  > router and fails on any path the committed spec omits. Its `undocumentedRoutes` ledger of
  > nineteen known gaps is now empty and deleted (N1). Separately, CI's `docs-check` job runs
  > `make docs-check` — it regenerates from the annotations and diffs — instead of grepping for
  > structural markers; the grep is kept as a floor under it.
- **Attachment `content_type` is client-declared on the generic upload route** and is echoed as the
  `Content-Type` of an inline, same-origin download. A space writer can therefore upload HTML
  declaring `text/html` and have it served as a page — reachable by a share recipient outside the
  space. This phase closes the hole for the paths it owns (page images sniff on upload, and publish
  re-sniffs every image a document references) but does **not** change the generic route, because
  sniffing every attachment would change the served type of legitimate non-image files.
  **Flagged for a maintainer as a security follow-up.**
  > **CLOSED for attachments — PR #74**, which introduced `attachments.ServeTypeFor`: the served
  > type is sniffed from the object's own bytes at serve time and anything off the inline
  > allow-list downloads as `application/octet-stream`.
  > **CLOSED for avatars — 2026-07-27, security & integrity pass (S7).** The avatar serve path had
  > the same shape and was missed: it returned the sniffed type unchecked, with
  > `Content-Disposition: inline` and `X-Content-Type-Options: nosniff`, so a stored object
  > sniffing as `text/html` rendered as a document on the app's origin for any org member. It now
  > checks the sniffed type against `doc.SupportedImageType` and refuses anything else.

### Importer-relevant notes (ADR-0012 anticipates an importer; these are its constraints)

- **An importer must write a `page_revisions` row alongside `pages.doc`.** An overwrite-after-conflict
  recovers its base document from history, so a document that reached `pages.doc` without a matching
  revision cannot be recovered from that version and the publish is refused (422). The behaviour is
  correct — refusing beats guessing — but it makes the paired write a requirement.
  `TestDocumentAPI_OverwriteRefusesWhenTheBaseVersionHasNoDocument` pins it.
- **Unknown content should be captured with `az_source` naming the source system**, not the
  `"document"` value this package produces. `doc.SourceDocument` exists so the two are
  distinguishable.
- **Go's `encoding/json` HTML-escapes a `json.RawMessage` it marshals**, and compacts it. An importer
  that round-trips documents through `json.Marshal` will silently alter their bytes. The `doc`
  package emits nodes through its own writer for exactly this reason.

---

# P4 — Saved views (migration 038)

## 1. Discrepancies found and corrected

### D56 — the section 4 migration table was stale for the fourth time

It assigned `029_saved_views.sql` to this table. Migrations 029–037 were all on disk before P4
started, taken by phases that were not in the plan, so saved views ship as **038**. Corrected in
§4 in the same PR that found it.

This is the fourth occurrence (see D52 and the two before it), and the pattern is now well enough
established to state as a rule rather than a coincidence: **the migration table in a design
document is a forecast, not a fact.** Read `migrations/` and take the next free number. Nothing is
renumbered here; the sketch's number was never shipped.

### D57 — the §4 sketch's `visibility_team_id` FK would destroy saved views

The sketch declares:

```sql
visibility_team_id UUID REFERENCES teams(id) ON DELETE CASCADE,
CONSTRAINT saved_views_visibility_team_present
    CHECK (visibility <> 'team' OR visibility_team_id IS NOT NULL)
```

On a nullable column, `ON DELETE CASCADE` deletes the **row**, so deleting a team would delete
every saved view shared with it — somebody else's saved work, destroyed as a side effect of an
unrelated administrative action. The CHECK then makes the gentler alternative impossible too: with
it in place, nulling the column on team deletion is itself a constraint violation.

Both are contrary to ADR-0009's own degradation rule (decision log **C1**): *"A saved view whose
scope team or space was deleted is marked invalid, renders 'scope unavailable', and prompts its
owner to re-scope. It never errors."*

Migration 038 therefore uses `ON DELETE SET NULL` and **omits the CHECK**, because the
`(visibility = 'team', visibility_team_id IS NULL)` state has to be *representable* — it is
precisely C1's degraded state. The write-path invariant is enforced one layer up (the API refuses
to create or update into it) and resolution requires `visibility_team_id IS NOT NULL` before a
team audience can match, so a degraded view is visible to nobody but its owner. Fail closed, then
prompt.

`TestSavedViewStore_RoundTripAndTeamDegradation` fails if the FK is changed back to CASCADE.

### D58 — the §4 sketch's `is_valid` column needs a writer that does not exist

The sketch carries `is_valid BOOLEAN NOT NULL DEFAULT true` and says it "goes false when the scope
team or space is deleted". Nothing says *who* sets it. A stored copy of derivable state needs some
sweeper or cross-domain hook to remember to flip it, and its failure mode is stale in the
safe-looking direction — marked valid while the scope is gone.

Validity is **derived at read time** instead, for a whole page of views in one query
(`ListLiveSpaceIDs`), so it cannot go stale and costs one constant query rather than a background
job. The column is not created.

### D59 — `shared-surfaces.md` said the route table holds 142 rows; it held 165

Counted from `route_accounting_test.go` at the start of P4. It holds 172 after this phase's seven
routes. Corrected, and the section now says what the count is *as of* a phase rather than
"currently" — which is the wording that went stale.

### D60 — the harness guards have a seam, and a whole feature can fall through it

`TestHarness_NoDarkDependencies` skips a nil handler, commenting that "a nil handler means the
surface is deliberately unmounted, which the route-accounting sweep already covers". It does not.
`TestReadPathSweep_EveryRouteAccounted` walks the router built by `newTestServerOn` — the same
router — so a handler left nil there contributes no routes to the walk, requires no accounting
rows, and is invisible to both tests simultaneously.

This is the dark-harness failure one level up: not a mounted handler missing a collaborator, but an
entire feature that exists in production and in no test. **P4 walked into it live**: the seven
saved-view routes were added and mounted in `cmd/server/main.go`, the sweep stayed green, and
nothing reported that the routes were absent from every test.

Closed by `TestHarness_NoUnmountedSurfaces`, which requires every `RouterConfig` handler field to
be mounted in the harness or named in `unmountedInHarness` with a reason. `unmountedInHarness` is
empty. Recorded in `shared-surfaces.md` §5.

## 2. Decisions taken (justified in the phase report, recorded here)

- **The filter document is a record, not a query language.** The §4 sketch models it as a list of
  `{field, op, value}` predicates over an open operator set (`eq`, `in`, `gt`, `contains`, …).
  That is a query language: it has a grammar, its field names come from the caller, and every
  consumer needs an evaluator for it. The phase brief locked the opposite (2026-07-28), and
  ADR-0011's reasoning about arbitrary scripting applies unchanged — a query you cannot reason
  about statically is one you cannot index, explain, bound or migrate. What shipped is a closed
  set of named fields, AND-ed with each other and OR-ed within themselves, defined in
  `internal/core/views/filter.go` and nowhere else. §4 is corrected to describe it.
  **ADR-0009 is untouched** — it never specified the document's shape, so this is not an ADR
  change.

- **`query` is `jsonb`, not `json`.** The opposite of migration 036's choice, for the opposite
  reason. `pages.doc` is `json` because it holds user content that must round-trip
  byte-identically (ADR-0012). A filter document is normalised configuration the server validates
  field by field and re-serialises from its own structs, so no caller byte is preserved and there
  is nothing to lose to key reordering. Stated in the migration so neither rule is cargo-culted
  onto the other.

- **`kinds` and `sprint_ids` are Vector-only, and rejected rather than ignored.** Verified against
  the running database: `tickets` has neither a `kind` column nor a `sprint_id` column, while
  `project_items` has both (plus `item_key`, `org_id`, `parent_id`). A filter naming either while
  Beacon is in the module set can never match a ticket, so it is refused at write time — a view
  that returns an empty Beacon half forever is a defect its author cannot see.

- **No new capability for saved views; ownership governs.** Creating a private view reads nothing
  the caller could not already read, so gating it would mean a capability every role holds.
  Editing and deleting are the owner's, with the org-admin bypass. The brief said to stop and flag
  rather than invent a capability if sharing seemed to need one; it does not, and nothing was
  invented.

- **No audit events.** The spec §6 audit list is teams, memberships, grants, space visibility,
  owner team and shares — every one an access change. A saved view grants nothing: sharing one
  shares a query, and each viewer still resolves it against their own access.

## 3. Observed, out of scope

- **D46 is not closed by P4, and its premise does not apply here.** Spec §5 says "One thing P4 must
  build first": `SharedEntities` exposes `CascadeRootPaths()` but hides each cascade root's space
  id, so `$shared_subtree_space_ids` cannot be populated. That is true, and it remains true. It is
  also **not a blocker for P4 as scoped**: migration 026 constrains cascade to pages
  (`entity_shares_cascade_pages_only`), and this phase's views read tickets and project items
  only, so a share on either is always exactly one entity and `DirectIDs(type)` is complete.
  **P6 search reads pages and will still need the accessor.** The §5 note is corrected to say
  which phase actually needs it.

- **`ResolveAccessRows` still holds its own inline copy of the ADR-0007 team expansion.** Migration
  038 extracts that expansion into `effective_team_ids(org, user)` and the saved-view queries use
  it, but the hot path was deliberately left alone: it is the product's most frequently executed
  query and the subject of the §2.5 case-23 constancy tracer, and rewriting it for tidiness would
  put a performance- and security-critical query in a feature phase's blast radius. The two are
  pinned together by `TestEffectiveTeamIDs_AgreesWithGrantResolution`. **For a maintainer:**
  `ResolveAccessRows` could adopt the function in a change that is only about that, with the
  tracer re-run.

- **sqlc has two limitations that shaped this SQL**, recorded so they are not rediscovered: it
  cannot parse `COLLATE` inside a row constructor (it reports "edited query syntax is invalid"),
  and it cannot infer the type of a column derived from a `CROSS JOIN LATERAL` — a `column:`
  override does not reach it either, so the value arrives as `interface{}` and the adapter asserts
  it explicitly rather than defaulting.

- **Spec §7's frontend route table places views under `/:module/:spaceId/views/:viewId`.** A view
  spans modules and containers and its API is org-scoped per ADR-0010, so a space-scoped route
  cannot express one. The nav placement decision is in the phase report.

### D61 — the dead-link state cannot be rendered without either a permission oracle or a new read path

The Obsidian-affordances brief decided that a wikilink whose target id no longer resolves renders
in a distinct dead-link style, on the reasoning that "deletion is knowable; access-denial is not —
do not conflate the two states". The same brief also requires that "a link must render identically
regardless of whether the current reader can access the target. No permission oracle."

Both are right, and together they are not satisfiable by the client. The reading surface's page
list is **space-scoped** (`useWikiPages(spaceId)`), so "this page_id is not in the list" covers
three different situations at once: the page was deleted, the page was moved to another space, and
the page is in a space this reader cannot see. Styling on that basis would mark a perfectly good
moved link as dead, and — because the answer would differ per reader — would be exactly the oracle
the second rule forbids.

This phase therefore ships the unresolved-link half in full (a link carrying `target_title` renders
dashed and dimmed, and clicking it offers to create the page, or to open an existing one of that
name rather than blind-creating a duplicate) and **does not ship the deleted-target style**. A
resolved link renders identically for every reader and navigates; if the target is gone the
navigation 404s, which is the navigate-then-404 behaviour S1 established for notifications and
which the brief cites approvingly for the access case.

Doing it properly needs the server to say so, and it *can* be said without an oracle: deleted-ness
is an org-wide fact, not a per-reader one, so a document response could carry
`link_targets: {<page_id>: "live" | "deleted"}` computed independently of the caller. That is a new
cross-space read path (ADR-0010) and a wire-format addition, which is more than an input affordance
should decide on its own.

**For a maintainer:** either accept navigate-then-404 as the whole answer for both states, or
commission the reader-independent resolution field. The decision is which of the two brief rules
yields, and it is not one to take in passing.

### D62 — `[[` autocomplete is space-scoped, so three of the decided wikilink semantics cannot arise

The brief's wikilink decisions include "Autocomplete always shows space context per candidate",
"bare-title resolution prefers the current space, then disambiguates via the picker", and that
duplicate titles across spaces are legal and must be handled. They assume the candidate list spans
spaces.

It does not, and deliberately: the brief also says to "reuse the PagePicker's search path — same
permission filtering; do not write a second page search", and `PagePicker` filters the space's
already-loaded page list client-side. There is no cross-space page search in the product, and
adding one is a route-shape question ADR-0010 governs rather than a detail of an input affordance.

So the candidates are the current space's pages, the same set the page-include macro and the
internal-link button have always offered. There is consequently never cross-space ambiguity to
disambiguate. The candidate rows still carry a space label — a constant string today — because the
SHAPE is the load-bearing part: a page reference is only unambiguous with its space, and a list
that omitted the column would need redesigning rather than extending on the day the scope widens.

**For a maintainer:** widening this means a cross-space page search endpoint filtered against the
caller's readable set, at which point the three decisions above become reachable as written and
`pageSearch.ts` is where the widening lands.

### D63 — `useWikiDiff` declared a response shape the server has never returned

`web/src/lib/api.ts` typed `GET …/wiki/{pageID}/diff` as `{ diff: string }`. The endpoint has
returned `{from_version, to_version, title_diff, content_diff}` since it was written. Nothing
noticed because no component ever called the hook.

Worse than the mismatch: `title_diff` and `content_diff` were produced by `diffmatchpatch`'s
`DiffPrettyText`, which wraps insertions and deletions in ANSI **terminal** colour codes. Over a
JSON API consumed by a browser those are not colour, they are unprintable bytes in the middle of
the text — the endpoint could not have been rendered by any client.

Both are corrected here rather than flagged: the wire format is now structured segments
(`{op, text}`), which is what a surface needs in order to decide how to show a change, and the
hook's type matches. It is recorded because it is the second time a Codex endpoint has shipped
with a client binding that never matched it, and because "no consumer" is what let it happen —
an endpoint nothing calls is not covered by the fact that everything else is green.

---

# Backend test speed and the pre-cutover bundle (no migration)

## 1. Discrepancies found and corrected

### D64 — `shared-surfaces.md` §5 said the route accounting table held 172 rows; it holds 185

The sentence has now been wrong three times: it said 142 until P4 counted them, 172 until this pass
did, and 172 was already stale before this branch existed — P4 PR-B's seven queue rows landed after
it was written, the Codex UX pass (migration 040) net-added five, and this pass adds one
(`GET /api/v1/orgs/{orgID}/config`). 179 at this branch's merge base, 184 on `main`, 185 here.

Corrected to 185, and the sentence now tells the reader to count the map rather than quote the
number. A count that three consecutive phases have had to fix is a count that should not be
restated in prose at all, and the next phase to touch the table should not have to discover that
for itself.

### D65 — known-issues #19 claimed no test depends on two servers having different signing keys

The entry states that "no test asserts that two servers have different signing keys, and if one did
it would fail loudly rather than pass weakly", and used that to argue the hoist was risk-free.

One does. `TestJWTService_WrongKey` in `internal/core/auth` issues a token from one service and
requires a second to reject it. The entry examined `internal/core/api` — the package it was
proposing to change — and generalised to the repository. Handing that test the shared key twice
would have left it green while asserting nothing, which is the exact failure mode the entry was
reasoning about, one package over.

Corrected in the entry. The test now calls `freshTestKey` explicitly, twice, with the reason
written beside it, so the next person to see two keygen calls in a hoisted file does not tidy them
away.

### D66 — `AZIMUTHAL_TICKET_REF_REQUIRED` was documented nowhere

Not in `README.md`, not in `docs/self-hosting.md`, not in `.env.example` — while being the flag an
entire administrative feature (A2/A3, and now B3–B5) is built around, and the flag this pass builds
an HTTP endpoint to expose to the browser. An operator could not have discovered it except by
reading `internal/config/config.go`.

Documented in all three, with the self-hosting entry saying what turning it on actually costs: a
restart, and a 400 on every administrative mutation that arrives without a reference.

### D67 — the admin CLI silently discarded `AZIMUTHAL_BCRYPT_COST`

`azimuthal admin create-user` and `azimuthal admin reset-password` both load configuration and both
hash passwords, but neither builds a server, so neither reached the boot-time step that pushes the
configured work factor into `internal/core/auth`. An operator who raised the cost got the raised
one from the API and the old one from the CLI, writing hashes of two different strengths into the
same table, with nothing anywhere reporting it.

Found by the adversarial review of this pass's own first commit, not by a test — which is the point
of the fix. Every command now loads through one `loadConfig` that loads *and* applies, and
`TestCmdServer_NoDirectConfigLoad` fails on any file in the package that reaches past it to
`config.Load`. Modelled on `web/src/lib/no-direct-fetch.test.ts`, which enforces the same shape of
rule on the frontend's single API client.

## 2. Decisions taken (justified in the phase report, recorded here)

- **The bcrypt work factor is chosen by `testing.Testing()`, not by configuration.** A test binary
  hashes at `bcrypt.MinCost`; anything built by `go build` hashes at the configured cost, which
  `internal/config` and `auth.SetPasswordCost` both refuse to let below 12. The rejected
  alternative was an `APP_ENV=test` exemption on the config floor: APP_ENV is an ordinary
  environment variable a production deployment can hold any value of, so that exemption would have
  been a one-line downgrade of every stored password.
  `TestLoad_BcryptCost_FloorNotRelaxedByAppEnv` is the regression test for the design that was not
  taken. A build tag was the other candidate and would be the strongest ship-time guarantee, but it
  needs a second build dimension in the Makefile, `ci.yml` and `.golangci.yml`, and a forgotten
  `-tags` reverts the saving silently.

- **The ticket reference is resolved AFTER the authorisation gate on grants and shares.** The
  argument is not information leakage — resolve-first makes the 400 uniform, which leaks less. It
  is that turning `AZIMUTHAL_TICKET_REF_REQUIRED` on must not change any authorisation outcome:
  resolve-first replaces every 403 and 404 with a 400 for reference-less requests, which rewrites
  the endpoint matrix on a flag that is supposed to be about audit records. `invites` resolves
  first and is safe only because its subtree sits behind `RequireOrgAdmin404`; that is recorded
  rather than copied.

- **The in-transaction `share.revoked` events carry no ticket reference.** Revoke-on-move and
  revoke-on-delete are the system enforcing ADR-0008, not operator administrative changes;
  demanding a change reference for them would put a change-management gate on ordinary authoring.
  A maintainer who would rather they inherit the causing mutation's reference should say so — it is
  a much larger change (a reference on ticket, page and item mutations) and was not taken here.

- **`GET /orgs/{orgID}/config` is org-scoped for the membership 404, not because the values are
  per-org.** They are process-wide. The alternative, `GET /api/v1/config` with a `user-scoped`
  class, would be readable by any authenticated principal including one with no org membership at
  all. The URL therefore implies a scoping the values do not have; that trade is stated on the
  handler. If the flags ever become per-org, that is a migration and a different endpoint.

## 3. Observed, out of scope

### D68 — required mode is unusable from ten admin surfaces, and this pass did not fix them

`SpaceSettingsPage`'s grants panel and `ShareDialog` have no `TicketRefField` at all, so with the
flag on they now 400 — B3 gave those endpoints a gate the UI cannot satisfy. Separately, eight
existing controls mutate through handlers that *already* required a reference and send none: the
`PeoplePage` org-role select, its primary-team select, "Sign out everywhere", "Reactivate", invite
Resend, invite Revoke, and the `TeamsAdminPage` member add and remove. Wiring the `required` prop
cannot help a surface that has no field to wire.

**This is the remaining work before the flag can be flipped in a real deployment.** The S10
orchestration is proven end to end
(`TestTicketRefRequired_TeamWithAutoSpaces_OneReferenceCoversTheWholeOrchestration`); the admin UI
as a whole is not. It is UI work with its own blast radius rather than something to fold in under
this item's fence.

### D69 — the E2E harness cannot exercise required mode

`web/playwright.config.ts` forwards an explicit allow-list of variables to the spawned server and
`AZIMUTHAL_TICKET_REF_REQUIRED` is not on it, so the E2E server always runs permissive. There is
one `webServer` for the whole suite, so adding the flag would turn required mode on for every spec
at once, and several issue reference-less administrative mutations — they would all 400.

The cutover is therefore proven at the integration layer, against the real router and the real
database, rather than in the browser. **For a maintainer:** the alternative is a second Playwright
project with its own `webServer` and port, which doubles the slowest gate in CI to cover what the
Go integration tests already cover.

### D70 — `README.md` still lists `JWT_SECRET` as a required variable

There is no such variable. known-issues #7 records that JWT signing moved to an RS256 key persisted
in the database in migration 018, and that "there is no `JWT_SECRET` to configure". The README's
configuration table still shows it as **Yes**, required.

Untouched here because it is unrelated to this work and the table belongs to no one phase, but it
is actively misleading to an operator setting up a deployment: the one row marked required that
cannot be satisfied, because the variable it names is read by nothing.

# v0.4 — Workflow tiers (ADR-0011, migrations 046 & 047)

## 1. Discrepancies found and corrected

- **CLAUDE.md §3's "`npm run lint` is not a gate" has been false since #82.** The `Frontend`
  job runs it, with no baseline file and per-filename exemptions in `web/eslint.config.js`.
  The repository wins; §3's factual claim is corrected in place, and the reasoning it records
  about why a baseline would be an exemption ledger is kept.

## 2. Decisions taken (justified in the phase report, recorded here)

- **Tier enforcement lives at a chokepoint every status route enters, not on the workflow
  engine.** See D71. A guard on the engine alone would have been unreachable by every real
  user and bypassable through the route they do use.
- **Guards are typed rows, not a `config` jsonb.** Migration 038's own test decides it: a
  document is right when the invariant "is not expressible as a column constraint either way".
  Here `team_id` is a real foreign key and "which parameter belongs to which kind" is a shape
  CHECK, so columns win — 038's sentence pointing the other way.
- **`ON DELETE SET NULL` on every guard subject, never CASCADE.** CASCADE would make deleting
  a team silently *remove a restriction*. The degraded state is representable and
  unsatisfiable, so it fails closed until an administrator re-scopes it.
- **A pending approval does not move the item.** Moving to the target and back on decline
  produces an item that is *closed pending approval*, which defeats the gate — every board,
  queue and saved view reads status. It also keeps approvals from introducing a status value
  those surfaces would have to enumerate.
- **No new `Capability` for approving.** Authority is data — being a named approver, directly
  or through an ADR-0007 effective team. A capability would have changed the access model,
  which §5 makes a stop-and-raise decision, and "who approves change requests" is per-gate
  rather than per-role anyway.

## 3. Observed, out of scope

### D71 — the workflow engine governs almost nothing at runtime, and the routes that do are not the engine's

An item's status can change through four routes, running three different rule sets — or none:

| route | what validated it before this phase |
|---|---|
| `POST .../tickets/{id}/status` | a hardcoded Go map, `internal/core/tickets/status.go` |
| `POST .../projects/items/{id}/status` | **nothing** — `ItemService.UpdateItemStatus` wrote any string |
| `POST .../tickets/{id}/workflow-state` | the DB engine over `workflow_transitions` |
| `POST .../projects/items/{id}/workflow-state` | the DB engine |

`web/src/lib/api.ts` calls **only the first two**; a grep for `workflow-state` under `web/src`
returns nothing. So the `workflow_*` tables (migrations 016/019/029) describe a machine with no
client, while the routes users actually reach ran a duplicate rule set or no rule set at all.
No spec sentence asserted otherwise, so the repository wins and this is recorded rather than
corrected.

Consequence for later phases: **any rule about transitions must be enforced at a chokepoint all
four routes enter**, which is why `workflow.TierService.Gate` speaks status *text* as well as
state ids.

Two inherited defects sit alongside it, both flagged rather than fixed under a feature phase.
`tickets.status` and `workflow_state_id` **diverge permanently after any legacy `/status` call**,
because `UpdateTicketStatus` writes `status` alone — and the engine then validates the *next*
transition from a stale state. This phase stops a gated transition adding to the drift, but does
not repair rows that already drifted, and does not reconcile the two state machines.

### D72 — a new project item's default status is not a state in its own workflow

`project_items.status` defaults to `'open'` (migration 014) while the seeded project workflow's
states are `backlog`/`todo`/`in_progress`/`in_review`/`done` with `backlog` as `is_initial`
(migration 016). Nothing reconciles the two, so a freshly created item sits at a status naming
no state, and its **first** transition resolves no edge — meaning ADR-0011 guards, approvals and
post-functions silently do not apply to it. Every subsequent transition is gated normally.

Pre-existing and orthogonal to the tiers: the same mismatch already meant a new item's first
move was unvalidated by the DB engine. Recorded rather than fixed because the fix is either a
column-default change or a seed change, both of which touch live data and neither of which
belongs in a feature phase. `TestTierAPI_ItemPostFunctionCommitsWithTheStatus` documents the
behaviour by moving the item onto a real state first.

### D73 — reserved out-of-order migration numbers make goose refuse an existing database

`internal/db/migrate.go` calls `goose.UpContext` with no `AllowMissing`, so a database that
applied a HIGHER number before a LOWER one shipped will not migrate:

    found 1 missing migrations before current version 47: version 40

This phase hit it the moment migration 040 landed on `main`: every local database that had
already run 046/047 stopped migrating. Harmless for a fresh deploy, where 001–047 apply in
order, and CI is unaffected for the same reason — but it bites **developer and CI databases that
tracked a branch**, and it is a structural consequence of the parallel-track reservation scheme
(a phase takes 046/047 while 040–045 are still unshipped) rather than of any one phase.

The workaround is to recreate the database. The fix, if the project wants one, is
`goose.WithAllowMissing` — a migration-policy decision, so it is raised rather than taken here.

### D74 — the pre-existing org-scoped workflow routes do not scope `{workflowID}` to `{orgID}`

`GetWorkflow`, `UpdateWorkflow`, `DeleteWorkflow` and the state/transition routes resolve the id
and act on it, so a workflow in another org is reachable by id. Every route this phase adds calls
`GetWorkflowInOrg` first and answers 404 (`TestTierAPI_TierRoutesAreScopedToTheOrg`), so the new
surface does not widen the exposure; the existing routes are unchanged because changing them
alters live behaviour and needs its own regression pair.

### D75 — `is_default` is not unique per `(org_id, applies_to)`

`workflows` carries only `UNIQUE (org_id, name)`, and `GetDefaultWorkflow` is `LIMIT 1` with no
`ORDER BY`, so an org with two defaults for one entity type gets an arbitrary one — which this
phase turns into "which approval policy applies". A partial unique index would close it, but
could fail on existing data, so it is flagged rather than taken.
---

# P5 — Dashboards and the gadget registry (migration 048)

## 1. Discrepancies found and corrected

### D76 — the §4 migration table was stale for the fifth time

It assigned `039+` to this table. `039_beacon_queues.sql` shipped from P4 PR-B while P5 was being
planned, and 040/041 were reserved for a Codex phase running concurrently, so this phase was
assigned 042 and 043 and the migration was written as **042**. It shipped as **048** — see D81,
which is the more interesting half of this entry.

Fifth occurrence, and D56 already stated the rule: *the migration table in a design document is a
forecast, not a fact.* This entry adds one fact to it. While P5 was in flight, sibling worktrees
took **044, 045, 046 and 047** — a customer-portal phase and a workflow phase, neither of them in
§9's plan; 044 and 045 then MERGED before this branch did. The table is corrected to what shipped
and the unassigned rows renumbered again; the next phase should expect the same thing to have
happened once more.

041, 042 and 043 are all free. A gap is harmless: goose cares about ORDER, not density.

### D77 — the §4 dashboards sketch repeats D57's FK defect verbatim

The sketch declares:

```sql
visibility_team_id UUID REFERENCES teams(id) ON DELETE CASCADE,
```

This is character for character the construct D57 condemned on the saved-views sketch, and it
fails the same way: on a **nullable** column `ON DELETE CASCADE` deletes the ROW, so deleting a
team would delete every dashboard shared with it — somebody else's work, destroyed as a side
effect of an unrelated administrative action.

It is contrary to ADR-0009's degradation rules, which require a scope that has gone to degrade
rather than to disappear. Migration 048 therefore uses `ON DELETE SET NULL` and, like 038, omits
the CHECK that would make the degraded `(visibility = 'team', visibility_team_id IS NULL)` state
unrepresentable. The invariant is enforced one layer up by `views.Audience.Normalise`, which is
now the single implementation of that rule for both models.

`TestDashboardStore_TeamDeletionDegradesRatherThanDestroys` fails with *"dashboard not found"* if
the FK is changed back to CASCADE. Verified by making that change and running it.

One half of D57 does **not** recur: the dashboards sketch carries no
`..._visibility_team_present` CHECK, so only the FK needed correcting.

### D78 — the sketch's `gadget_key` needs no CHECK, and must not have one

The sketch leaves `gadget_key TEXT NOT NULL` unconstrained, and that is right for a reason worth
recording rather than leaving to look like an oversight.

The registry is a **closed set in code** (spec §7, ADR-0009 decision 5) and every write validates
against it, so a key this build does not know cannot be written through the API. But a key this
build does not know can still be **read** — from a row an older or newer build wrote — and
decision log **C5** requires that to render a placeholder tile rather than crash the dashboard. A
CHECK constraint would turn that degradation case into a failed migration or an unreadable row.

Strict on write, tolerant on read, and the tolerance has to be in the schema. Migration 048 says
so in place. `TestDashboardStore_AnUnknownStoredKeyStillLoads` inserts an unknown key straight
through SQL and fails if the read path refuses it.

### D79 — a user-fixable error on the saved-view surface answered 500

Not a spec discrepancy: a live defect in P4 code, found by P5's endpoint matrix and fixed in the
same PR because P5 inherited the shape.

`views.Draft.validate` returned a bare `fmt.Errorf` for a name or description over its bound, and
the handler's error switch had no case for it, so a saved view with a 200-character name answered
**500 INTERNAL_ERROR** rather than 422 with the bound it exceeded. The same shape would have
reached every gadget-configuration bound in P5.

`views.ValidationError` is now the typed carrier for anything the caller can fix by changing their
request, and both handlers match it with `errors.As` before their switches.
`TestDashboardsMatrix_LayoutWriteRefusals` covers nine of them; the saved-view equivalent is
covered by the existing matrix.

### D80 — `/spaces` was missing from the Home product-tab prefix list

Also not a spec discrepancy, and also live: `ProductTabs.isHomeActive` enumerated the Home-scoped
top-level paths and omitted `/spaces`, so the space directory rendered with **no product tab lit
at all**. Found while adding `/dashboards` to the same list. Both are there now.

### D81 — a pre-assigned migration number expires when a higher one merges first

The coordination for these parallel phases pre-assigned migration numbers: 040/041 to the Codex
phase, 042/043 to P5, and the portal and workflow phases took 044–047. That is sound as collision
avoidance and unsound as an ordering guarantee, and this phase hit the difference.

The customer portal merged 044 and 045 to `main` while this branch was open. goose refuses a
migration numbered below the current version, and `internal/db/migrate.go` calls
`goose.UpContext` at **boot**. So a 042 landing after 045 does not produce a failed migration
step:

```
running migrations: error: found 1 missing migrations before current version 45:
    version 42: migrations/042_dashboards.sql
```

That is the server refusing to start, on every deployment already carrying the portal.

**Nothing in CI would have reported it.** Every CI database is built fresh from an empty schema,
where ordering gaps are irrelevant and out-of-order cannot arise. The failure exists only on a
database with history. It was found by applying `main`'s migrations to a throwaway database and
then applying this branch's — reproduced in both directions, before and after the fix.

**D73, from the workflow-tiers phase, is the same finding reached independently** — reserved
out-of-order numbers make goose refuse an existing database. Two phases hitting one behaviour in
the same week is the argument for treating it as a standing rule rather than as either phase's
incident.

Numbering is immutable ONCE SHIPPED (§10). This had not shipped, so it moved to **048**, the first
free number at the time and still the first free number after 046/047 merged with the workflow
phase. The rule to carry: a pre-assigned number is a claim on a position in a SEQUENCE, and it
expires the moment a higher number merges first. Re-check it against `main` when you open the
PR, not when you plan the phase — and if the branch is long-lived, re-check it again before merge.

## 2. Decisions taken (justified in the phase report, recorded here)

- **The audience rule is now written once.** Saved views and dashboards carry the identical
  three-audience rule — owner, org-admin bypass, subject-side expanded team set, and a degraded
  team row that must match nobody. `views.Audience` (`internal/core/views/audience.go`) holds it
  and both models delegate. Two copies of an authorisation rule drift, and the direction they
  drift in is "one of them grants more".

- **`CapReadAggregates` remains uncalled**, as P4 left it. The test ADR-0007 implies is whether
  anyone can read items but should not see counts of them, and the answer is no: an aggregate
  resolves the identical readable set a results page does and returns counts of rows the caller
  could enumerate one at a time, so a gate there would refuse somebody who could get the same
  number by paging. Its placement in `minRoleFor` at `RoleViewer` stays; **the placement is still
  speculative** and this phase declines to invent a use for it.

- **No audit events for dashboards**, on P4's reasoning restated: spec §6's audit list is teams,
  memberships, grants, space visibility, owner team and shares — every one of them an access
  change. A dashboard grants nothing to anybody, and sharing one shares an arrangement.

- **Gadget data travels the existing resolution path rather than a new one.** The dashboard
  response hands each tile the filter document it should resolve; the client posts that to
  `/views/preview` or the new `/views/aggregate`, both of which carry `ResolveShares`. A
  per-gadget server-side results endpoint would have been a second resolution path, which is the
  drift `shared-surfaces.md` exists to prevent. A viewer editing the document they were handed
  changes only what they themselves see — which is what typing into the filter builder already
  does.

- **Spec §7 places dashboards under `/:module/:spaceId/dashboards/:dashboardId`.** A dashboard
  arranges gadgets that cross modules and containers and its API is org-scoped per §6, so a
  space-scoped route cannot express one — the identical contradiction this document already
  recorded for views (P4 §3). Dashboards shipped top-level at `/dashboards` and
  `/dashboards/:dashboardId`, beside `/views`.

- **`GadgetDefinition.configSchema: JSONSchema7`** (spec §7) shipped as `configKeys: ConfigKey[]`.
  The configuration vocabulary is four optional keys whose bounds are validated server-side; a
  JSON-schema runtime in the client would be a second copy of those bounds, and a second copy
  drifts. `web/src/lib/dashboards/registry.test.ts` reads the Go registry and fails in both
  directions on the key set, the configuration keys, the breakdown fields, every bound, and
  migration 048's `col_span` CHECK.

## 3. Observed, out of scope

- **There is no clean source for an actor-attributed activity feed.** ADR-0009's gadget list and
  the P5 brief both name one, and this phase ships none, deliberately. `audit_log` has **no
  `space_id`** and none in its payload, so scoping it to a viewer's readable containers means
  deriving the container by joining `(entity_kind, entity_id)` back to three tables — a new query,
  and a new member-visible read path over a table that is `org-admin-404` today. `notifications`
  is not a candidate at all: it is a per-recipient inbox with exactly one producer in the whole
  product (`TicketService.Assign`), and a feed built on it would contain assignment rows and
  nothing else. **For a maintainer:** the cheap fix is a nullable `audit_log.space_id` populated
  at write time, exactly the precedent migration 030 set for `notifications.entity_space_id`.

- **Two audit events are silently discarded today.** `audit.dbLogger.Log` drops any event whose
  `OrgID` does not parse, and two live call sites set no `OrgID` at all:
  `internal/core/api/comments/handler.go` (`comment.created` — so **comments never appear in the
  audit log**) and `internal/core/api/auth/handler.go` (`user.login_failed`). Found while assessing
  the activity feed. Not touched: both are outside this phase's surface, and the comments one in
  particular changes what the append-only log contains.

- **A count gadget has no comparison window.** The prototype's stat tiles carry a delta line
  ("+1 since Monday"). The filter vocabulary has no time dimension, so a previous-period count is
  not expressible without extending it — a change to a locked decision rather than a render
  choice. The tile shows the view's name in that slot instead.

- **`make e2e-test` is destructive in a shared environment.** It ends with `make test-db-down`,
  which is `docker compose down -v` and takes the volumes. Four phases were sharing one compose
  stack while P5 ran, so this phase drove Playwright directly against an isolated database and
  its own `E2E_PORT` rather than through the make target. **For a maintainer:** the target is
  correct for CI and a lone developer, and a `make e2e-test-keep` that omits the teardown would
  make it safe for the concurrent-worktree working style this project actually uses.

- **The shared test database at `DATABASE_URL` accumulates other phases' migrations.**
  `internal/db` and `cmd/server` apply goose against it directly, and goose refuses to apply a
  migration numbered below the current version — so with 044–047 already applied by sibling
  worktrees, migration 048 could not be applied and eleven tests failed on the toolchain rather
  than on any change. `testutil.NewTestDB` is unaffected (it clones a fingerprinted template into
  its own database). Recorded because the failure reads exactly like a broken migration.
