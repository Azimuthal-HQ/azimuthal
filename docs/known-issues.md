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
**Status**: Resolved — the CI floor is now 84%, enforced, and met

The 60% floor this entry describes no longer exists. P1.5 raised the CI gate from 70 to the 80
the specification requires, and P3 merged at 80.2%. The gate is the "Enforce minimum coverage
(84%)" step in `.github/workflows/ci.yml`, run with `-coverpkg=./internal/...` and `-p 1` — it
read 80 until 2026-08-01, when it was ratcheted to 84, half a point under the then-measured
figure. The detail below is retained only as a record of where the v0.1.x line started;
`internal/db` and `cmd/server` are now exercised by the integration suites.

Spec section 2.8 schedules a rise to 85% at the end of P5. **That sentence read "It rises to 85%
at the end of P5" here until 2026-08-01, in the future tense, which had stopped being true: P5
merged as #88/#89 and the flip never shipped.** It is still outstanding, tracked as **D98** in
`docs/design/spec-repo-reconciliation.md`. Measured at CI parity on 2026-08-01: **84.7727%,
15,087 of 17,797 statements** — below 85, so the 85 flip is deferred pending a coverage pass
rather than closed by moving the target. The floor moved to 84 in the same PR, which is a ratchet
against silent decay and not a substitute for the 85 the specification owes.

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

**Impact** *(corrected 2026-07-31 — the gap is LARGER than this said)*: nothing consumes the
`labels` table. This entry said "labels created in the admin UI have no effect on items", which
implies an admin UI exists. **There is none.** `/{module}/{spaceId}/labels` — a per-space Vector
route, not an admin URL — renders a branded "Label management is coming soon" empty state and
nothing else: no form, no list, no mutation (`web/src/pages/vector/LabelsPage.tsx`, whose own doc
comment says "Label management is not built yet" and cites this issue). There is no labels page
under `web/src/pages/admin/` at all. On the client side both label functions are orphans —
`fetchLabels` is reached only by `useLabels`, which has zero component consumers, and
`createLabel` is exported and called by nothing in `web/src` or `web/e2e`. The three backend
routes do exist and are accounted for.

So: items display label text from the `TEXT[]` array; the table's colour metadata is never shown
because nothing ever writes a row to it; and there is no way to query "all items with label X"
efficiently. Anyone scoping the fix from the old wording would have budgeted for a join-table
migration and missed that the entire creation surface has to be built too.

**Root cause**: The design was split across two PRs during Phase 0/1 with
conflicting approaches. Neither was backed out before shipping.

**Proper fix**: two halves, not one. (1) Migrate the array columns to a join table referencing the
`labels` table. (2) Build the surface that would use it — a real admin page, an item-side picker,
and a `labels` field in the saved-view filter vocabulary, which today has none.

**Still open.** No `item_labels` join table exists in migrations 001-050. Note that the "Items
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

1. ~~**`CustomFieldsSection.tsx:38`** — `CustomFieldRow` re-seeds its local `value` from
   `field.value` on every query refetch.~~ **CLOSED** by the maintenance mini-pass, together with a
   third instance the closing pass's own list did not name as a defect: **`OrgSettingsPage.tsx:28`**
   carries the identical unguarded pattern, and because its effect depends on the whole `org`
   object, a change to *either* field re-seeded *both*.

   Both now carry `BoardConfigSection`'s `dirty` guard. One adaptation was needed and is worth
   knowing before the pattern is copied a fourth time: `BoardConfigSection` can clear the flag the
   moment a save resolves because its mutations `setQueryData` before invalidating, so the fresh
   value is already in the cache. `useSetItemField` and `useUpdateOrganization` only invalidate, so
   for one render after a successful save the query still holds the *pre-save* value — clearing on
   success there would flash the old text back into the field somebody had just left. Both clear the
   flag when the **server catches up** with what is on screen instead, which covers the save and
   also covers typing a change and then typing it back.

   Tests: `keeps an in-progress edit when a refetch changes the server value` (and its
   organisation twin) fail before the guard; the "picks up a server change when there is no edit in
   progress" and "follows the server again once the save has landed" cases guard the other
   direction, so a flag that never cleared could not pass. Both suites re-render with a **different**
   server value on purpose — the seeding effect depends on a string, so a re-render with identical
   data cannot fail under either implementation.

   The sibling audit found no fourth instance. `AccessMatrixPage` stages edits as an overlay over
   the query data and never copies it into state — the shape the other three should probably have
   used. `ItemTypesAdminPage`, `PeoplePage`, `TeamsAdminPage`, `SpacesAdminPage` and the two
   dashboard dialogs all seed on an explicit open/click or at the mount of a per-edit dialog, which
   a refetch cannot stomp. `SpacesAdminPage` carries the *inverse* hazard rather than this one — its
   open dialog holds a Space object captured at click time, so a refetch leaves it stale rather than
   overwriting it. That is a different question and was left alone.

   The eslint rule stays off for all three files: the guard is still a `setState` in an effect, and
   the config's own note on this group already anticipated the fix.
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

- `CreateComment` in `internal/core/api/comments/handler.go` — the file's sole `audit.Event{`
  literal, logging `EventTypeCommentCreated`. **Every comment ever posted is absent from the audit
  log.**
- `Login` in `internal/core/api/auth/handler.go` — `EventTypeLoginFailed`. Arguably intentional (the
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

## 23. ~~`POST /tickets/{id}/assign` performs no referential check, and its 500 discloses SQL~~ (RESOLVED, with one part deliberately left open)

**Severity**: High (information disclosure, and a cross-organisation write)
**Status**: Resolved by the write-authorisation pass. Both defects this entry names are closed and
each has a test that fails with the property reverted, verified in both directions.

**(a) and (c).** `TicketService.Assign` now asks whether the assignee belongs to the organisation
that owns the ticket's space, and refuses with `tickets.ErrAssigneeNotOrgMember` → **400
VALIDATION_ERROR** when they do not. Membership is resolved *through the space*
(`UserIsMemberOfSpaceOrg`) rather than read from the caller's token, so the check is about the
entity being written rather than about who is asking. The check runs before the already-assigned
comparison, so a foreign assignee is refused as one rather than being told the ticket is already
theirs. A refused assignment writes nothing and enqueues no notification — which matters, because
the notification carried the ticket's title.

**(b).** `handleTicketError`'s default arm is `respondUnmapped`, matching the three project
surfaces H5 closed. The client gets a fixed message plus the request id it already had; the full
error goes to the server log under that id. `TestUnmappedTicketError_DoesNotLeakInternalDetailToTheWire`
and `..._FullErrorReachesTheServerLog` fail against the old arm, the first on the constraint name
appearing in the body.

**The wiki sibling this entry named is closed too**, by the same change in the same shape
(`internal/core/api/notifications` — see #28; `internal/core/api/wiki/handler.go` remains and is
the last of the family).

**Still open, deliberately.** The weaker sibling case: assigning an org member who holds no grant
on the ticket's space still answers 200. This entry calls that "arguably policy" and asks for a
maintainer's decision, so the pass changed no behaviour for it. Deciding it means deciding whether
assignment is a statement about who *may work on* an item or about who *is responsible for* it,
and those give opposite answers.

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

> **Partially closed (hygiene-gates pass, H5).** The three default arms in
> `internal/core/api/projects` — `handleProjectError`, `handleItemTypeError` and
> `handleCustomFieldError` — now answer a fixed message and send the full error to the server log
> under the caller's request id (`respondUnmapped` in that package's handler.go).
> `TestUnmappedProjectError_*` and `TestUnmappedSchemaErrors_*` fail if any of them interpolates
> again, and assert the log side too, so the detail is moved rather than discarded.
>
> **Still open, same shape:** `handleTicketError` (`internal/core/api/tickets/handler.go:830`) —
> the arm this issue was actually filed against — and `internal/core/api/wiki/handler.go:864`.
> Both remain exactly as described above. Each is the same small change plus its fails-before
> test; they were left out because they are different packages from that pass's scope, and
> because the tickets one is entangled with (a) and (c) below.

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

## 24. ~~Five handlers answer 500 where their own OpenAPI annotations promise 4xx~~ (RESOLVED)

**Severity**: Medium (a client is told to retry something that will never succeed)
**Status**: Resolved by the maintenance mini-pass. All five answer their documented class, each
through the standard error envelope, and each has the negative test that would have caught it —
every one of which fails against the unfixed source with the 500 this entry describes.

**Three of the five were fixed at the layer that was missing the sentinel, not at the switch.** The
error vocabulary this entry left to a decision turned out to be already written down in every case:
`wiki.ErrParentPageNotFound` existed and had one producer, `projects.ErrLabelDuplicate` existed and
had none, and `projects.LabelRepository`'s own doc comment already promised it. The only genuinely
new sentinel is `wiki.ErrMalformedDocument`.

| Site | Now answers | How |
|---|---|---|
| `POST /wiki` with an unknown `parent_id` | **404 NOT_FOUND** | `wiki.Service.CreatePage` maps `pgx.ErrNoRows` to the existing `ErrParentPageNotFound`; `handleWikiError` gained the arm |
| a `doc` that is valid JSON but not a ProseMirror document, on draft-save and publish | **400 VALIDATION_ERROR** | new `wiki.ErrMalformedDocument` wraps `doc.Validate` at both call sites; `handleDocumentError` gained the arm |
| `ItemAdapter.UpdateStatus`, `ItemAdapter.Update`, `SprintAdapter.Update`, `SprintAdapter.UpdateStatus` | **404 NOT_FOUND** | each maps `pgx.ErrNoRows` to `projects.ErrNotFound`, in the idiom their own sibling getters use |
| `LabelAdapter.Create` on a repeated name | **409 CONFLICT** | maps the `labels_org_id_name_key` violation to `projects.ErrLabelDuplicate`, which makes `handleProjectError`'s arm live for the first time |

**One annotation was wrong, and it was the annotation that changed.** `CreatePage` declared only
400, 401 and 500 — there was no `@Failure 404` for the table's "400/404" to refer to. Since
`ErrParentPageNotFound` already means 404 on the move route, answering 400 on create would have
given one sentinel two statuses in one switch, so the route gained `@Failure 404 "Parent page not
found"` and `docs/api/openapi.yaml` was regenerated (a six-line diff, `make docs-check` green). No
other annotation needed a change: the item-status route already declares 404 and the label route
already declares 409.

**Three corrections to the entry below, verified rather than assumed.**

1. **A sixth site was found, and a seventh, both unrecorded.** `handleWikiError` matched *none* of
   the four tree sentinels the move path raises — `ErrParentPageNotFound`, `ErrTargetSpaceNotFound`,
   `ErrParentNotInTargetSpace`, `ErrPageMoveCycle` — so `POST /wiki/{pageID}/move` answered 500 for
   all four while annotating both 400 and 404. Fixing site 1 required adding the first arm anyway;
   leaving the other three at 500 in the same switch would have been arbitrary, so all four are
   mapped (404 for the two "names something that does not exist" cases, 400 for the two "both exist,
   the combination is wrong" cases). Separately, `SprintAdapter.CompleteWithDisposition` calls
   `UpdateSprintStatus` a second time inside its transaction and was unmapped there too.
2. **Site 3 is no longer a plain 404-as-500.** Commit `7950307` (P-W workflow tiers) added a
   pre-load to `UpdateItemStatus`, so the route 404s before the adapter is reached. Sites 3 and 4
   are now the same shape — a TOCTOU window between a handler's read and its write. That is why
   those five are pinned at the adapter boundary rather than over HTTP: reaching them through the
   router means losing a race with a concurrent delete.
3. **The "related observation" about `UpdateSprint` is correct as written.** `ItemAdapter.UpdateSprint`
   calls `UpdateProjectItemSprint`, which is `:exec` and returns only an error, so an item id naming
   nothing really is silently accepted. (The similarly-named `UpdateSprint` *query* is `:one` — a
   different query, easy to confuse.) Left alone: the entry is right that `DeleteRelation` and
   `DeleteLabel` share the shape and are plausibly meant to be idempotent, so it is a maintainer's
   call about idempotency rather than a status-class defect.

**Tests.** `internal/core/api/wiki_error_classes_integration_test.go` (create, all three reachable
move refusals, and all four malformed-document shapes on both routes),
`TestProjectsNeg_DuplicateLabelNameIs409` in
`internal/core/api/projects_negative_integration_test.go`, and
`TestProjectWriteAdapters_MissingRow_ReturnsErrNotFound` plus
`TestLabelAdapter_DuplicateName_ReturnsErrLabelDuplicate` in
`internal/db/adapters/notfound_integration_test.go`. Each asserts the status *and* the envelope
code, because a fix that answered 400 with `INTERNAL_ERROR` would be half a fix, and each family
carries a success case so a handler that refused everything could not pass.

`ErrTargetSpaceNotFound` has no test: an unknown `target_space_id` is refused earlier by the
destination's `edit_any` guard, which already answers 404, so a test naming a random space uuid
would pass with the new arm deleted. Its arm is reachable only by losing a race with a space
deletion. Recorded rather than covered by something vacuous.

<details><summary>Original entry</summary>

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

</details>

---

## 25. ~~A team-shared saved view cannot be renamed without re-naming its team~~ (RESOLVED)

**Severity**: Low (a 422 on a request that is not wrong; no data loss, no disclosure)
**Status**: Resolved by the maintenance mini-pass. PATCH is a merge: `views.Service.Update` now
inherits `existing.VisibilityTeamID` alongside `existing.Visibility`, and stops inheriting the
moment the caller changes the audience.

**The semantics that were decided**, since the entry below records the decision as open. Every
unspecified field inherits, exactly as the other fields already did. The team id inherits when the
visibility is unspecified **or unchanged** — restating `"visibility":"team"` on a view that is
already team-shared keeps the team it is shared with. Explicitly *changing* the visibility still
states the whole pair: `org` and `private` drop the team id, and a move **to** `team` that names no
team is still refused with `ErrTeamRequired`. That refusal is the half of this entry that was always
defensible, and it is now asserted on its own so the inheritance cannot swallow it.

**Where.** `internal/core/views/view.go`, in `Service.Update`. Two lines and the reasoning above
them. Nothing in `internal/core/views/filter.go` or the query vocabulary was touched.

**A twin exists and was NOT fixed.** `internal/core/dashboards/dashboard.go`'s `Service.Update`
carries the identical omission — it inherits `Visibility` and `Module` and not `VisibilityTeamID`,
so a team-shared **dashboard** cannot be renamed either. It is the same two-line repair against the
same `views.Audience.Normalise`. It was left alone because the dashboards surface belongs to another
track in flight, not because it is correct. Recorded as its own entry, #26.

**Tests.** `TestViewUpdate_TheTeamInheritsWithTheVisibility` (four cases) and
`TestViewUpdate_MovingToATeamAudienceStillNamesTheTeam` in
`internal/core/views/view_refusals_test.go` replace the pin-test named below, and
`TestViewsMatrix_RenamingATeamSharedViewKeepsItsTeam` in
`internal/core/api/views_endpoint_matrix_integration_test.go` asserts it over HTTP, which is where
it was reported. They fail against the unfixed service with the exact 422 quoted below.

**One thing found while fixing it.** `failingStore.Create` and `failingStore.Update` in
`internal/core/views/service_errors_test.go` returned the *stored* view rather than the view they
were handed, so every assertion on an updated field was vacuous — the service could compute anything
and the test still saw the pristine row. Both now return their argument, which is what the real
store does (`UpdateSavedView` is `:one ... RETURNING *`). No existing assertion was changed; several
became load-bearing for the first time.

<details><summary>Original entry</summary>

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

</details>

---

## 26. ~~A team-shared dashboard cannot be renamed without re-naming its team~~ (RESOLVED)

**Severity**: Low (a 422 on a request that is not wrong; no data loss, no disclosure)
**Status**: Resolved by the config & build integrity follow-up. `dashboards.Service.Update` now
inherits `existing.VisibilityTeamID` alongside `existing.Visibility`, with the semantics #25
decided: inherit while the audience is unchanged, never across an explicit change.

**Tests.** `TestDashboardUpdate_TheTeamInheritsWithTheVisibility` (four cases) and
`TestDashboardUpdate_MovingToATeamAudienceStillNamesTheTeam` in
`internal/core/dashboards/service_test.go`, plus
`TestDashboardsMatrix_RenamingATeamSharedDashboardKeepsItsTeam` in
`internal/core/api/dashboards_endpoint_matrix_integration_test.go`, which asserts it over HTTP
against real PostgreSQL — the layer it was reported at. Both fail against the unfixed service with
the exact 422 quoted below. The HTTP test uses a **member**, not the org owner: an org admin
bypasses the team-membership check in `Normalise`, so an owner-persona test would have passed with
half the rule deleted.

**One thing found while fixing it, which corrects #25's own record.** #25's test comment claims
both halves of the pattern are load-bearing — "delete the visibility-unchanged guard and the
team→org case keeps a team id it was told to drop". Mutation-tested here, that is **false**:
removing the `d.Visibility == existing.Visibility` guard fails no test, because `Normalise`
independently nils the team id for a private or org audience. The guard is still worth keeping —
it means the merge never fabricates a pair the caller did not state, rather than relying on a
downstream function to tidy one away — but it is belt-and-braces, not a second load-bearing half.
The dashboards tests say so; `internal/core/views/view_refusals_test.go` still carries the
overstated claim and is left for whoever next has that file open.

**Also noticed, not fixed.** The 422 a *dashboard* PATCH returns reads "a team-visible **view**
must name a team" — `views.ErrTeamRequired` is a shared sentinel worded for the model it was first
written in. Right behaviour, wrong noun on the dashboards surface. Rewording it changes the
saved-views message too, so it is recorded rather than done.

<details><summary>Original entry</summary>

**Severity**: Low (a 422 on a request that is not wrong; no data loss, no disclosure)
**Status**: Open. Found by the maintenance mini-pass while closing #25, which is the identical
defect one model over. Not fixed — the dashboards surface belongs to another track in flight, and
a one-line change there would have been a merge conflict in somebody else's file rather than a
favour.

`dashboards.Service.Update` (`internal/core/dashboards/dashboard.go`) inherits two fields when the
request omits them and not the third:

```go
if d.Visibility == "" {
    d.Visibility = existing.Visibility
}
if d.Module == "" {
    d.Module = existing.Module
}
```

`existing.VisibilityTeamID` is never inherited, so a PATCH carrying only a new name against a
team-shared dashboard merges to `team` with no team, and `views.Audience.Normalise` — the same
shared rule saved views use — refuses it with `ErrTeamRequired`. Dashboards and saved views share
`Audience` precisely so this rule has one implementation (see the type's own comment on why), and
they have now drifted on the *merge* rather than on the rule.

**What closing it takes.** The two lines #25 took, in `dashboards.Service.Update`:

```go
if d.VisibilityTeamID == nil && d.Visibility == existing.Visibility {
    d.VisibilityTeamID = existing.VisibilityTeamID
}
```

with the same decided semantics (#25): inherit while the audience is unchanged, never across an
explicit change. Plus the mirror of the saved-view tests — a rename-only case that fails before the
fix, and a move-to-team-without-a-team case that must keep failing after it.

**Not pinned.** Unlike #25 there is no test asserting the current behaviour, so nothing will fail
when somebody fixes this. `internal/core/dashboards/service_test.go:196` renames a dashboard, but
the fixture is a private one, which is the visibility that carries no payload — so it passes either
way and says nothing about this.

</details>

---

## 27. ~~`POST /wiki` accepts a `parent_id` in another space, and roots the new page under it~~ (RESOLVED)

**Severity**: Medium (silent tree corruption; no disclosure beyond what the endpoint already told
you, no data loss)
**Status**: Resolved by the cross-space read-authorisation pass, which reached this path while
closing the entity-by-id family. `wiki.Service.CreatePage` resolves the parent with
`GetPageInSpace` against the space the page is being created in, and a parent elsewhere answers
`ErrParentPageNotFound` → 404, identically to one that does not exist.

**This entry was stale for one release.** It was recorded as open, and the description below still
reads as though `GetPageByID` were the call — it is not, and has not been since that pass. Verify
the code before acting on the paragraphs that follow. The decision the entry asked for (what to do
about pages *already* in this state) was not made and is not made here: the fix refuses new ones.

`wiki.Service.CreatePage` (`internal/core/wiki/page.go`) resolves the parent with a bare
`s.store.GetPageByID(ctx, *input.ParentID)` and never compares `parent.SpaceID` to
`input.SpaceID`. The move path does compare — `resolveMoveParent` in
`internal/db/adapters/content_tx.go` raises `wiki.ErrParentNotInTargetSpace` for exactly this — so
the two paths that set a page's parent disagree about whether a cross-space parent is legal.

Verified against a real server: creating in space B with a `parent_id` from space A answers **201**,
and the row comes back with `space_id` = B while `path` is rooted at A's page:

```
space_id: ca5310ba-…            (B, the caller's space)
parent_id: 8530e5fb-…           (a page in A)
path: 8530e5fb-….4f9a73e1-…     (rooted outside its own space)
```

`GET /wiki` for space B then lists it as a page whose `parent_id` names nothing in the list — an
orphan the tree cannot render under anything. The materialised path is what
`PathWithinSubtree` and the share-revocation subtree queries operate on, so the wrong root is not
cosmetic.

**Not made worse by #24's fix, and not made better either.** Before it, an unknown parent answered
500 and a known one 201, so a caller could already tell whether a page id existed; now it is 404 and
201. The cross-space case was 201 before and is 201 still.

**What closing it takes.** The comparison the move path already makes, raised as the sentinel that
already exists (`ErrParentNotInTargetSpace`, now mapped to 400), plus a decision the fix cannot make
on its own: whether any page is already stored this way, and what to do about it. A create-time
refusal leaves existing bad rows unrepaired, and a migration that re-roots them is a data change
somebody has to sign off. That is why it is recorded rather than fixed in a pass scoped to error
classes.

---

## 28. ~~Four `notifications` handlers interpolate the raw error into the 500 body~~ (RESOLVED)

**Severity**: Low–Medium (internal disclosure; same mechanism as #23(b), smaller surface)
**Status**: Resolved by the write-authorisation pass, which was already changing this file for the
notification read gate. All four arms call a package-local `respondUnmapped`: the client gets a
fixed message and the request id, the full error and the operation name go to the server log.

**The "where does the helper live" question was not answered, and did not need to be.** Each
package has its own small `respondUnmapped` — projects, tickets and now notifications — because
the surface name in the message differs and a shared one would need it passed in anyway. If a
fourth copy appears, that is the point to extract it.

```
internal/core/api/notifications/handler.go:74   fmt.Sprintf("listing notifications: %v", err)
internal/core/api/notifications/handler.go:80   fmt.Sprintf("counting unread: %v", err)
internal/core/api/notifications/handler.go:126  fmt.Sprintf("marking notification read: %v", err)
internal/core/api/notifications/handler.go:151  fmt.Sprintf("marking all notifications read: %v", err)
```

Each ships whatever the layer below produced — pgx wording, constraint names, SQLSTATE — to any
authenticated caller, exactly as the projects arms did before H5.

**Why these were missed until now, which is the part worth keeping.** The obvious repo-wide sweep
for this defect is a regex over `fmt.Sprintf("…failed…%v", err)` or `"…error…%v"`. None of these
four literals contains "failed" or "error", so that sweep returns nothing and the sites read as
clean. Any future audit of this class should match on the *shape* — a `%v`/`%s` of `err` inside a
`respond.Error(..., CodeInternal, ...)` call — not on the wording.

**Where the fix belongs.** `respondUnmapped` (`internal/core/api/projects/handler.go`) is the
version H5 wrote, and it is unexported in that package. Closing #23(b) for tickets and wiki plus
this one for notifications means four more copies unless it is hoisted first. The natural home is
`internal/core/api/respond` — e.g. `respond.Internal(w, r, err, msg)`, which already owns the
request id that makes the log/wire split work. CLAUDE.md is explicit that a second implementation
of a shared surface is a defect, so **hoist before the second caller, not after the fourth.**

---

## 29. `go mod tidy` dirties `go.mod`, and the `lint` gate cannot see it

**Severity**: Low (no runtime effect; a stale dependency graph and a gate that under-reports)
**Status**: Open. Found by the hygiene-gates pass while checking H1's preconditions. Not fixed —
`go.mod` is contended by every concurrent track, and the one-line change is not worth a conflict in
a pass that touches no dependencies.

`.github/workflows/ci.yml`'s `lint` job runs `go mod tidy` and then diffs **only `go.sum`**:

```yaml
- name: Check go.sum is up to date
  run: |
    go mod tidy
    git diff --exit-code go.sum || (echo "❌ go.sum out of date — run go mod tidy" && exit 1)
```

On a clean checkout of `main`, `go mod tidy` leaves `go.sum` untouched and changes `go.mod`:

```diff
-	github.com/spf13/pflag v1.0.10 // indirect
+	github.com/spf13/pflag v1.0.10
```

`pflag` is a direct dependency in fact — the tree imports it — but `go.mod` still marks it
indirect, so the file has been out of date for some time and the gate reports green because it
never looks at `go.mod`.

**What closing it takes.** Run `go mod tidy`, commit the one-line change, and widen the diff to
`git diff --exit-code go.mod go.sum`. Both halves belong in one commit: widening the check without
the fix turns the `lint` gate red on every pull request.

**Note for the porcelain gate (H1).** This is why H1 lives in the `build` job rather than `lint`.
`build` runs `go mod download` and `go mod verify`, neither of which writes; `lint` runs `tidy`,
which does. Adding H1 to `lint` today would fail on this pre-existing drift rather than on anything
the pull request did.

---

## 30. A new item's first transition is ungated, and D72 names the wrong cause

**Severity**: Medium (a configured restriction silently does not apply; no disclosure, no data
loss)
**Status**: **CLOSED 2026-08-01** by the workflow fail-closed phase, taking **option (e)** below —
the one this entry recommends. Entities are now born in their space workflow's initial state, with
`status` and `workflow_state_id` written together, resolved through `tiergate.Gate.InitialPosition`.
A space with no workflow keeps the old literal default.

`internal/core/api/workflow_d72_ungated_first_transition_test.go` was skipped and now runs, with its
assertions unchanged: the item is created at `backlog`, and its first move is refused 422 by the
validator on the initial edge.

Two things this entry raised are worth carrying forward. The blast radius it warned about was real
but smaller than feared, because `testutil.CreateTestSpace` assigns no workflow — so the Go suite
was almost entirely unaffected, and the visible change landed in the frontend, where both status
pickers now derive their options from the server rather than from a hardcoded list. And the backfill
decision it flags as "a data decision that needs a maintainer" was **not** taken: migration 051
reconciles `workflow_state_id` from the status text and deliberately leaves `status` alone, so items
already sitting at `open` stay there and are placed at read time instead.

The original analysis is kept below unedited, because the rejected options are the useful part of
the record.

`ItemService.CreateItem` in `internal/core/projects/item.go` writes `item.Status = "open"`
unconditionally — the only occurrence of that literal in the file. The seeded project workflow's states are
`backlog`/`todo`/`in_progress`/`in_review`/`done` (migration 016). So a freshly created item sits
at a status that names no state, `TierService.Gate` resolves no edge, and — because absence is
deliberately not refusal — **no guard, approval or post-function applies to its first move**. Every
subsequent transition is gated normally.

An administrator who configures "nothing leaves the backlog without an assignee" therefore has a
rule that every item evades exactly once, on the move that matters most.

### The recorded cause is wrong, and the wrongness is load-bearing

`docs/design/spec-repo-reconciliation.md` D72 and a comment in
`workflow_tiers_integration_test.go` both blamed **migration 014's column default**. They are
factually wrong: `CreateProjectItem` names `status` in its INSERT column list
(`internal/db/queries/project_items.sql`), so the `DEFAULT 'open'` is never evaluated by the
application.

This is not pedantry — it rules out the only fix that looked cheap. Changing the column default
would alter nothing except raw-SQL test fixtures that omit the column, silently changing test
setup while fixing nothing in production. Both statements are corrected in P-W PR-B.

### The ticket side is protected only by a coincidence

Tickets are created at `"open"` too, and the seeded TICKET workflow happens to have a state called
`open`, so their first transition does resolve an edge. That is a name collision, not a design. An
administrator who creates a custom default ticket workflow whose initial state is called anything
else reopens the same hole on the ticket side, with nothing to warn them.

### Three fixes were considered; two are wrong

- **Fall back to the workflow's initial state inside `Gate`.** Rejected. It conflates "never
  transitioned" with "the status names no state for some other reason", which is precisely the case
  `tier_service.go:246` documents — a state can be renamed out from under an item. It would run the
  initial edge's validators, approvals *and post-functions* for a move that has nothing to do with
  them, and post-functions **mutate**, so it applies the wrong rule rather than a stricter one. It
  also fails a subtest of `TestGate_UntouchedWorkflowIsUnaffected`; making that pass would mean
  weakening an assertion, which §2 forbids.

- **Use `workflow_state_id IS NULL` as the discriminator.** Rejected, and it is the attractive one:
  it reuses the exact flag the engine-backed routes already consult. It is broken by D71 — the
  legacy `/status` route writes `status` alone, so the column stays NULL after arbitrarily many
  moves, and an item that went `open → todo → in_progress` would still resolve "from = initial" on
  its fourth move. Making it correct requires the D71 status/state drift repair that PR #86
  explicitly deferred.

- **Recommended: create items already inside their state machine.** At creation, resolve the space
  workflow's initial state and write BOTH `status = initial.Name` and `workflow_state_id =
  initial.ID`. It fixes the cause rather than a symptom, needs no migration, changes no existing
  row, and closes the ticket side structurally instead of by name coincidence. Where a space has no
  workflow — a supported live state, since assignment is best-effort — keep `"open"`, which is
  legitimately "not configured".

### Why the recommended fix was still not taken here

It changes the default status of every new project item across the product. `board.go`'s
`DefaultColumnNames`, `SprintBoardPage.tsx`'s column list and `ItemDetailPage.tsx`'s
`ALL_STATUSES` all enumerate the literal `"open"`, and the last of those would render a new item's
status `<select>` with no matching option — a visible regression no Go test can catch. It also
needs a `With*` collaborator on both create services, so the harness rules engage.

And it repairs nothing already stored. Every item created before it ships keeps its ungated first
move, so shipping it inside a feature phase would produce something that reads as a closed hole
and is not one. The backfill is a data decision that needs a maintainer, not a phase.

**Re-enable condition for the skipped test**: `ItemService.CreateItem` and `TicketService.Create`
resolve the space workflow's initial state at creation, together with a decision about items
already sitting at `"open"`.

---

## 31. `verify-api.sh` races the server's startup migration on a fresh database

**Severity**: Low (a local gate fails and reports the wrong reason; no product impact)
**Status**: Open. Found by P-W PR-B while running the battery, and reproduced identically on
`origin/main`, so it is not that phase's doing.

`scripts/verify-api.sh` starts the server, sleeps 2 seconds, then runs `admin create-user` and
logs in. Both the server and the CLI run goose on startup. On a database that has never been
migrated the server is still applying migrations when the CLI starts, the CLI loses the goose
advisory lock, and the user is never created — after which the login answers **401** and the
script fails at step 4 with no indication of the real cause, because the CLI's error is swallowed:

```
/tmp/azimuthal-test admin create-user … 2>/dev/null || true
```

It gets worse with every migration added, since the server's startup window grows. At 50
migrations it is reliable on this Windows box; it was presumably intermittent before.

**Workaround**: pre-migrate the database (any `admin create-user` against it will do) before
running the script. With that, `verify-api` passes end to end.

**Fix, when somebody wants it**: drop the `2>/dev/null || true` so the failure is visible, and
replace the `sleep 2` with a poll on `/health` — which the script already knows how to reach, and
which is exactly the readiness probe `test-db-up` learned to do for postgres for the same class of
reason.

---

Write-path authorization hardening was carried out as a follow-up to the read-path work. As with
that pass, the specifics stay out of public history and went to the maintainer; what is recorded
here is only that the work happened and which already-public entries it closed (#23, #27, #28).
