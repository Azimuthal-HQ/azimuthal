# Multi-Agent Task Briefs
# Hand these directly to each Claude Code agent session.
# Each brief is self-contained — the agent needs nothing else to start.

---

## AGENT 0A — Repo Scaffold & CI Pipeline
**Phase**: 0 (sequential first)
**Branch**: `agent/0a-foundation`

### Your Job
Bootstrap the entire repository structure and core CI pipeline.
Everything else blocks on you. Do not write application logic.

### Deliverables (must all be in your PR)
- [ ] `go.mod` and `go.sum` with all dependencies listed in CLAUDE.md
- [ ] Full directory structure from CLAUDE.md layout section
- [ ] `cmd/server/main.go` — wires everything together, no logic yet (stubs OK)
- [ ] `.forgejo/workflows/ci.yml` — full pipeline (already written, copy verbatim)
- [ ] `.forgejo/workflows/release-community.yml`
- [ ] `Makefile` with all targets
- [ ] `build/Dockerfile` — multi-stage, distroless final image
- [ ] `build/docker-compose.yml` — postgres + minio + app
- [ ] `.golangci.yml` — copy from repo root
- [ ] `.editorconfig`, `.gitignore`, `.gitattributes`
- [ ] `CONTRIBUTING.md` — references CLA requirement
- [ ] `LICENSE` — Apache 2.0 full text
- [ ] Verify `make build` passes before opening PR

### Dockerfile requirements
- Multi-stage: builder stage (golang:1.23-alpine) + final stage (gcr.io/distroless/static)
- CGO_ENABLED=0 for static binary
- Runs as non-root user
- EXPOSE 8080
- HEALTHCHECK via /health endpoint

### Definition of Done
`make build` passes. `make test` passes (even with empty stubs). Pipeline YAML is valid.

---

## AGENT 0B — Security Scan Layer
**Phase**: 0 (sequential — starts after Agent 0A merges)
**Branch**: `agent/0b-security-pipeline`
**Depends on**: Agent 0A merged to main

### Your Job
Add all four security scanning tools to the pipeline Agent 0A created.
Do not write application logic.

### Deliverables
- [ ] `.gitleaks.toml` — copy from repo root + any project-specific additions
- [ ] `trivy.yaml` — copy from repo root
- [ ] `trivy-ignore.yaml` — empty file with comment explaining its purpose
- [ ] Update `.forgejo/workflows/ci.yml` to add all four scan jobs if not present
- [ ] Verify gosec installs and runs clean against current (empty) codebase
- [ ] Verify govulncheck runs clean
- [ ] Verify gitleaks runs clean
- [ ] Add `make scan` target verification to Makefile if missing
- [ ] Document each scanner in `docs/security-scanning.md`

### `docs/security-scanning.md` must cover
- What each tool scans for
- How to suppress a false positive (with required documentation comment)
- How to run scans locally
- What severity levels fail the build

### Definition of Done
`make scan` passes on current codebase. All four jobs appear in CI YAML.

---

## AGENT 1A — Data Layer
**Phase**: 1 (parallel — starts after Phase 0 complete)
**Branch**: `agent/1a-data-layer`
**Depends on**: Phase 0 complete

### Your Job
Build the entire database layer: migrations, connection pooling, and sqlc queries.
Do not write business logic — just the data access layer.

### Deliverables
- [ ] `sqlc.yaml` configuration file
- [ ] All goose migration files in `migrations/` (full schema from architecture.md section 4)
  - `001_organizations.sql`
  - `002_users_memberships.sql`
  - `003_spaces.sql`
  - `004_items.sql`
  - `005_pages.sql`
  - `006_comments.sql`
  - `007_notifications.sql`
  - `008_audit_log.sql`
  - `009_indexes.sql`
- [ ] `internal/db/connect.go` — pgxpool connection with health check
- [ ] `internal/db/migrate.go` — runs goose on startup
- [ ] sqlc query files for every table (CRUD + common queries)
- [ ] Generated sqlc output in `internal/db/generated/`
- [ ] `internal/db/db_test.go` — integration tests (require real postgres)
- [ ] Tests use `DATABASE_URL` env var, skip with `t.Skip()` if not set

### Migration rules
- Each migration is append-only — never edit existing files
- Down migrations must be included for every up migration
- All tables must have `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- All user-facing tables must have `deleted_at TIMESTAMPTZ` (soft delete)
- The `audit_log` and `space_members` tables are standard tables available to all users

### Definition of Done
`make migrate` runs clean. `make sqlc` generates without errors.
`make test` passes including DB integration tests.

---

## AGENT 1B — Core Auth & SSO
**Phase**: 1 (parallel)
**Branch**: `agent/1b-auth`
**Depends on**: Phase 0 complete

### Your Job
Build local authentication: user creation, login, sessions, JWT, and HTTP middleware.
SSO/SAML is a standard feature — build the interface and default provider in `internal/core/sso/`.

### Deliverables
- [ ] `internal/core/auth/user.go` — user CRUD (uses DB layer via interface)
- [ ] `internal/core/auth/password.go` — bcrypt hashing, comparison
- [ ] `internal/core/auth/jwt.go` — token generation, validation, refresh
- [ ] `internal/core/auth/session.go` — session management (postgres-backed)
- [ ] `internal/core/auth/middleware.go` — HTTP auth middleware for chi
- [ ] `internal/core/sso/provider.go` — SSO Provider interface + default implementation
- [ ] Full test coverage for all above files
- [ ] No hardcoded secrets — all config via `internal/config/`

### Auth requirements
- Passwords: bcrypt cost 12 minimum
- JWT: RS256 (not HS256) — asymmetric keys
- Sessions: store in postgres `sessions` table, not in-memory
- Rate limiting: stub the interface — implementation in Phase 2

### Definition of Done
`make test` passes. Auth middleware correctly rejects unauthenticated requests.

---

## AGENT 1C — Config, Jobs, Storage, Audit, RBAC & Analytics
**Phase**: 1 (parallel)
**Branch**: `agent/1c-infrastructure`
**Depends on**: Phase 0 complete

### Your Job
Build the shared infrastructure that all modules depend on:
config loading, background job queue, object storage interface,
audit logging, RBAC, and analytics reporting.

### Deliverables
- [ ] `internal/config/config.go` — viper config struct with all env vars from CLAUDE.md
- [ ] `internal/config/config_test.go` — validates required vars, defaults
- [ ] `internal/core/storage/store.go` — ObjectStore interface
- [ ] `internal/core/storage/s3.go` — S3/MinIO implementation
- [ ] `internal/core/storage/memory.go` — in-memory implementation for tests
- [ ] `internal/jobs/queue.go` — river job queue setup
- [ ] `internal/jobs/email.go` — email job worker (SMTP)
- [ ] `internal/jobs/notification.go` — in-app notification worker
- [ ] `internal/core/email/sender.go` — email sender interface + SMTP impl
- [ ] `internal/core/audit/logger.go` — AuditLogger interface + default implementation
- [ ] `internal/core/rbac/checker.go` — RBAC Checker interface + role-based implementation
- [ ] `internal/core/analytics/reporter.go` — Analytics Reporter interface + default implementation
- [ ] Tests for all above (use memory storage in tests, not real S3)

### Config must include validation
- All required vars fail fast on startup with clear error messages
- Optional vars have sensible defaults
- `APP_ENV=test` skips certain validations (e.g. SMTP not required)

### Definition of Done
`make test` passes. `make build` passes. Config fails loudly if `DATABASE_URL` is missing.

---

## AGENT 2A — Service Desk Module
**Phase**: 2 (parallel — starts after Phase 1 complete)
**Branch**: `agent/2a-service-desk`
**Depends on**: All Phase 1 agents merged

### Your Job
Build the core service desk: ticket lifecycle, email ingestion/egress, kanban state machine.

### Deliverables
- [ ] `internal/core/tickets/ticket.go` — Ticket domain model + CRUD
- [ ] `internal/core/tickets/status.go` — status state machine (open→in_progress→resolved→closed)
- [ ] `internal/core/tickets/assignment.go` — assignment logic + notifications
- [ ] `internal/core/tickets/email_ingest.go` — parse inbound email → create ticket
- [ ] `internal/core/tickets/email_reply.go` — send outbound email replies
- [ ] `internal/core/tickets/kanban.go` — kanban board queries (by status, by assignee)
- [ ] `internal/core/tickets/search.go` — full-text search via postgres
- [ ] Full test suite including state machine transitions
- [ ] No direct DB calls — all via sqlc generated queries from Agent 1A

### Definition of Done
`make test` passes with >80% coverage on this package.
State machine rejects invalid transitions.

---

## AGENT 2B — Wiki Module
**Phase**: 2 (parallel)
**Branch**: `agent/2b-wiki`
**Depends on**: All Phase 1 agents merged

### Your Job
Build the wiki/docs module: page tree, markdown rendering, version history, conflict detection.

### Deliverables
- [ ] `internal/core/wiki/page.go` — Page CRUD with optimistic locking
- [ ] `internal/core/wiki/tree.go` — page tree navigation (parent/child)
- [ ] `internal/core/wiki/revision.go` — version history, diff between revisions
- [ ] `internal/core/wiki/render.go` — markdown → HTML (use goldmark)
- [ ] `internal/core/wiki/conflict.go` — 409 on version mismatch + merge guidance
- [ ] `internal/core/wiki/search.go` — full-text search
- [ ] Full test suite including concurrent edit conflict scenario
- [ ] Optimistic locking: `UPDATE pages SET version = version+1 WHERE version = $expected`

### Definition of Done
`make test` passes. Concurrent edit test demonstrates 409 on conflict.

---

## AGENT 2C — Project Tracking Module
**Phase**: 2 (parallel)
**Branch**: `agent/2c-projects`
**Depends on**: All Phase 1 agents merged

### Your Job
Build project tracking: backlog, sprints, roadmap, cross-tool item linking.

### Deliverables
- [ ] `internal/core/projects/item.go` — project item CRUD (reuses items table)
- [ ] `internal/core/projects/sprint.go` — sprint create/start/complete lifecycle
- [ ] `internal/core/projects/backlog.go` — backlog ordering and prioritisation
- [ ] `internal/core/projects/roadmap.go` — date-based roadmap queries
- [ ] `internal/core/projects/relations.go` — cross-tool links (ticket→wiki page, etc.)
- [ ] `internal/core/projects/labels.go` — label management
- [ ] Full test suite

### Definition of Done
`make test` passes. Sprint lifecycle state machine works correctly.

---

## AGENT 2D — API Layer & Unified Router
**Phase**: 2 (sequential — starts after 2A, 2B, 2C all merged)
**Branch**: `agent/2d-api`
**Depends on**: Agents 2A, 2B, 2C all merged

### Your Job
Wire all modules together into a unified REST API with chi.
Generate the OpenAPI spec. Write integration tests.

### Deliverables
- [ ] `internal/core/api/router.go` — chi router, all routes registered
- [ ] `internal/core/api/middleware.go` — logging, auth, rate limiting, CORS, request ID
- [ ] `internal/core/api/health.go` — /health and /ready endpoints
- [ ] `internal/core/api/tickets/handler.go` — ticket HTTP handlers
- [ ] `internal/core/api/wiki/handler.go` — wiki HTTP handlers
- [ ] `internal/core/api/projects/handler.go` — project HTTP handlers
- [ ] `internal/core/api/auth/handler.go` — login/logout/refresh handlers
- [ ] `internal/core/api/spaces/handler.go` — space management handlers
- [ ] `docs/api/openapi.yaml` — OpenAPI 3.1 spec for all endpoints
- [ ] Integration tests covering happy path + auth failure for each module
- [ ] Consistent error response format across all endpoints

### Error response format (all errors must use this)
```json
{
  "error": {
    "code": "ITEM_NOT_FOUND",
    "message": "item with id abc123 not found",
    "request_id": "req_xyz"
  }
}
```

### Definition of Done
`make test` passes including integration tests.
`curl http://localhost:8080/health` returns 200.
OpenAPI spec validates without errors.

---

## AGENT 3A — Frontend Shell
**Phase**: 3 (sequential — after Phase 2 complete)
**Branch**: `agent/3a-frontend`
**Depends on**: Agent 2D merged

### Your Job
Build the unified frontend shell — global nav, shared design system,
and basic views for each module. All embedded into the Go binary.

### Deliverables
- [ ] `web/` — React + TypeScript or Templ + HTMX (choose one, document why)
- [ ] Unified top nav with: logo, space switcher, notifications bell, user avatar
- [ ] Left sidebar: context-sensitive per space type
- [ ] Shared design tokens: colors, typography, spacing (consistent across all tools)
- [ ] Basic views (not full-featured — scaffold only):
  - Ticket list + ticket detail
  - Wiki page tree + page view
  - Project backlog view
- [ ] `web/dist/` built and ready to embed
- [ ] `//go:embed all:web/dist` wired into `cmd/server/main.go`
- [ ] `make build` still produces a single binary with frontend embedded

### Design requirements
- Must feel like one product, not three stitched together
- Same font, same color palette, same component shapes throughout
- No tool should feel like you've left the app

### Definition of Done
`make build` produces a binary. Visiting http://localhost:8080 serves the frontend.
All three module views load without JS errors.

---

## AGENT 3B — Single Binary Validation & Self-Hoster UX
**Phase**: 3 (sequential — after Agent 3A merges)
**Branch**: `agent/3b-release-validation`
**Depends on**: Agent 3A merged

### Your Job
Validate the single binary works end-to-end, build the backup/restore CLI,
and make the self-hoster experience excellent.

### Deliverables
- [ ] `build/docker-compose.yml` — production-ready (postgres + minio + app + caddy)
- [ ] `build/docker-compose.dev.yml` — dev override (mailhog, no TLS)
- [ ] `cmd/server/backup.go` — `azimuthal backup --output ./backup.tar.gz`
- [ ] `cmd/server/restore.go` — `azimuthal restore --input ./backup.tar.gz`
- [ ] `cmd/server/admin.go` — `azimuthal admin create-user`, `azimuthal admin reset-password`
- [ ] End-to-end smoke test: start binary → migrate → create org → create ticket → verify
- [ ] `docs/self-hosting.md` — complete self-hosting guide
- [ ] `docs/upgrade.md` — how to upgrade between versions
- [ ] Verify `./azimuthal --help` shows clear, useful output

### Backup must include
- Postgres dump (pg_dump)
- All object storage files
- Single compressed archive with manifest
- Restore is idempotent (safe to run twice)

### Definition of Done
`make test` passes. Docker Compose one-liner works.
`./azimuthal backup` and `./azimuthal restore` work end-to-end.
