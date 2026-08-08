# Self-Hosting Azimuthal

Run Azimuthal on your own infrastructure with Docker Compose.

## Prerequisites

| Requirement | Minimum |
|---|---|
| Docker Engine | 24+ |
| Docker Compose | v2.20+ |
| CPU | 2 cores |
| RAM | 4 GB |
| Disk | 20 GB |

## Quick Start

```bash
# 1. Download the compose file and env template, pinned to a release
curl -O https://raw.githubusercontent.com/Azimuthal-HQ/azimuthal/v0.4.1/build/docker-compose.yml
curl -O https://raw.githubusercontent.com/Azimuthal-HQ/azimuthal/v0.4.1/.env.example

# 2. Create your .env file
cp .env.example .env

# 3. Edit .env and set your passwords. There is no JWT secret to configure —
#    Azimuthal generates and persists its own RS256 signing key in the
#    database on first start, so tokens survive restarts.
#    Set POSTGRES_PASSWORD and MINIO_ROOT_PASSWORD.

# 4. Start Azimuthal
docker compose -f docker-compose.yml up -d

# 5. Create your first admin user
docker compose exec app /azimuthal admin create-user \
  --email admin@example.com \
  --name "Admin" \
  --password your-secure-password
```

Azimuthal is now running at http://localhost:8080.

> **Why those two URLs name a tag rather than `main`.** The image you run is versioned; fetching
> its compose file and env template from `main` pairs it with infrastructure from some later day.
> **Every release's notes carry these same commands pinned to that release** — copy them from the
> [release you are installing](https://github.com/Azimuthal-HQ/azimuthal/releases) instead of from
> here, and the three can never drift apart.

## Running from a source checkout (before a release)

The Quick Start above pulls the published `ghcr.io/azimuthal-hq/azimuthal`
image, which is only available once a version has been tagged and released.
To run from a source checkout (e.g. a feature branch) that has no release
image yet, build the image locally with the build overlay:

```bash
git clone https://github.com/Azimuthal-HQ/azimuthal
cd azimuthal
cp .env.example .env          # then edit POSTGRES_PASSWORD and MINIO_ROOT_PASSWORD

docker compose -f build/docker-compose.yml -f build/docker-compose.build.yml \
  --env-file .env up -d --build

docker compose -f build/docker-compose.yml -f build/docker-compose.build.yml \
  --env-file .env exec app /azimuthal admin create-user \
    --email admin@example.com --name "Admin" --password your-secure-password
```

Everything else (migrations on first boot, admin CLI, the app at
http://localhost:8080) behaves identically to the published-image path.

## First-Run: Create an Admin User

After starting Azimuthal for the first time, you must create an admin user before
you can log in through the web UI:

```bash
docker compose exec app /azimuthal admin create-user \
  --email admin@example.com \
  --name "Admin" \
  --password changeme
```

Replace the email and password with your own values. This user will have full admin
access. You can then log in at `http://localhost:8080/login` with these credentials.

> **Important:** Change the default password immediately after your first login.

## Environment Variable Reference

### Required

| Variable | Description |
|---|---|
| `POSTGRES_PASSWORD` | Password for the PostgreSQL database user |
| `MINIO_ROOT_USER` | MinIO root access key |
| `MINIO_ROOT_PASSWORD` | MinIO root secret key |

> **There is no signing secret to configure.** JWT signing uses an RS256 key pair that Azimuthal
> generates once and persists in the database (migration 018,
> [ADR-0004](adr/0004-signing-keys-in-database.md)). Nothing reads a `JWT_SECRET` environment
> variable; setting one has no effect.

### Optional

| Variable | Default | Description |
|---|---|---|
| `APP_PORT` | `8080` | Host port to expose the application on |
| `APP_BASE_URL` | `http://localhost:8080` | Public URL of the application (used in emails and links) |
| `AZIMUTHAL_VERSION` | `latest` | Docker image tag to run. `:latest` is only reassigned once the GitHub Release for that version exists, and never backwards onto an older version — so it always names a released build, and a backport release cannot walk you down a version. Pin an explicit tag if you want upgrades to be a decision rather than a `docker compose pull`. |
| `STORAGE_BUCKET` | `azimuthal` | MinIO/S3 bucket name for file storage |
| `JWT_EXPIRY` | `24h` | Access token lifetime (Go duration format) |
| `SMTP_HOST` | `localhost` | SMTP relay host for outbound email. Leave it unset unless you have a relay: the server distinguishes "explicitly configured" from "defaulted" to decide whether an `email` delivery mode may start. |
| `SMTP_PORT` | `1025` | SMTP relay port. |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` (any case). An unrecognised value is refused at startup. The logger starts at `info` and is re-levelled once config loads, so the first startup line is always emitted. |
| `DATABASE_URL` | (auto) | PostgreSQL connection string. Auto-constructed in Docker Compose from `POSTGRES_PASSWORD` |
| `JWT_PRIVATE_KEY_PATH` | `./data/jwt-private.pem` | One-time import path for a legacy file-based RS256 key. Not required, and **not** where the signing key lives — see the note above. Forwarded by `build/docker-compose.yml`. |

`APP_ENV` is **not** settable through `.env` in this deployment: `build/docker-compose.yml`
hardcodes `APP_ENV: production`.

### Administration, invitations, the portal, and email

All of these are forwarded by `build/docker-compose.yml` — set any of them in `.env` and restart.

> **A note on how the forwarding works, because this file used to get it wrong.**
> `build/docker-compose.yml` declares no `env_file:`, so its `environment:` block is the **only**
> channel into the container: a variable in `.env` is available for `${...}` interpolation inside
> the Compose file, and reaches the application only because the block names it explicitly. Every
> setting the binary reads is now named there. Until this was fixed, none of the ten `AZIMUTHAL_*`
> settings was — so an operator who set `AZIMUTHAL_TICKET_REF_REQUIRED=true` or
> `AZIMUTHAL_BCRYPT_COST=14`, restarted, and saw a clean startup would reasonably have concluded
> the policy was in force when the container had never seen the variable.
>
> The block forwards operator settings as bare `${KEY}` rather than `${KEY:-default}`, so an unset
> variable arrives empty and the binary applies its own default. Two Go tests in
> `internal/config` read this document's subject matter directly — the real Compose file against
> the real config source — and fail if a setting is added to one and not the other, or if a
> default creeps back into the Compose file where it could drift.

| Variable | Default | Description |
|---|---|---|
| `AZIMUTHAL_ALLOW_REGISTRATION` | `false` | Open self-registration at `/auth/register`. Off by default since v0.3.2 — admins invite people from the Administration area instead. Instances relying on open registration must set this to `true` explicitly. |
| `AZIMUTHAL_INVITE_DELIVERY` | `link` | How invite links reach people: `link` (the admin copies the one-time URL) or `email` (Azimuthal sends it — requires `SMTP_HOST` **and** `SMTP_FROM` to be set explicitly; startup fails otherwise). An unrecognised value is refused at startup. |
| `AZIMUTHAL_INVITE_TTL` | `168h` | Invite expiry window (Go duration format). Must be positive. |
| `AZIMUTHAL_TICKET_REF_REQUIRED` | `false` | Require an operator ticket reference on every administrative change — creating a team, granting space access, sharing an entity, deactivating a person, and so on. Off by default; behaviour is unchanged until you opt in. Turning it on is a production cutover: every administrative mutation without a reference is refused with a 400 and writes nothing, and the admin UI marks its reference fields required and waits for one before it will submit. Boot-time only, deliberately — an organisation turns this on once, and a restart is the honest cost of changing what every admin action requires. |
| `AZIMUTHAL_BCRYPT_COST` | `12` | Password hashing work factor. Twelve is a floor, not just a default: a configuration asking for less is refused at startup in every environment, `APP_ENV` included. The knob exists so you can raise it as hardware gets faster — expect roughly a doubling of login CPU cost per step. Existing passwords keep verifying at the cost they were stored with, so raising it is safe and takes effect as people next change their password. |
| `AZIMUTHAL_ALLOWED_ORIGINS` | (empty) | Comma-separated CORS allow-list. Empty means no CORS headers are emitted and the browser enforces same-origin, which is correct for this deployment — the frontend is served by the same binary on the same origin. Set it only if you serve the frontend from somewhere else. |
| `AZIMUTHAL_QUEUE_ENABLED` | `true` | Runs the background job queue in-process. |
| `AZIMUTHAL_PORTAL_LINK_DELIVERY` | `link` | How a customer-portal sign-in link reaches a requester. Set this to `email` for any instance with the portal exposed to real customers, and set `SMTP_HOST` and `SMTP_FROM` with it — `email` without a relay is refused at startup. `link` means the operator is responsible for getting the URL to the requester, and on this deployment there is no way to do that, so the practical effect of leaving it at `link` in production is that portal sign-in links go nowhere. An unrecognised value is refused at startup. **This setting no longer decides disclosure** — see the row below. |
| `AZIMUTHAL_PORTAL_DISCLOSE_LINK` | `false` | Return the portal sign-in URL in the body of the unauthenticated request-link response. Inert on this deployment for two independent reasons: `build/docker-compose.yml` sets `APP_ENV: production`, and disclosure requires this flag **and** a development `APP_ENV` — a **safelist** of `development` and `test` only, so `production`, `staging`, or any unrecognised name refuses it. Setting it here is harmless and does nothing; the server logs a startup warning naming both variables rather than failing, so a working configuration is never locked out over a combination that is already safe. Leave it off in any case: `POST /portal/{key}/auth/request-link` is unauthenticated by design and accepts any address, so a disclosed URL signs the caller in as anyone they can name. |
| `AZIMUTHAL_PORTAL_LINK_TTL` | `1h` | How long a portal sign-in link stays redeemable. Must be positive. |
| `AZIMUTHAL_PORTAL_SESSION_TTL` | `72h` | Lifetime of the session a redeemed portal link produces. Must be positive. |
| `SMTP_FROM` | `azimuthal@localhost` | Envelope sender for outbound mail. Required when `AZIMUTHAL_INVITE_DELIVERY=email`. |
| `STORAGE_USE_SSL` | `false` | Reach the object-storage endpoint over TLS. An `https://` prefix on `STORAGE_ENDPOINT` forces this to `true` regardless of what you set. The bundled Compose file sets `http://storage:9000`, so MinIO is reached over the internal network on plain HTTP. |

## Running Migrations Manually

Migrations run automatically on startup. To run them manually:

```bash
# Inside the container
docker compose exec app /azimuthal serve
# Migrations execute on startup before the HTTP server begins listening.

# Or with goose directly (requires goose installed, AND a published database port —
# see the note below; the bundled compose file does not publish one)
export DATABASE_URL="postgres://azimuthal:yourpassword@localhost:5432/azimuthal?sslmode=disable"
goose -dir migrations postgres "$DATABASE_URL" up
```

> **The bundled `build/docker-compose.yml` publishes only the `app` port.** Neither `db` nor
> `storage` declares a `ports:` mapping, so PostgreSQL is reachable only as `db:5432` and MinIO
> only as `storage:9000`, on the Compose network. Any `localhost:5432` or `localhost:9000` command
> in this document — the goose recipe above, and the MinIO health check under Troubleshooting —
> fails with connection refused on a stock deployment unless you add a mapping yourself. (The dev
> and test overlays do publish ports; they are not the file this guide deploys.) Migrations run
> automatically at startup, so the goose recipe is a fallback, not a required step. *Noted
> 2026-07-31.*
>
> This note's scope has **not** widened to Backup and Restore, and no longer needs to: those
> commands run inside the `app` container and reach `db:5432` and `storage:9000` on the Compose
> network, so they need no published port and no host-side client. Earlier revisions of this guide
> told you to add a `ports:` mapping to the `db` service in order to back up. You do not need to,
> and adding one exposes your database to the host. *Narrowed 2026-08-01.*

## Backup and Restore

Both commands run **inside the application container**, which carries the PostgreSQL client
tools for exactly this purpose. Nothing here needs a published database port, a client
installed on the host, or any step outside Compose.

> **The bundled client is PostgreSQL 16, and that is a floor, not a coincidence.** `pg_dump`
> refuses to dump a server newer than itself, so the client's major version must be **greater
> than or equal to** the server's. `build/docker-compose.yml` runs `postgres:16-alpine`, so the
> two match. If you point this deployment at an external PostgreSQL 17 or newer, the bundled
> client cannot back it up — take the dump with a client of at least that major version instead.
> `TestDockerfiles_ClientMajorMeetsServer` fails the build if the bundled pair ever drifts apart.

### Creating a Backup

```bash
docker compose exec app /azimuthal backup --output /tmp/backup.tar.gz
docker cp "$(docker compose ps -q app)":/tmp/backup.tar.gz ./backup-$(date +%Y-%m-%d).tar.gz
```

The backup archive contains:
- PostgreSQL database dump
- All object storage files
- A `manifest.json` with the Azimuthal version, the timestamp, the source PostgreSQL server's
  version, and the file inventory

Copy the archive off the host. A backup that only exists inside the container is lost with the
container.

> **The backup archive is a credential — store and move it like one.** The database dump inside it
> includes the `auth_signing_keys` table, which holds the RS256 private key this deployment signs
> every session token with (the key lives in the database by design — see
> [ADR-0004](adr/0004-signing-keys-in-database.md)). Anyone who holds a backup can mint valid tokens
> for **every** user, so this is a full authentication compromise, not merely a data disclosure.
> Encrypt the archive at rest, and keep it out of shared drives, ticket attachments, and
> unencrypted buckets. The `backup` command creates the file owner-only (`0600`) on the host that
> takes it — that protects it there, not wherever you copy it next.
>
> **If an archive leaks, be clear about what you can and cannot do today.** There is no key-rotation
> command, and `auth_signing_keys` is a singleton that cannot hold a second key without a schema
> change, so an in-place rotation with a grace window does not exist yet (ADR-0004 records this;
> tracked as D100). Signing every user out — the `token_generation` bump behind logout — does **not**
> help here: it never touches the signing key, so a holder of the leaked key can still forge tokens
> for anyone. The only way to retire a leaked signing key is to replace it — bring up a deployment
> whose key store is empty so a fresh key is generated, or import a known-good PEM via
> `JWT_PRIVATE_KEY_PATH` (consulted only when the store is empty), then migrate your data onto it.
> That invalidates every outstanding token at once; there is no softer path today.

### Restoring from Backup

```bash
docker cp ./backup-2026-04-04.tar.gz "$(docker compose ps -q app)":/tmp/backup.tar.gz
docker compose exec app /azimuthal restore --input /tmp/backup.tar.gz
```

Restore prints the archive's manifest — including which PostgreSQL server the dump came from —
before it changes anything, so you can check you are restoring what you think you are.

Restore is idempotent and safe to run multiple times: the dump is taken with `--clean
--if-exists`, and object storage is rewritten with overwriting puts.

**A restore that fails part-way exits non-zero and says why.** It does not report success over a
partial recovery. If the command reports an error, treat the database as being in an
indeterminate state and restore again from a known-good archive rather than assuming the failure
was cosmetic.

### Automated Backups

Set up a cron job to run backups on a schedule:

```bash
# Daily backup at 2 AM
0 2 * * * cd /path/to/azimuthal && docker compose exec -T app /azimuthal backup --output /tmp/backup.tar.gz && docker cp "$(docker compose ps -q app)":/tmp/backup.tar.gz /backups/azimuthal-$(date +\%Y-\%m-\%d).tar.gz
```

The `&&` chain means a failed backup produces no file rather than an empty or half-written one.
Cron mails the output of a failing run to the crontab's owner — check that the mailbox is one
somebody reads, or redirect the output somewhere you monitor. **Verify the first run produced a
file**; a backup schedule nobody has ever seen produce output is not a backup schedule.

## User Administration

### Create a User

```bash
docker compose exec app /azimuthal admin create-user \
  --email user@example.com \
  --name "Jane Doe" \
  --password secure-password
```

### Reset a Password

```bash
docker compose exec app /azimuthal admin reset-password \
  --email user@example.com \
  --password new-secure-password
```

## Upgrading

See [upgrade.md](upgrade.md) for step-by-step upgrade instructions.

## Troubleshooting

### Application won't start

**Symptom**: Container exits immediately or restarts in a loop.

1. Check logs: `docker compose logs app`
2. Verify all required environment variables are set in `.env`
3. Ensure the database is healthy: `docker compose ps db`
4. Verify DATABASE_URL is correct: `docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$(docker compose ps -q app)" | grep DATABASE_URL` — the app image is distroless and has no `env` binary, so `docker compose exec app env` fails with an exec error rather than answering

### Database connection refused

**Symptom**: `connecting to database: ... connection refused`

1. Check the database is running: `docker compose ps db`
2. Wait for the healthcheck to pass: `docker compose exec db pg_isready -U azimuthal`
3. Verify POSTGRES_PASSWORD matches between app and db services

### MinIO connection issues

**Symptom**: `connecting to object storage: ... connection refused`

1. Check MinIO is running: `docker compose ps storage`
2. Verify MinIO is healthy: `docker inspect --format '{{.State.Health.Status}}' "$(docker compose ps -q storage)"` — the bundled compose file does not publish `9000` on the host, so `curl http://localhost:9000/...` cannot reach it
3. Ensure MINIO_ROOT_USER and MINIO_ROOT_PASSWORD match between services

### A setting in `.env` appears to have no effect

**Symptom**: you set an `AZIMUTHAL_*` variable in `.env`, restarted, and the behaviour did not
change — with no error and nothing in the log.

The bundled Compose file forwards only the variables named in its `app` service `environment:`
block. Every setting the binary reads is now named there, so this should not happen with the
bundled file — but it happens immediately in a Compose file you have edited or replaced, and the
symptom is indistinguishable from the setting itself not working.

To confirm what the container actually received:

```bash
docker compose -f build/docker-compose.yml config
docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$(docker compose ps -q app)" | grep AZIMUTHAL
```

Every `AZIMUTHAL_*` setting should appear, with an empty value for the ones you have not set — an
empty value is treated as unset and the binary applies its own default. A variable **absent** from
that output is one the application never saw.

### Port already in use

**Symptom**: `bind: address already in use`

1. Change the host port: set `APP_PORT=9090` in `.env`
2. Or stop the conflicting service: `lsof -i :8080`

### Frontend shows blank page

**Symptom**: Browser shows white screen at http://localhost:8080

1. Clear browser cache and hard refresh
2. Check browser console for JavaScript errors
3. Verify the binary was built with the frontend: `docker compose exec app /azimuthal bundle-hash` — the binary reports its own embedded bundle digest. (`docker compose exec app ls /web/dist/` cannot work and was the instruction here until 2026-07-31: the frontend is compiled *into* the binary by `//go:embed all:dist`, so `/web/dist` never exists in the image, and the distroless image has no `ls` either.)

### Out of disk space

1. Check Docker disk usage: `docker system df`
2. Clean unused images: `docker image prune`
3. Check MinIO storage: the `azimuthal_storage` volume holds uploaded files
4. Check PostgreSQL data: the `azimuthal_db` volume holds database files
