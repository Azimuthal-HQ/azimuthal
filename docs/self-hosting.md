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

# 3. Generate a secure JWT secret and set passwords
# On Linux/macOS:
sed -i "s/change-me-run-openssl-rand-hex-32/$(openssl rand -hex 32)/" .env
# Then edit .env and set POSTGRES_PASSWORD and MINIO_ROOT_PASSWORD

# 4. Start Azimuthal
docker compose -f docker-compose.yml up -d

# 5. Create your first admin user
docker compose exec app /azimuthal admin create-user \
  --email admin@example.com \
  --name "Admin" \
  --password your-secure-password
```

Azimuthal is now running at http://localhost:8080.

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
| `JWT_SECRET` | 64-character hex string for JWT signing. Generate with `openssl rand -hex 32` |

### Optional

| Variable | Default | Description |
|---|---|---|
| `APP_PORT` | `8080` | Host port to expose the application on |
| `APP_BASE_URL` | `http://localhost:8080` | Public URL of the application (used in emails and links) |
| `APP_ENV` | `production` | Environment: `development`, `test`, or `production` |
| `AZIMUTHAL_VERSION` | `latest` | Docker image tag to run |
| `STORAGE_BUCKET` | `azimuthal` | MinIO/S3 bucket name for file storage |
| `JWT_EXPIRY` | `24h` | Access token lifetime (Go duration format) |
| `SMTP_HOST` | `localhost` | SMTP relay host for outbound email |
| `SMTP_PORT` | `25` | SMTP relay port |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `DATABASE_URL` | (auto) | PostgreSQL connection string. Auto-constructed in Docker Compose from `POSTGRES_PASSWORD` |

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

### JWT_SECRET errors

**Symptom**: `JWT_SECRET is required`

1. Ensure JWT_SECRET is set in your `.env` file
2. Generate a new one: `openssl rand -hex 32`
3. The secret must be at least 1 character (64 hex chars recommended)

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
