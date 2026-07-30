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
# 1. Download the compose file and env template
curl -O https://raw.githubusercontent.com/Azimuthal-HQ/azimuthal/main/build/docker-compose.yml
curl -O https://raw.githubusercontent.com/Azimuthal-HQ/azimuthal/main/.env.example

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
| `AZIMUTHAL_VERSION` | `latest` | Docker image tag to run |
| `STORAGE_BUCKET` | `azimuthal` | MinIO/S3 bucket name for file storage |
| `JWT_EXPIRY` | `24h` | Access token lifetime (Go duration format) |
| `SMTP_HOST` | `localhost` | SMTP relay host for outbound email |
| `SMTP_PORT` | `25` | SMTP relay port. Note this is the Compose default; the binary's own default is `1025`. |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. **Currently inert** — the value is parsed but nothing reads it; the logger is built with a hardcoded `info` level before config loads. Setting this changes nothing today. |
| `DATABASE_URL` | (auto) | PostgreSQL connection string. Auto-constructed in Docker Compose from `POSTGRES_PASSWORD` |

`APP_ENV` is **not** settable through `.env` in this deployment: `build/docker-compose.yml`
hardcodes `APP_ENV: production`.

### ⚠ Settings the binary reads that the bundled Compose file does not pass through

`build/docker-compose.yml` declares no `env_file:`, so its `environment:` block is the **only**
channel into the container. A variable placed in `.env` is available for `${...}` interpolation
inside the Compose file, but it does **not** reach the application unless the Compose file forwards
it explicitly — and the settings below are not forwarded.

**This matters most for the security-policy settings.** An operator who sets
`AZIMUTHAL_TICKET_REF_REQUIRED=true` or `AZIMUTHAL_BCRYPT_COST=14` in `.env`, restarts, and sees a
clean startup will reasonably conclude the policy is in force. It is not: the container never saw
the variable, and the application is running on the default. There is no warning, because from the
application's point of view nothing was ever set. (`.env.example` also lists
`AZIMUTHAL_BCRYPT_COST` and `AZIMUTHAL_TICKET_REF_REQUIRED` as though setting them there were
enough. It is not.)

To use any of these with the bundled Compose file, add the variable to the `app` service's
`environment:` block yourself:

```yaml
    environment:
      # ... the existing entries ...
      AZIMUTHAL_TICKET_REF_REQUIRED: ${AZIMUTHAL_TICKET_REF_REQUIRED:-false}
      AZIMUTHAL_BCRYPT_COST: ${AZIMUTHAL_BCRYPT_COST:-12}
```

| Variable | Default | Description |
|---|---|---|
| `AZIMUTHAL_ALLOW_REGISTRATION` | `false` | Open self-registration at `/auth/register`. Off by default since v0.3.2 — admins invite people from the Administration area instead. Instances relying on open registration must set this to `true` explicitly. |
| `AZIMUTHAL_INVITE_DELIVERY` | `link` | How invite links reach people: `link` (the admin copies the one-time URL) or `email` (Azimuthal sends it — requires `SMTP_HOST` **and** `SMTP_FROM` to be set explicitly; startup fails otherwise). An unrecognised value is refused at startup. |
| `AZIMUTHAL_INVITE_TTL` | `168h` | Invite expiry window (Go duration format). Must be positive. |
| `AZIMUTHAL_TICKET_REF_REQUIRED` | `false` | Require an operator ticket reference on every administrative change — creating a team, granting space access, sharing an entity, deactivating a person, and so on. Off by default; behaviour is unchanged until you opt in. Turning it on is a production cutover: every administrative mutation without a reference is refused with a 400 and writes nothing, and the admin UI marks its reference fields required and waits for one before it will submit. Boot-time only, deliberately — an organisation turns this on once, and a restart is the honest cost of changing what every admin action requires. |
| `AZIMUTHAL_BCRYPT_COST` | `12` | Password hashing work factor. Twelve is a floor, not just a default: a configuration asking for less is refused at startup in every environment, `APP_ENV` included. The knob exists so you can raise it as hardware gets faster — expect roughly a doubling of login CPU cost per step. Existing passwords keep verifying at the cost they were stored with, so raising it is safe and takes effect as people next change their password. |
| `AZIMUTHAL_ALLOWED_ORIGINS` | (empty) | Comma-separated CORS allow-list. Empty means no CORS headers are emitted and the browser enforces same-origin, which is correct for this deployment — the frontend is served by the same binary on the same origin. Set it only if you serve the frontend from somewhere else. |
| `AZIMUTHAL_QUEUE_ENABLED` | `true` | Runs the background job queue in-process. |
| `AZIMUTHAL_PORTAL_LINK_DELIVERY` | `link` | How a customer-portal sign-in link reaches a requester. Set this to `email` for any instance with the portal exposed to real customers: `link` returns the sign-in URL in the API response, and the endpoint that issues it is necessarily unauthenticated. Production refuses to disclose the link regardless, so the practical effect of leaving it at `link` in production is that portal sign-in links go nowhere. |
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

# Or with goose directly (requires goose installed)
export DATABASE_URL="postgres://azimuthal:yourpassword@localhost:5432/azimuthal?sslmode=disable"
goose -dir migrations postgres "$DATABASE_URL" up
```

## Backup and Restore

### Creating a Backup

```bash
docker compose exec app /azimuthal backup --output /tmp/backup.tar.gz
docker cp "$(docker compose ps -q app)":/tmp/backup.tar.gz ./backup-$(date +%Y-%m-%d).tar.gz
```

The backup archive contains:
- PostgreSQL database dump
- All object storage files
- A `manifest.json` with version, timestamp, and file inventory

### Restoring from Backup

```bash
docker cp ./backup-2026-04-04.tar.gz "$(docker compose ps -q app)":/tmp/backup.tar.gz
docker compose exec app /azimuthal restore --input /tmp/backup.tar.gz
```

Restore is idempotent and safe to run multiple times.

### Automated Backups

Set up a cron job to run backups on a schedule:

```bash
# Daily backup at 2 AM
0 2 * * * cd /path/to/azimuthal && docker compose exec -T app /azimuthal backup --output /tmp/backup.tar.gz && docker cp "$(docker compose ps -q app)":/tmp/backup.tar.gz /backups/azimuthal-$(date +\%Y-\%m-\%d).tar.gz
```

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
4. Verify DATABASE_URL is correct: `docker compose exec app env | grep DATABASE_URL`

### Database connection refused

**Symptom**: `connecting to database: ... connection refused`

1. Check the database is running: `docker compose ps db`
2. Wait for the healthcheck to pass: `docker compose exec db pg_isready -U azimuthal`
3. Verify POSTGRES_PASSWORD matches between app and db services

### MinIO connection issues

**Symptom**: `connecting to object storage: ... connection refused`

1. Check MinIO is running: `docker compose ps storage`
2. Verify MinIO is healthy: `curl http://localhost:9000/minio/health/live`
3. Ensure MINIO_ROOT_USER and MINIO_ROOT_PASSWORD match between services

### A setting in `.env` appears to have no effect

**Symptom**: you set an `AZIMUTHAL_*` variable in `.env`, restarted, and the behaviour did not
change — with no error and nothing in the log.

The bundled Compose file forwards only the variables in its `app` service `environment:` block, and
most `AZIMUTHAL_*` settings are not among them. See
[the warning in the Environment Variable Reference](#-settings-the-binary-reads-that-the-bundled-compose-file-does-not-pass-through).

To confirm what the container actually received:

```bash
docker compose -f build/docker-compose.yml exec app env | grep AZIMUTHAL
```

If the variable is absent from that output, the application never saw it.

### Port already in use

**Symptom**: `bind: address already in use`

1. Change the host port: set `APP_PORT=9090` in `.env`
2. Or stop the conflicting service: `lsof -i :8080`

### Frontend shows blank page

**Symptom**: Browser shows white screen at http://localhost:8080

1. Clear browser cache and hard refresh
2. Check browser console for JavaScript errors
3. Verify the binary was built with the frontend: `docker compose exec app ls /web/dist/`

### Out of disk space

1. Check Docker disk usage: `docker system df`
2. Clean unused images: `docker image prune`
3. Check MinIO storage: the `azimuthal_storage` volume holds uploaded files
4. Check PostgreSQL data: the `azimuthal_db` volume holds database files
