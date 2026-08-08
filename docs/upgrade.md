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
export AZIMUTHAL_VERSION=v0.4.1

# Or pull latest. `:latest` only moves once the GitHub Release for that version
# exists, and never backwards onto an older version, so it always names a
# released build.
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
export AZIMUTHAL_VERSION=v0.4.0  # the version you were running before
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

Alternatively, restore the whole archive — database *and* object storage — with the app
container's own command. The image now ships the PostgreSQL client tools, so this works inside
the container and, unlike the `tar`-and-pipe form above, also restores attachments:

```bash
docker compose up -d          # the app container must be running to exec into it
docker cp backup-pre-upgrade.tar.gz "$(docker compose ps -q app)":/tmp/restore.tar.gz
docker compose exec app /azimuthal restore --input /tmp/restore.tar.gz
```

Both forms abort on the first failing statement (`-v ON_ERROR_STOP=1`) rather than reporting
success over a partial restore. If either exits non-zero, do not proceed — the database is in an
indeterminate state.

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

*Corrected 2026-08-02. The two example versions in this guide were `v1.2.0` (the pull step) and
`v1.1.0` (the rollback step). Neither has ever existed — the version series has never reached 1.x —
so an operator following the rollback recipe literally pinned a tag with no image behind it, at the
point in the process where they were least able to absorb a surprise.*

### v0.4.1 (trust patch)

**A patch release about believing what the software tells you.** Nothing here adds a feature; every
change closes a gap between what a surface claimed and what it did.

Two of them can change what an existing caller sees, and neither affects a stock Docker Compose
deployment. **The portal sign-in URL is no longer returned in an API response** unless a new flag
is set on a non-production host — if a script or test harness of yours reads `magic_link_url`, it
now gets nothing until you set `AZIMUTHAL_PORTAL_DISCLOSE_LINK=true`. And **ticket `PATCH` stopped
being a full replace**, which is a fix, but it is a fix that changes what a sparse request body
does. Both are detailed below.

- **`APP_ENV` now defaults to `production` (was `development`).** This is the change most likely to
  surprise you, and it is a posture change rather than a functional one. Docker Compose deployments
  are untouched: `build/docker-compose.yml` hardcodes `APP_ENV: production` and always has. What
  moves is the *unset* case — a bare `docker run`, a `go run ./cmd/server`, `make dev` — which now
  resolves to `production`. Outside tests the environment name is read in exactly two places, the
  portal disclosure rule below and one startup log field, so in practice nothing breaks: the only
  observable difference is that a developer who wants the portal to hand back a sign-in URL must
  now set `APP_ENV=development` *and* the new flag. Every scripted path (`.env.test`,
  `scripts/verify-api.sh`, `scripts/regression-test.sh`, Playwright, CI) already sets `APP_ENV`
  explicitly and is unaffected. The old default meant a bare `docker run` was not a production
  server as far as the code was concerned, which is the wrong way round for a self-hosted product:
  an unset variable should describe somebody's server, not somebody's laptop.

- **The customer-portal sign-in URL is never disclosed on a production server, and disclosing it
  anywhere now takes an explicit flag.** `POST /portal/{key}/auth/request-link` is unauthenticated
  by necessity, so a response body carrying the sign-in URL signs the caller in as any address they
  can name. That body used to carry it whenever delivery was `link` **and** the environment was not
  `production` — and both of those were the defaults, so the unsafe state was the one an operator
  reached by doing nothing. Disclosure now requires the new `AZIMUTHAL_PORTAL_DISCLOSE_LINK`
  (default `false`) **and** an `APP_ENV` that names a development environment — `development` or
  `test`; `AZIMUTHAL_PORTAL_LINK_DELIVERY` no longer influences it at all. Setting the flag on a
  production server is harmless and does nothing — the server logs a startup warning naming both
  variables rather than refusing to boot, so a configuration that is already safe cannot lock you
  out. Two things worth knowing: that warning is at `warn` level, so `LOG_LEVEL=error` hides it, and
  the environment test is a **safelist**, not a blocklist — only `development` and `test` disclose.
  (v0.4.1 shipped this as a literal `APP_ENV != production` comparison, which fails open: an
  `APP_ENV=staging` host with the flag set *would* disclose and got no warning, because `staging`
  is not `production`. v0.4.2 replaced the comparison with the safelist above, so `staging`, an
  unknown name, or a typo like `produciton` now discloses nothing and *does* emit the warning.)

- **Signing out revokes tokens on every device**, not just the one signing out. Logout now deletes
  every session row for the user and bumps their token generation, which invalidates outstanding
  access tokens rather than waiting for them to expire.

- **Backup archives are created owner-only (`0600`)** on POSIX hosts, and a backup reports success
  only after the archive is verifiably flushed and closed — not when the last write call returned.
  The mode applies at creation: overwriting an existing output file keeps that file's permissions,
  and the bits are not enforced on Windows.

- **Restore refuses an archive with no `database.sql`** instead of silently skipping the database
  and reporting success. A restore that recovers nothing now says so and exits non-zero.

- **Ticket `PATCH` is a partial update.** It accepts five keys — `title`, `description`,
  `priority`, `labels`, `due_at` — and omitting one now leaves that field alone. It was previously
  a full replace of the four it then accepted, so a sparse body silently blanked everything it did
  not mention; API consumers sending sparse bodies get the behaviour they probably always assumed
  they had. An explicitly empty title is still refused. `assignee` and `status` are not `PATCH`
  fields and have their own routes, and an unrecognised key is still a 400.

- **Due dates are settable on tickets.** The ticket API previously rejected `due_at` outright on
  both create and `PATCH`, while a workflow guard could already *require* a due date — which made
  that guard unsatisfiable. Project items are a UI change only here: their API has accepted `due_at`
  on create and `PATCH` since before this release, and now there is a control that sets it.

- **Releases are CI-gated, and `:latest` is trustworthy.** A tagged commit is now built, vetted,
  linted, tested, scanned and booted by the full CI battery before any artifact is published —
  previously the release workflow triggered on tags and CI did not, so binaries and images shipped
  having never been through it. `:latest` now moves only after the GitHub Release object exists,
  and never backwards onto an older version: the retag step compares the tag being released against
  the repository's newest non-prerelease tag and moves `:latest` only if they match, so a backport
  cannot walk a `docker compose pull` down a version.

No migration. Upgrading from v0.4.0 is a `docker compose pull && docker compose up -d`.

### v0.4.0

**The first release of the 0.4 line, and the largest so far.** The load-bearing change for
operators is workflow enforcement.

- **A configured workflow now decides, and refuses.** Status changes are validated against the
  space's workflow rather than applied on request, so a transition the workflow does not permit is
  rejected instead of written. Migration **051** backfills workflow state for existing rows.
  Note that "we never configured a workflow" is rarer than it sounds — every space is created with
  one — so a space whose workflow was set up loosely will start refusing transitions that
  previously went through. Check yours before upgrading if you have customised them.
- Cross-module search, dashboards, the workflow admin editor, the customer-portal requester
  surface, and the `azimuthal assess` migration-assessor CLI.
- Two security passes closing the cross-space read- and write-authorisation classes.
- Backup and restore made to actually work in the shipped image: it now carries the PostgreSQL 16
  client tools those commands shell out to, so both run inside the container with no published
  database port and no host-side client.
- **Honest maturity posture.** The README gained a `Not yet shipped` section for SSO and email
  ingestion, both of which the feature list had been advertising and neither of which is reachable.
  v0.4.1 continues that work across the rest of the release surface.

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
