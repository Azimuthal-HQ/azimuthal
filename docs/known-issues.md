# Known Issues

Documented by Agent 2E (Integration Validator) after validating Phases 0-2.

---

## 1. Missing Repository Adapter Layer (Blocking: full API serving)

**Severity**: High
**Status**: Needs implementation (Phase 3 prerequisite)

The domain services (auth, tickets, projects) define repository interfaces using domain types (e.g., `auth.User`, `tickets.Ticket`, `projects.Item`), and the data layer has sqlc-generated queries using DB types (e.g., `generated.User`, `generated.Item`). There is no adapter code bridging these two layers.

**Impact**: The binary serves health/ready endpoints but cannot serve the full API routes because the chi router (`api.NewRouter`) requires service instances, which require repository implementations.

**Design mismatches to resolve**:
- `auth.User` has no `OrgID` field, but `generated.User` and `CreateUserParams` require one
- `auth.SessionRepository.GetByToken` accepts a plain token, but `generated.GetSessionByTokenHash` expects a hashed value
- `generated.GetUserByEmail` requires `OrgID` parameter, but `auth.UserRepository.GetByEmail` takes only email
- Domain types use `time.Time`, `*uuid.UUID`; generated types use `pgtype.Timestamptz`, `pgtype.UUID`

**Note**: The wiki `PageStore` interface uses generated types directly, so `*generated.Queries` already satisfies it. Only auth, tickets, and projects modules need adapters.

**Recommendation**: Create `internal/db/adapters/` package with adapter structs for each repository interface. Resolve the OrgID and token-hashing mismatches at the adapter boundary.

---

## 2. Test Coverage Below 60% Floor (47.1%)

**Severity**: Medium
**Status**: Needs additional tests

Overall statement coverage is 47.1%. Lowest-coverage packages:

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

## 5. cmd/server/main.go Does Not Wire Full API Router

**Severity**: High (related to issue #1)
**Status**: Blocked on adapter layer

`cmd/server/main.go` loads config and serves health/ready through a chi router with the full middleware stack (RequestID, Logging, CORS, Recoverer). However, it does not call `api.NewRouter()` because that requires all service/handler instances, which in turn require the missing adapter layer (issue #1).

Once adapters are implemented, main.go should be updated to:
1. Connect to the database via `db.Connect()`
2. Run migrations via `db.Migrate()`
3. Construct services with DB-backed adapters
4. Call `api.NewRouter()` with the full `RouterConfig`
