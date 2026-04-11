# Known Issues

Documented by Agent 2E (Integration Validator) after validating Phases 0-2.
Updated by test/backend-coverage branch with test references.

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

## 2. Test Coverage Below 60% Floor (47.1%) — IMPROVED

**Severity**: Medium
**Status**: Improved to 82.4% (cross-package) / 59.2% (per-package) with integration tests

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

---

## 3. Race Detector Requires CGO (Windows CI)

**Severity**: Low
**Status**: Environment constraint

`go test -race ./...` requires `CGO_ENABLED=1` and a C compiler (GCC). On Windows without GCC installed, race detection cannot run locally. The CI pipeline (Linux-based) should handle this. Ensure the CI runner has GCC available.

---

## 4. Soft-Delete Missing on Some Tables

**Severity**: Low
**Status**: Design review needed

The following tables lack `deleted_at` columns:
- `memberships` — may need audit trail for membership removal
- `space_members` — same concern
- `sprints` — sprint history could be useful for reporting

These may be intentional design choices (ephemeral data), but should be reviewed before GA.

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

## 6. Testing Gap — Real Database Integration Tests

**Discovered:** v0.1.4 testing
**Status:** Partially mitigated — see CLAUDE.md Testing Requirements

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

## 7. RSA Key Generated at Runtime on Every Startup

**Severity**: Medium
**Status**: Open — documented with skipped test

JWT signing uses an RSA key pair generated fresh each time the server starts
(`cmd/server/main.go`). All issued JWTs and sessions are invalidated on every
restart. The key should be loaded from persistent storage or derived from `JWT_SECRET`.

**Test**: `internal/core/api/known_issues_test.go` — `TestRSAKey_SurvivesRestart` (skipped)
**See also**: `docs/project-state.md` Section 4, issue #1

---

## 8. CORS Allows All Origins

**Severity**: Medium (security)
**Status**: Open — documented with skipped test

`internal/core/api/middleware.go` sets `Access-Control-Allow-Origin: *`.
This is appropriate for development but a security risk in production.

**Test**: `internal/core/api/known_issues_test.go` — `TestCORS_RestrictedInProduction` (skipped)

---

## 9. Audit Logger Discards All Events

**Severity**: Low
**Status**: Open — documented with skipped test

The default audit logger implementation silently discards all events.
`IsAvailable()` returns false. No events are persisted to the database.

**Test**: `internal/core/api/known_issues_test.go` — `TestAuditLog_PersistsEvents` (skipped)
**See also**: `docs/project-state.md` Section 3 — Audit Logging

---

## 10. Profile Update Endpoint Missing

**Severity**: Low
**Status**: Open — documented with skipped test

The frontend profile form exists but the Save button is not connected to any
API endpoint. There is no `PUT /api/v1/me` or `PATCH /api/v1/me` endpoint.

**Test**: `internal/core/api/known_issues_test.go` — `TestProfileUpdate_SavesChanges` (skipped)
**See also**: `docs/project-state.md` Section 3 — Profile Settings Save

---

## 11. Duplicate Email Registration Returns 500 Instead of 409

**Severity**: Medium
**Status**: Open — captured by integration test

When registering a user with an already-taken email, the API returns 500
(INTERNAL_ERROR) instead of 409 (CONFLICT). The `UserAdapter.Create` method
does not map postgres unique constraint violations to `auth.ErrEmailTaken`.

**Test**: `internal/core/api/routes_integration_test.go` — `TestIntegration_Register_DuplicateEmail`

---

## 12. Goose Migration Mutex Required for Parallel Tests

**Severity**: Low
**Status**: Fixed in test/backend-coverage branch

`goose.SetTableName()` uses a package-level global variable. When integration
tests run in parallel, concurrent `goose.Up()` calls with different schema-scoped
table names race and cause `SQLSTATE 42P01` or `SQLSTATE 23505` errors.

**Fix**: Added `sync.Mutex` in `internal/testutil/db.go` around `goose.SetTableName` + `goose.Up` calls.

---

## 13. Smoke Test login_user Failure (Pre-existing)

**Severity**: Medium
**Status**: Open — pre-existing on main branch

`cmd/server/smoke_test.go` `TestSmoke/login_user` fails because it registers
two users with the same email. The second registration returns 500 (due to
issue #11 above), causing the login step to fail.

**Test**: `cmd/server/smoke_test.go` — `TestSmoke/login_user`
