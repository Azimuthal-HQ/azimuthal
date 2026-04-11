# Azimuthal

A fully open-source, self-hostable alternative to the Atlassian suite (Jira, Confluence, Jira Service Desk), built in Go.

**License**: Apache 2.0 — Azimuthal is fully open source. All features are available to all users.

## Features

- **Service Desk** — ticket lifecycle, email ingestion, kanban boards
- **Wiki** — page tree, markdown rendering, version history, conflict detection
- **Project Tracking** — backlog, sprints, roadmap, cross-tool linking
- **Unified Frontend** — React + TypeScript SPA embedded in the Go binary, dark mode by default
- **SSO** — SAML/OIDC single sign-on
- **RBAC** — role-based access control
- **Audit Log** — append-only event logging

## What works today

- **Single binary** — `make build` produces one binary with the frontend embedded. Run `./azimuthal serve` and visit http://localhost:8080
- **Docker Compose self-hosting** — `docker compose -f build/docker-compose.yml up -d` runs the full stack (app + PostgreSQL + MinIO)
- **Backup and restore** — `azimuthal backup --output backup.tar.gz` creates a full archive; `azimuthal restore --input backup.tar.gz` restores it
- **Admin CLI** — `azimuthal admin create-user` and `azimuthal admin reset-password` for user management
- **Dark mode by default** — steel blue and silver design system with light mode opt-in via settings
- **Service Desk** — ticket list, ticket detail, kanban board with drag-and-drop
- **Wiki** — page tree with collapsible navigation, markdown rendering
- **Project Tracking** — backlog view, sprint board with drag-and-drop
- **Unified navigation** — top nav with space switcher, context-sensitive sidebar, consistent design across all modules
- **REST API** — full CRUD for tickets, wiki pages, projects, sprints, labels, and spaces

## Self-Hosting

The fastest way to run Azimuthal is with Docker Compose:

```bash
# 1. Download compose file and environment template
curl -O https://raw.githubusercontent.com/Azimuthal-HQ/azimuthal/main/build/docker-compose.yml
curl -O https://raw.githubusercontent.com/Azimuthal-HQ/azimuthal/main/.env.example
cp .env.example .env

# 2. Edit .env — set passwords and generate a JWT secret
#    openssl rand -hex 32

# 3. Start everything
docker compose up -d

# 4. Create your first user
docker compose exec app /azimuthal admin create-user \
  --email admin@example.com \
  --name "Admin" \
  --password your-secure-password
```

See [docs/self-hosting.md](docs/self-hosting.md) for the full guide including environment variable reference, backup/restore instructions, and troubleshooting.

## Quick Start (from source)

### Prerequisites

- Go 1.23+
- Node.js 20+ (for building the frontend)
- PostgreSQL 15+
- MinIO or S3-compatible storage (for file attachments)

### Run locally

```bash
# 1. Clone and install tools
git clone https://github.com/Azimuthal-HQ/azimuthal.git
cd azimuthal
go install github.com/pressly/goose/v3/cmd/goose@latest

# 2. Start local services (postgres + minio)
docker compose -f build/docker-compose.dev.yml up -d

# 3. Set required env vars
export DATABASE_URL="postgres://azimuthal:dev@localhost:5432/azimuthal_dev?sslmode=disable"
export JWT_SECRET="$(openssl rand -hex 32)"

# 4. Run migrations
make migrate

# 5. Build and run
make build
./bin/azimuthal serve
```

The server starts on http://localhost:8080 by default.

## CLI Commands

```
azimuthal serve                          Start the HTTP server
azimuthal backup --output file.tar.gz    Create a full backup
azimuthal restore --input file.tar.gz    Restore from backup
azimuthal admin create-user              Create a new user
azimuthal admin reset-password           Reset a user's password
azimuthal --version                      Show version
azimuthal --help                         Show all commands
```

## Running Tests

```bash
make test
```

Tests run with the race detector and require `CGO_ENABLED=1` (a C compiler must be available). On systems without GCC, run without the race detector:

```bash
go test ./...
```

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | Yes | — | PostgreSQL connection string |
| `JWT_SECRET` | Yes | — | Random 64-char string for JWT signing |
| `APP_PORT` | No | `8080` | HTTP listen port |
| `APP_ENV` | No | `development` | `development`, `test`, or `production` |
| `STORAGE_ENDPOINT` | No | — | S3/MinIO endpoint |
| `STORAGE_ACCESS_KEY` | No | — | S3/MinIO access key |
| `STORAGE_SECRET_KEY` | No | — | S3/MinIO secret key |
| `STORAGE_BUCKET` | No | `azimuthal` | Object storage bucket name |
| `SMTP_HOST` | No | `localhost` | SMTP relay host |
| `SMTP_PORT` | No | `1025` | SMTP relay port |
| `LOG_LEVEL` | No | `info` | Log level (`debug`, `info`, `warn`, `error`) |

## Project Structure

```
cmd/server/        — single binary entrypoint (serves API + embedded frontend)
internal/core/     — all application logic
  api/             — HTTP handlers and router (chi)
  auth/            — authentication, JWT, sessions
  sso/             — SAML/OIDC single sign-on
  audit/           — append-only audit log
  rbac/            — role-based access control
  tickets/         — service desk module
  wiki/            — wiki/docs module
  projects/        — project tracking module
  storage/         — object storage interface
internal/db/       — database migrations and sqlc queries
internal/config/   — configuration loading
internal/jobs/     — background workers
web/               — React + TypeScript frontend (Vite, Tailwind, shadcn/ui)
migrations/        — goose SQL migration files
build/             — Dockerfile and docker-compose files
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
