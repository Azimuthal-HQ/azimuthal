# Azimuthal

A fully open-source, self-hostable alternative to the Atlassian suite (Jira, Confluence, Jira Service Desk), built in Go.

**License**: Apache 2.0 — all features ship to every user, no enterprise tier.

## Features

- **Service Desk** — ticket lifecycle, email ingestion, kanban boards
- **Wiki** — page tree, markdown rendering, version history, conflict detection
- **Project Tracking** — backlog, sprints, roadmap, cross-tool linking
- **SSO** — SAML/OIDC single sign-on
- **RBAC** — role-based access control
- **Audit Log** — append-only event logging

## Quick Start

### Prerequisites

- Go 1.23+
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
export DATABASE_URL="postgres://azimuthal:azimuthal@localhost:5432/azimuthal_dev?sslmode=disable"
export JWT_SECRET="$(openssl rand -hex 32)"

# 4. Run migrations
make migrate

# 5. Build and run
make build
./azimuthal
```

The server starts on http://localhost:8080 by default.

### Run with Docker

```bash
docker compose -f build/docker-compose.yml up -d
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
cmd/server/        — single binary entrypoint
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
migrations/        — goose SQL migration files
build/             — Dockerfile and docker-compose files
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
