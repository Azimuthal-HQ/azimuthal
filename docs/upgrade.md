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
docker compose exec -T db psql -U azimuthal -d azimuthal < backup-pre-upgrade.sql
# Or use the full restore command:
docker cp ./backup-pre-upgrade.tar.gz "$(docker compose ps -q app)":/tmp/backup.tar.gz
docker compose up -d app
docker compose exec app /azimuthal restore --input /tmp/backup.tar.gz
```

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
