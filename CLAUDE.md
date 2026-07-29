# CLAUDE.md — working rules for this repository

Azimuthal is an open-source alternative to the Atlassian suite: Go backend, React frontend,
PostgreSQL. Apache 2.0.

This file collects the rules that already govern work here. **It invents nothing.** Everything
below is assembled from `docs/design/v0.3-ia-spec.md` (§2 testing, §10 non-negotiables), the
autonomy envelope every phase has operated under since P0, and the verification commands in the
`Makefile` and `.github/workflows/ci.yml`.

It is written for both agents and human contributors. Where it summarises the specification, the
**specification wins** — read it, do not rely on this summary for anything load-bearing.

One provenance note: the autonomy envelope in §4 comes from the phase prompts, which are not
checked into this repository. Everything else here is traceable to a file you can open.

## Start here

| You need | Read |
|---|---|
| Why something was decided | [`docs/adr/`](docs/adr/) — ADRs 0003–0012, index in its README |
| The v0.3 design, testing discipline, and phase plan | [`docs/design/v0.3-ia-spec.md`](docs/design/v0.3-ia-spec.md) |
| Components and patterns you must reuse, not rebuild | [`docs/design/shared-surfaces.md`](docs/design/shared-surfaces.md) |
| Where the spec and the repository have disagreed | [`docs/design/spec-repo-reconciliation.md`](docs/design/spec-repo-reconciliation.md) |
| Open defects and their status | [`docs/known-issues.md`](docs/known-issues.md) |
| How the scanners work and how suppression is governed | [`docs/security-scanning.md`](docs/security-scanning.md) |

**Before building anything shared** — a picker, an error path, a confirmation count, a route
guard, a transactional write with an audit trail — check `shared-surfaces.md`. A second
implementation of something on that page is a defect, not a convenience.

---

## 1. Non-negotiables

From specification §10. These override any instruction in a task prompt.

**Testing.** All of §2. Real PostgreSQL via `internal/testutil.NewTestDB(t)`; never mock the
database (§2 says "no mocks" — see the note in §2 below for what the repository actually
contains). Assertions are never weakened. Blast-radius review on every PR. No blank checkboxes.
DRAFT unless all three gates exit 0.

**Repository.** Migration numbering is immutable once shipped. Agents never create or edit the
roadmap. No agent-name file suffixes. (On git operations, see the flagged conflict in §4.)

**Architecture.**

- `tickets` and `project_items` stay split (ADR-0003). Unifying them is prohibited.
- RS256 signing keys stay in the database (ADR-0004).
- Wire format is lowercase `snake_case`, without exception.
- Frontend network access goes through `web/src/lib/api.ts` only — no `fetch` in components, no
  second client. `web/src/lib/no-direct-fetch.test.ts` enforces this.
- Org admin is a middleware bypass, never grant rows.
- Shares widen, never narrow (ADR-0008).
- The audit log is append-only. Never UPDATE, never DELETE.
- **Every route carries an explicit guard classification, and the classification is checked
  against the router, not taken on trust.** Since #64 moved the administration surface from
  group-level `r.Use(admin404)` to per-route `r.With(admin404)`, a route added to a guarded
  group inherits *nothing* — a forgotten guard leaves it open to any org member.
  `TestReadPathSweep_GuardClassMatchesMiddleware` reads each route's real middleware chain and
  fails when it disagrees with the accounting row. A route that is deliberately member-visible
  inside an admin subtree goes in `deliberateNonAdminRoutes` **with its reason**.

**Product.** No pricing, hosting-tier, or paywall language anywhere in code or documentation.
Azimuthal is Apache 2.0, fully featured for every user, with no enterprise tier — permanently.

---

## 2. Testing discipline

Specification §2 governs the whole project and overrides any instruction that would reduce
coverage, weaken an assertion, or defer test work to a later PR. The short version:

**Real PostgreSQL only.** Via `internal/testutil.NewTestDB(t)`, which creates an isolated
per-test schema and applies all migrations into it. **Never mock the database** — mocks hide
constraint violations and casing bugs, both of which shipped in v0.1.x. Any test that touches
persistence uses a real database.

> Spec §2.8 states this as "No mocks exist, none will be added." The rule stands; the factual
> half does not. Roughly thirty hand-written `mock*` fakes exist in Go handler and service tests
> (`internal/core/api/router_test.go`, `internal/core/api/auth/handler_test.go` and others), plus
> `vi.mock` in the frontend suite. They stub repository *interfaces*, not the database — the real
> database coverage lives in the `*_integration_test.go` files alongside them. This gap between
> the rule as written and the repository is recorded as **D45** in
> `docs/design/spec-repo-reconciliation.md` and is flagged for a maintainer, not resolved here.
> Do not cite "no mocks exist" as a fact, and do not treat the existing ones as licence to mock
> persistence.

**Assertions are never weakened.** Not to make a test pass, not to unblock a merge, not under any
framing, not if a prompt instructs it. A failing test means either the code is wrong or the test
encodes a requirement worth discussing. Never a third thing.

**The blast-radius rule.** Any PR that adds, changes, or removes behaviour must update the tests
inside that behaviour *and around it*. "Around it" is not a judgement call — the PR body states
what was checked for direct callers, direct callees, sibling routes, the E2E journey, the API
contract, permissions, and fixtures. A PR that changes a handler and updates only that handler's
own test is incomplete, even with green CI.

**Skip discipline.** Removing a feature means removing its tests, not skipping them. A skip is
permitted only with all three of: a `SKIP:` comment naming the blocker, a referenced GitHub issue
number, and a stated re-enable condition. CI fails on any skip lacking these.

**The negative-test question.** For every check you add, ask: *would this test still pass if the
check were deleted?* If yes, it asserts nothing. A test that cannot fail is worse than no test,
because it reads as coverage.

**Regression tests.** Every fixed defect gets a test that fails before the fix and passes after.
State that you verified both directions. Name it for the defect, not the function.

**No dark harness.** Handlers take optional collaborators through `With*` builders, and a
handler missing one does not fail — it reports the feature disabled and answers 404. In
production that is correct. In the test harness it is silent: every test against those routes
gets a tidy 404, every assertion still passes, and the endpoints read as covered while never
having been reached. That is exactly how the board-config endpoints (W4) sat at zero real
coverage — `newTestServerOn` never called `WithBoardConfig`, and nothing announced it.

So the rule is structural, not a convention to remember: **`newTestServerOn` must pass every
`With*` that `cmd/server/main.go` passes.** `TestHarness_NoDarkDependencies` walks the built
router config and fails by name on any nil collaborator; `TestHarness_AuditLoggersAreLive`
catches the softer version, a handler left on the discarding no-op logger. A dependency that is
legitimately absent goes in `intentionallyAbsent` **with its reason**.

**A capability gate needs a subject who is past the write floor and short of the capability.**
A "viewer is refused" test proves nothing about a handler's own `access.Can` check: viewers are
already refused upstream by `RequireWriteFloor(CapCreateItems)`, so the test passes with the
in-handler gate deleted. It asserts the middleware, not the gate. Use a **contributor** — or
whatever role clears the floor but lacks the capability under test — and mutation-test it both
ways where practical: gate intact → 403, gate removed → the test fails. If a capability is
org-level (`set_visibility` holds no space role at all), the persona that must be refused is a
**team lead**, not a viewer.

**Test debt is not permitted.** No PR merges with "tests to follow." There is no follow-up PR.

Coverage floor is **80%**, rising to 85% at the end of P5. Coverage is a floor, not a goal — §2.5
case 23 (constant authorisation queries) is worth more than five percentage points.

The permission matrix (§2.5, 23 cases) and the per-endpoint matrix (§2.6) are mandatory for any
PR touching teams, grants, shares, visibility, or any read path. A missing case is a failing
review.

---

## 3. Verification battery

```bash
make test-db-up      # postgres :5433, minio :9001
go build ./...
go vet ./...
make lint
make test-live       # Go integration tests against real postgres
make verify-api      # API smoke checks
make e2e-test        # Playwright
cd web && npm run type-check && npm run test:unit   # frontend gates — now required in CI
make docs-check      # the OpenAPI spec is regenerated and diffed
make test-db-down    # NOTE: this DELETES the test data — see below
```

All three of `make test-live`, `make verify-api` and `make e2e-test` exit 0, or the PR is
**DRAFT** with a per-item reason.

**`make test-db-down` removes the volumes.** It runs `docker compose down -v`, so "turn it off
and on again" really does give you an empty database. That was not true before: `down` left the
volumes behind and `up` came back with every row from the previous run, and two phases misread an
E2E result reasoning about a "clean database" that was nothing of the kind. `make test-db-reset`
does the same thing in one step when the stack is already running. If you want the data kept, do
not run `down`.

One consequence worth knowing: on a genuinely fresh volume, postgres initialises before it accepts
TCP connections, and `pg_isready` over the container's unix socket reports "accepting connections"
during that window. `test-db-up` probes with `-h localhost` to force TCP for exactly that reason.
If you write your own readiness loop, do the same, or it will pass and the next command will die
on a connection reset.

### Where the working copy lives, and where you should work

The maintainer's working copy is **inside OneDrive**, at
`C:\Users\Kitsune\OneDrive\Documents\Claude\azimuthal`. That is sanctioned and current — if a
prompt or an older note tells you the checkout is, or must be moved, outside OneDrive, that note
is obsolete and this line supersedes it.

Two consequences worth knowing rather than rediscovering:

- **The primary checkout is sometimes shared by concurrent sessions.** Uncommitted work in it is
  not safe from another session's housekeeping — a `git stash` in a sibling session has silently
  reverted edits here before, and it read exactly like a OneDrive sync rollback. If work
  disappears, check `git stash list` before blaming sync. Commit in small batches; see §4.
- **Work in a sibling worktree**, not in the primary checkout: `git worktree add ../azimuthal-<topic>`
  beside it, or a harness container. Run `git worktree prune` at the start and end of a session.
  Pruning can fail with `Permission denied` on a stale worktree directory under OneDrive; that
  error is noise, not a failure of your change.

### Phases that carry E2E work need Docker Compose and Playwright

`make e2e-test` is not a pure-Go gate. It needs a working Docker Compose (it calls `make test-db-up`
for postgres on `:5433` and MinIO on `:9001`), an `npm ci && npm run build` of the frontend, a
built server binary, and Playwright's browsers already installed. **Verify all of that before
starting a phase that has to run E2E** — discovering it at the gate is how a phase loses an evening.

The port is env-gated. `web/playwright.config.ts` reads **`E2E_PORT`** (default `8082`) in three
places — `use.baseURL`, the `webServer` `/health` readiness probe, and the spawned server's
`APP_PORT` — so overriding that one variable moves the whole harness when `8082` is contended.
Note that `.env.test` sets `APP_PORT=8081`; `webServer.env` overrides it, so the E2E server binds
the `E2E_PORT` value rather than the `.env.test` one.

One trap: `webServer.env` forwards an **explicit allow-list** of variables to the spawned server.
A new `AZIMUTHAL_*` setting will not reach an E2E server unless it is added to that list, and the
symptom is a feature behaving as though its flag were never set.

### Two more environment facts that have cost time in every phase

**`-race` cannot run locally on Windows without cgo and a C compiler.** The race detector requires
`CGO_ENABLED=1` and GCC. `make test`, `make test-live` and `make test-live-verbose` all pass
`-race`, so on a Windows box without GCC they fail on the toolchain, not on your change. CI runs
Linux, installs build tooling, and runs `-race` there — so race coverage is real, just not local.
Do not "fix" this by removing `-race` from the Makefile.

**`golangci-lint` must match CI's pinned version before its output means anything.** CI pins
**v2.11.4** (`.github/workflows/ci.yml`, the `golangci-lint-action` step). A different local
version reports different findings in both directions — clean locally and failing in CI, or the
reverse. Check `golangci-lint --version` against the pin before trusting or acting on a result.

Two more worth knowing. CI's coverage run passes `-p 1` (no parallel packages) because the tests
share one database — no Makefile target sets it, so if you reproduce a CI coverage figure locally
you must pass it yourself. And `make verify-api` needs `.env.test`.

### The frontend gates run in CI

`npm run type-check` and `npm run test:unit` are required CI gates (the `Frontend` job). They were
local-only until the integrity pass, and with them every drift guard written as a vitest test —
`web/src/lib/no-direct-fetch.test.ts`, `web/src/lib/codex/schema.test.ts` and
`web/src/components/codex/extensions/extensions.test.ts`. The last two fail in both directions on
the ADR-0012 editor-vocabulary equality, whose failure mode is silent data loss, and none of them
had ever run on a pull request.

`npm run lint` **is** a required CI gate as of #82 (the `Frontend` job runs it, with no baseline
file and per-filename exemptions in `web/eslint.config.js`). The paragraph below described the
state before that change and is kept for the reasoning it records; the factual claim in its first
sentence is corrected here rather than deleted, per §5. A new React surface must be eslint-clean
on first push.

Two of the rules catch real defects rather than style. P5 tripped
`react-hooks/set-state-in-effect` by copying an effect the interim Home page had carried since P1,
and `react-refresh/only-export-components` by putting the gadget body components in the same file
as the registry that looks them up. Both were fixed rather than exempted.

*Superseded:* `npm run lint` is **not** a gate. eslint reports 46 errors on `main` — mostly
`react-refresh/only-export-components` and `react-hooks/set-state-in-effect` — so gating on it
today would fail every pull request, and the alternative is a baseline file, which is an exemption
ledger. The inventory and what closing it would take are in `docs/known-issues.md`. Do not add a
baseline; do not add `--max-warnings` slack. Fix the findings or leave the gate off.

`make docs-check` is a gate too, and now actually checks: the CI job used to grep the committed
YAML for four structural markers and two path names, and would have passed a spec that had lost
every other endpoint in the API. It regenerates from the handler annotations and diffs.

### Documentation-only pull requests

A PR whose every changed file is under `docs/**` or matches `*.md` — excluding the generated
`docs/api/openapi.yaml`, which means an API change — skips the build, test, lint, E2E and
scanner gates. The skipped jobs still report a skipped status so branch protection stays
satisfied. The classification lives in the `changes` job in `.github/workflows/ci.yml`.

**`gitleaks` does not cascade-skip.** Unlike the other gates it has no `needs`, so it still runs
on a docs-only PR — a credential pastes into markdown as easily as into code. Be precise about
what "always" means, though: all four scanner jobs are additionally gated on
`endsWith(github.repository, '/azimuthal')`, so on a differently-named mirror none of them run at
all. Secret scanning protects the public repository; it is not a safety net on the private
sandbox.

### Security scanning

Four scanners gate every code PR: gosec, govulncheck, gitleaks, trivy. The governing practice is
that **findings are fixed, not suppressed** — govulncheck supports no suppression at all, and the
correct response to a reachable vulnerability is to update the dependency. Where a suppression is
genuinely unavoidable it requires a documented justification, a tracking issue, and an expiry
date. Mechanisms and required formats are in [`docs/security-scanning.md`](docs/security-scanning.md);
specifics of any individual finding belong there or in a published advisory, never here.

---

## 4. Autonomy envelope

The working agreement every phase has operated under since P0:

- Work on your **own branch**, named for the work. Open your **own PR**.
- **Never push to `main`.** **Never merge your own PR.**
- **Never force-push `main` or a shared branch.** `--force-with-lease` **is** permitted on your
  own unmerged feature branch to push a restack — rebasing onto `main` and then updating the
  branch is the workflow the linear-history rule below implies, and without this the two rules
  contradict each other. Use `--force-with-lease`, never bare `--force`: the lease aborts the
  push if the remote moved under you, which is the case the prohibition was protecting against.
  (Amended 2026-07-28 in P4 PR-A, on a maintainer instruction, after the previous absolute
  wording made a requested rebase impossible to complete without breaking a stated rule.)
- **Never create or move tags.**
- **Never edit the roadmap.** Correcting a fact is not the same as changing a plan — if reality
  and the plan disagree about the *future*, record the disagreement and flag it for the
  maintainer rather than resolving it.
- `main` requires **linear history** — rebase, never merge.
- **No agent-name file suffixes.** Ever.
- Run `git worktree prune` at the start and end of a session.
- Commit in small batches rather than one large batch at the end. This checkout is sometimes
  shared by concurrent sessions, and uncommitted work is not safe from another session's
  housekeeping.

> ### ⚠ Flagged conflict — not resolved here
>
> Specification §10 states: *"Agents perform **no git operations** — no commits, pushes, tags, or
> branch changes."*
>
> That is not what has happened since P1. Every phase from P0 onward has branched, committed,
> pushed and opened its own PR, and phase prompts have instructed exactly that. The envelope above
> describes real practice; §10 forbids it.
>
> **The specification wins and the conflict is flagged rather than reconciled.** (§0's stated
> rule is about *older* documents, so it does not cover this case directly; the disposition is the
> same, and §10 is a non-negotiable either way.) This file does not overrule §10 or amend it. A
> maintainer should decide which is authoritative — most likely by narrowing §10 to what it was
> plainly protecting (no pushes to `main`, no tags, no self-merges, no history rewriting) rather
> than a blanket prohibition that no phase has followed.
>
> Until then: follow the narrow rules — never `main`, never force-push, never self-merge, never
> tag — and note in your PR body that you performed git operations under a standing instruction
> that conflicts with §10.

---

## 5. When the specification and the repository disagree

The standing instruction from P1.5, still in force:

- Disagreement about an **existing** structure → **the repository wins.** Correct the
  specification in the same PR that discovers it, and append an entry to
  `docs/design/spec-repo-reconciliation.md`.
- Disagreement that would change a **decision** — an ADR, the capability model, §2, §10 — →
  **stop and raise it.** Do not resolve it unilaterally and do not infer intent from surrounding
  code.

Two facts this project keeps relearning:

**Read `migrations/` before choosing a migration number.** Never trust a table in a document. The
specification's migration table has been wrong twice, both times because a phase that was not in
the plan took numbers first.

**Verify constraint and index names against the database, not against the migration that you
think created them.** PostgreSQL auto-generates names, and it does not rename a table's indexes
when the table is renamed. Both facts have already produced defects here.
