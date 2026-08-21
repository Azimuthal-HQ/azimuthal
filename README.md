# Azimuthal

**Early, actively developed software**, self-hosted and built in Go, working toward the ground the
Atlassian suite covers — service desk, wiki, and project tracking. It is not a replacement for
Jira, Confluence or Jira Service Desk today. [What works today](#what-works-today) and
[Not yet shipped](#not-yet-shipped) below are the honest account of how far it has got.

**License**: Apache 2.0 — Azimuthal is fully open source. Every feature is available to every
user: there is no paid tier, no enterprise edition, and there never will be.

## Features

- **Service Desk** — ticket lifecycle, queues, kanban boards
- **Wiki** — page tree, rich-text editing, version history, diff and restore
- **Project Tracking** — backlog, sprints, roadmap, typed item relations
- **Unified Frontend** — React + TypeScript SPA embedded in the Go binary, dark mode by default
- **RBAC** — role-based access control
- **Audit Log** — append-only event logging

### Not yet shipped

Listed here because earlier versions of this file listed them as features. Neither is reachable
today, and this section is the honest place for them until they are.

- **SSO (SAML/OIDC)** — `internal/core/sso` is an interface plus a no-op returning
  `ErrNotConfigured`. It is not wired into the router.
- **Email ingestion** — the RFC 2822 parser and `CreateFromEmail` exist and are tested, but there
  is no IMAP client, POP client, inbound webhook or mail-drop poller, so nothing reaches them.
  Outbound mail (invites, portal sign-in links) does work.

## What works today

- **Single binary** — `make build` produces one binary with the frontend embedded. Run `./azimuthal serve` and visit http://localhost:8080
- **Docker Compose self-hosting** — `docker compose -f build/docker-compose.yml up -d` runs the full stack (app + PostgreSQL + MinIO)
- **Backup and restore** — `azimuthal backup --output backup.tar.gz` creates a full archive; `azimuthal restore --input backup.tar.gz` restores it. Both run inside the container (`docker compose exec app /azimuthal backup ...`): the image ships the PostgreSQL 16 client tools they shell out to. A restore that fails part-way exits non-zero rather than reporting success over a partial recovery. See [docs/self-hosting.md](docs/self-hosting.md)
- **Admin CLI** — `azimuthal admin create-user` and `azimuthal admin reset-password` for user management
- **Dark mode by default** — steel blue and silver design system with light mode opt-in via settings
- **Service Desk** — ticket list, ticket detail, kanban board with drag-and-drop
- **Wiki** — page tree with collapsible navigation, markdown rendering
- **Project Tracking** — backlog view, sprint board with drag-and-drop
- **Unified navigation** — top nav with space switcher, context-sensitive sidebar, consistent design across all modules
- **REST API** — CRUD endpoints for tickets, wiki pages, project items, sprints, labels, and spaces (labels have no update endpoint and sprints no delete endpoint)

## Self-Hosting

The fastest way to run Azimuthal is with Docker Compose:

```bash
# 1. Download compose file and environment template, pinned to a release
curl -O https://raw.githubusercontent.com/Azimuthal-HQ/azimuthal/v0.4.1/build/docker-compose.yml
curl -O https://raw.githubusercontent.com/Azimuthal-HQ/azimuthal/v0.4.1/.env.example
cp .env.example .env

# 2. Edit .env — set POSTGRES_PASSWORD and the two MINIO_ROOT_* values
#    (there is no JWT secret to generate: the RS256 signing key is created
#     and stored in the database on first start — see ADR-0004)

# 3. Start everything
docker compose up -d

# 4. Create your first user
docker compose exec app /azimuthal admin create-user \
  --email admin@example.com \
  --name "Admin" \
  --password your-secure-password
```

Those two URLs are pinned to a tag on purpose: fetching them from `main` pairs tomorrow's
infrastructure with the image you actually run. **Each release's notes carry the same commands
pinned to that release** — take them from the [release you are installing](https://github.com/Azimuthal-HQ/azimuthal/releases)
rather than from here, and they cannot drift out of step with the image.

See [docs/self-hosting.md](docs/self-hosting.md) for the full guide including environment variable reference, backup/restore instructions, and troubleshooting.

## Quick Start (from source)

### Prerequisites

- Go 1.26+ (`go.mod`'s `go` directive requires 1.26.0 and its `toolchain go1.26.6` directive selects the build version; CI and the release image build with 1.26.6)
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
azimuthal bundle-hash                    Print the SHA-256 of the embedded frontend (--verify to compare)
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
| `APP_ENV` | `production` | The deployment's environment name: `development`, `test`, or `production`. Only `production` is special: it withholds the customer-portal sign-in URL from API responses. Set it to `development` or `test` only on a machine that is not serving real users. It is **not** a security exemption — see the note below. |
| `APP_PORT` | `8080` | HTTP listen port. |
| `APP_BASE_URL` | `http://localhost:8080` | Public URL of this instance. Used to build the links that go out in invites and portal sign-in emails, so a wrong value produces links nobody can follow. |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` (any case). An unrecognised value is refused at startup. |

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
| `AZIMUTHAL_PORTAL_LINK_DELIVERY` | `link` | How a customer-portal sign-in link reaches a requester. `email` sends it, and requires `SMTP_HOST` and `SMTP_FROM` to be set explicitly; `link` means the operator is responsible for getting the URL to the requester. **This setting no longer decides whether the URL appears in the API response** — `AZIMUTHAL_PORTAL_DISCLOSE_LINK` does. It used to, and that was the defect: disclosure was `link` **and** non-production, and since both were the defaults, a stock install disclosed. An unrecognised value is refused at startup. |
| `AZIMUTHAL_PORTAL_DISCLOSE_LINK` | `false` | Return the customer-portal sign-in URL in the body of the unauthenticated request-link response. Disclosure requires this flag **and** a non-`production` `APP_ENV`; setting it on a production server is harmless but does nothing, and the server says so with a startup warning naming both variables. Leave it off: the request-link endpoint is unauthenticated by design, so a disclosed URL lets anyone sign in as any address they can name. It exists so a browser test and a developer without a mailbox can follow a link. |
| `AZIMUTHAL_PORTAL_LINK_TTL` | `1h` | How long a portal sign-in link stays redeemable. Short by design: it is a credential sitting in an inbox. Must be positive. |
| `AZIMUTHAL_PORTAL_SESSION_TTL` | `72h` | Lifetime of the session a redeemed link produces. Must be positive. |

### Email

| Variable | Default | Description |
|---|---|---|
| `SMTP_HOST` | `localhost` | SMTP relay host. The default exists for a local dev relay, so "configured" for email delivery means an operator set it *explicitly*. |
| `SMTP_PORT` | `1025` | SMTP relay port. |
| `SMTP_FROM` | `azimuthal@localhost` | Envelope sender for outbound mail. |

### Operations

| Variable | Default | Description |
|---|---|---|
| `AZIMUTHAL_TICKET_REF_REQUIRED` | `false` | Require an operator ticket reference on every administrative mutation that accepts one. Turning it on is a production cutover, not a preference — see `docs/self-hosting.md`. |
| `AZIMUTHAL_QUEUE_ENABLED` | `true` | Runs the background job queue in-process. |
| `MIGRATIONS_DIR` | (embedded) | Read by the `migrate` command only (`cmd/migrate`), to point goose at migrations on disk instead of the embedded copy. |

> **All of these are boot-time policy, and that is the design.** Nothing in this table can be
> changed from a settings page at runtime. For the security-bearing ones — the bcrypt floor, the
> CORS allow-list, registration, ticket-reference enforcement, portal link disclosure — that is the
> point: a policy an administrator can flip through the web UI is a policy an attacker who reaches
> the web UI can flip. Changing any of them costs a restart, deliberately.
>
> `APP_ENV` in particular grants no exemptions. It is an ordinary environment variable that a
> production deployment can hold any value of, so the bcrypt floor is enforced even under
> `APP_ENV=test`; test binaries get cheap hashing from the linker knowing they are test binaries,
> not from configuration.

> **`LOG_LEVEL` is live**, and an unrecognised value is refused at startup rather than quietly run
> at `info`. It accepts `debug`, `info`, `warn` and `error` in any case, plus `slog` offsets such as
> `info+2`. The logger is necessarily built before configuration is loaded — loading the config can
> itself fail, and that failure has to be logged — so it starts at `info` and is re-levelled in
> place once `LOG_LEVEL` is known. One startup line is therefore always emitted at `info`;
> everything from `configuration loaded` onwards obeys the setting.

## Project Structure

```
cmd/server/        — single binary entrypoint (serves API + embedded frontend, plus the CLI above)
cmd/migrate/       — standalone migration runner (honours MIGRATIONS_DIR)
internal/core/     — all application logic
  api/             — HTTP handlers and router (chi)
  access/          — the capability model and permission resolution (ADR-0007)
  auth/            — authentication, JWT, sessions
  audit/           — append-only audit log
  rbac/            — role-based access control
  teams/           — teams and membership
  spaces/          — spaces, the scope unit (ADR-0006)
  tickets/         — Beacon, the service desk module
  wiki/            — Codex, the wiki module
  projects/        — Vector, the project tracking module
  workflow/        — workflow states, transition guards, approvals (ADR-0011)
  views/           — saved views and Beacon queues (ADR-0009)
  dashboards/      — dashboards and the gadget registry (ADR-0009)
  search/          — cross-module full-text search
  portal/          — the customer portal (external requesters)
  attachments/     — file attachments
  customfields/    — custom field definitions and values
  itemtypes/       — Vector item types
  tags/            — Codex tags
  invites/         — org invitations
  people/          — the org directory
  email/           — outbound SMTP
  storage/         — object storage interface
  sso/             — placeholder interface; no SAML/OIDC implementation (see "Not yet shipped")
internal/db/       — sqlc queries, generated code and adapters
internal/assess/   — read-only Jira/Confluence export assessor (the `assess` command)
internal/bundle/   — frontend bundle hashing (the `bundle-hash` command)
internal/config/   — configuration loading
internal/jobs/     — background workers (River)
internal/testutil/ — the test-database harness
web/               — React + TypeScript frontend (Vite, Tailwind, shadcn/ui)
migrations/        — goose SQL migration files
build/             — Dockerfile and docker-compose files
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
