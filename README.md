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

# 3. Set the one required env var
#    (JWT signing needs no secret — the RS256 key lives in the database; see ADR-0004)
export DATABASE_URL="postgres://azimuthal:dev@localhost:5432/azimuthal_dev?sslmode=disable"

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
azimuthal assess                         Assess a Jira/Confluence export for migration (read-only)
azimuthal admin create-user              Create a new user
azimuthal admin reset-password           Reset a user's password
azimuthal admin verify-split             Verify items_archive counts against tickets + project_items
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

Every setting below is read once, at startup, by `internal/config`. `DATABASE_URL` is the only
one without a default, and the only one whose absence stops the server.

**There is no `JWT_SECRET`.** JWT signing uses an RS256 key pair persisted in the database
(migration 018, [ADR-0004](docs/adr/0004-signing-keys-in-database.md)). `JWT_PRIVATE_KEY_PATH` is a
one-time import path for deployments upgrading from the older file-based key, not a secret to
generate.

### Core

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | — (**required**) | PostgreSQL connection string. Startup fails without it. |
| `APP_ENV` | `development` | `development`, `test`, or `production`. Selects environment-dependent behaviour; it is **not** a security exemption — see the note below. |
| `APP_PORT` | `8080` | HTTP listen port. |
| `APP_BASE_URL` | `http://localhost:8080` | Public URL of this instance. Used to build the links that go out in invites and portal sign-in emails, so a wrong value produces links nobody can follow. |
| `LOG_LEVEL` | `info` | **Currently inert — see below.** Intended to select `debug`, `info`, `warn`, or `error`. |

### Object storage (MinIO / S3-compatible)

| Variable | Default | Description |
|---|---|---|
| `STORAGE_ENDPOINT` | — (empty) | S3/MinIO endpoint. Empty disables attachment storage. |
| `STORAGE_ACCESS_KEY` | — (empty) | S3/MinIO access key. |
| `STORAGE_SECRET_KEY` | — (empty) | S3/MinIO secret key. |
| `STORAGE_BUCKET` | `azimuthal` | Bucket name for attachments. |
| `STORAGE_USE_SSL` | `false` | Reach the endpoint over TLS. An `https://` prefix on `STORAGE_ENDPOINT` forces this to `true` regardless of what you set here; `http://` leaves your setting alone. |

### Authentication and access

| Variable | Default | Description |
|---|---|---|
| `AZIMUTHAL_BCRYPT_COST` | `12` | Password hashing work factor. Twelve is a **floor**, not just a default: a lower value is refused at startup in every environment, `APP_ENV=test` included. The knob is up-only, so an operator can raise it as hardware gets faster. |
| `AZIMUTHAL_ALLOWED_ORIGINS` | — (empty) | Comma-separated CORS allow-list. Empty in **every** environment means no CORS headers are emitted and the browser enforces same-origin; `*` matches any origin. There is no permissive development default — nothing in this repository needs one, because the SPA is served from this same binary in production and Vite proxies `/api` server-side in development. |
| `AZIMUTHAL_ALLOW_REGISTRATION` | `false` | Opens `POST /auth/register`. Off by default: the endpoint 404s and invites are the only way in. |
| `JWT_EXPIRY` | `24h` | Access-token lifetime (Go duration). |
| `JWT_PRIVATE_KEY_PATH` | `./data/jwt-private.pem` | One-time import path for a legacy file-based RS256 key. Not required, and not where the signing key lives — see above. |

### Invitations and the customer portal

| Variable | Default | Description |
|---|---|---|
| `AZIMUTHAL_INVITE_DELIVERY` | `link` | `link` (an admin copies the one-time URL) or `email` (Azimuthal sends it). `email` requires `SMTP_HOST` and `SMTP_FROM` to be set explicitly; startup fails loudly otherwise rather than dropping invites at send time. An unrecognised value is refused at startup. |
| `AZIMUTHAL_INVITE_TTL` | `168h` | Invite expiry window (Go duration). Must be positive. |
| `AZIMUTHAL_PORTAL_LINK_DELIVERY` | `link` | How a customer-portal sign-in link reaches a requester. `email` sends it; `link` returns it in the API response, which is a **development and test convenience only** — the request-link endpoint is necessarily unauthenticated, so disclosing the URL to its caller would let anybody sign in as any address they can name. Production never discloses the link regardless of this setting. |
| `AZIMUTHAL_PORTAL_LINK_TTL` | `1h` | How long a portal sign-in link stays redeemable. Short by design: it is a credential sitting in an inbox. Must be positive. |
| `AZIMUTHAL_PORTAL_SESSION_TTL` | `72h` | Lifetime of the session a redeemed link produces. Must be positive. |

### Email

| Variable | Default | Description |
|---|---|---|
| `SMTP_HOST` | `localhost` | SMTP relay host. The default exists for a local dev relay, so "configured" for email delivery means an operator set it *explicitly*. |
| `SMTP_PORT` | `1025` | SMTP relay port. (The Docker Compose deployment defaults this to `25` instead — see `docs/self-hosting.md`.) |
| `SMTP_FROM` | `azimuthal@localhost` | Envelope sender for outbound mail. |

### Operations

| Variable | Default | Description |
|---|---|---|
| `AZIMUTHAL_TICKET_REF_REQUIRED` | `false` | Require an operator ticket reference on every administrative mutation that accepts one. Turning it on is a production cutover, not a preference — see `docs/self-hosting.md`. |
| `AZIMUTHAL_QUEUE_ENABLED` | `true` | Runs the background job queue in-process. |
| `MIGRATIONS_DIR` | (embedded) | Read by the `migrate` command only (`cmd/migrate`), to point goose at migrations on disk instead of the embedded copy. |

> **All of these are boot-time policy, and that is the design.** Nothing in this table can be
> changed from a settings page at runtime. For the security-bearing ones — the bcrypt floor, the
> CORS allow-list, registration, ticket-reference enforcement, portal link delivery — that is the
> point: a policy an administrator can flip through the web UI is a policy an attacker who reaches
> the web UI can flip. Changing any of them costs a restart, deliberately.
>
> `APP_ENV` in particular grants no exemptions. It is an ordinary environment variable that a
> production deployment can hold any value of, so the bcrypt floor is enforced even under
> `APP_ENV=test`; test binaries get cheap hashing from the linker knowing they are test binaries,
> not from configuration.

> **`LOG_LEVEL` does nothing today.** `config.Load` parses it into `Config.LogLevel`, and nothing
> reads that field: `cmd/server/serve.go` builds the logger with a hardcoded `slog.LevelInfo`, and
> it does so *before* configuration is loaded, so the value could not reach it as written. Setting
> `LOG_LEVEL=debug` changes nothing. Documented here rather than quietly dropped from the table,
> because the variable is real and the gap is in the wiring — closing it is a code change and needs
> its own review.

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
