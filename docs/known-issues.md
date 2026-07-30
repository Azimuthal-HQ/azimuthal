# Known Issues

Documented by Agent 2E (Integration Validator) after validating Phases 0-2.
Updated by test/backend-coverage branch with test references.
Reviewed against the repository after P3 (post-P3 reconciliation): entries resolved by P1-P3
struck, stale premises corrected. No new entries were added in that pass.

---

## 0. ~~Ticket Detail Navigation — Redirect Loop~~ (RESOLVED)

**Severity**: High
**Status**: Resolved in PR #37 (branch `fix/ticket-detail-auth`, since deleted)

**Was:** Clicking a ticket in the service desk caused a redirect loop back to login.
Two bugs were involved:

1. `GET /api/v1/auth/me` returned 401 with a valid token because the route was
   registered in the public auth group (without `RequireAuth` middleware). Chi
   matched the public route first, so claims were nil and the handler returned 401.
   The frontend auth interceptor then treated every 401 as session expiry and
   redirected to `/login`.

2. Comments endpoint returned 404 because no HTTP handler for comments existed.
   The DB layer (`ListCommentsByItem`, `CreateComment`) was implemented but never
   wired to an API route.

**Root cause (Bug 1):** `/me` was registered twice: once in the public `Routes()`
method of the auth handler (no middleware) and once in the protected `/api/v1`
block. Chi matched the public route first.

**Root cause (Bug 2):** Missing comments API handler — only the DB queries existed.

**Fix:**
- Removed `/me` from the public auth `Routes()` method so only the protected
  version (with `RequireAuth` middleware) is reachable.
- Fixed the frontend auth interceptor to only redirect on auth-critical 401s
  (`/auth/login`, `/auth/me`, `/auth/refresh`), not on every 401.
- Created `internal/core/api/comments/handler.go` with List and Create endpoints.
- Registered comments at `/orgs/{orgID}/spaces/{spaceID}/items/{itemID}/comments`.
- Updated frontend to include orgId in all comments API calls.

**Tests added:**
- `TestAuthMe_ValidToken_Returns200`
- `TestAuthMe_NoToken_Returns401JSON`
- `TestAuthMe_SameTokenWorksOnBothEndpoints`
- `TestComments_CorrectURLIncludesOrgId`
- `TestComments_PostAndRetrieve`
- Playwright: `clicking a ticket opens detail view and stays there`
- Playwright: `ticket detail comments section loads without 404 error`
- Playwright: `can add a comment to a ticket`

**Lesson:** Never skip a test because a feature "might not be fully implemented."
A failing test is a signal. A skip is silence.

---

## 1. ~~Missing Repository Adapter Layer~~ (RESOLVED)

**Severity**: High
**Status**: Resolved — implemented in `internal/db/adapters/` by Agent 2F

The domain services (auth, tickets, projects) define repository interfaces using domain types (e.g., `auth.User`, `tickets.Ticket`, `projects.Item`), and the data layer has sqlc-generated queries using DB types (e.g., `generated.User`, `generated.Item`). There is no adapter code bridging these two layers.

**Impact**: The binary serves health/ready endpoints but cannot serve the full API routes because the chi router (`api.NewRouter`) requires service instances, which require repository implementations.

**Design mismatches to resolve**:
- `auth.User` has no `OrgID` field, but `generated.User` and `CreateUserParams` require one
- `auth.SessionRepository.GetByToken` accepts a plain token, but `generated.GetSessionByTokenHash` expects a hashed value
- `generated.GetUserByEmail` requires `OrgID` parameter, but `auth.UserRepository.GetByEmail` takes only email
- Domain types use `time.Time`, `*uuid.UUID`; generated types use `pgtype.Timestamptz`, `pgtype.UUID`

**Note**: The wiki `PageStore` interface uses generated types directly, so `*generated.Queries` already satisfies it. Only auth, tickets, and projects modules need adapters.

**Resolution**: Created `internal/db/adapters/` package with `UserAdapter`, `SessionAdapter`, `TicketAdapter`, `ItemAdapter`, `SprintAdapter`, `RelationAdapter`, and `LabelAdapter`. The OrgID mismatch is resolved by injecting a default org ID at the adapter boundary. The token hashing mismatch is resolved by SHA-256 hashing plain tokens in `SessionAdapter` before calling `GetSessionByTokenHash`. The `GetByEmail` signature mismatch is resolved by the adapter injecting the configured OrgID into the `GetUserByEmailParams`.

---

## 2. ~~Test Coverage Below 60% Floor (47.1%)~~ (RESOLVED)

**Severity**: Medium
**Status**: Resolved — the CI floor is now 80%, enforced, and met

The 60% floor this entry describes no longer exists. P1.5 raised the CI gate from 70 to the 80
the specification requires, and P3 merged at 80.2%. The gate is the "Enforce minimum coverage
(80%)" step in `.github/workflows/ci.yml`, run with `-coverpkg=./internal/...` and `-p 1`. It
rises to 85% at the end of P5 per spec section 2.8. The detail below is retained only as a record
of where the v0.1.x line started; `internal/db` and `cmd/server` are now exercised by the
integration suites.

<details><summary>Original entry</summary>

Overall statement coverage was 47.1%. After adding integration tests (test/backend-coverage branch), cross-package coverage is 82.4% using `-coverpkg=./internal/...`. Previously-lowest-coverage packages:

| Package | Coverage |
|---|---|
| `internal/db` | 1.8% |
| `internal/jobs` | 34.4% |
| `internal/core/api/projects` | 35.8% |
| `internal/core/api/tickets` | 40.8% |
| `internal/core/api/spaces` | 41.5% |
| `internal/core/api/wiki` | 47.0% |
| `cmd/server` | 0.0% |
| `internal/db/generated` | 0.0% (generated code) |

The DB and generated packages are integration-test only (require real Postgres). API handler sub-packages have partial test coverage.

</details>

---

## 3. ~~Race Detector Requires CGO (Windows CI)~~ (RESOLVED as an issue; retained as an environment fact)

**Severity**: Low
**Status**: Resolved — CI runs `-race` with a C toolchain installed

The action this entry asked for is done. `.github/workflows/ci.yml` has an "Install C build tools
(required for -race detector)" step and runs `go test -race` with `CGO_ENABLED=1`, so race
coverage is real on every code PR.

The underlying environment fact is unchanged and is not a defect: `-race` still cannot run on a
Windows box without GCC, so `make test` and `make test-live` fail there on the toolchain rather
than on your change. That is documented as a standing environment fact in `CLAUDE.md` rather than
tracked here. Do not "fix" it by removing `-race` from the Makefile.

---

## 4. Soft-Delete Missing on Some Tables

**Severity**: Low
**Status**: Design review needed

The following tables lack `deleted_at` columns:
- `memberships` — may need audit trail for membership removal
- `space_members` — same concern
- `sprints` — sprint history could be useful for reporting

These may be intentional design choices (ephemeral data), but should be reviewed before GA.

**Still open**, confirmed against migrations 001-028: none of the three has gained a `deleted_at`.
One clarification — `space_members` still exists but is no longer an access-control table. Space
access moved to `space_grants` in migration 023; the remaining `space_members` endpoints are
explicitly legacy metadata.

---

## 5. ~~cmd/server/main.go Does Not Wire Full API Router~~ (RESOLVED)

**Severity**: High (related to issue #1)
**Status**: Resolved — `cmd/server/main.go` now wires the full API router (Agent 2F)

`cmd/server/main.go` now:
1. Connects to the database via `db.Connect()`
2. Runs migrations via `db.Migrate()`
3. Bootstraps a default organisation
4. Constructs all services with DB-backed adapters from `internal/db/adapters/`
5. Calls `api.NewRouter()` with the full `RouterConfig`

All API routes (auth, tickets, wiki, projects, spaces) are served alongside health/ready.

---

## 6. ~~Testing Gap — Real Database Integration Tests~~ (RESOLVED)

**Discovered:** v0.1.4 testing
**Status:** Resolved — permanent fix shipped, and the gap is closed by the integration suite

The permanent fix landed (see below), and both successor tables carry the default inline:
`tickets.labels` and `project_items.labels` are `TEXT[] NOT NULL DEFAULT '{}'` (migration 014).
The broader gap is closed by the real-PostgreSQL integration suite under `internal/core/api/`
and by CI running real Postgres and MinIO containers with migrations applied before tests.

The mitigation clause below pointed at "CLAUDE.md Testing Requirements" when `CLAUDE.md` was
`.gitignore`d and therefore could not exist — see `spec-repo-reconciliation.md` D44. It exists at
the repository root as of this pass, and the governing rules are spec section 2.

Agent tests use `go test ./...` which does not catch database constraint
violations that only surface when inserting real rows with missing fields.

Specific example: The `labels` column on the `items` table had a NOT NULL
constraint with no default value. Creating a ticket or project item with
no labels field caused SQLSTATE 23502. This was not caught by any automated
test because no test exercised minimum-field creation against a real database.

**Mitigation:** All agents must now follow the Testing Requirements in
CLAUDE.md before opening PRs that touch write operations.

**Permanent fix:** Add `DEFAULT '{}'` to the labels column migration and
default labels to `[]` in the item adapter layer (fixed in v0.1.5).

---

## 7. ~~RSA Key Generated at Runtime on Every Startup~~ (RESOLVED)

**Severity**: Medium
**Status**: Resolved — RS256 signing key persisted in the database (migration 018)

JWT signing previously used an RSA key pair generated fresh on every server
start, invalidating all issued JWTs and sessions on restart. The signing key
now lives in a singleton `auth_signing_keys` row and is loaded (or generated
first-writer-wins) on startup via `auth.EnsureSigningKey`. `JWTPrivateKeyPath`
remains only as a one-time import path for deployments upgrading from the legacy
file-based key. There is no `JWT_SECRET` to configure.

Verified live in the Docker Compose stack: a token issued before
`docker compose restart app` still returns HTTP 200 on `/api/v1/auth/me` after
the restart.

**Tests**: signing-key restart-safety suite in `internal/core/auth` (real Postgres).

---

## 8. ~~CORS Allows All Origins~~ (RESOLVED)

**Severity**: Medium (security)
**Status**: Resolved — no CORS headers in any environment unless an allow-list is set

`api.NewCORS` echoes `Access-Control-Allow-Origin` only for origins on an
explicit allow-list from `AZIMUTHAL_ALLOWED_ORIGINS`, and it is now the only
CORS middleware in the codebase.

This entry was previously marked resolved while its own closing sentence
described the part that was not: `config.parseAllowedOrigins` returned `["*"]`
outside production, and `NewRouter` fell back to a legacy permissive `CORS`
middleware — one that echoed `Access-Control-Allow-Origin: *` on every
response — whenever `RouterConfig.AllowedOrigins` was `nil`. That fallback was
a fail-open default rather than a compatibility shim: every construction site
that omitted the field silently got wildcard CORS, which is why no test in the
repository had ever exercised the restrictive path through the router.

The S5 pass closed both halves. The legacy `CORS` middleware is deleted,
`NewRouter` always uses `NewCORS`, and an unset `AZIMUTHAL_ALLOWED_ORIGINS`
yields an empty list in **every** environment — so the default is same-origin
and cross-origin access is an explicit boot-time decision. Nothing in the
product regressed: in production the SPA is served from the same binary, and in
development Vite proxies `/api` server-side (`web/vite.config.ts`), so in
neither case does a browser issue a cross-origin request.

**Breaking change for API consumers**: a browser-based client on another origin
that previously worked by accident now needs the operator to set
`AZIMUTHAL_ALLOWED_ORIGINS`. Server-to-server clients send no `Origin` header
and are unaffected.

**Tests**: `internal/config/config_test.go` — `TestConfig_AllowedOrigins_EmptyByDefaultInEveryEnv`,
`TestConfig_AllowedOrigins_WildcardStaysOptIn`, `TestConfig_AllowedOrigins_ProductionEmpty`,
`TestConfig_AllowedOrigins_Explicit`. `internal/core/api/middleware_test.go` —
`TestCORSMiddleware_EmptyAllowListEmitsNoHeaders`,
`TestCORSMiddleware_UnlistedOriginPreflightIsRefused`,
`TestCORSMiddleware_ListedOriginIsEchoed`. `internal/core/api/router_test.go` —
`TestCORS_CrossOriginPreflightRefusedByDefault`.
`internal/core/api/routes_integration_test.go` —
`TestIntegration_CORS_UnlistedOriginPreflightIsRefused`,
`TestIntegration_CORS_SameOriginRequestIsUnaffected`.

---

## 9. ~~Audit Logger Discards All Events~~ (RESOLVED)

**Severity**: Low
**Status**: Resolved — `audit.NewDBLogger` persists events; wired in production

`internal/core/audit/db_logger.go` writes events to `audit_log` via the `CreateAuditEvent`
query, and `IsAvailable()` returns true. It is constructed in `cmd/server/main.go` and threaded
into the handlers. The no-op `defaultLogger` still exists in the package but is not what the
server uses. P2, P2.5 and P3 all write real audit events through it.

**Tests**: `internal/core/audit/db_logger_integration_test.go` (six cases, real Postgres) and
`internal/core/api/audit_events_integration_test.go`.

**Note on the old references**: `TestAuditLog_PersistsEvents` in
`internal/core/api/known_issues_test.go` is *not* a skipped test — it is an empty function that
asserts nothing and passes unconditionally. It is a placeholder, not coverage. The referenced
`docs/project-state.md` is not missing by accident — `.gitignore` lists it under "private repo
only — never push to public", so it is unreachable from this repository by design. Treat it as a
dead link here.

---

## 10. ~~Profile Update Endpoint Missing~~ (RESOLVED)

**Severity**: Low
**Status**: Resolved — `PATCH /api/v1/auth/me`

The endpoint exists and is wired end to end: route in `internal/core/api/router.go`, handler
`Handler.UpdateMe` in `internal/core/api/auth/handler.go`, service `UserService.UpdateProfile`,
persistence `UserAdapter.UpdateProfile`. It is classified `user-scoped` in the route accounting
table.

It landed at `PATCH /api/v1/auth/me`, not the `PUT/PATCH /api/v1/me` this entry anticipated.

**Tests**: `TestIntegration_Auth_UpdateMe`, `TestUserService_UpdateProfile`,
`TestUserService_UpdateProfile_NotFound`, `TestUserAdapter_UpdateProfile`.

**Note on the old references**: `TestProfileUpdate_SavesChanges` is an empty placeholder that
asserts nothing, not a skipped test. The referenced `docs/project-state.md` is private-repo-only
per `.gitignore` and is unreachable from here.

---

## 11. Duplicate Email Registration Returns 500 Instead of 409

**Severity**: Medium
**Status**: Open — captured by integration test

When registering a user with an already-taken email, the API returns 500
(INTERNAL_ERROR) instead of 409 (CONFLICT). The `UserAdapter.Create` method
does not map postgres unique constraint violations to `auth.ErrEmailTaken`.

**Test**: `internal/core/api/routes_integration_test.go` — `TestIntegration_Register_DuplicateEmail`

---

## 12. ~~Goose Migration Mutex Required for Parallel Tests~~ (RESOLVED)

**Severity**: Low
**Status**: Fixed in PR #33 (branch `test/backend-coverage`, since deleted)

`goose.SetTableName()` uses a package-level global variable. When integration
tests run in parallel, concurrent `goose.Up()` calls with different schema-scoped
table names race and cause `SQLSTATE 42P01` or `SQLSTATE 23505` errors.

**Fix**: Added `sync.Mutex` in `internal/testutil/db.go` around `goose.SetTableName` + `goose.Up` calls.

---

## 13. ~~Smoke Test login_user Failure (Pre-existing)~~ (RESOLVED)

**Severity**: Medium
**Status**: Resolved — the subtest mints a unique address

`cmd/server/smoke_test.go` `TestSmoke/login_user` now registers
`smoke-login-<nanos>@test.local`, expects 201, then logs in expecting 200. It no longer collides
with a previously registered address and no longer depends on issue #11's behaviour.

**Test**: `cmd/server/smoke_test.go` — `TestSmoke/login_user`

---

## 14. Labels: Two Parallel Label Stores (deferral target stale — see below)

**Severity**: Medium
**Status**: Documented — fix deferred to Phase 5 (Items Table Split)

Two label mechanisms exist in parallel and are not linked:

1. A `labels TEXT[]` array column. **The premise has shifted**: `items` was renamed to
   `items_archive` by migration 015, and the live columns are now `tickets.labels` and
   `project_items.labels` (both `TEXT[] NOT NULL DEFAULT '{}'`, migration 014). So there are
   two array columns plus the table, not one. Items store labels here as plain strings and the
   frontend reads and writes the array directly.

2. `labels(id, org_id, name, color)` table — a proper labels admin table with
   color/grouping support. Accessible at `GET /orgs/{orgID}/labels`. Not
   joined to items.

**Impact**: Labels created in the admin UI (`/labels`) have no effect on items.
Items display label text from the array; color metadata is never shown.
There is no way to query "all items with label X" efficiently.

**Root cause**: The design was split across two PRs during Phase 0/1 with
conflicting approaches. Neither was backed out before shipping.

**Proper fix**: Migrate the array columns to a join table referencing the `labels` table.

**Still open.** No `item_labels` join table exists in migrations 001-028. Note that the "Items
Table Split" this entry defers to has partly already happened — migration 014 created `tickets`
and `project_items` — so the scheduling assumption below is stale and is a maintainer's call, not
a documented plan.

**Not fixed in P2, P2.5 or P3.** Document only.

---

## Issue 15 — Ticket relations not implemented (the recorded FK blocker no longer exists)

**Phase discovered**: P3  
**Status**: Deferred to P5 (Items Table Split)

**Symptom**: The P3 spec intended to add `GET/POST /tickets/{id}/relations` and `DELETE /relations/{id}` endpoints for service desk tickets, reusing the existing `item_relations` table and `RelationService`.

**Root cause as originally recorded**: `item_relations.from_id` / `to_id` carry
`REFERENCES items(id)` foreign keys, so a ticket ID cannot be inserted.

**That root cause is no longer true, and was already untrue when this entry was written.**
Migration 015 — which predates this entry — dropped both foreign keys, added `from_type` /
`to_type` with a CHECK over `('ticket', 'project_item', 'page')`, renamed the table to
`entity_relations`, and added polymorphic indexes. The schema change this entry defers to has
already shipped.

**What is actually missing**: the endpoints. Relations routes exist only for project items
(`GET/POST /items/{itemID}/relations`, `DELETE /relations/{relationID}` in the projects handler);
the tickets handler has no relations routes. On the current schema this appears to need handler
work and no migration.

**Still open**, but the blocker recorded above is not the reason. Whether and when to build it is
a maintainer's decision — this entry no longer supports the "do not fix before P5" instruction it
originally carried.

---

## 16. ~~Object Storage Service Not Yet Wired (attachments/uploads)~~ (RESOLVED)

**Severity**: Low
**Status**: Resolved in P3 — `attachments` (migration 027) is the first production consumer

P3 wired it. `cmd/server/main.go` constructs an `S3Store`, calls `EnsureBucket` on startup, and
passes it into the attachments handler — degrading gracefully to a disabled feature rather than
failing boot if the store is unavailable. Migration 027 created the `attachments` table, and the
API serves list, upload, download and delete under `/spaces/{spaceID}/attachments`, plus a
share-authorised read path under `/shared/...`.

This was forced by ADR-0008 rule 3: a page shared org-wide must render its images for a viewer
with no space access. The object key is derived from the row rather than accepted from the
client, so the shared read path cannot be used to fetch arbitrary keys — there is a test for
exactly that.

**Tests**: `internal/core/api/entity_attachments_integration_test.go`, including
`TestAttachment_SharedPageLoadsForViewerWithoutSpaceAccess` and
`TestAttachment_CannotReadArbitraryKeys`. CI runs MinIO for these.

**Scope note**: frontend support is currently read-only and limited to the shared-entity view;
there is no in-app upload UI on ticket, wiki or project pages.

**Update (issue #15, migration 036):** Codex pages now have a dedicated image
upload endpoint, `POST /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/images`,
which stores through this same table and object store and sniffs the content
type from the bytes rather than trusting the client. The in-app UI that calls it
lands with the editor surface; ticket and project pages still have neither.

The backend issue this entry
tracked is closed.

<details><summary>Original entry</summary>

`build/docker-compose.yml` brings up a MinIO `storage` service and the app
accepts `STORAGE_*` env vars, but the running server does not yet wire an
`ObjectStore` into any handler. `storage.NewS3Store` / `EnsureBucket` have no
production caller, and there is no attachment/upload API or UI. As a result the
`storage` container is currently inert — the app boots and every module
(service desk, wiki, projects, comments) works without it.

**Impact for testers**: file attachments/uploads are not available yet; nothing
else is affected. The MinIO service is retained in Compose because attachments
are planned and the deploy topology is already documented for them.

**Proper fix**: construct an `S3Store`, call `EnsureBucket` on startup, and
pass it to the handlers that need it, behind a failing test that uploads and
retrieves an object end-to-end.

</details>

---

## 17. ~~eslint is not a CI gate — 46 errors on `main`~~ (gate CLOSED; narrowed to three deferred effects)

**Severity**: Low (code quality; no known user-facing defect)
**Status**: Closed in PR #82. The gate is on: `npm run lint` is a required step in the `Frontend`
job (`.github/workflows/ci.yml`) and `eslint .` exits 0. There is no baseline file and no
`--max-warnings` slack. What remains open is narrower and is listed at the end of this entry.

The count above was **46**; the real count when the closing pass measured it was **48 across 33
files** — it had drifted by two in the phases between. The corrected inventory and its disposition:

| Rule | Count | Disposition |
|---|---:|---|
| `react-refresh/only-export-components` | 21 | rule off, scoped to 12 files |
| `react-hooks/set-state-in-effect` | 13 | rule off, scoped to 11 files |
| `@typescript-eslint/no-unused-vars` | 5 | 4 fixed, 1 met by `argsIgnorePattern` |
| `@typescript-eslint/no-explicit-any` | 3 | fixed |
| `react-hooks/preserve-manual-memoization` | 2 | fixed |
| `jsx-a11y/no-autofocus` | 2 | fixed |
| `@typescript-eslint/no-empty-object-type` | 1 | fixed |
| `react-hooks/purity` | 1 | rule off, scoped to 1 file |

**The two `jsx-a11y` findings were never a11y violations.** The message was *"Definition for rule
'jsx-a11y/no-autofocus' was not found"* — `eslint-plugin-jsx-a11y` is not installed, and two files
carried `eslint-disable-next-line` directives naming a rule that does not exist. The directives are
gone and the justification each carried is kept as ordinary prose; `autoFocus` is untouched in
both. Installing the plugin was deliberately not done — it would surface a new class of findings
across the app, which is its own piece of work.

**Where the rule-offs live.** `web/eslint.config.js`, as scoped `files:` overrides, each with its
file list and reason written above it — visible in the config a reviewer already reads, not in a
generated ledger nobody re-reads. Each override is scoped rather than global, so every rule stays
live for the rest of the codebase and for every file added after this. Adding a file to one of
those lists is a diff somebody has to justify in review.

The short version of each reason:

- **`react-refresh/only-export-components`** is a Vite fast-refresh developer-experience rule with
  no runtime, correctness or security effect. Satisfying it means moving non-component exports into
  new modules and rewriting every import site. The 21 findings are the `useX`+`XProvider` context
  idiom (5 files), shared vocabulary modules (`priority.tsx` alone carries 5 findings, and
  `normalizePriority` has twelve production importers), and helpers exported solely so a unit test
  can reach them — where un-exporting breaks the test.
- **`react-hooks/set-state-in-effect`** is worth having and stays on everywhere else. All 13
  findings are behavioural: controlled-form seeding, the `?create=…` deep-link pattern,
  reset-before-async, a post-layout DOM measurement, and selection mirroring in Codex. None has an
  edit that silences the rule and leaves the rendered output identical.
- **`react-hooks/purity`** is `SprintTimeline`'s `now = Date.now()` default parameter. Relocating
  the call into the function body does not silence the rule; freezing it in a `useState`
  initialiser does, but changes behaviour — and all thirteen assertions in `SprintTimeline.test.tsx`
  pass `now` explicitly, so no test exercises the default. A change there would be invisible to the
  suite, which is exactly when not to guess.

**On `argsIgnorePattern`.** The rule now honours a leading underscore as "deliberately unused",
which is the convention the repository already writes (`_args`, `_e`). This is the rule's own
standard option rather than slack, and `varsIgnorePattern` / `caughtErrorsIgnorePattern` are set
alongside it. The single finding it covers — `RoadmapPage.test.tsx`'s `(..._args: unknown[])` —
could not be closed by deletion: the mock is invoked as `useRoadmapMock(...args)` and the test
asserts on the captured arguments, so removing the parameter breaks `tsc`, and editing the
assertion is not permitted.

**What must NOT happen** (unchanged, and now load-bearing): do not add an eslint baseline or
suppression file, and do not gate with `--max-warnings` slack. If a rule cannot be satisfied, turn
it off in `eslint.config.js`, scoped to the files, with the reason written down.

### Still open — three effects worth a look on their own merits

These are no longer lint findings; the rule is off in those files. They are recorded because the
closing pass read all 13 effects and three of them looked like more than style. Each needs its own
change and its own test, which is why a pass contracted to zero behaviour change did not touch
them:

1. **`CustomFieldsSection.tsx:38`** — `CustomFieldRow` re-seeds its local `value` from
   `field.value` on every query refetch. A background refetch landing while somebody is typing in a
   custom field appears able to discard what they typed. `BoardConfigSection.tsx:70` guards the
   same pattern with a `dirty` flag; this one does not.
2. **`WikiPage.tsx:145`** — the auto-select-first-page effect carries a comment recording that an
   earlier `useMemo` implementation of the same logic was wrong. Whatever that defect was, no
   regression test captures it.
3. **`PageEditor.tsx:164`** — fixed here by naming the two `useState` setters the compiler infers,
   which is behaviour-preserving because React holds setter identity stable. Worth knowing
   alongside it: the React Compiler is **not** in the build (`web/vite.config.ts` runs
   `@vitejs/plugin-react` with no `babel-plugin-react-compiler`), so these diagnostics are
   lint-only today and nothing in the shipped bundle is compiler-optimised.

---

## 18. ~~The race detector, not the database, is the largest single cost in the Test job~~ (RESOLVED)

**Severity**: Low (CI wall-clock only; no user-facing defect)
**Status**: Resolved in PR #84. The work factor is now a variable whose boot value is chosen by
`testing.Testing()`, so a test binary hashes at `bcrypt.MinCost` and anything built by `go build`
hashes at 12.

The floor this entry insisted on is not merely preserved, it is enforced in places it was not
before. `AZIMUTHAL_BCRYPT_COST` lets an operator RAISE the cost; `internal/config` refuses any
value below 12 in every environment — there is deliberately no `APP_ENV=test` exemption, because
APP_ENV is an ordinary environment variable a production deployment can hold any value of — and
`auth.SetPasswordCost` re-checks the same floor for callers that never go through config. Every
command in `cmd/server` applies it through one `loadConfig`, with a drift test that fails on any
file reaching past it: `azimuthal admin create-user` and `admin reset-password` were silently
discarding the setting until that landed.

`password_test.go` keeps its assertions unchanged. One new test pays the real cost 12 and reads
the work factor back out of the emitted hash with `bcrypt.Cost`, so it cannot be satisfied by the
constant it guards.

<details><summary>Original entry</summary>

`internal/core/auth` and `internal/core/api/auth` cost 142s of the 793s `go test` step on CI, and
almost none of it is database work. `bcryptCost = 12` (`internal/core/auth/password.go:11`) costs
about 0.16s per operation on a developer machine and **3.2s per operation under `-race`** — a 20x
multiplier, measured against the CI timings of the four pure-crypto tests in
`internal/core/auth/password_test.go`, which open no connection at all:

```
TestHashPassword                 3.27s / 1 bcrypt op
TestHashPassword_DifferentEachTime  6.46s / 2
TestComparePassword_Match        6.36s / 2
TestComparePassword_Mismatch     6.54s / 2
```

Across the suite that is roughly **200s of the Test job spent in bcrypt**, which the template
database change in the same pass recovers exactly none of. `internal/core/auth` is 91% bcrypt and
8.6% database; only 11 of its 66 tests call `NewTestDB`.

**What must NOT happen.** Do not lower `bcryptCost`. Twelve is the stated security minimum and it
is the production value; a test suite that exercises a weaker hash than production is not testing
production.

**Proper fix**: make the cost injectable rather than constant — a package-level variable that
production sets to 12 and the test harness sets to `bcrypt.MinCost`, with the *real* cost still
exercised by the handful of tests that exist to prove the hashing itself (which is what
`password_test.go` is for). `testutil.CreateTestUserWithRole` already ships a pre-computed cost-4
hash for exactly this reason, so the precedent is in the repository; what is missing is the same
treatment for the paths that hash at run time. Any such change is product code and needs its own
review — it is recorded here rather than done in passing.

</details>

---

## 19. ~~`newTestServerOn` generates a fresh RSA-2048 key for every integration test~~ (RESOLVED)

**Severity**: Low (CI wall-clock only)
**Status**: Resolved in PR #84. Each affected package memoises one key with `sync.OnceValue`; a Go
test binary is per package, so the sharing never crosses a package boundary and never reaches
production code.

**One correction to this entry, and it is the part that needed care.** It states that "no test
asserts that two servers have different signing keys, and if one did it would fail loudly rather
than pass weakly". One does: `TestJWTService_WrongKey` in `internal/core/auth` issues a token from
one service and requires a second to reject it. It sits in a different package from the one this
entry examined. Handing it the shared key twice would have left it passing while asserting
nothing, so it now calls `freshTestKey` explicitly, twice, with the reason written beside it.

`TestHarness_ServersShareOneSigningKey` locks the sharing in from the other side: two
independently built servers must each accept the other's token, which is true only while they hold
one key.

<details><summary>Original entry</summary>

`internal/core/api/routes_integration_test.go:114` calls `rsa.GenerateKey(rand.Reader, 2048)` on
every `newTestServerOn`, and that helper backs 203 of the 208 `NewTestDB` calls in
`internal/core/api`. Measured at 38.6ms per keygen locally and an estimated 0.1–0.15s on CI, that
is roughly **25–38s per Test job** spent minting keys no test asserts anything about.

**Why it was not taken.** The fix is small — hoist the key into a `sync.OnceValue` so each test
binary mints one — but `routes_integration_test.go` is the busiest test file in the repository and
P4 (saved views) was working in it concurrently. A 5–7% saving is not worth a merge conflict in
that file. Nothing about the change is risky: no test asserts that two servers have different
signing keys, and if one did it would fail loudly rather than pass weakly.

**Proper fix**: one `var testSigningKey = sync.OnceValue(func() *rsa.PrivateKey { ... })`, used by
`newTestServerOn` and by `setupRouter` in `router_test.go`.

</details>

---

## 20. Two structural liabilities in the project-item-detail E2E assertions

**Severity**: Low (test robustness; the underlying product behaviour is correct)
**Status**: Open. Found by the CI optimization pass while investigating the status-dropdown flake.
Not changed there, because changing a passing assertion was outside that pass's scope.

The flake itself was fixed in #70 (`bbadf7c`), which replaced `await page.waitForTimeout(1000)`
with a `waitForResponse` on the status POST. It has passed first-attempt on every `main` run since,
and 12 repeats at 6 parallel workers locally did not reproduce it. Two things around it are still
shaped badly:

**a. The reload assertion can fail terminally rather than slowly.**
`web/e2e/projects.spec.ts:199-200` reloads the page and then waits 5s for a status badge.
`web/src/main.tsx` configures react-query with `retry: 1` and no refetch trigger, and
`ItemDetailPage` renders its entire body behind `useProjectItem` — its error branch renders only
`friendlyErrorMessage(...)`, no badge and no select. A reload fires roughly nine queries at once,
including the org-wide `useSpaces`; if that one item query fails twice, nothing ever re-fires it
and no timeout increase can help. The failure surface exists only because of the reload, and the
reload is redundant: the very next test, `project item status change persists after page reload`,
is the dedicated persistence test and asserts on the select's value, which is the more robust
check.

**b. `[class*="inline-flex"]:has-text("In Progress")` resolves correctly today by accident.**
`:has-text` matches ancestors as well as the element itself, `.first()` does not skip hidden
matches, and any button, chip or pill carrying `inline-flex` satisfies the class part. It happens
to land on the header status `Badge` because that badge is first in DOM order. A layout change that
puts an `inline-flex` wrapper above it would make this pass vacuously or fail spuriously, and
neither would be obvious from the failure message.

**Proper fix**: give the status badge a `data-testid` and assert on that; drop the reload from the
first test and leave persistence to the second, which already proves it.

---

## 21. `comment.created` audit events are silently discarded

**Severity**: Medium (the append-only log is missing a whole entity's history)
**Status**: Open. Found by P5 while assessing whether an activity-feed gadget could be built.
Not fixed there: changing what the append-only log contains is not a dashboard phase's call.

`audit.dbLogger.Log` (`internal/core/audit/db_logger.go`) drops any event whose `OrgID` does not
parse — `slog.Warn`, then `return nil`, so the caller sees success:

```go
orgID, err := uuid.Parse(event.OrgID)
if err != nil {
    slog.Warn("audit: dropping event with invalid org_id", ...)
    return nil
}
```

A scan of every non-test `audit.Event{...}` literal found exactly two that set no `OrgID` at all:

- `internal/core/api/comments/handler.go:264` — `EventTypeCommentCreated`. **Every comment ever
  posted is absent from the audit log.**
- `internal/core/api/auth/handler.go:153` — `EventTypeLoginFailed`. Arguably intentional (the
  event fires pre-authentication, so there may be no org to name), but it is a real gap in the
  failed-login trail and it is a gap by accident rather than by decision.

**What closing it takes.** For comments, `OrgID: claims.OrgID` at the call site, plus a regression
test that posts a comment and asserts the row exists — which fails before the fix. For failed
logins, a decision first: either resolve the org from the submitted email, or record that
pre-auth events are deliberately not audited and make `Log` say so rather than warn.

**Why it went unnoticed.** `Log`'s return value is discarded at every call site (`_ = h.auditLog.Log(...)`),
which is correct — an audit write must not fail a user's request — so the only signal is a warning
line nobody reads.

---

## 22. `audit_log` has no `space_id`, so no activity feed can be scoped to a viewer

**Severity**: Low (a feature cannot be built, rather than something being broken)
**Status**: Open. Recorded by P5, which shipped no activity gadget because of it.

ADR-0009's gadget list names a recent-activity gadget and P5's brief asked for one "scoped to the
viewer's readable containers". It is not currently buildable from anything that exists:

- **`audit_log` carries `org_id` and no `space_id`**, and none in `payload`. Scoping it to a
  viewer's readable set means deriving the container by joining `(entity_kind, entity_id)` back to
  `tickets`, `project_items` and `pages` — a new query, and a new member-visible read path over a
  table whose routes are `org-admin-404` today. It also structurally loses deletions: the join
  drops soft-deleted entities, so `ticket.deleted` events disappear, and a `LEFT JOIN` does not
  help because those rows then have no container to authorise against.
- **`notifications` is not a candidate.** It is a per-recipient inbox with exactly ONE producer in
  the whole product (`TicketService.Assign` → `queueAssignmentNotifier`), no org or space filter,
  and no consultation of the readable set — migration 030's own comment says so. A feed built on
  it would contain assignment rows and nothing else.

**What closing it takes.** A nullable `audit_log.space_id` populated at write time, which is
exactly the precedent migration 030 set for `notifications.entity_space_id`: nullable, no backfill,
a routing/scoping hint captured where the writer already knows the answer. Then one query with the
readable-space array as its access control, and one accounting row for the new read path.

Item 21 is a prerequisite for the feed being worth having: a "recent activity" list with no
comments in it is a poor feed.

---

## 23. `POST /tickets/{id}/assign` performs no referential check, and its 500 discloses SQL

**Severity**: High (information disclosure, and a cross-organisation write)
**Status**: Open. Found by P5's coverage pass, confirmed live against real PostgreSQL. Deliberately
**not fixed there**: a dashboards phase must not quietly rewrite the ticket assignment path, and
two of the three parts need a policy decision rather than a patch.

`internal/core/api/tickets/handler.go` `Assign` writes `assignee_id` with no check that the id
names anybody this organisation knows. Three consequences, in increasing order of seriousness:

**a. A nonexistent user answers 500 with the driver's message.** A well-formed uuid naming no user
reaches the UPDATE, violates `tickets_assignee_id_fkey`, and `handleTicketError`'s default branch
returns it to the caller:

```
ticket operation failed: assigning ticket: ticket adapter update: ERROR: insert or update on
table "tickets" violates foreign key constraint "tickets_assignee_id_fkey" (SQLSTATE 23503)
```

Table name, constraint name and SQLSTATE, to any caller holding `edit_any_item`. A client error
reported as a server fault, and internal wording that must not reach a user.

**b. The disclosure mechanism is the fallback itself.** `handleTicketError`'s default arm formats
the underlying error into the client-visible message —
`fmt.Sprintf("ticket operation failed: %v", err)`. Every other 500 in the API uses a fixed string.
Any future unmapped repository error will ship the same way.

**c. A ticket can be assigned to a user in ANOTHER organisation.** `tickets.assignee_id` references
the global `users` table, so the FK is satisfied and the write lands: HTTP 200, and the row now
names somebody with no membership in the org and no access to the space. The notification enqueuer
then targets that foreign user id. Confirmed live with a user seeded into a second organisation.

**Why this is a gap rather than a new rule.** The grants surface already enforces exactly this
obligation (`access.ErrSubjectNotOrgMember`, "grant subject is not a member of this organisation"),
and so does the share audience (`ErrShareAudienceTeamNotFound`). Assignment is the one referential
write that skips it.

**What closing it takes.** (a) and (b) are mechanical: an org-membership check before the write
returning 400, and a fixed fallback message. (c) is the same check. The weaker sibling case —
assigning an org member who holds no grant on the ticket's space — returns 200 too; that one is
arguably policy and wants a maintainer's decision alongside.

No test was written asserting the current behaviour: pinning it would encode the defect.

---

## 24. Five handlers answer 500 where their own OpenAPI annotations promise 4xx

**Severity**: Medium (a client is told to retry something that will never succeed)
**Status**: Open. Found by P5's coverage pass; each one verified empirically with a throwaway probe
and confirmed against the handler's own `@Failure` annotation. Not fixed — all are in non-test
source outside P5's surface, and each is somebody's decision about their own error vocabulary.

Every one has the same shape: a repository returns a bare `pgx.ErrNoRows` or a raw unique
violation, the domain layer wraps it without mapping it to a sentinel, and the handler's error
switch falls through to `default` → 500.

| Where | Symptom | Documented as |
|---|---|---|
| `internal/core/wiki/page.go:143` (via `api/wiki/handler.go:839`) | `POST /wiki` with an unknown `parent_id` returns 500 and echoes *"fetching parent page: no rows in result set"* | 400/404 |
| `api/wiki/document_handler.go:351` | a `doc` that is valid JSON but not a ProseMirror document returns 500 on both draft-save and publish | 400 "Malformed document" (`document_handler.go:90`, `:188`) |
| `db/adapters/projects.go:109` | `ItemAdapter.UpdateStatus` does not map `ErrNoRows`, so a status change on an unknown item returns 500 | 404 (`api/projects/handler.go:621`) |
| `db/adapters/projects.go:86, :280, :298` | the same omission on `ItemAdapter.Update`, `SprintAdapter.Update` and `SprintAdapter.UpdateStatus` — narrower, because the handlers pre-load, so it is a TOCTOU window rather than a plain 404-as-500 | 404 |
| `db/adapters/projects.go:490` | `LabelAdapter.Create` does not map the `UNIQUE (org_id, name)` violation, so a duplicate label returns 500 — **and `handleProjectError`'s `ErrLabelDuplicate` arm is therefore dead code** | 409 (`api/projects/handler.go:1557`) |

Every sibling getter in those same files maps `ErrNoRows` correctly, so these are omissions rather
than design choices.

**One related observation, lower confidence.** `MoveToSprint`, `MoveToBacklog` and `AssignToSprint`
route through `ItemAdapter.UpdateSprint`, whose query is `:exec` and cannot report zero rows
affected — so an item id naming nothing is silently accepted and the handler answers
`200 {"message":"item moved to backlog"}`. Each declares `@Failure 404`. `DeleteRelation` and
`DeleteLabel` share the shape and are plausibly meant to be idempotent, so this one is a
maintainer's judgement rather than a clear defect.

**None of these has a test asserting the current behaviour** — a test that pinned the 500 would
encode the bug. Each fix wants a regression test written against the documented status.

**A sixth instance was found in the same sweep and FIXED rather than recorded**, because it sat on
P5's own inherited surface. `views.ErrUnknownField` — raised when a *stored* filter document names
a key this build does not know — was listed in `api/views/handler.go`'s 422 branch, so every
saved-view, queue, dashboard and Home route answered `422 VALIDATION_ERROR` carrying the internal
wording *"saved view <uuid> holds an unreadable filter document: unknown field \"...\""*. Two faults
in one: the wrong class (nothing the caller sent was invalid — every caller-supplied document is
parsed and refused by `views.ParseQuery` before the service is entered), and a disclosure of the
row id and the stored key. It now has its own branch answering 500 with the fixed fallback.
`internal/core/api/views_unreadable_document_integration_test.go` drives every affected route and
fails in both directions.

---

## 25. A team-shared saved view cannot be renamed without re-naming its team

**Severity**: Low (a 422 on a request that is not wrong; no data loss, no disclosure)
**Status**: Open. Found by P5's coverage pass, verified over HTTP. Not fixed — it is P4 behaviour
outside this phase's surface, and the repair is a decision about PATCH semantics rather than an
omission.

`views.Service.Update` inherits the row's own visibility when the request omits it:

```go
if d.Visibility == "" {
    d.Visibility = existing.Visibility
}
```

It does not inherit `existing.VisibilityTeamID`. For the one visibility that carries a payload the
inheritance therefore cannot succeed — the merged draft is `team` with no team, which `Normalise`
refuses:

```
PATCH /api/v1/orgs/{org}/views/{id}   {"name":"Renamed","query":{...}}
422 {"error":{"code":"VALIDATION_ERROR","message":"a team-visible view must name a team"}}
```

The message describes a field the caller did not send and state they did not create. Naming the
team explicitly works, so this is a gap in the inheritance rather than a rule about team views.

**What closing it takes.** One line — inherit `existing.VisibilityTeamID` alongside the visibility
when the request omits both. The decision it needs first is whether an omitted `visibility_team_id`
alongside an explicit `"visibility":"team"` should mean "unchanged" or "you must say"; today it
means the latter, consistently, and that part is defensible. It is the partial-PATCH tri-state
shape again: one field cannot distinguish "absent" from "cleared".

**The current behaviour is pinned**, not left unasserted:
`TestViewUpdate_OmittingTheTeamOnATeamViewIsRefused` in
`internal/core/views/view_refusals_test.go` fails if somebody changes it, which is the point — the
fix is to invert that test rather than to discover the change downstream.
