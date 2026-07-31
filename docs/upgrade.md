# Upgrading Azimuthal

## Check Current Version

```bash
# From a running container
docker compose exec app /azimuthal --version

# Or from a local binary
./azimuthal --version
```

## Upgrade Process

### 1. Back up your data

Always create a backup before upgrading:

```bash
docker compose exec app /azimuthal backup --output /tmp/backup.tar.gz
docker cp "$(docker compose ps -q app)":/tmp/backup.tar.gz ./backup-pre-upgrade.tar.gz
```

### 2. Pull the new image

```bash
# Update to a specific version
export AZIMUTHAL_VERSION=v1.2.0

# Or pull latest
export AZIMUTHAL_VERSION=latest

docker compose pull app
```

### 3. Apply the upgrade

```bash
docker compose up -d
```

Database migrations run automatically on startup. The application will not begin serving requests until all migrations have completed.

### 4. Verify the upgrade

```bash
# Check the version
docker compose exec app /azimuthal --version

# Check health
curl http://localhost:8080/health

# Check logs for errors
docker compose logs --tail=50 app
```

## Rollback

If something goes wrong after an upgrade:

### 1. Stop the application

```bash
docker compose down
```

### 2. Pin the previous version

Edit your `.env` file (or set the variable directly):

```bash
export AZIMUTHAL_VERSION=v1.1.0  # the version you were running before
```

### 3. Restore the database backup

```bash
docker compose up -d db storage  # start only infrastructure

# The backup step produced backup-pre-upgrade.tar.gz. The SQL dump is a member
# named database.sql INSIDE that archive — there is no bare .sql file to redirect.
# The db service is postgres:16-alpine and does carry psql, so pipe the member in:
tar -xzOf backup-pre-upgrade.tar.gz database.sql \
  | docker compose exec -T db psql -v ON_ERROR_STOP=1 -U azimuthal -d azimuthal
```

*Corrected 2026-07-31. This step led with
`docker compose exec -T db psql -U azimuthal -d azimuthal < backup-pre-upgrade.sql` — a file no
backup step has ever produced (`azimuthal backup` only ever writes a gzip-compressed tar). It
failed with "No such file or directory" at the worst possible moment, mid-rollback.*

> The `/azimuthal restore` alternative that used to follow **cannot run in the shipped image** —
> it forks `psql`, and the app image is distroless. See the warning at the top of
> "Backup and Restore" in [self-hosting.md](self-hosting.md); that fix is ledgered as D105. The
> `tar`-and-pipe form above works today because it runs `psql` in the **db** container, not the
> app one.

### 4. Start the old version

```bash
docker compose up -d
```

## Version Compatibility

- Azimuthal uses append-only database migrations. Each release may add new migrations but never modifies existing ones.
- Downgrading the application version after running new migrations may cause errors if the code expects schema that does not exist in the older version.
- Always keep a backup of your database before upgrading so you can restore if a rollback is needed.

## Upgrade Notifications

Check the [releases page](https://github.com/Azimuthal-HQ/azimuthal/releases) for new versions and changelogs.

## Version Notes

### v0.3.2 (P2.5 — administration)

**Behaviour change: open registration is now off by default.** `POST
/auth/register` returns 404 unless `AZIMUTHAL_ALLOW_REGISTRATION=true` is
set. Admins add people from the Administration area (avatar menu →
Administration → People → Invite people); each invite is a single-use link.
Instances that relied on open self-registration must set the variable
explicitly before upgrading.

Deactivating a person now signs them out everywhere immediately (all
outstanding tokens are invalidated, not just future sign-ins), and the same
applies to the new "Sign out everywhere" action and to password changes on
other devices. No operator action is needed; existing sessions are not
disturbed by the upgrade itself.
