# CLAUDE.md — Azimuthal Project Context

This file is read by every Claude Code agent working on this repo.
Read it fully before writing any code.

---

## What Azimuthal Is

**Azimuthal** is a fully open-source, self-hostable alternative to the Atlassian suite
(Jira, Confluence, Jira Service Desk), built in Go.

The name comes from navigation: an azimuthal bearing is the precise angle that
tells you exactly where you're headed. That's what Azimuthal does for teams.

- **Binary name**: `azimuthal`
- **CLI examples**: `azimuthal backup`, `azimuthal restore`, `azimuthal admin`
- **License**: Apache 2.0, single repository, fully featured for all users
- **Business model**: Azimuthal is fully open source under Apache 2.0. All features are available to all users.
- **Repo**: github.com/Azimuthal-HQ/azimuthal (this repo)
- **Container registry**: ghcr.io/azimuthal-hq/azimuthal

Full architecture: `docs/architecture.md`

---

## Non-Negotiable Rules

These apply to every agent, every PR, no exceptions.

### Licensing
1. Never add a dependency with an AGPL, GPL, or LGPL license
2. Run `go-licenses check ./...` before adding any new dependency
3. All features live in `internal/core/` — SSO, audit, RBAC, analytics are standard
4. Interfaces are defined in `internal/core/` — implementations alongside them

### Code quality
5. Write the test first, then the implementation (TDD)
6. Every exported function needs a godoc comment
7. Every error must be handled — no `_` on error returns
8. Wrap errors with context: `fmt.Errorf("creating item: %w", err)`

### Data integrity
9. Multi-step writes always use transactions (`db.BeginTx`)
10. Never hard-delete user data — use `deleted_at` soft deletes
11. Never store files on local disk — always use `ObjectStore` interface
12. New columns need a goose migration file — never edit existing migrations

### Security
13. Never log passwords, tokens, or secrets
14. All user input must be validated before hitting the DB
15. Use parameterised queries only — never string-concatenate SQL

---

## Pipeline Gates (all must pass before any PR merges)

GitHub branch protection enforces these — no bypassing even for admins.

```
build          → go build ./...
test           → go test -race ./...  (80% coverage minimum)
lint           → golangci-lint
sast           → gosec (HIGH+ severity fails) — results in Security tab
vuln-scan      → govulncheck
secret-scan    → gitleaks  (+ GitHub native secret scanning always on)
container-scan → trivy (HIGH/CRITICAL fails) — results in Security tab
all-checks     → final gate job, branch protection requires this
```

Run `make pre-push` locally before opening a PR.

---

## Testing Requirements — Non-Negotiable

Before opening any PR that touches create, update, or delete operations,
agents MUST run the full local verification suite below. No exceptions.

### Step 1 — Build verification
```bash
go build ./...
go vet ./...
go test -race ./...
```

### Step 2 — Start real local services
```bash
# Start postgres and minio
docker compose -f build/docker-compose.dev.yml up -d db storage

# Wait for healthy
docker compose -f build/docker-compose.dev.yml exec db pg_isready -U azimuthal

# Run migrations
make migrate
```

### Step 3 — Start the binary
```bash
go build -o /tmp/azimuthal-test ./cmd/server
DATABASE_URL=postgres://azimuthal:dev@localhost:5432/azimuthal_dev?sslmode=disable \
JWT_SECRET=test-secret-for-local-testing-only \
STORAGE_ENDPOINT=http://localhost:9000 \
STORAGE_ACCESS_KEY=minioadmin \
STORAGE_SECRET_KEY=minioadmin \
STORAGE_BUCKET=azimuthal \
APP_ENV=development \
/tmp/azimuthal-test serve &
SERVER_PID=$!
sleep 2  # wait for server to start
```

### Step 4 — Create a test user and get a JWT
```bash
/tmp/azimuthal-test admin create-user \
  --email test@azimuthal.dev \
  --name "Test User" \
  --password testpassword123

TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@azimuthal.dev","password":"testpassword123"}' \
  | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

echo "Got token: ${TOKEN:0:20}..."
```

### Step 5 — Get org ID
```bash
ORG_ID=$(curl -s http://localhost:8080/api/v1/me \
  -H "Authorization: Bearer $TOKEN" \
  | grep -o '"org_id":"[^"]*"' | cut -d'"' -f4)

echo "Org ID: $ORG_ID"
```

### Step 6 — Test create operations with MINIMUM required fields
```bash
# Create a service desk space
SPACE=$(curl -s -X POST \
  "http://localhost:8080/api/v1/orgs/$ORG_ID/spaces" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Desk","type":"service_desk","slug":"test-desk"}')
SPACE_ID=$(echo $SPACE | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "Space ID: $SPACE_ID"

# Create a ticket with minimum fields only
curl -s -X POST \
  "http://localhost:8080/api/v1/orgs/$ORG_ID/spaces/$SPACE_ID/items" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Test ticket","type":"ticket","status":"open","priority":"medium"}' \
  | grep -v '"error"' && echo "✅ Ticket created" || echo "❌ Ticket failed"

# Create a wiki space
WIKI=$(curl -s -X POST \
  "http://localhost:8080/api/v1/orgs/$ORG_ID/spaces" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Wiki","type":"wiki","slug":"test-wiki"}')
WIKI_ID=$(echo $WIKI | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)

# Create a page with minimum fields only
curl -s -X POST \
  "http://localhost:8080/api/v1/orgs/$ORG_ID/spaces/$WIKI_ID/pages" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Test page","slug":"test-page","content":""}' \
  | grep -v '"error"' && echo "✅ Page created" || echo "❌ Page failed"

# Create a project space
PROJ=$(curl -s -X POST \
  "http://localhost:8080/api/v1/orgs/$ORG_ID/spaces" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Project","type":"project","slug":"test-project"}')
PROJ_ID=$(echo $PROJ | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)

# Create an item with minimum fields only
curl -s -X POST \
  "http://localhost:8080/api/v1/orgs/$ORG_ID/spaces/$PROJ_ID/items" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Test item","type":"task","status":"open","priority":"medium"}' \
  | grep -v '"error"' && echo "✅ Item created" || echo "❌ Item failed"
```

### Step 7 — Verify API routes return correct Content-Type
```bash
# API routes must return application/json
CT=$(curl -s -I \
  "http://localhost:8080/api/v1/orgs/$ORG_ID/spaces/$SPACE_ID/items" \
  -H "Authorization: Bearer $TOKEN" \
  | grep -i content-type)
echo "API Content-Type: $CT"
echo $CT | grep "application/json" && echo "✅ API returns JSON" || echo "❌ API returning wrong content type"

# Frontend routes must return text/html
CT=$(curl -s -I "http://localhost:8080/spaces/$SPACE_ID/tickets" \
  | grep -i content-type)
echo "Frontend Content-Type: $CT"
echo $CT | grep "text/html" && echo "✅ Frontend returns HTML" || echo "❌ Frontend routing broken"
```

### Step 8 — Clean up
```bash
kill $SERVER_PID
docker compose -f build/docker-compose.dev.yml down
```

### PR Rules

- If **ALL steps pass**: open a normal ready-to-merge PR
- If **ANY step fails**: fix the issue before opening the PR
- If a step **cannot run** (e.g. Docker not available in environment): open a
  **DRAFT** PR, mark the step unchecked, add comment "Requires human verification"
- **NEVER** open a ready-to-merge PR with unchecked Definition of Done items

---

## Local Test Database

A real Postgres + MinIO test environment is available for integration testing.

```bash
# Start test database (postgres on :5433, minio on :9001)
make test-db-up

# Run all tests including integration tests
make test-live

# Generate coverage report
make test-live-coverage

# Reset to clean state
make test-db-reset

# Stop when done
make test-db-down
```

All integration tests use `testutil.NewTestDB(t)` from `internal/testutil/db.go`.
Tests automatically skip if `DATABASE_URL` is not set.
Each test gets its own isolated schema — tests never interfere with each other.

Before opening any PR that touches write operations:

1. Run `make test-db-up`
2. Run `make test-live`
3. Confirm all tests pass
4. Run `make test-db-down`

---

## Repository Layout

```
.github/workflows/    → GitHub Actions CI/CD pipelines
cmd/server/           → single binary entrypoint (main.go)
internal/core/        → all application code — ships to all users
  auth/               → local users, JWT, sessions, middleware
  sso/                → SAML/OIDC single sign-on
  audit/              → append-only audit log
  rbac/               → role-based access control
  analytics/          → usage and performance reporting
  tickets/            → service desk domain logic
  wiki/               → wiki/docs domain logic
  projects/           → project tracking domain logic
  notifications/      → email + in-app alerts
  storage/            → ObjectStore interface
  api/                → HTTP handlers, chi router, middleware
internal/db/          → goose migrations, sqlc-generated queries
internal/config/      → viper config (env + file)
internal/jobs/        → river background workers
web/                  → frontend (compiled, embedded into binary)
migrations/           → goose SQL files (never edit existing ones)
build/                → Dockerfile, docker-compose.yml
```

---

## Key Interfaces

```go
// ObjectStore — never use local disk
// internal/core/storage/store.go
type ObjectStore interface {
    Put(ctx context.Context, key string, r io.Reader) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
}

// SSOProvider — SAML/OIDC
// internal/core/sso/provider.go
type Provider interface {
    BeginAuth(w http.ResponseWriter, r *http.Request) error
    CompleteAuth(r *http.Request) (*User, error)
    IsAvailable() bool
}

// AuditLogger — append-only event log
// internal/core/audit/logger.go
type Logger interface {
    Log(ctx context.Context, event Event) error
    IsAvailable() bool
}

// RBAC — role-based access control
// internal/core/rbac/checker.go
type Checker interface {
    CanPerform(ctx context.Context, userID, orgID, resourceType string, action Action) (bool, error)
    UserRole(ctx context.Context, userID, orgID string) (Role, error)
    IsAvailable() bool
}
```

---

## Container Images

```
ghcr.io/azimuthal-hq/azimuthal:v1.2.3
ghcr.io/azimuthal-hq/azimuthal:latest
```

No separate registry credentials needed — `GITHUB_TOKEN` handles ghcr.io auth.

---

## GitHub Secrets Required

Settings → Secrets and variables → Actions:

```
JWT_SECRET            → random 64-char string for test runs
                        generate: openssl rand -hex 32
```

---

## Multi-Agent Worktree Rules

1. Stay in your assigned module — don't touch other modules
2. Open one PR per agent — small, focused, reviewable
3. Don't merge your own PR — wait for pipeline + human review
4. If you need something from another module that doesn't exist yet,
   define the interface you need and stub it — don't block yourself
5. Prefix your branch: `agent/1a-data-layer`, `agent/1b-auth`, etc.

---

## Agent Assignment Map

```
Phase 0 (sequential — must complete first):
  Agent 0A → Repo scaffold, CI pipeline, Makefile, Dockerfile
  Agent 0B → Security scan layer (gosec, govulncheck, gitleaks, trivy)

Phase 1 (parallel — all start after Phase 0):
  Agent 1A → internal/db/ + all migrations + sqlc queries
  Agent 1B → internal/core/auth/ + internal/core/sso/
  Agent 1C → internal/config/ + internal/jobs/ + internal/core/storage/
             + internal/core/audit/ + internal/core/rbac/ + internal/core/analytics/

Phase 2 (parallel — start after Phase 1):
  Agent 2A → internal/core/tickets/
  Agent 2B → internal/core/wiki/
  Agent 2C → internal/core/projects/
  Agent 2D → internal/core/api/ (sequential — after 2A/2B/2C)

Phase 3 (sequential — after Phase 2):
  Agent 3A → web/ frontend shell + embed
  Agent 3B → single binary validation + docker-compose + backup CLI
```

---

## Environment Variables

```bash
# Required
DATABASE_URL=postgres://user:pass@localhost:5432/azimuthal_dev?sslmode=disable

# Storage (MinIO locally, S3/R2 in production)
STORAGE_ENDPOINT=http://localhost:9000
STORAGE_ACCESS_KEY=minioadmin
STORAGE_SECRET_KEY=minioadmin
STORAGE_BUCKET=azimuthal

# Auth
JWT_SECRET=<random 64-char string>
JWT_EXPIRY=24h

# Email
SMTP_HOST=localhost
SMTP_PORT=1025

# App
APP_ENV=development
APP_PORT=8080
APP_BASE_URL=http://localhost:8080
LOG_LEVEL=debug
```

---

## Getting Started (for new agents)

```bash
# 1. Install Go tools
go install github.com/pressly/goose/v3/cmd/goose@latest
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
go install github.com/air-verse/air@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest
go install github.com/google/go-licenses@latest

# 2. Start local services
docker compose -f build/docker-compose.dev.yml up -d

# 3. Run migrations
make migrate

# 4. Run tests
make test

# 5. Start dev server with live reload
make dev
```
