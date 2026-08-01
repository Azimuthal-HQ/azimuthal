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
- **Recount, 2026-07-31 — still open, and the number has grown.** "Roughly thirty across eight
  files" is now **40** top-level `type mock*` declarations, plus **91** `vi.mock(` calls across 51
  frontend test files. `internal/core/api/router_test.go` alone declares **20**, not twelve. The
  qualitative finding is unchanged and was re-verified: every one stubs a repository or module
  *interface*, none stands in for PostgreSQL, and the real-database coverage still sits in the
  `*_integration_test.go` files beside them — so the **rule** is being obeyed and only the factual
  half of §2.8 is false. `CLAUDE.md` §2's note has been updated to the new counts. Restated as
  **D118** below with the two readings the maintainer must choose between.

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
- **CLOSED 2026-07-31 — P6 added the accessor, not P4.** `SharedEntities.CascadeRoots()` returns
  `(SpaceID, Path)` pairs and `CascadeSubtreeArrays()` returns the index-aligned space-id and
  escaped-pattern arrays, from **one** call so the pairing cannot be lost between caller and query;
  `internal/core/api/search/handler.go` consumes it and feeds `search.sql`'s `unnest`.
  `CascadeRootPaths()` survives for single-space callers and now carries a warning that binding it
  into a cross-space query *is* this defect. The bullet above is left as written — it was accurate
  when recorded, and it named P4 because P4 was expected to need it; P4 turned out not to (saved
  views read `tickets` and `project_items` only, and cascade is constrained to pages), so the need
  fell to P6. Spec §5 has been corrected to match.
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

**CLOSED 2026-07-31.** The README's Configuration section has since been rewritten and this entry
is now stale in both of its particulars. There is no `JWT_SECRET` row in the Core table, and the
table has no "Required" column at all — its columns are Variable / Default / Description, with
`DATABASE_URL` the only row marked `— (**required**)`. The section additionally opens with a
standing correction, "**There is no `JWT_SECRET`.**", citing migration 018 and ADR-0004, and
reframes `JWT_PRIVATE_KEY_PATH` as a legacy one-time import path. The entry is kept rather than
deleted — the ledger is a history — but a future pass must not act on it: the README line it
complains about is already correct, and "fixing" it would be a regression.

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

---

## Filter vocabulary v2 — dates and negation

The saved-view filter document is now versioned 1..2. This section records what changed, and one
decision a maintainer should confirm rather than inherit.

- **The "locked decision" P5 flagged has been acted on, under this phase's brief.** P5 recorded, in
  the entry above: *"The filter vocabulary has no time dimension, so a previous-period count is not
  expressible without extending it — a change to a locked decision rather than a render choice."*
  It declined the extension as out of its own scope and left it for a maintainer. This phase was
  commissioned to make exactly that extension, so the lock was lifted by the brief rather than by
  the implementation. **For a maintainer:** if that reading is wrong, this is the entry to object
  to — the change is real, it is in `internal/core/views/filter.go`, and it is not something a
  reviewer should have to infer from a diff.

- **No ADR changes, and that is checked rather than assumed.** ADR-0009 does not mention the filter
  document's shape at all — it constrains storage-versus-results, scope ownership and the
  per-module fan-out. P4 already established this precedent when it replaced the spec's
  `{field, op, value}` sketch with a closed record without amending the ADR. ADR-0011's exclusion
  clause forbids EXECUTION ("no Groovy, no JavaScript hooks, no user-supplied code"), not
  expressiveness, and its Tier 3 is the governing precedent: a fixed closed set, defined in code,
  extended only by a deliberate release decision. A closed relative-token grammar is an extension
  of a closed set; an open duration parser would not be, and v2 does not add one.

  ADR-0011 also fixes the rule that *"a vocabulary that grows without amending its own ADR is how
  the boundary this document draws stops meaning anything"*. This entry is that record.

- **The document is still a record, not a query language.** `{after, before}` are two fixed keys on
  a named field rather than a caller-supplied operator, and `not: true` is a boolean attribute of a
  named field rather than a node in a tree. There is no `op`, no nesting, and **no cross-field OR in
  v2 either** — that remains unrepresentable by decision, and the JQL classifier reports it as such.

- **"The two SQL fan-outs" is now six.** The phrase appears in `shared-surfaces.md` §18 and in the
  header of `saved_views.sql`, and it was accurate when P4 wrote it. P5's dashboard gadgets added
  `CountView*` and `BreakdownView*`, so a filter field must now be added to SIX predicate blocks.
  During this change a replace-all matched five of the six — `ListViewTickets` carries a comment the
  others do not — and the result compiled, generated, and passed every existing test with the Beacon
  list fan-out ignoring every v2 filter. `TestSavedViewFanouts_CarryIdenticalFilterPredicates` now
  asserts the parity that nothing previously did.

- **The relative-token month unit is `mo`, not `m`, and the spelling is load-bearing.** JQL's
  relative date literals use `w`/`d`/`h`/`m` where `m` is MINUTES, and JQL has no month unit at all.
  Sharing the spelling would have made `-1m` valid in both vocabularies meaning two things three
  orders of magnitude apart, and the Jira importer would have translated it silently and wrongly.
  An unshared spelling turns that into a parse error.

- **`resolved_at` is filterable but nothing in the product writes it.** The column exists on both
  tables, is already selected by both fan-outs and is already a valid SORT field, so sorting by it
  has been an equally silent no-op since P4. v2 adds a `resolved_at` range for symmetry with the
  other three date fields, and the builder says plainly that it matches nothing yet. **For a
  maintainer:** the fix is to set `resolved_at` at the workflow done-category chokepoint, which
  belongs to the workflow track rather than here. Until then the "Recently resolved" default queue
  is also a no-op.

- **No index backs any of the four date columns on either table**, and the saved-view fan-outs are
  the product's only cross-space reads. A date-filtered view over a large org will sequential-scan.
  Not addressed here because it needs a migration, and this phase was scoped to take none. **For a
  maintainer:** read `migrations/` for the next free number rather than trusting any table — 041,
  042 and 043 are abandoned gaps and the sequential next is above 048.

- **`localToRFC3339` now has two private copies.** `pages/admin/AuditLogPage.tsx` has one and
  `components/views/QueryFilterBuilder.tsx` has the other, which also needs the inverse. Two is the
  point at which a third would be a defect: if another surface needs them, lift both halves into
  `lib/` rather than writing a third.

### Filter v2, after adversarial review

An adversarial review of this branch confirmed thirteen findings. Eleven were fixed on the branch;
the two below were not, each for a stated reason.

- **The six fan-out adapters had no wiring guard, and the first draft of v2 was broken because of
  it.** The four shared negation flags and all eight date bounds were wired into the three TICKET
  adapters and missing from the three ITEM adapters — a bulk edit anchored on two adjacent lines
  that the item literals separate with `Kinds` and `SprintIds`. It compiled, sqlc regenerated
  cleanly, and the whole suite passed: a missing field in a Go composite literal is a zero value,
  which reads to SQL as "this filter is absent", and the cross-module count-versus-list parity test
  compared a list and a count that had BOTH lost the same parameters, so they agreed with each other
  about the wrong rows. On a Vector view, `not` inverted to plain inclusion and every date range was
  silently dropped.

  `TestSavedViewAdapters_AssignEveryGeneratedParam` now parses the adapter sources and requires
  every field of each generated `*Params` struct to be assigned by name. It is structural on
  purpose: it fails when a parameter is added, not when somebody notices.

- **NOT FIXED — `internal/core/dashboards/registry.go` stamps `views.Version` on two gadget
  queries that need only v1.** This is the rule `queueQuery` was changed to follow, so the
  inconsistency is real. It is left alone because that file belongs to the dashboards surface,
  which is in flight on another track, and because the practical impact is nil: `MyWorkQuery` and
  `RecentWorkQuery` have no production caller at all and neither document is ever persisted, so
  no stored row carries the stamp. **For a maintainer:** two lines, `q.V = q.RequiredVersion()`,
  whenever the dashboards track next touches that file.

- **NOT FIXED — `Filter.Priorities` has no length bound.** Every other list field has one. The loop
  that validates priorities runs to completion over an arbitrarily long list, which is the DoS shape
  the other bounds exist to prevent. Pre-existing, and adding a `Max*` constant would require the
  frontend bound-parity map to learn it in the same change; noted rather than smuggled in here.

---

## P6 — cross-module search

### D82 — §7's "tagged with module and owning team" collides with matrix case 16 — **RESOLVED BY DERIVATION, matrix wins**

- **§7 says** search results are *"tagged with module and owning team"*.
- **Matrix case 16 says** a share-read must not disclose its container. It is not prose: it is
  enforced field-by-field at `internal/core/api/entity_shares_integration_test.go`, which asserts
  the absence of the container fields by exact key on the `/shared` read route.
- **The collision:** a search hit reached ONLY through a share is a share-read that arrived by a
  different route. Tagging it with its owning team — or with `space_key` / `space_name`, which
  every other cross-space list returns — tells a viewer who cannot enter the space that the space
  exists, what it is called, and who owns it. An outsider holding one share on one page in
  "Acquisition Planning" would learn the name of the space and the identity of its team.
- **Resolution:** the enforced matrix case wins over the §7 sentence. A share-only hit carries no
  container identity at all — no space id, key or name — and renders the way `/shared` already
  renders one: module chip, entity title, shared-provenance chip. A hit in a space the viewer CAN
  read is unaffected and carries its container as usual.
- **Why this is a derivation and not a new decision:** case 16 already governs share-reads, and
  nothing about arriving via search changes what the viewer is permitted to learn. §7's sentence
  was written for the ordinary case and does not appear to have contemplated the share-only one.
- **Where it is enforced:** once, in `search.redactSharedContainers`, rather than in the
  serializer — so a second response shape cannot forget it. The decision is by SPACE, not by share
  membership: an entity that is both shared and in a readable space keeps its container, because
  the viewer can already see it.
- **Tested both directions**, with the negative half mutation-tested: leaving the fields populated
  fails on "a share-only hit must not carry its space id".

### D83 — `shared-surfaces.md` said the route accounting table holds 185 rows; it holds 217 — **fourth occurrence**

The figure was already stale before this phase started: the whole #85–#92 wave (portal, workflow
tiers, dashboards, the cleanup sweep, the maintenance pass and filter vocabulary v2) landed rows
after the sentence was last written. P6 adds one more, for `GET /orgs/{orgID}/search/`.

Corrected to 217, and the standing warning in that section is updated from "wrong three times" to
"wrong four times". The warning itself is the useful part — **count the map, do not quote the
number** — and it is now correct about its own history. This entry exists mainly to record that
the pattern recurred exactly as the warning predicted, in the same way D52/D56 record the
migration table's repeats.

### D84 — §4's search sketch predates `project_items.item_key`, so the shipped vector is a superset

§4's "Full-text search" sketch specifies `setweight(title,'A') || setweight(description,'B')` for
both `tickets` and `project_items`. That was written against the schema at migration 028;
`project_items.item_key` arrived later, in 031.

Migration 049 ships the sketch's expression for `tickets` verbatim and **adds `item_key` at weight
A** for `project_items`, so an item is findable by its key. This is an extension of the sketch, not
a contradiction of it, and it is recorded here rather than silently absorbed.

`tickets` deliberately does NOT gain an equivalent: a ticket has no key column — its reference is
composed from `spaces.key` and `number` by `tickets.ComposeRef` — and a generated column may only
reference columns of its own row, so `spaces.key` is unreachable from the expression. Ticket
references stay served by the `/ticketref` resolver, which is an exact lookup and a better answer
than a ranked match. Both vectors still carry title `A` and body `B`, so cross-type rank
comparability is unaffected by the asymmetry.

---

# P-W PR-B — the workflow admin editor and approval surfaces (migration 050)

## 1. Discrepancies found and corrected

### D85 — D72 named the wrong cause, and the wrong cause was the cheap one

D72 recorded that a new project item's default status is not a state in its own workflow, and
attributed it to **migration 014's column default**. A comment in
`internal/core/api/workflow_tiers_integration_test.go` repeated the attribution.

Both are wrong. `CreateProjectItem` names `status` in its INSERT column list
(`internal/db/queries/project_items.sql`), so the `DEFAULT 'open'` is never evaluated by the
application. The value comes from `internal/core/projects/item.go:114`.

The repository wins and both statements are corrected here. The distinction is load-bearing rather
than pedantic: D72 offered "a column-default change or a seed change" as the two candidate fixes,
and the first of those is a **no-op in production** that would silently alter only the raw-SQL test
fixtures which omit the column. D72 also asserts both candidates "touch live data", when in fact
one touches none and the other has the largest blast radius of any option considered.

The hole itself is not closed. Its full disposition — three candidate fixes, why two are wrong, and
why the right one is out of scope for a UI phase — is **known-issues #30**, with a failing-shaped
test skipped per §2 at `internal/core/api/workflow_d72_ungated_first_transition_test.go`.

One fact D72 does not record and should: the **ticket side is protected only by a name
coincidence**. Tickets are also created at `"open"`, and the seeded ticket workflow happens to have
a state called `open`. An administrator creating a custom default ticket workflow with a
differently-named initial state reopens the same hole, silently.

### D86 — `guard.go` described a TypeScript mirror that did not exist

`internal/core/workflow/guard.go:21` states that the guard vocabulary is "mirrored in TypeScript
and held equal by `web/src/lib/workflow/guards.test.ts`". Neither the mirror nor the test existed:
`web/src/lib/workflow/` was not a directory, and grepping `web/src` for `actor_is_assignee`
returned nothing.

The comment described intent as fact — the same shape as D51's lesson about ADRs describing
implementations that had not happened. Both files are created in this PR and the comment is now
true. The test asserts SET equality in both directions and resolves the guard capability list
through the same `access` constants Go uses, so a capability rename fails the test rather than
silently producing a picker offering a value `ValidateGuard` refuses.

### D87 — the admin workflow page was mounted outside the admin guard

`web/src/App.tsx` declared `admin/workflows` as a SIBLING of the `AdminLayout` route rather than as
a child, so React Router matched the more specific literal first and the page never passed through
`AdminLayout`'s `caller_is_admin` check. It rendered for any authenticated org member.

Nothing caught it. `web/e2e/workflow-admin.spec.ts` is the only thing that visits the URL and it
signs in as an org admin, for whom both mountings are indistinguishable.

The severity is worth stating precisely rather than inflating: the workflow READ routes are
deliberately org-member, and every tier MUTATION carries `RequireOrgAdmin` server-side, so this
disclosed admin *chrome* and never admin *power*. It became load-bearing when this PR added
mutations to that page.

Corrected, with a fails-before test that keeps a permanent negative control — it renders both
mountings and asserts the sibling arrangement leaks.

### D88 — the tier deletes did not scope the child to its transition

`DeleteGuard`, `DeletePostFunction` and `DeleteApprover` called `resolveTransition` — which proves
workflow-in-org and transition-in-workflow — and then deleted by the raw `{guardID}`. Pairing one
of your own transitions with a foreign child id removed it, **including across organisations**.

Same class as D74 one level down, and reachable from the delete buttons this PR's editor ships.
The three `DELETE` statements now carry `AND transition_id = $2`, and zero rows maps to the
existing `ErrNotFound` → 404 path.

## 2. Findings recorded, not repaired

### D89 — nothing notified anybody about an approval

The phase brief stated that PR #86 had wired approval notifications and that this phase should
"verify, don't rebuild". Verification found none: no enqueuer field, no `With*` builder, no kind
string and no call site anywhere in `internal/core/workflow`, `internal/core/api/tiergate` or
`internal/core/api/workflows`. An approver had no way to learn they were needed except by opening
the item.

Wired in this PR on a maintainer ruling, at exactly two points, onto the existing
`jobs.NotificationEnqueuer`. Recorded here because the premise correction is the finding — the
"verify" half of an instruction is what caught it.

### D90 — `ErrApprovalRequired` is declared and never returned

`internal/core/workflow/errors.go` declares `ErrApprovalRequired` with the comment that a caller
"turns it into the 'requested, pending approval' answer". No code path returns it: `Gate` reports
a pending approval through `GateResult.Pending` with a nil error, and `Decide` never produces it.

Harmless today and left alone — but a caller keying off that sentinel would wait forever, so it is
recorded rather than deleted, since deleting an exported symbol is a contract change.

### D91 — approval and application are two transactions, with no compensation

`TierService.Decide` commits the decision row, and the handler then separately calls
`ApplyTransition`. If the apply fails the caller gets a 500 saying "the approval was recorded but
the transition could not be applied" — and the approval is now decided, so the one-pending-per-item
index no longer blocks a fresh request, while the item is still in its source status. There is no
retry, no compensation, and nothing surfaces the stranded state.

Pre-existing in PR #86 and untouched here. Repairing it means either folding the decision into the
applier's transaction or adding a reconciliation path, both of which are larger than a UI phase.

### D75 — `is_default` uniqueness, re-confirmed and still not taken

The fence made D74 and D75 in scope "if the editor work touches those seams naturally". D74 did:
an editor cannot show a transition without listing transitions, and those routes were unscoped.
D75 does not — the editor configures rules on edges and never asks which workflow is default — so
it is left exactly as PR #86 recorded it. A partial unique index would close it and could fail on
existing data, which is a maintainer's call.

---

# Docs-only reconciliation pass — 2026-07-31 (no migration)

## 0. Scope, method, and how to read this section

A documentation-only sweep of the specification, all ten ADRs, the ADR index, `README.md`,
`CLAUDE.md`, `shared-surfaces.md`, the non-security parts of `known-issues.md`, and the four
operational documents, against the tree at `1694eab6`. **No code changed.** Where a drift needs a
code change it is ledgered here and not fixed.

**Method.** Every claim was checked against a code line before being written down, then
adversarially re-checked by a second reader whose brief was to refute it. That second pass
mattered: it corrected 25 of the 62 findings — wrong line numbers, overstated absences, and in
four cases a wrong disposition. Two examples of the kind of error it caught, because they set the
standard for the next pass: "the word *webhook* appears nowhere in the repository" is false (a
vendored Swagger-UI bundle contains 31 occurrences) and a ledger entry saying so would be
contradicted by a one-line grep; and the claim that ADR-0012 is "the only implemented ADR whose
status still reads as pending" was wrong — ADR-0009 had the identical defect, so a fix scoped to
one file would have left the other.

**Dispositions.** Each entry is A, B or C:

| | Meaning | What this pass did |
|---|---|---|
| **A** | Stale doc, repo authoritative | Fixed the doc. For an ADR, an appended dated Correction note — never a rewritten decision. |
| **B** | Repo violates a still-valid documented decision | **Left the doc alone** and ledgered a code follow-up. Editing the decision to match the bug is the one move never available. |
| **C** | Genuinely open or self-contradictory | Presented both readings with a recommendation. The maintainer chooses. |

**The one thing to read first** is §1. Everything else is bookkeeping by comparison.

**A note on the brief that commissioned this pass.** Two of its seeded items were themselves
stale, and finding that out is part of the result. It said `CLAUDE.md` §3 still claims eslint is
not a CI gate and that this correction "never got carried by an earlier PR; carry it here." It had
been carried: `CLAUDE.md` already said "`npm run lint` **is** a required CI gate as of #82", the
gate is live in the `Frontend` job, and `known-issues.md` #17 records it closed. The brief was
written against the **stale primary checkout**, not `origin/main`. It also gave the D-number
mapping as an open problem; it is not (§6). *Verify, don't assume, applies to the brief too.*

---

## 1. The decision this pass exists to surface — spec §10 versus `CLAUDE.md` §4

### D106 — "Agents perform no git operations" versus the autonomy envelope that has governed every phase — **SETTLED 2026-08-01: §10 NARROWED**

> **Resolution.** The maintainer ruled Option B. Specification §10's blanket prohibition is
> **narrowed in this PR** to the four hazards it was protecting: never commit or push to `main`,
> never create or move a tag, never merge a PR including your own, never force-push a **shared**
> branch. Agents commit, push, rebase and `--force-with-lease` on their own branch as a matter of
> stated rule, not tolerated practice. `CLAUDE.md` §4 is the operative detail and §10 is the
> boundary; where they appear to disagree, §10 wins.
>
> **The reason is recorded as stated, not as convenience.** The specification is narrowed because
> it should state the rule that actually governs. A stated non-negotiable that everyone knowingly
> works around is corrosive — it teaches every reader that a rule in the document can be
> disregarded provided you write a sentence about it, and it spends the authority of every other
> non-negotiable alongside it. That is why this was settled rather than annotated for a third time.
>
> **This closes D33 as well**, which reached the same conclusion once before and left it open.
> `CLAUDE.md` §4's ⚠ flagged-conflict block is replaced with the resolution, and **PR bodies no
> longer carry the conflict note.**
>
> The analysis below is left as written — it is the record of why, and of the state that made it
> necessary.

This was flagged by five separate agents across the wave, and by `CLAUDE.md` §4 itself. It was
recorded here as a decision the maintainer owed, and was **not** settled by this pass unilaterally.

**What the specification says.** §10 "Non-negotiables", prefaced "These override any instruction
in a task prompt":

> **Repository.** Agents perform **no git operations** — no commits, pushes, tags, or branch
> changes. Agents **never create or edit the roadmap**. No agent-name file suffixes. Migration
> numbering is immutable once shipped.

**What has actually happened.** `CLAUDE.md` §4 sets out an autonomy envelope — "Work on your own
branch, named for the work. Open your own PR", bounded by "Never push to `main`. Never merge your
own PR", never force-push a shared branch, never tag. Every phase since P0 has worked that way:
**every PR that has reached `main` has done so under this envelope**, and the phase prompts
instructed exactly this. The envelope has itself been amended once under a maintainer instruction — on 2026-07-28
during P4 PR-A, to permit `--force-with-lease` on an agent's own unmerged branch, because the
previous absolute wording made a requested rebase impossible to complete without breaking a stated
rule. So the envelope is not an informal drift; it is a working agreement that has been
deliberately maintained.

**Why it cannot be left as it is.** `CLAUDE.md` §4 currently resolves the conflict by conceding —
"the specification wins and the conflict is flagged rather than reconciled" — and then instructs
agents to do the thing §10 forbids anyway, noting it in each PR body. That is a stable enough
workaround to have survived a whole wave, and it is corrosive: it teaches every agent that reads
these documents that a stated non-negotiable can be knowingly disregarded provided you write a
sentence about it. §10's authority is the thing being spent.

**Option A — §10 means what it says.** Agents produce diffs; a human performs every git operation.
Coherent, and the only reading that takes the word "non-negotiable" at face value. It has never
been practised, and adopting it means rewriting the phase-prompt workflow, the review flow, and
`CLAUDE.md` §4 in full.

**Option B — narrow §10 to what it was protecting.** The four hazards the blanket wording plainly
targets are pushes to `main`, tags, self-merges, and history rewriting on shared refs. Narrow the
sentence in place to something like:

> Agents never push to `main`, never merge their own PR, never create or move tags, and never
> force-push a shared branch. `--force-with-lease` on an agent's own unmerged feature branch is
> permitted, so that the linear-history requirement is satisfiable.

**Recommendation: Option B.** *(Adopted — see the resolution above.)* It is the envelope that has
actually governed the wave and worked; it has already been amended once by maintainer instruction,
which is evidence the narrow rules are the ones anyone actually intends; and it makes §10
satisfiable alongside the linear-history requirement, which the blanket wording does not — rebasing
onto `main` and updating your branch is a "branch change", so the absolute reading forbids the
workflow the rest of the document requires. Closing it this way would also close **D33**, which
reached this same point once already.

**But this is the maintainer's call, and Option A is real.** It is a coherent position with a real
argument behind it — an agent with commit access is a different risk surface from an agent that
emits patches — and nothing in this pass is evidence against it. What is not tenable is the third
state we are in now: a non-negotiable that every phase knowingly breaks with a footnote.

*The paragraph that stood here said: "Until it is ruled on, the standing practice is unchanged —
follow the narrow rules and note in each PR body that git operations were performed under a
standing instruction that conflicts with §10." It was ruled on before this PR merged. The narrow
rules are now the stated rule, and the PR-body note is retired.*

---

## 2. Maintainer-owed decisions (disposition C)

### D94 — ADR-0011 names webhooks as the home of "genuine automation"; no first-party webhook surface exists

ADR-0011's Consequences: "Genuine automation belongs at the integration boundary — webhooks and the
job queue — rather than inside the workflow engine." Neither half is available. **No first-party
webhook surface exists** — no route, table, config key, handler, test or tracking issue. (Be
precise: a repo-wide grep *does* hit `internal/core/api/swaggerui/assets/swagger-ui-bundle.js`, a
vendored third-party bundle implementing OpenAPI 3.1's `webhooks` keyword. Say "no first-party
surface", or the claim is falsified by one grep. The parity review overstates this at its §4.1 and
the overstatement should not propagate.) The job queue exists but is closed: two workers, email and
notification, and the only enqueue paths are callable from Go application code — no administrator,
workflow or external route reaches it.

**Two readings.** Read normatively, the sentence sits under Consequences, says "belongs at", makes
no existence claim, and needs only the dated note this pass added. Read as **the mitigation that
makes the permanent scripting exclusion tenable** — which is how the parity review reads it, and how
a migrating team with 40 Jira automation rules will read it — it leans on a capability that does not
exist, and the note should instead reference a tracking issue for the webhook surface.
**Recommendation:** treat it as the second and open the tracking issue; the exclusion is stronger,
not weaker, when its stated alternative is real. Either way the exclusion itself is not softened.
*Dated note added to ADR-0011; the reading is not chosen.*

### D96 — ADR-0012 maps dynamic-content macros onto saved views; the saved-views layer has since excluded Codex

ADR-0012 §4: "Dynamic content macros (content by label, page properties reports) map onto the
saved-views layer, since they are queries." Nothing shipped, and **the named substrate has since
ruled itself out.** `internal/core/views/filter.go` defines exactly two queryable modules and the
validator refuses anything else, stating the exclusion as a decision: "Codex is deliberately absent:
pages are found through P6 search, which owns the page read path and its cascade share semantics."
Content-by-label and page-properties reports are *page* queries, so under the shipped design they
cannot map onto saved views at all.

This is a **live conflict between two accepted decisions**, decided on a cascade-share argument
ADR-0012 never weighed — not an unbuilt plan. ADR-0009 never considered pages either way (it
contains no mention of Codex or pages), so `filter.go`'s exclusion is the newer and more specific
decision. **Recommendation:** record the substrate as reopened — which the amendment added to
ADR-0012 does — and let whoever picks these macros up choose between search and a new page-query
layer. *One half needs no adjudication and is ledgered separately: the code comment deferring these
macros gives its reason as "that is P4", and P4 merged in #79/#81, so the stated reason has expired.*

### D97 — "CI fails on any skip lacking these" — no such check exists

`CLAUDE.md` §2 and specification §2.4 both state that a skip is permitted only with a `SKIP:`
comment, an issue number and a re-enable condition, and that **CI fails on any skip lacking these**.
Nothing in CI inspects skips: the Test job runs a plain `go test` and gates only on exit status and
the coverage floor; there is no grep, no skip-audit script, and no Go test walking the tree.
**Eleven unmarked `t.Skip` calls pass every gate today.** Most are environment guards, but
`internal/core/api/harness_wiring_test.go` is not — `t.Skip("portal surface is not mounted in this
harness")`: no marker, no issue, no re-enable condition, and green. Exactly one skip in the tree
carries the marker.

**Recommendation: do both halves, in this order.** (1) Correct the claim now — done in `CLAUDE.md`,
which reads "A skip lacking these is a failing review", because telling an agent a gate will catch
it is the worst state to leave this in. (2) Open a code follow-up adding the enforcement, in the
mould of `TestHarness_NoDarkDependencies`: walk the test tree and fail on any `t.Skip`/`t.Skipf`
lacking the triple. If (2) lands, revert (1).

**Why this is C and not simply B:** an enforcement test must exempt or accommodate the
environment-guard pattern or it fails on eleven legitimate skips on day one, and designing that
exemption *is* the decision. **The specification's identical sentence at §2.4 is §2 text and was not
edited** — per §5, a reconciliation pass does not amend §2.

### D100 — ADR-0004 promises key rotation with a grace window; the schema is a singleton row

The core decision is implemented correctly — RS256, generated once, persisted in the database, never
regenerated at boot, with the restart test the ADR demands. But `auth_signing_keys` is a hard
singleton (`id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1)`, migration 018) with four columns and
no key id, no active/retired flag and no validity window, so two keys cannot coexist and the grace
window has nowhere to live. The entire query set is read-at-`id = 1` and insert-if-absent; there is
no UPDATE, no DELETE, no second-key verification path, and no `kid`, `jwks`, `rotate` or `rotation`
anywhere in `internal/core/auth/`. The documented procedure the Consequences section requires does
not exist either — a search across `docs/` for "key rotation" or "grace window" matches only
ADR-0004 itself.

**Why C.** The rationale sentence overstates a benefit (pure A, and a dated Correction note is
added). The Consequences sentence — "Rotation needs a documented procedure" — is an unmet standing
obligation, closest to B but awkward there because what is unmet is documentary. The two halves have
materially different fixes and different owners. **Recommendation:** take the doc correction now
(done) and route the capability to whoever owns auth and operations, rather than annotating
operational rotation detail into a public ADR. Note the dependency: the ADR's own restore remedy
("rotation after restore") is unavailable until the capability exists.

### D104 — the phase→version mapping in §9 no longer matches any tag

§9 heads its phases "P4 — v0.3.3", "P5 — v0.3.4", "P6 — v0.3.5". The tags carrying those numbers
contain entirely different work: `v0.3.3` is #71 (stabilisation pass), `v0.3.4` is #75 (Codex
editor), `v0.3.5` is #77 (security and integrity release). **P4 actually shipped as `v0.3.6`.** P5
and P6 are merged and contained in **no tag at all**.

This is D27's predicted shift, realised at three rather than one. **The headings are left as
written**, and this pass has no authority to change them: §9's own ⚠ block says "This document does
not renumber them. Renumbering the roadmap is not a documentation correction, and no reconciliation
pass has the authority to do it", and `CLAUDE.md` §1 forbids an agent editing the roadmap. What *was*
added is a factual note recording what the tags contain. **Recommendation:** either retitle the
headings to their released versions and tag P5 and P6, or declare the phase→version mapping
abandoned. Either is a one-line decision that unblocks four stale headings. The separate v0.3.2
collision above it remains open and is untouched — nothing in the tag graph resolves it.

### D118 — §2.8's "No mocks exist, none will be added" (restating D45 with current numbers)

Recounted: **40** Go `type mock*` declarations and **91** `vi.mock(` calls across 51 frontend files,
against a sentence saying none exist. See the closure note appended to D45 for the counts and their
verification. The **rule** is being obeyed — every double stubs an interface, none stands in for
PostgreSQL — and only the factual half is false.

**Two readings.** (a) Keep §2 verbatim and treat the 40 doubles as debt to retire; this preserves the
sentence and costs real work. (b) Narrow the sentence to the rule it was protecting: "Real PostgreSQL
only for anything touching persistence. Never mock the database. Interface-level doubles in handler
and service unit tests are permitted only where a real-database `*_integration_test.go` covers the
same path." **Recommendation: (b)** — it makes the document true without weakening anything §2.1 was
written to prevent, and (a) has been notionally available since D45 was written without being taken.
**§2 was not edited.** A reconciliation pass does not amend §2; this is the maintainer's.

### D121 — README advertises backup and restore, and the deployment it recommends cannot run them

Both commands shell out to the PostgreSQL client binaries; the shipped image is
`gcr.io/distroless/static:nonroot`, which has neither them nor a shell. The README's claim is
**literally true on a host with `pg_dump`/`psql` on `PATH`** — which is presumably how it was tested
— and false for the Docker Compose deployment the same file calls "the fastest way to run Azimuthal".
The distroless choice is reasoned in a comment in the Dockerfile (multi-arch build time), so this is
not a sentence someone forgot.

**Recommendation, taken in part:** the doc half is done — README now states the host requirement
explicitly, and `docs/self-hosting.md` carries a warning block above the whole section. The code half
is **D105** and is not fixed here. Which way to close it — ship the client tools in an ops image
variant, or replace the fork with an in-process dump — is the maintainer's.

---

## 3. Code follow-ups (disposition B) — ledgered, not fixed

**None of these was fixed in this PR, and no document was edited to match the defect.** Each names
what the follow-up must do.

### D93 — condition-class workflow guards are configurable, audited, and never evaluated

ADR-0011 Tier 1 defines a condition as determining whether a transition is **offered**; its v0.4
amendment requires a guard the build cannot honour to be "refused, never skipped", because "a skipped
condition would offer a transition an administrator restricted". `TierService.Gate` — the chokepoint
all four status routes reach — evaluates the validator class only. The single production evaluation
of `GuardConditionClass` lives in `TierService.AvailableTransitions`, which has **no HTTP route and
no non-test caller**; the only transition-listing route returns the workflow's edges unfiltered.
Nothing offers, so the assumption `Gate` relies on is never established. Meanwhile the whole
configuration path ships: the route accepts `guard_class`, migration 046 CHECKs it, creation is
audited, and the admin UI renders "hides" beside each one.

An administrator configuring **ADR-0011's own Tier-1 example** as a condition gets a 201, an audit
row, a badge saying it hides the transition, and no enforcement. Reproduced live in
`docs/design/parity-review-2026-07.md` §5.1: same guard kind and item, `condition` → 200 and the
transition proceeds, `validator` → 422.

**The follow-up is two-part, and one part alone is insufficient.** Route `AvailableTransitions` so a
condition removes the transition from the offer, **and** have `Gate` evaluate conditions too, so a
transition POSTed directly still refuses — hiding is a UI control any HTTP client bypasses. It must
state that the gate was verified **both ways**: `tier_service_test.go` calls `AvailableTransitions`
directly and passes regardless of whether any route reaches it, so a test added there proves nothing
about reachability. Interim mitigation worth taking early: suppress `condition` in the admin picker,
so no administrator can save a guard that does nothing.

### D98 — the coverage floor was to rise to 85% at the end of P5; CI still enforces 80%

Specification §2.8 states it, and P5's Definition of Done states it as a **delivery obligation**:
"Coverage gate raised to 85%." CI enforces 80, and its own comment beside the check still describes
the raise in the future tense. P5 merged (#88/#89) — its PR title even claims "the 85% floor" — and
P6 merged after it.

**Neither spec line was edited.** §2.8 is §2 text, and editing 85 down to 80 would be the
assertion-weakening §2.3 forbids. `CLAUDE.md` §2 now records the floor as 80 with a dated note that
the raise is overdue and flagged. The follow-up must raise `ci.yml` (the step name, the comparison
and the failure message) and fix the stale comment — **after** measuring actual coverage with CI's
own invocation, including `-p 1`, because the tests share one database and an unmeasured flip fails
every PR. If the measurement lands below 85, this is a coverage PR, not a one-line threshold change,
and **it must not be closed by lowering the target.**

**Measured 2026-08-01 — the raise is deferred, and the decision is the maintainer's.** The
measurement this entry asks for has been taken, at CI parity, on `3e888636` (`v0.4.0`), against a
freshly reset database, alone:

```
go test -p 1 -count=1 -coverprofile=coverage.out -covermode=atomic \
        -coverpkg=./internal/... -timeout=900s ./...
```

**84.7615% — 15,085 of 17,797 statements.** Zero failures, zero skips beyond the five
`requirePostgresClientTools` gates in `cmd/server`, which skip off CI by design and are `package
main` (outside this `-coverpkg` denominator). `-race` was omitted: it needs cgo and a C compiler,
which the Windows box has not got, and it does not move the statement percentage.

`go tool cover -func | tail -1` prints that as `84.8`, and the gate compares `84.8 < 85`, so
**flipping the floor to 85 fails CI today.**

That is not an inference from a local run. **CI measured the same figure on this very branch**, in
the `Test` job of Actions run `30706459987`, on GitHub's own runner and *with* `-race`:

```
Total coverage: 84.8%
✅ Coverage 84.8% meets minimum
```

It passed because the step it ran was still `Enforce minimum coverage (80%)` at the time — the
floor has since been ratcheted to 84, see below. Rename that step to **85** — which is all the
raise does — and the same `84.8` fails the same comparison. The local and CI figures agreeing also
settles the `-race` question: it does not move the statement percentage.

The predecessor's branch measured 84.96% (14,018 of
16,500) on 2026-07-30 and passed only because `-func` rounded it to `85.0`. That rounding no longer
covers it. #100, #101, #103 and #104 merged in between: the denominator grew by 1,297 statements
and the numerator by 1,067 — the same denominator-outruns-numerator arithmetic this entry's
follow-up predicted, run four more times.

The gap is **43 statements** to a true 85, or 34 to clear the `84.95` rounding. Those statements
are in other phases' newly merged code, so closing them is coverage-shaped work by construction —
which §2.8 and the gate's own comment forbid. The raise commit was therefore **dropped** rather
than shipped red; it is recoverable as `587132ac` (pre-rebase) or `5fac8bcc` (rebased), and it is
a three-file, ~22-line diff.

**This defers the raise; it does not lower the target.** §2.8 and P5's Definition of Done are
untouched and still say 85. This entry stays **open**. The release-timing argument that once
favoured haste is gone — `v0.4.0` is cut at `3e888636`, so nothing waits on the answer.

**Maintainer ruling, 2026-08-01 — (a) defer, and ratchet the floor.** Two readings were put up.
Reading **(a)**, treat D98 as the coverage PR it says it is and fund a pass over the phases that
diluted the ratio before flipping, is taken. Reading **(b)**, flip to 85 and carry a red gate until
that pass lands, is **struck rather than weighed**: a red required check blocks every PR including
security fixes, and it teaches everyone on the project to read red as the normal state. That is not
the stricter option, it is the broken one. It is recorded here so it is not re-proposed as a live
alternative.

A third option was taken that neither reading covered. **The floor is ratcheted from 80 to 84** —
`Enforce minimum coverage (84%)` in `.github/workflows/ci.yml` — set half a point below the
measured figure. The diagnosis above is that the gap opened *silently*: the suite sat at 84.7–85.0
for a month while the gate asked for 80, so 4.76 points of decay were available with no signal at
all, and that is what let P5's raise pass on a rounding and then stop clearing the bar unnoticed.
Raising the target does not fix that. The floor does.

Measured on the branch head rather than on `main`, because CI scores the branch:
**84.7727% — 15,087 of 17,797 statements**, zero failures. (Two statements above the `3e888636`
figure, and not noise: the surviving test in this PR reaches `lockReparentTarget`'s `pgx.ErrNoRows`
arm, which no test in the tree had executed — that block reads `1 0` in the earlier profile and
`1 1` in this one.)

Headroom at 84 is roughly **146 statements**, not 137, because the gate compares a *rounded*
figure: `go tool cover -func | tail -1` prints one decimal, so a floor of 84 trips at a true
**83.95**, not 83.99. That is the same rounding that let 84.96 pass an 85 gate, now working in the
gate's favour. Ordinary work will not spend 146 statements; a large phase landing at low coverage
can, and should be interrupted when it does.

**84 is a ratchet, not a settlement.** The target stays 85, §2.8's *second* half is unchanged, and
this entry stays open. The constraint this entry set still holds and is not evaded: D98 must not be
closed by lowering the target, and raising the floor is not lowering the target.

> **Resolved on the maintainer's ruling — §2.8's gate figure is corrected 80 → 84 in this PR.**
>
> This was raised as *"flagged, not resolved,"* and the flag's reasoning is kept rather than
> deleted, because it is the record of why the edit was permissible. §2.8 read "Coverage gate is
> 80%, rising to 85% at the end of P5." Ratcheting CI to 84 made the first half understate the
> gate. Correcting it *strengthens* the assertion, so it is not the §2.3 problem that kept the
> second half untouched — but it is still §2 text, and `CLAUDE.md` §5 sends a §2 change to the
> maintainer rather than to the phase. The drift was benign in direction: an agent trusting the
> spec would aim at 80 and be surprised by CI rather than ship a defect.
>
> **The ruling was to fix it here rather than carry it, and the reason is not severity.** This PR
> is what made §2.8 false. Before the ratchet the sentence was correct; after it, the specification
> contradicts the pipeline in the one document a new agent reads first. Shipping a knowingly-false
> spec sentence because the direction is benign is the move **D106** was settled to stop — a stated
> rule left wrong provided someone writes a paragraph about it. The paragraph is not the fix.
>
> Only the gate figure moved. *"Rising to 85% at the end of P5"* is untouched: it is §2.3-fenced,
> it is this entry's subject, and this entry stays **open**. P5's Definition of Done ("Coverage gate
> raised to 85%") is likewise untouched.
>
> **Carried to the 0.5.0 hardening wave (W4):** a `docs-check` rule that reads the floor out of
> `.github/workflows/ci.yml` and out of §2.8 and fails when they disagree. Under **D148** a figure
> that *is* the claim must be backed by a test that fails when it drifts, and this one is not —
> which is exactly how it drifted. New tooling does not belong on a branch that is otherwise ready;
> it joins the citation-guard-for-Go-source item already queued there.

### D105 — backup cannot run where every document says to run it, and restore reports success on a partial recovery

`azimuthal backup` forks `pg_dump` and `psql`; `azimuthal restore` forks `psql`. The shipped image is
`gcr.io/distroless/static:nonroot` and the final stage copies in one file, the Go binary — no shell,
no coreutils, no PostgreSQL client. `docs/self-hosting.md` prescribes
`docker compose exec app /azimuthal backup` in three places including a nightly cron, and
`docs/upgrade.md` makes it the mandatory pre-upgrade step. The parity review ran it: it exits 1. **An
operator following the documentation believes they have nightly backups and has none.**

A second failure compounds it and is the more dangerous of the two, because it only manifests on the
day someone is recovering from an incident: `restore` runs `psql` without `-v ON_ERROR_STOP=1` and
discards stdout, so a dump whose statements failed still exits 0 and prints "Database restored."
**Fixing the first without the second produces backups that restore wrongly and say they worked.**

A third, independent: `backup.go` and `restore.go` pass `STORAGE_ENDPOINT` to `minio.New`
unmodified, while the serve path strips the `http(s)://` scheme first — and the shipped compose file
sets a scheme-ful value. The serve path was specifically taught to normalise this; the backup path
was not.

**Follow-up:** decide between an ops image variant carrying the PostgreSQL 16 client and an
in-process dump; add `-v ON_ERROR_STOP=1` and stop discarding psql's output; normalise
`STORAGE_ENDPOINT` in both commands. **Documentation half is done** — a warning block now sits above
the whole section, and `docs/upgrade.md`'s rollback no longer routes through the app container. Do
not close this by deleting the backup documentation.

> **CLOSED 2026-08-01 in PR #103.** All three parts are fixed, and the ⚠ block in `docs/self-hosting.md` is
> deleted rather than softened.
>
> - **The image carries the client.** `build/Dockerfile` gained a `pgclient` stage that copies
>   `pg_dump` and `psql` plus their shared-library closure from `postgres:16-bookworm` onto
>   `gcr.io/distroless/base-debian12:nonroot`. The ops-variant option was not taken: a second
>   image would leave the documented `docker compose exec app` recipes still broken.
>   `build/Dockerfile.ci` — which is what CI actually scans and boots — carries a byte-identical
>   stage, and `TestDockerfiles_PgClientStageIsIdentical` fails when the two drift.
> - **Restore fails loud.** `restorePostgres` in `cmd/server/restore.go` passes
>   `-v ON_ERROR_STOP=1`, captures both streams, and attaches psql's diagnostics to the returned
>   error. `TestRestorePostgres_PartialRestoreIsAFailure` fails with the flag removed.
> - **`STORAGE_ENDPOINT` is normalised.** The rule moved to `NormalizeEndpoint` in
>   `internal/core/storage/endpoint.go`; the serve, backup and restore paths all call it.
>
> Two things this uncovered that the entry did not anticipate. The version probe in
> `dumpPostgres` now returns its error instead of discarding it, and `validateManifest` prints
> the recorded server version back — previously nothing read the field. And the entry's phrase
> "discards its output" was half right: `restorePostgres` discarded *stdout* while streaming
> stderr to the terminal, so the failure was visible to a human watching but invisible to the
> exit code and absent from the returned error.

### D140 — "`AZIMUTHAL_INVITE_DELIVERY=email` requires `SMTP_FROM`; startup fails otherwise" — the SMTP_FROM half can never fire

`docs/self-hosting.md`, `.env.example` and `README.md` all state it. The `SMTP_HOST` half is enforced
and works — it deliberately uses a raw `os.Getenv` **precisely so a default cannot satisfy it**, with
a comment explaining that a config field carrying a default "is never empty and could not
distinguish" the two cases. The `SMTP_FROM` half then fell into that exact trap two lines later:
`SMTP_FROM` carries a viper default of `azimuthal@localhost`, viper runs without `AllowEmptyEnv`, so
an unset *or* empty value resolves to the default and the `if smtpFrom == ""` branch is unreachable.
Startup never fails for a missing `SMTP_FROM`; delivery proceeds with envelope sender
`azimuthal@localhost`, which most real relays reject. No test exercises the branch.

**The docs are right and were not edited.** The follow-up must make the requirement real for both
delivery modes — read it with a raw `os.Getenv` as `SMTP_HOST` already does, or drop the `SetDefault`
so the field can be empty — and add a config test that sets the delivery mode plus `SMTP_HOST` and
asserts `Load()` fails. **Mutation-test it:** with the guard removed the test must fail, or it is
asserting nothing.

### D147 — the shipped migration assessor tells a migrating team their Jira links are unmappable

`internal/assess/jira_assess.go` classes Jira issue links `VerdictUnmappable` with the reason
"Azimuthal models a parent/child hierarchy on `project_items` but has no typed link graph, so
blocks/relates-to/duplicates links have nowhere to go", rendered to the user in
`testdata/report.golden.md`. The second clause is false, and the verdict follows from it:
`entity_relations` is a polymorphic typed link graph whose kind CHECK names `blocks`,
`is_blocked_by`, `duplicates` and `relates_to` **literally** — the exact families the sentence says
have nowhere to go (see D92). Since the ledger defines `VerdictClean` as "a direct representation in
Azimuthal's model" and `Unmappable` as "would not survive the import at all", those four kinds are
Clean, and **the report currently tells a migrating team to write off data the target schema holds
natively.**

One correction to how this is usually stated, because the ledger should be precise: the *first*
clause is **not** backwards. `project_items.parent_id` is a real self-referencing column carried on
`CreateProjectItemParams` and written by the adapter, and the assessor judges the **model**, not the
HTTP API — its own report already reasons about "an importer writing straight to the repository".
What is true of the hierarchy is narrower: it is unreachable through the API and invisible in the UI,
not absent from the model.

**Follow-up:** rewrite the reason string so it neither denies the typed link graph nor offers the
hierarchy as the thing that works; reclassify the four mapped kinds as **Clean** (not "Approximated
pending an importer" — there is no importer to name, and the assessor is read-only by construction);
regenerate the golden report; and add a test that fails if the links class is `Unmappable` while
`entity_relations` still declares those kinds. Ledgered rather than fixed because this pass is
docs-only and the string is Go code — but it feeds the migration tool, so it is the highest-value
item on this list for anyone actually evaluating Azimuthal.

### Code-side companions to A-class doc fixes

Small, and each is the in-code twin of a document corrected above.

- **D92-code** — two code comments repeat "no link table exists":
  `internal/core/workflow/postfunction.go` and `migrations/046_workflow_transition_guards.sql`. 046
  is shipped, so the widening migration that eventually lands the deferred post-function should carry
  the correction rather than editing it in place.
- **D96-code** — the Codex macro module defers dynamic-content macros because saved views are "P4".
  P4 merged in #79/#81; the live reason is that saved views do not query pages.
- **D132-code** — the doc comment above `guardClasses` in `route_accounting_test.go` enumerates ten
  guard classes and omits `org-read` and `portal-session`, exactly as the catalogue did. It is very
  likely where the catalogue was copied from, and it is the more load-bearing of the two because it
  sits next to the map it describes.
- **D134-code** — the JSDoc above `friendlyErrorMessage` in `web/src/lib/api.ts` names three
  pass-through error codes; the array on the line below it holds four.
- **D119-code** — `internal/core/auth/doc.go` states "SSO/SAML authentication is available in
  `internal/core/sso`", and `internal/core/sso/provider.go` asserts "SSO is a standard feature
  available to all Azimuthal users" directly above the no-op that returns `ErrNotConfigured`. Both
  restate the README claim corrected below, and would reintroduce the belief.

---

## 4. Documentation corrected in this PR (disposition A)

Grouped by file. Each was verified against a code line and re-verified adversarially.

### The specification

- **D107 — the §4 migration table was stale for the SIXTH time.** The preamble said "The shipped
  sequence ends at `028`. The repository holds 28 migrations, `001`–`028`, with no gaps." The tree
  holds **47** migrations, the highest is **050**, and there *are* gaps. Two shipped migrations, 049
  and 050, had no row at all, and 049 was still listed as "unassigned". D76 corrected the table
  *body* in P5 and left the prose around it untouched, which is how the preamble survived saying 028
  while the rows beneath it listed 048 as shipped. **Also corrected: the "next free number" line said
  `037`; it is `051`.** And the paragraph declaring 041/042/043 "free" is now a warning: the
  *existing* gap is harmless, but **taking one of those numbers is not** — goose refuses a migration
  numbered below the current version and `migrate.go` runs at **boot**, so a new 041 landing after
  050 stops the server on every deployment with history, and no CI job can catch it because every CI
  database is built fresh. That is D73 and D81, reached independently by two phases in one week, and
  the sentence inviting it was still there.
- **D108 — §5 said no shipped endpoint binds a readable set as a query parameter.** It is bound in
  five query files today, and `search.sql` carries the full designed shape including the paired
  `unnest` with the per-root pin. The section contradicted itself: a passage further down already
  acknowledged P4's shipped fan-outs.
- **D109 — §5 said the `(space_id, pattern)` accessor "P6 must build first" does not exist.** P6
  built it; see the closure note on D46.
- **D110 — §6 documented the search endpoint with `modules=`, `team_id=` and `space_id=` and a
  scoped/global split.** The endpoint reads `q`, `cursor`, `limit` and `snippet`. There is no
  scoped/global split at this route and nothing to widen; `modules` is an **output** on the response
  envelope, never an input; and no result carries a team. Module narrowing exists as an in-query
  operator, not a parameter.
- **D111 — §7's published `GadgetDefinition` had four wrong members and two missing.**
  `configSchema: JSONSchema7` does not exist; the shipped member is `configKeys`, a closed enumerated
  vocabulary. `icon` is a `LucideIcon`. `description` and `Body` are required and were absent. The
  one rule §7 attaches to the interface — no switch over gadget key in the render path — is honoured
  and was left verbatim.
- **D112 — §7's route tree listed two space-scoped routes that were never built.** Views and
  dashboards are **org-scoped**, and correctly so: a saved view spans spaces by design, so nesting it
  under a space id would misstate its scope. `ShareBadge.tsx` was also listed under `web/src/shell/`;
  it is in `web/src/components/`.
- **D113 — §9's P4 DoD said unknown query fields return 400.** They return **422**
  `VALIDATION_ERROR`, pinned by an integration test — and §4 of the same document already said so.
- **D114 — §4 presented the saved-view filter document as v1.** The build writes **v2**: four
  half-open date ranges and a per-field `Not`. v1 remains valid and round-trips byte-identically.
  Carried into the spec because it is the stated mapping target for the anticipated importer: **the
  relative month unit is `mo`, never `m`** — JQL's `m` means minutes, and a shared spelling would let
  an importer mistranslate by three orders of magnitude.
- **D115 — §9's tag table stopped at v0.3.2 and asserted "P3 is not contained in any tag".** Six
  v0.3.x tags exist; P3 is in four of them. `git describe` reads nineteen commits past the newest
  tag, not four.
- **D116 / D117 — the search and dashboard migration numbers.** §4's heading, §9's P5 and P6 entries,
  and Appendix B's decision C4 all still called them unassigned. They shipped as **048** and **049**.

### `README.md`

- **D119 — the Features list advertised SSO and email ingestion.** `internal/core/sso` is an
  interface plus a no-op returning `ErrNotConfigured`, not imported by `main.go` or the router. The
  email parser and `CreateFromEmail` exist and are tested but have **zero non-test callers** — no
  IMAP client, no POP client, no inbound webhook, no poller. Both moved to a clearly-labelled **"Not
  yet shipped"** section rather than deleted, so the intent survives and the claim does not.
- **D120 — "Go 1.23+".** `go.mod` requires **1.26.0**; CI and the release image use 1.26.5.
- **D122 — the README said Compose defaults `SMTP_PORT` to 25.** Compose forwards it **bare**, and
  its own comment records that `${SMTP_PORT:-25}` was *removed* precisely because it diverged from
  the binary's 1025 — it is the worked example for why every setting is now forwarded bare. The
  parenthetical also pointed at `docs/self-hosting.md`, which says 1025. Deleted, not renumbered.
- **D123 — "Project Structure" listed 9 of 26 `internal/core` packages** and omitted four top-level
  directories, including `access`, `teams`, `spaces`, `workflow`, `search`, `portal`, `views` and
  `dashboards`. Regenerated. `notifications` and `analytics` are deliberately **not** added — the
  first has zero importers repo-wide (the wired package is `internal/core/api/notifications`), the
  second is imported only by its own test.
- **D124 — the CLI list omitted `bundle-hash`**, which is the preflight the E2E suite runs.
- **D125 — "full CRUD" for six resources.** Labels have no update endpoint and sprints no delete.
  Softened; adding the endpoints is a product call and was deliberately not bundled into a docs pass.

### `CLAUDE.md`

- **D126 — §2 described `NewTestDB` as a per-test *schema* with migrations applied per call.** It
  clones a per-test **database** from a pre-migrated template whose name embeds a SHA-256 fingerprint
  of the migration set. Migrations run **once**. Both halves mattered: the `Schema` field survives
  only to feed a `search_path`, and a reader who believes in per-schema isolation reasons wrongly
  about migration cost too. CI's own comment already said so.
- **D127 — two drifted counts in §3.** `webServer.env` has nine entries, not seven — the count is now
  **dropped** rather than incremented, since the argument does not depend on it. And
  `playwright.config.ts` reads `E2E_PORT` in **four** places, not three; the missing fourth is
  `APP_BASE_URL`, and it is not cosmetic — without it a captured portal magic link points at 8080,
  and a real dev server answering there makes the test pass for the wrong reason.
- **D128 — "`make verify-api` needs `.env.test`" without saying the target does not load it.** Unlike
  the six other database-touching targets, `verify-api` has no `export $(ENV_TEST_VARS)`. With
  nothing exported the script falls back to the **dev** database on `:5432` — so if a dev stack is up
  it passes against the wrong database instead of failing. The corrected text gives the export line.
- **D129 — the retained superseded eslint paragraph said "46 errors", in the present tense.** The
  repo records **48** in two places, one of which explicitly corrects 46 to 48. Fixed, and the whole
  block moved to past tense so a skim cannot read it as current. Also: §3's verification battery
  listed two frontend gates; there are three.

### The ADRs (dated Correction notes; no decision rewritten)

- **D92 — ADR-0011's "no link table exists" is false and inverted.** Full evidence in D147 above. A
  polymorphic typed link table has existed since migration 004, is polymorphic since 015, and is live
  end to end through a service, three routes, three hooks and a Relations panel. Conversely
  `project_items.parent_id` — the thing the ADR offers as what *does* exist — is absent from both
  request structs, has no reparent route, and is null on every item the application creates. **The
  deferral may stand; the stated reason must not.** Discovered in the parity review and never carried
  into this ledger until now.
- **D95 — ADR-0012 §4 promised a Jira-issue/item embed and "Codex↔Vector embedding".** Eight macros
  shipped; the item embed did not, and no such node exists. The absence is deliberate and reasoned in
  code — a cross-space route-shape question ADR-0010 governs — but the reason lived only in code. The
  zero-silent-data-loss guarantee is still met: an imported Jira macro is preserved as
  `unknownContent`, this ADR's own designed fallback.
- **D99 — ADR-0008 rule 10 asserts a nightly sweeper.** None exists, and the repo decided against
  one: revoke-on-delete and revoke-on-move run in the mutation's transaction, shares are never
  hard-deleted, and expiry is evaluated in the resolution query, so there is no orphan class to
  collect. Two things make it unarguable — **rule 8, five lines above, already says expiry denies
  "without waiting for a sweeper"**, and there is a passing test named
  `TestShare14_ExpiredDeniesWithoutSweeper`. The rule itself is honoured in full.
- **D101 — ADR-0003 justifies the table split partly on SLA clocks and first-response/resolution
  timers.** None exists — no table, timer, target, calendar, pause or breach column, and no queue
  ordering by breach risk. Every *other* attribute in both lifecycle lists is real. The decision is
  untouched and is being honoured; the rationale is corrected so it stops being cited as evidence
  that SLA machinery exists, which the parity review already records a reader doing.
- **D102 — ADR-0007's capability table omits `set_visibility`,** the thirteenth capability and the
  only org-level one. No space role holds it; it is granted only by the org-admin bypass, and because
  there is no space to check at creation time it is asked through `CanOrgWide`, not `Can`.
  Structurally distinct enough to carry its own map and a build-time exhaustive-partition test. This
  ADR is the **only** human-authored statement of the capability model — spec §3 is now a pointer —
  so the omission left it documented nowhere but the generated OpenAPI.
- **D103 — ADR-0012 §5 requires a fidelity report from every import; there is no importer.** `cmd/`
  holds `migrate` and `server`. `internal/assess` is a read-only assessor with a test keeping it
  unable to reach a database. The consequence worth stating: **the three preservation carriers have
  no producer** — correct, tested, drift-guarded in both directions, and never reached by real
  imported content. The migration story today is assessment, not migration.
- **D130 — three stale status lines.** ADR-0009 still said "implementation planned for P4 and P5"
  after both shipped; ADR-0010 still said views, dashboards and search were "planned for P4–P6" after
  all three shipped (the sentence spans two lines — an edit anchored on the first leaves the false
  half); and the ADR index still recorded ADR-0011 as "implementation deferred to v0.4", contradicting
  the file it indexes, which had already been amended. A note was added to the index recording that
  the Status column abbreviates each ADR's own line and that the ADR wins.
- **D131 — ADR-0012's Decision section was rewritten on 2026-07-27 and the document recorded
  nothing.** §1 went from one carrier to three, §3 was extended to marks, and the round-trip
  consequence was tightened — a decision-level change, authorised, and invisible to any reader of the
  file. Meanwhile a code comment already cited "the S1 amendment" as established fact. The amendment
  block and the `Amended:` header field are added now, several days late. This is the precise failure
  the ADR directory's own preamble exists to prevent.

### `shared-surfaces.md`

- **D132 — the guard-class table listed ten classes; the vocabulary the sweep enforces has twelve.**
  `org-read` appeared only in later prose, and `portal-session` was absent from the entire document —
  not just the table — although it guards the customer-portal requester surface and its own code
  comment calls it "the only route family reachable from the public internet by someone with no
  account".
- **D133 — the description of `TestReadPathSweep_GuardClassMatchesMiddleware` understated it and
  mis-stated it.** It now checks **three** classes, not two, and not uniformly: `org-admin-404` and
  `portal-session` are bidirectional, `org-admin` is **one-directional** — nothing catches a chain
  carrying `RequireOrgAdmin` under a weaker claim. Neither of the two **prefix rules** was documented
  here at all, though they are the stronger guarantee: a route added under the admin or portal
  subtrees fails on its chain even if its row is honest. `deliberatePublicPortalRoutes` is
  deliberately empty — a new public portal route belongs outside `/my/`, not inside it with an
  exemption. `public` and `user-scoped` were also missing from the list of unverified classes.
- **D134 — §2 named three pass-through error codes; `friendlyErrorMessage` honours four.**
  `INVALID_TRANSITION` is genuinely on the wire from two handlers with a 409 — the opposite of the
  `GONE` case this document already annotates as inert.
- **Checked and found accurate:** the route-accounting row count. It says **217** and the map holds
  217 (218 method-keyed strings in the file, one of which belongs to `deliberateNonAdminRoutes`).
  This figure has drifted four times — D59, D64, D83 — and did **not** drift this time. Recorded
  because a "checked and correct" is worth as much to the next pass as a correction.

### `known-issues.md` (non-security entries only)

- **D135 — §14 described a label admin UI that does not exist**, and was therefore stale in the
  **worse** direction: the real gap is larger than recorded. There is no label admin page anywhere,
  the route renders a "coming soon" empty state, and both client functions are orphans. Anyone
  scoping the fix from the old wording would have budgeted a join-table migration and missed that the
  whole creation surface must be built too. The Proper fix now names both halves, and the
  verification bound moved from "migrations 001-028" to 001-050 (the claim survives the widening).
- **D136 / D137 — two stale citations, and the reason they are now converted rather than
  renumbered.** §21 cited `comments/handler.go:264` for the un-orged `comment.created` event; §30
  cited `projects/item.go:114` for the hardcoded status. Both were wrong by roughly ninety and
  seventeen lines. Both substantive findings were re-derived independently and both still hold —
  §21's "exactly two un-orged audit events" claim was re-run across all 34 non-test `audit.Event{}`
  literals and confirmed.

  **The first draft of this pass renumbered them, to `:354` and `:131`. Both were wrong again
  before the PR merged**, moved to `:377` and `:135` by #101 landing in between — a correction with
  a shelf life of one merge. They now cite symbols: `CreateComment` in
  `internal/core/api/comments/handler.go` (the file's sole `audit.Event{` literal) and
  `ItemService.CreateItem` in `internal/core/projects/item.go`. That is **D148**, below.

### The operational documents

- **D138 — `upgrade.md`'s rollback led with `psql … < backup-pre-upgrade.sql`,** a file no backup
  step has ever produced: `azimuthal backup` only writes a gzip-compressed tar, and the SQL dump is a
  member *inside* it. It failed with "No such file or directory" at the worst possible moment,
  mid-rollback. Replaced with a `tar -xzO … | docker compose exec -T db psql` form, which works
  because the `db` service is `postgres:16-alpine` and does carry `psql` — and which therefore
  survives D105 rather than depending on it.
- **D139 — `JWT_PRIVATE_KEY_PATH` was absent from the self-hosting environment reference.** It is the
  only one of `.env.example`'s 24 variables missing from the file `.env.example` itself calls "the
  full reference". Added adjacent to the "there is no signing secret" callout so the two are not read
  as contradictory.
- **D141 — three troubleshooting steps run utilities the image does not contain.** Two run `env` and
  one runs `ls`, inside a distroless image with neither. (`grep` is fine — it runs host-side on the
  far end of the pipe.) The `ls /web/dist/` check is doubly wrong: the frontend is compiled *into* the
  binary by `//go:embed`, so that path never exists in the image and the check would report a false
  negative even with `ls` present. Replaced with `azimuthal bundle-hash`, which the repository already
  ships for exactly this, and host-side `docker inspect` for the env checks.
- **D142 — documented commands reach Postgres and MinIO on `localhost` ports the bundled Compose file
  does not publish.** Only `app` declares a `ports:` mapping; `db` and `storage` are reachable only on
  the Compose network. Both host-side commands fail with connection refused on a stock deployment.
  (The dev and test overlays do publish ports — they are not the file this guide deploys.)
- **D143 — the gosec annotation census read 8 `#nosec` / 36 total; it is 11 / 39.** Notable for *how*
  it was wrong: this was a **miscount at authoring time**, not later drift. The census paragraph was
  written in PR #97, the three unaccounted-for directives arrived in PR #96 — an ancestor — so they
  were already in the tree on the day a paragraph whose next line corrects two other counts was
  written. All 39 were re-checked against the policy and all still comply. **A hand-maintained count
  that has now been wrong twice should be a test.**
- **D144 — "production paths in `cmd/server/` use `#nosec`, test helpers use `//nolint:gosec`" is not
  the split in the tree.** Five annotations contradict it in both directions, one of them a test
  helper whose reason reads "Same idiom and same rule as `cmd/server/backup.go`" — a test file citing
  a production annotation as its model, the exact inverse of the advice. The real rule is the one the
  document states correctly two paragraphs earlier: write whichever directive the failing tool reads.
  This one matters more than the census: it is the sentence that tells a contributor which directive
  to write.
- **D145 — `local-dev-requirements.md` set a swag minimum CI's own pin does not satisfy.** It said
  "Minimum version: v2.0.0"; CI pins `v2.0.0-rc5`, and under semver a prerelease precedes its release.
  A contributor who checks their version against the stated floor reads it correctly and concludes
  they are below it. Worse, `make docs-check` byte-diffs regenerated output, so a different generator
  fails the gate with an error naming the spec, not the toolchain. Changed from a minimum to an exact
  pin.
- **D146 — the same file claims to list "all tools required for local development" and omits the Go
  toolchain entirely** — the tool every entry in its own Go Tools section is installed through. Node,
  the less constrained of the two, gets an explicit minimum. Added, with the failure mode stated
  precisely: it is *quiet*, because `go.mod` carries no `toolchain` directive and nothing sets
  `GOTOOLCHAIN`, so an older Go silently downloads 1.26.0 rather than erroring.

---

## 5. Claims checked and found accurate — recorded so the next pass does not re-litigate them

A drift is sometimes the document being right. These were checked against code and **needed no
change**:

- **`CLAUDE.md` on the eslint gate.** The brief that commissioned this pass said the correction was
  owed; it had already landed. `npm run lint` **is** a required CI gate, there is no baseline file,
  exemptions are per-filename scoped overrides in `web/eslint.config.js` with counts and reasons in
  their headers, and `known-issues.md` #17 records the gate closed. Only the retained superseded
  paragraph's "46" was stale (D129).
- **`shared-surfaces.md`'s route-accounting row count** — 217, and the map holds 217. Four prior
  passes had to correct this number; this one did not.
- **ADR-0011's Tier-1 guard vocabulary** — exactly the four kinds the amendment names, matched by
  `guard.go` and migration 046's CHECK, with the `actor_has_capability` subset matching the four
  capability constants verbatim.
- **ADR-0011's Tier-2 and Tier-3 amendments** — approvers are user or team with role genuinely absent
  rather than approximated; `set_field` ships over `due_at` and `labels` only; `assign_to` ships; team
  assignment is genuinely not representable (`assignee_id REFERENCES users(id)` on both tables);
  post-functions run inside the status transaction; an unperformable action aborts rather than being
  skipped. Every row checked.
- **ADR-0011's "no resolution field" claim** — correct; only `resolved_at`, a timestamp, exists.
- **ADR-0012's three preservation carriers** — all three exist and are drift-guarded in both
  directions. The best-engineered thing in the repository, and it holds up.
- **ADR-0009's four degradation rules** — all four implemented, server-computed and rendered, and the
  no-switch-over-gadget-key rule is honoured as a map read.
- **ADR-0008's rule 10 first sentence, rule 8, and rule 11** — revoke-on-delete and revoke-on-move run
  in the mutation's transaction, and expiry is evaluated per request.
- **`README.md`'s configuration section** — no `JWT_SECRET`, `DATABASE_URL` the only required
  variable, `LOG_LEVEL` live and refused at startup if unrecognised. D70 is closed.
- **`known-issues.md` §21's "exactly two un-orged audit events"** — re-derived across all 34 non-test
  `audit.Event{}` literals; still exactly two.
- **`known-issues.md` §30's load-bearing correction** — `CreateProjectItem` does name `status` in its
  INSERT column list, so migration 014's `DEFAULT 'open'` is never evaluated and changing the column
  default would fix nothing. Only the line citation was stale.
- **`docs/security-scanning.md`'s qualitative claims** — zero accepted-risk suppressions, every
  annotation naming a rule and a reason, no tracking issues or expiries needed under the policy, and
  `nolintlint` enabled without `require-explanation`. Only the counts and the split were wrong.

---

## 6. D-number reconciliation — the ledger numbering is canonical

The brief for this pass recorded that the workflow phase's brief used D-numbers running **seven
behind** this ledger (its D65 = D72, D67 = D74, D68 = D75), and asked for the two to be reconciled or
the mapping documented once, authoritatively. **This is that record, and the finding is that there is
nothing in the repository to fix.**

- **This ledger is canonical.** Its numbering is dense and unique from D1 through D91, and this
  section continues at **D92**.
- **Every in-repository cross-reference already uses it correctly.** `known-issues.md` §30 cites D72
  for the ungated first transition; the spec cites D77, D78 and D81 in its migration discussion;
  D85's own text cites D72; the workflow section cites D71, D74 and D75. Each resolves to the right
  entry. There is no in-repo reference using the offset numbering.
- **The offset existed only in an out-of-repo phase brief.** Phase prompts are not checked in, so
  there is no artefact here to correct — only a hazard to name.

**The rule to carry, since the underlying hazard is real:** a phase brief that assigns D-numbers is
guessing, exactly as a pre-assigned migration number is (D73, D81). **Read the tail of this file when
you write an entry, not the number your brief gave you.** If a brief and this ledger disagree, this
ledger wins and the brief's numbers are noise.

**Next free D-number: D149.** (D92–D148 are taken by this section, including the `-code` suffixed
companions in §3, which are deliberately suffixed rather than separately numbered because each is the
in-code twin of a numbered documentation entry.)

---

## 6a. D148 — documentation cites a symbol and a file, never `file:line`

**Minted 2026-08-01, on the maintainer's instruction, and recorded here because this pass is the
evidence for it.** The rule now lives in `CLAUDE.md` §6:

> Cite `ItemService.CreateItem` in `internal/core/projects/item.go` — never
> `internal/core/projects/item.go:131`. Where a line genuinely needs pinning, quote the line's text
> alongside it so a reader can grep when the number rots. When you correct a citation, **convert it
> to symbol form rather than renumbering it** — renumbering buys one merge.

**Why this is a rule and not a preference.** A line number is the one form of evidence that goes
stale on *every* merge, including merges that change nothing about the claim it supports. A symbol
moves only when someone renames or deletes it — the same event that invalidates the claim anyway —
so a stale symbol reference is a real signal and a stale line number is noise.

**This pass is the proof.** D136 and D137 were themselves line-number corrections: `:264` → `:354`
and `:114` → `:131`. Between this branch being pushed and the PR merging, **#101 landed and moved
both again** — to `:377` and `:135` — along with `content_tx.go` `:309` → `:315`. Three of the four
citations under review were wrong a second time, and two of the three *were the corrections*. A
correction with a shelf life of one merge is not a correction. Set against the wider record —
`shared-surfaces.md`'s route count drifted four times, the §4 migration table six — the pattern is
not carelessness, it is the format.

**Two carve-outs, both narrow and both deliberate.**

- **Migrations are immutable once shipped** (§10), so a line range into `migrations/*.sql` is
  genuinely stable and is kept. Those are the only line citations this pass left in place on
  purpose.
- **Quoting a stale citation you are correcting** — "this said `item.go:114`" — is quoting, not
  citing, and must stay as written or the correction stops making sense.

**Extended in review to cover counts.** A count in prose rots exactly like a line number, and for
the same reason — measured once, then merged past. But "don't cite volatile figures" would be the
wrong rule, because it sweeps up figures that are load-bearing. §6 draws the line by asking what
the figure is *doing*:

- **The figure is the claim** — D45 is about how many mocks exist; the route-accounting table is
  about how many routes are accounted for. Keep it, and back it with a test that fails when it
  drifts. `TestReadPathSweep_EveryRouteAccounted` is the pattern: it walks the fully wired router
  rather than a hand-maintained list and fails bidirectionally, so the table cannot silently
  disagree with reality. A figure guarded that way is evidence; a figure nobody re-measures is a
  rumour with a decimal point.
- **The figure is incidental support** for a claim that stands without it — "the §4 envelope is
  what governed, and here is how many PRs prove it". Cut it; the claim is stronger without a number
  that expires.

The occasion for the extension was this PR shipping the §10 amendment note with "52 squash-merged
PRs reached `main`". It was 53 by the time it landed, moved by #101, and would have been 54 after
this PR — a fact stale by one merge inside the note explaining why stale facts are corrosive. Both
copies are now count-free. The four counts this pass *kept* — 47 migrations, 217 route-accounting
rows, 40 Go mock types, 26 `internal/core` packages — were re-verified after the rebase and are all
of the first kind. Two of them already carry a "this should be a test" note (D143 for the gosec
census, and the route count, which has drifted four times).

**Scope applied here.** All 24 `file:line` citations this pass introduced were converted, not only
the four the review flagged — minting a convention and shipping 24 violations of it in the same PR
would be the D106 failure mode one order of magnitude smaller. **Pre-existing ledger entries were
deliberately not retro-converted**: D85's citation of `item.go:114`, for instance, is another
phase's historical record and was accurate when written. The convention is forward-looking, and
converting a past entry would edit a record rather than fix a document.

---

## 6b. Workflow enforcement — four entries settled, D149–D152 opened

Added by the workflow fail-closed phase, which is a CODE pass rather than a reconciliation pass. The
new entries are here because this is where the ledger lives; the four settlements above them are the
point of the phase.

### D71 — status and `workflow_state_id` drift apart — **SETTLED**

Both position columns are now written together by every route that moves an entity, through
`UpdateTicketWorkflowState` and `UpdateProjectItemWorkflowState`.
`migrations/051_workflow_state_backfill.sql` reconciles the rows that predate this by name-match,
resolving the workflow through the entity's own space rather than through the org's seeded default —
the mistake a copy of migration 016's block would have made, since a space can now be pointed at any
workflow.

It deliberately does NOT rewrite `status`, and does not fall back to the initial state for rows whose
status names none. Both are decisions about user-visible data; that migration's header records why
each was refused. Rows it leaves NULL are handled at read time by `TierService.ResolveFromState`.

### D72 — a new item's first transition is ungated — **SETTLED**

Closed by option (e) of known-issues #30, the one that entry recommends: entities are born in their
space workflow's initial state with both columns written, resolved through
`tiergate.Gate.InitialPosition`. `TestTierAPI_ANewItemsFirstTransitionIsGated` was skipped and now
runs with its assertions unchanged.

The gate-level initial-state fallback that #30 rejects is NOT what shipped. `ResolveFromState`
consults the stored `workflow_state_id` *before* falling back, so a renamed state resolves exactly
and the fallback is reached only when neither recorded position resolves. That distinction is only
available because the same phase repaired D71.

### D91 — an approval's verdict and its transition are two writes — **SETTLED**

`TierService.Decide` commits both through `ApprovalApplier.DecideAndApply`, one transaction. The
route's "the approval was recorded but the transition could not be applied" branch is deleted rather
than left as a comment on a fixed bug: the state it described is unreachable.

The second half of the same defect — that the apply was unconditional — is closed by
`ApplyInput.ExpectFromStatus`, a compare-and-swap predicate on the status write. An approval decided
after the entity has moved on fails the whole transaction instead of blind-overwriting, and the
verdict rolls back with it so the request is still decidable.

### D93 — condition-class guards are configurable and never evaluated — **SETTLED, both parts**

Both halves of the follow-up this ledger specified, with the warning it gave about testing heeded.
`TierService.OfferedTransitions` is reachable at
`GET /orgs/{orgID}/spaces/{spaceID}/workflow/entities/{entityType}/{entityID}/transitions`, and
`TierService.Gate` evaluates `GuardConditionClass` regardless of what the client asked first.

The ledger warned that a test added to `tier_service_test.go` proves nothing about reachability.
`TestWorkflowFailsClosed_OfferedTransitionsAndTheMutationRouteAgree` therefore drives both halves
through the router. The interim mitigation this entry suggested — suppressing `condition` in the
admin picker — was NOT taken, and must not be now: the class works.

### D149 — a missing edge answers 409, not the 422 the phase brief asked for

The brief specified 422 for both structural refusals. `tiergate.Refused` answers **409
INVALID_TRANSITION** for `CheckNoSuchTransition` and 422 VALIDATION_ERROR for everything else.

`tickets.ValidateTransition` has answered 409 INVALID_TRANSITION for "cannot transition from x to y"
since before workflows existed, the Beacon board rolls a card back on it, and now that the workflow
adjudicates the same question it must not answer it under a different number. An unknown target
status keeps 422, which is the brief's own wording — "status not in the workflow" is exactly that
case. Recorded as a deliberate departure rather than an oversight.

### D150 — `workflow.Engine` is no longer wired into any request path

The two engine-backed `/workflow-state` routes ran `Engine.ValidateTransition` and carried their own
"fall back to the initial state" branch — a second legality authority that placed the entity by its
stored state id while the `/status` routes placed it by status text. On a drifted row the two
disagreed about which edge was being traversed, which under D71 was nearly every row.

`workflows.NewHandler` no longer takes an `Engine`. The type and its tests remain; it has no
production caller. Left as a live exported API rather than deleted, because deleting an exported
symbol is a contract change — but a reader should know it is no longer load-bearing.

### D151 — `Repository.GetState` resolves a state id across every workflow in the installation

Found while removing the engine check. It takes a bare id, so a state belonging to another org's
workflow used to resolve happily on the `/workflow-state` routes and was stopped only by the engine
check that no longer runs there. `Handler.targetStateInSpace` now reconciles the state against the
space's own workflow and answers 404; `WorkflowTierAdapter.StateByID` does the same on the read side.

`Repository.GetState` itself is unchanged and still unscoped. Its other callers were not audited by
this phase.

### D152 — `spaces.workflow_id` has no `applies_to` check

`AssignWorkflowToSpace` is `UPDATE spaces SET workflow_id = $1 WHERE id = $2`, and `GetSpaceWorkflow`
never checks `applies_to`, so nothing stops a beacon space being pointed at a `project_items`
workflow. Pre-existing and unchanged — but materially more visible now the workflow decides, because
such a space would refuse every ticket transition with `no_such_transition`, the graph it is checked
against being the wrong one.

Not fixed here: it is a constraint decision with a data question attached (what to do with any
existing mismatched rows), which is a maintainer's call rather than a phase's.

---

## 7. What this pass deliberately did not touch

- **Specification §2.** Non-negotiable text. Three findings land on it — D97 (skip enforcement),
  D118 (no mocks), D98 (the coverage floor) — and all three are recorded rather than edited.
- **Specification §10 — with one exception, on the maintainer's ruling.** §10 was untouched when
  this pass was written. The maintainer then settled **D106** and lifted the fence for that one
  section, so §10's git-operations paragraph *is* edited here. Everything else in §10 is untouched,
  and the fence stands for the next pass: a reconciliation pass does not amend a non-negotiable
  without a ruling.
- **The roadmap.** The phase→version headings in §9 are stale and were left stale (D104). `CLAUDE.md`
  §1 forbids an agent editing the roadmap, and §9's own text forbids renumbering in a reconciliation
  pass.
- **`known-issues.md`'s security entries.** Owned by the concurrent write-authorization track. Not
  read for drift, not edited.
- **Any ADR decision.** Every ADR change in this PR is either a status line, an appended dated
  Correction note, or an amendment block recording a change that had already been made and not
  written down. No Decision section was rewritten.
- **Any code.** Ten findings need a code change — five substantive (D93, D98, D105, D140, D147) and
  five in-code companions to documents corrected here (D92-code, D96-code, D119-code, D132-code,
  D134-code). All ten are in §3 with the evidence a follow-up needs, and none was fixed here. Two
  further code items are recommended under §2 rather than ledgered as defects, because building each
  one *is* the decision: the skip-enforcement test (D97) and multi-key rotation (D100).
