# Azimuthal — Project State

Last updated: 2026-04-11

This document is based on the actual git log, code inspection, and existing documentation.
Items marked **unverified** could not be confirmed from code inspection alone and
require manual testing against a running instance.

---

## Section 1 — Release History

### v0.1.0 — Initial Release

Tagged at `cb45a12`. This was the first tagged release, containing the entire build-up
of the project across all agent phases. Phase 0 delivered the repository scaffold, CI
pipeline, Makefile, Dockerfile, and security scanning (gosec, govulncheck, gitleaks,
trivy). Phase 1 added the data layer (migrations, sqlc), core authentication (JWT,
sessions, middleware), infrastructure (config, object storage, email, background jobs),
and enterprise interface stubs. Phase 2 delivered the three domain modules (service desk,
wiki, project tracking), the unified REST API with chi router, an integration validation
pass, and the DB adapter layer bridging domain types to sqlc-generated types. Phase 3
added the React + TypeScript frontend shell with dark mode, the Create Space modal,
ticket/item/page creation modals, backup/restore CLI, admin commands, and self-hosting
documentation. The release pipeline itself required several iterations to fix
(`release.yml` was updated twice). Known issues at this point: the Dockerfile did not
include a frontend build stage, the frontend used mock data instead of real API calls,
and onboarding/org creation had bugs.

### v0.1.1 — Docker Frontend Build Fix

Tagged at `e0b7ab2` (PR #17). Fixed the Dockerfile to include a frontend build stage
and added a `serve` command to docker-compose. Without this fix, the Docker image
shipped without the compiled frontend assets, so `docker compose up` served a blank page.

### v0.1.2 — Real Auth Flow

Tagged at `c1efed5` (PR #18). Replaced all mock/hardcoded data in the frontend with
real API calls. Implemented the actual login flow, token storage in localStorage,
authenticated fetch wrapper, and logout. Removed unused TypeScript imports that caused
strict-mode build failures.

### v0.1.3 — Onboarding and Org Fixes

Tagged at `de18063` (PR #19). Fixed onboarding and organization bugs discovered during
first user testing. The `admin create-user` command was not correctly creating the
organization and membership chain. Added lint fixes (funlen) and tests for the new
endpoints.

### v0.1.4 — Blank Screen Fix

Tagged at `86af9e0` (PR #20). Fixed a blank screen that appeared when navigating into a
space. The frontend routing was not correctly matching space-scoped URLs, causing the
React router to render nothing.

### v0.1.5 — Routing and Request Body Fixes

Tagged at `0159d42` (PR #21). Fixed space-scoped API routing so that requests to
`/api/v1/orgs/{orgId}/spaces/{spaceId}/items` were correctly handled. Also fixed
request body mismatches where the frontend was sending fields the backend did not expect
(or vice versa).

### v0.1.6 — Nil Labels Fix

Tagged at `999bb9d` (PRs #22, #23). The `labels` column on the `items` table had a
NOT NULL constraint with no default value. Creating a ticket or project item without a
`labels` field caused a Postgres constraint violation (SQLSTATE 23502). Fixed by
defaulting nil labels to an empty array at the adapter layer. Also added testing
requirements to CLAUDE.md and documented this as a known issue to prevent recurrence.

### v0.1.7 — SPA Fallback Fix

Tagged at `28e9ce1` (PR #24). The SPA fallback handler was intercepting API routes —
when the frontend requested `/api/v1/...` and the server had no matching handler, it
returned `index.html` (with `Content-Type: text/html`) instead of a JSON 404. Fixed by
checking for the `/api/` prefix in the SPA handler and returning a proper 404.

### v0.1.8 — Frontend Crash Guards

Tagged at `d68ba52` (PRs #25, #26). Guarded `.slice()` and `.map()` calls in the
frontend to prevent crashes when the API returned `null` instead of an empty array.
Also added comprehensive agent testing standards and the `verify-api` script to
documentation.

### v0.1.9 / v0.1.10 — JSON Serialization Fix

Both tagged at `24c62bd` (PR #28). Added lowercase JSON tags to all domain structs.
Without these tags, Go's default JSON marshalling produced PascalCase keys
(`Title`, `Priority`) instead of the snake_case keys (`title`, `priority`) the frontend
expected. v0.1.10 appears to be a re-tag of the same commit.

### v0.1.11 — Global Email Lookup (Current)

Tagged at `edcc6f6` (PRs #29, #30). Fixed login to look up users by email globally
across all organizations, rather than scoping the lookup to a single default org. Also
removed the concept of a default organization created at startup — organizations are now
created on-demand when users are created via `admin create-user`.

---

## Section 2 — Current Working State

Based on code inspection of main branch at `edcc6f6`.

Legend: ✓ = code exists and appears correct, ✗ = code is missing or known broken,
⚠ = code exists but has known issues or is unverified.

### Infrastructure

- [x] Docker Compose one-liner deployment — `build/docker-compose.yml` defines app, db (PostgreSQL 16), and storage (MinIO)
- [x] Single binary with embedded frontend — `web/embed.go` uses `//go:embed all:dist`, served by `newSPAHandler()` in `cmd/server/main.go`
- [x] `azimuthal admin create-user` creates user + org + membership — implemented in `cmd/server/admin.go`
- [x] Login returns JWT + org context — `internal/core/api/auth/handler.go` returns `access_token`, `refresh_token`, and `org` object
- [x] Global email lookup on login (not org-scoped) — fixed in PR #30, `auth.Authenticate()` calls `GetByEmail(ctx, email)` with no org filter
- [x] Migrations run automatically on startup — `db.Migrate(ctx, pool)` called in `newServer()` before router init
- [x] No default org created at startup — orgs created on-demand by `admin create-user`
- [x] Backup CLI (`azimuthal backup`) — `cmd/server/backup.go`, creates tar.gz with pg_dump + object storage + manifest
- [x] Restore CLI (`azimuthal restore`) — `cmd/server/restore.go`, restores from backup archive

### Authentication

- [x] Login with email + password — `POST /api/v1/auth/login` in `internal/core/api/auth/handler.go`
- [x] JWT token issued and returned in response body — RS256 signing in `internal/core/auth/jwt.go`
- [x] Authenticated routes protected — `auth.Authenticator.RequireAuth()` middleware on all `/api/v1` routes
- [x] Logout clears sessions — `POST /api/v1/auth/logout` deletes all sessions; frontend removes tokens from localStorage

### Dashboard

- [x] Shows all spaces with correct counts — `DashboardPage.tsx` fetches via `useSpaces(orgId)` hook
- [x] Stat cards with correct icons and labels — 3 cards: Total Spaces, Service Desks, Projects
- [x] Create Space modal works and submits to real API — POSTs to `/api/v1/orgs/{orgId}/spaces`
- [x] Redirects to correct module after space creation — service_desk → tickets, wiki → wiki, project → backlog
- [x] Empty state for new users — compass icon, welcome message, and create button

### Service Desk

- [x] Ticket list loads without error — `TicketListPage.tsx` with search and filters
- [x] Create ticket with minimum fields succeeds — title + priority, POSTs to API
- [x] Ticket appears in list after creation — React Query invalidation refreshes list
- [⚠] Priority displays correctly (not "Unknown") — maps numeric 0-3 to labels, but falls back to "Unknown" for unmapped values; needs verification that API always returns mapped values
- [x] Status displays correctly — maps string status to badge
- [x] Click ticket row opens detail view — `TicketDetailPage.tsx` shows full ticket
- [x] Kanban board loads — `KanbanPage.tsx` with drag-and-drop via dnd-kit

### Wiki

- [x] Page list loads without error — `WikiPage.tsx` fetches pages
- [x] Create page works — modal POSTs to API with title + empty content
- [x] Page appears in sidebar after creation — sets created page as active
- [x] Page content renders — uses `ReactMarkdown` for markdown → HTML
- [⚠] Edit button present but non-functional — button exists with pencil icon but has no onClick handler

### Projects

- [x] Backlog loads without error — `BacklogPage.tsx` with sprint grouping
- [x] Create item works — modal with title, type, priority, description
- [x] Item appears in backlog after creation — React Query invalidation
- [x] Priority displays correctly — maps string priority to badge
- [x] Sprint board loads — `SprintBoardPage.tsx` with drag-and-drop
- [x] Click item row opens detail view — `ItemDetailPage.tsx` (read-only)

### Settings

- [⚠] Profile settings — form exists but Save button is not connected to an API endpoint
- [x] Org settings save — connected to `useUpdateOrganization()` mutation
- [x] Dark/light mode toggle works and persists — stored in localStorage as `azimuthal-theme`

---

## Section 3 — Known Gaps

Features that exist in the UI but are not fully functional.

### Wiki Edit Button

The wiki page view has an "Edit" button with a pencil icon, but clicking it does nothing.
The button has no `onClick` handler. To make editing work, the button needs to switch the
view to an editor component and wire up `PUT /api/v1/orgs/{orgId}/spaces/{spaceId}/pages/{pageId}`.

**File**: `web/src/pages/wiki/WikiPage.tsx` (around line 150)

### Profile Settings Save

The profile settings form (display name, email) renders correctly, but the Save button
is not connected to any API call. Org settings save correctly via `useUpdateOrganization()`,
but there is no corresponding `useUpdateProfile()` hook.

**File**: `web/src/pages/settings/SettingsPage.tsx` (around line 120-180)

### Wiki Page Tree Hierarchy

The wiki sidebar shows a flat list of pages, not a hierarchical tree. The backend
supports parent/child relationships via `internal/core/wiki/tree.go`, but the frontend
does not render nested structure or collapse/expand.

**File**: `web/src/pages/wiki/WikiPage.tsx` (around line 91-135)

### SSO (SAML/OIDC)

The `internal/core/sso/provider.go` interface is defined with `BeginAuth()`,
`CompleteAuth()`, and `IsAvailable()`, but the default provider is a no-op stub that
returns `ErrNotConfigured`. No actual SAML or OIDC integration exists yet.

**File**: `internal/core/sso/provider.go`

### Audit Logging

The `internal/core/audit/logger.go` interface is defined with event types
(user.login, item.created, permission.changed, etc.), but the default implementation
silently discards all events. No events are persisted to the database. `IsAvailable()`
returns false.

**File**: `internal/core/audit/logger.go`

### Analytics Reporting

The `internal/core/analytics/reporter.go` interface defines query types (OrgSummary,
UserActivity), but the implementation returns `ErrNotImplemented` for all queries.
`IsAvailable()` returns false.

**File**: `internal/core/analytics/reporter.go`

### Project Item Detail — Read Only

The project item detail page displays all fields but offers no way to edit them inline.
The ticket detail page has a status dropdown for transitions, but the project item
detail does not.

**File**: `web/src/pages/projects/ItemDetailPage.tsx`

### Email Ingestion and Replies

The backend has `internal/core/tickets/email_ingest.go` and `email_reply.go` with
the logic for converting inbound emails to tickets and sending outbound replies.
However, no SMTP listener or webhook endpoint is wired up to actually receive
inbound email. The email job worker exists in `internal/jobs/email.go` but requires
SMTP configuration that is not validated on startup.

**Files**: `internal/core/tickets/email_ingest.go`, `internal/core/tickets/email_reply.go`, `internal/jobs/email.go`

---

## Section 4 — Known Issues

### 1. RSA Key Generated at Runtime on Every Startup

JWT signing uses an RSA key pair that is generated fresh each time the server starts
(`cmd/server/main.go`). This means all issued JWTs and sessions are invalidated on
every restart. The key should be loaded from persistent storage or derived from
`JWT_SECRET`.

**Severity**: Medium
**GitHub issue**: None

### 2. Test Coverage Below 80% Target (47.1%)

Overall statement coverage is approximately 47.1%, well below the 80% target stated in
CLAUDE.md. Lowest packages: `internal/db` (1.8%), `internal/jobs` (34.4%),
`internal/core/api/projects` (35.8%), `cmd/server` (0.0%), `internal/db/generated` (0.0%).
The DB and generated packages require a real Postgres instance for integration tests.

**Severity**: Medium
**GitHub issue**: None (documented in `docs/known-issues.md` as issue #2)

### 3. CORS Allows All Origins

`internal/core/api/middleware.go` sets `Access-Control-Allow-Origin: *`. This is
appropriate for development but is a security risk in production. The allowed origin
should be configurable via `APP_BASE_URL` or a dedicated `CORS_ORIGINS` env var.

**Severity**: Medium (security)
**GitHub issue**: None

### 4. Soft-Delete Missing on Some Tables

The `memberships`, `space_members`, and `sprints` tables lack `deleted_at` columns.
CLAUDE.md requires soft deletes for all user-facing tables. These may be intentional
design choices for ephemeral join data, but should be reviewed.

**Severity**: Low
**GitHub issue**: None (documented in `docs/known-issues.md` as issue #4)

### 5. Race Detector Requires CGO on Windows

`go test -race ./...` requires `CGO_ENABLED=1` and GCC. On Windows without GCC, race
detection cannot run locally. CI (Linux-based) handles this.

**Severity**: Low
**GitHub issue**: None (documented in `docs/known-issues.md` as issue #3)

---

## Section 5 — Tech Debt

### RSA Key Generation in main.go

**File**: `cmd/server/main.go` (around line 92)
**Issue**: RSA key pair is generated at runtime on every startup. Should load from a
persistent key file or derive from `JWT_SECRET` to survive restarts.

### Hardcoded HTTP Timeouts

**File**: `cmd/server/main.go` (around lines 81-83)
**Issue**: `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` are hardcoded to 15s/15s/60s.
Should be configurable via environment variables.

### Wiki Uses Generated Types Directly

**File**: `internal/core/wiki/` and `internal/core/api/wiki/handler.go`
**Issue**: The wiki module uses `generated.Queries` directly instead of going through a
repository adapter like tickets and projects do. This makes the wiki tightly coupled to
the database layer and harder to mock in tests. Should follow the adapter pattern used
by `internal/db/adapters/tickets.go` and `internal/db/adapters/projects.go`.

### Permissive CORS Configuration

**File**: `internal/core/api/middleware.go` (around line 72)
**Issue**: `Access-Control-Allow-Origin: *` is hardcoded. Should read from config and
default to `APP_BASE_URL` in production.

### No Adapter for Spaces

**File**: `internal/core/api/spaces/handler.go`
**Issue**: The spaces handler uses `generated.Queries` directly, similar to the wiki
issue. Should use a repository adapter for consistency and testability.

### Profile Update Endpoint Missing or Unwired

**File**: `web/src/pages/settings/SettingsPage.tsx`
**Issue**: The frontend profile form exists but has no API hook. Either the backend
endpoint exists but is not wired to the frontend, or the endpoint itself is missing.
Needs a `PUT /api/v1/me` or `PATCH /api/v1/me` endpoint and a corresponding React
Query mutation.

### v0.1.9 and v0.1.10 Are Identical Tags

**Issue**: Both `v0.1.9` and `v0.1.10` point to commit `24c62bd`. This appears to be an
accidental double-tag. One should be removed to avoid confusion in release history.
