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
make test-db-down
```

All three of `make test-live`, `make verify-api` and `make e2e-test` exit 0, or the PR is
**DRAFT** with a per-item reason.

### Two environment facts that have cost time in every phase

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
- **Never push to `main`.** **Never force-push.** **Never merge your own PR.**
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
