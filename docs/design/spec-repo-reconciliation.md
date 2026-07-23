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
