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
| `AZIMUTHAL_PORTAL_DISCLOSE_LINK` | `false` | Return the portal sign-in URL in the body of the unauthenticated request-link response. Inert on this deployment for two independent reasons: `build/docker-compose.yml` sets `APP_ENV: production`, and disclosure requires this flag **and** a development `APP_ENV` — a **safelist** of `development` and `test` only, so `production`, `staging`, or any unrecognised name refuses it. Setting it here is harmless and does nothing; the server logs a startup warning naming both variables rather than failing, so a working configuration is never locked out over a combination that is already safe. Leave it off in any case: `POST /portal/{key}/auth/request-link` is unauthenticated by design and accepts any address, so a disclosed URL signs the caller in as anyone they can name. To try the portal locally without a mail server, prefer the `build/docker-compose.portal-trial.yml` override over setting this by hand — see [Try the portal in five minutes](#try-the-portal-in-five-minutes). |
| `AZIMUTHAL_PORTAL_LINK_TTL` | `1h` | How long a portal sign-in link stays redeemable. Must be positive. |
| `AZIMUTHAL_PORTAL_SESSION_TTL` | `72h` | Lifetime of the session a redeemed portal link produces. Must be positive. |
| `AZIMUTHAL_CREDENTIAL_LINK_TTL` | `60m` | How long an internal-user credential link stays redeemable — the one window for all three purposes: an admin-issued sign-in link, a password reset (self-service or admin-issued), and an email-change confirmation. A credential sitting in an inbox, so the default is short. Must be positive. Whether these links are *emailed* is not a mode here — it follows whether a relay is configured (`SMTP_HOST` set explicitly): with one, forgot-password and email-change email the link; without one, forgot-password delivers nothing (issue an admin link instead) and email-change returns the link to the reauthenticated requester. |
| `SMTP_FROM` | `azimuthal@localhost` | Envelope sender for outbound mail. Required (set explicitly) when `AZIMUTHAL_INVITE_DELIVERY=email` or `AZIMUTHAL_PORTAL_LINK_DELIVERY=email` — the default is not accepted for email delivery, because mail sent as `azimuthal@localhost` is rejected or junked by real relays. |
| `SMTP_TLS` | `none` | Transport security for the SMTP connection: `none` (plaintext — for a local relay such as mailhog, or a trusted internal network), `starttls` (connect in the clear, upgrade with STARTTLS), or `implicit` (TLS from the first byte, the classic port-465 style). An unrecognised value is refused at startup. |
| `SMTP_USERNAME` | (empty) | Username for SMTP `PLAIN` authentication. Set it together with `SMTP_PASSWORD` — exactly one of the two is refused at startup. |
| `SMTP_PASSWORD` | (empty) | Password for SMTP `PLAIN` authentication. If you set auth with `SMTP_TLS=none`, the server **boots with a warning**: the credentials travel in the clear. Use `starttls` or `implicit` for anything but a trusted local relay. |
| `AZIMUTHAL_AUTH_RATE_LIMIT_ENABLED` | `true` | Rate-limit the unauthenticated, auth-critical endpoints (login, invite acceptance, the internal-user credential links — forgot-password, inspect and consume — and the portal request-link and redeem) with a per-client-IP token bucket. On by default. See the note below. |
| `AZIMUTHAL_AUTH_RATE_LIMIT_PER_MINUTE` | `30` | Sustained requests per minute allowed per client IP, per route class. Must be positive when rate limiting is enabled. |
| `AZIMUTHAL_AUTH_RATE_LIMIT_BURST` | `10` | Largest instantaneous burst allowed per client IP, per route class, before the sustained rate applies. Must be positive when rate limiting is enabled. |
| `STORAGE_USE_SSL` | `false` | Reach the object-storage endpoint over TLS. An `https://` prefix on `STORAGE_ENDPOINT` forces this to `true` regardless of what you set. The bundled Compose file sets `http://storage:9000`, so MinIO is reached over the internal network on plain HTTP. |

### Auth-surface rate limiting

The unauthenticated endpoints where an attacker would stuff credentials or
enumerate accounts — `POST /auth/login`, invite inspection and acceptance, the
internal-user credential links (`forgot-password`, `inspect` and `consume`), and
the portal's request-link and redeem — carry a per-client-IP **token bucket**.
A client gets `AZIMUTHAL_AUTH_RATE_LIMIT_BURST` requests immediately, then one
every `60 / AZIMUTHAL_AUTH_RATE_LIMIT_PER_MINUTE` seconds; over the limit the
endpoint answers **`429 Too Many Requests`** with a `Retry-After` header and no
detail in the body (so it cannot itself become the oracle it exists to close).
Each of the endpoints above is a separate bucket, so exhausting one does not
lock out the others, and the client IP is taken from the same extraction the
audit log records against.

Two things worth knowing:

- **It is per instance, not per cluster.** The bucket lives in the process, so
  if you run more than one replica behind a load balancer each limits
  independently — a round-robin balancer loosens the effective limit by roughly
  the replica count. Azimuthal is a single binary with no shared cache, and this
  is the honest consequence; it is fine at the scale this targets.
- **It keys on the client IP as the server sees it.** Behind a reverse proxy
  that terminates the connection, that is the proxy's address unless the proxy
  is configured to preserve the client's — so many users can share one bucket.
  Size `BURST`/`PER_MINUTE` for the number of people who legitimately sit behind
  one address, or disable it (`AZIMUTHAL_AUTH_RATE_LIMIT_ENABLED=false`) if your
  proxy already does this job.

## Try the portal in five minutes

The customer portal lets people outside your organisation raise and track requests without an
Azimuthal account. On a production install its sign-in links are **emailed**, so exercising the
requester side normally needs an SMTP relay. For a **local trial**, a committed override turns on
*link disclosure* instead: the request-link API returns the sign-in URL in its own response, so you
can sign in with no mail server.

> **This is an authentication bypass — never layer it onto a deployment with real data.**
> `POST /portal/{key}/auth/request-link` is unauthenticated and answers for any address, so a
> disclosed sign-in URL signs the caller in as whoever they named. The server only honours
> disclosure on a development or test `APP_ENV` and refuses it in production
> (`config.Config.PortalLinkDisclosureAllowed`); the override sets `APP_ENV: development` precisely
> so the flag takes effect, which is exactly why it must stay off any deployment that holds real
> data.

**1 — Start the stack with the trial override.** Layer `build/docker-compose.portal-trial.yml` on
the production compose file. It is named for the trial rather than `override` on purpose, so Compose
never merges it implicitly — disclosure is on only when you ask for it, visibly, on the command
line:

```bash
docker compose -f build/docker-compose.yml -f build/docker-compose.portal-trial.yml up -d
```

If you deploy from the curl'd Quick Start files rather than a source checkout, fetch the override
next to your `docker-compose.yml` (pinned to the same release) and drop the `build/` prefix from
both `-f` paths. Create an admin user if you have not already (see
[First-Run](#first-run-create-an-admin-user)) and log in at `http://localhost:8080/login`.

**2 — Create a portal on a Beacon space.** A portal attaches only to a Beacon (service-desk) space.
Open such a space, go to **Settings**, and in the **Customer portal** card give the portal a public
name and intro, then create it. The card then shows the portal's key and its full customer URL,
`http://localhost:8080/portal/{key}` — that URL is the customer's front door.

**3 — Request a link, and read the URL from the API response.** Open the customer URL in a
**signed-out** browser; an incognito/private window is the simplest way to be sure you are not
carrying your staff session. Enter any email address and submit — the page confirms with a
deliberately conditional "if that address can raise requests here…" and nothing more.

The sign-in page **never displays the link**, by design: showing it would hand a working sign-in
credential to anyone who typed an address into an unauthenticated form. With the trial override on,
the URL rides in the **API response body** instead, where only you — running the trial — can see it:

```bash
curl -sS -X POST http://localhost:8080/api/v1/portal/{key}/auth/request-link \
  -H 'Content-Type: application/json' \
  -d '{"email":"tester@example.com"}'
# → {"status":"sent","delivered":false,"magic_link_url":"http://localhost:8080/portal/{key}/signin/…"}
```

The endpoint answers `202` for *every* address, known or not — so the form cannot be used to
enumerate your customers — and `magic_link_url` is present only because the override enabled
disclosure. Copy that URL.

**4 — Sign in and raise a request.** Paste `magic_link_url` into the incognito window. It redeems
the one-time link, lands on the requester's request list, and **New request** opens the compose
form; fill in a summary and description and submit. You are now a customer with a live request.

**5 — See it from the staff side.** Back in your staff window, open the same Beacon space's tickets:
the request is there as a ticket, flagged as coming from the portal, with the requester's address on
it. A **public** reply from staff appears on the requester's thread; an **internal** note stays
invisible to them.

**6 — Turn the override off.** When you are done, bring the stack back up on the production compose
file alone:

```bash
docker compose -f build/docker-compose.yml up -d
```

`APP_ENV` returns to `production` and link disclosure goes dark. To run a portal for real customers,
set `AZIMUTHAL_PORTAL_LINK_DELIVERY=email` with `SMTP_HOST` and `SMTP_FROM` so links are mailed, and
leave `AZIMUTHAL_PORTAL_DISCLOSE_LINK` off.

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

**Stop the application first, and restore in a one-off container — never inside the live one.**

```bash
# 1. Stop the server so nothing writes to the database while it is dropped and
#    recreated. Restore refuses to run while a server is up (see the note below).
docker compose stop app

# 2. Restore in a one-off container, mounting the archive read-only. This is a
#    fresh `run` container, not `exec` into the live app (which is now stopped).
docker compose run --rm \
  -v "$PWD/backup-2026-04-04.tar.gz:/tmp/backup.tar.gz:ro" \
  app /azimuthal restore --input /tmp/backup.tar.gz

# 3. Bring the application back up.
docker compose up -d app
```

Restore prints the archive's manifest — including which PostgreSQL server the dump came from —
before it changes anything, so you can check you are restoring what you think you are.

Restore is idempotent and safe to run multiple times: the dump is taken with `--clean
--if-exists`, and object storage is rewritten with overwriting puts.

> **Why stop the app first — and why the tool now insists.** Restore replays a `--clean
> --if-exists` dump: it **drops and recreates every table**. Run against a live server — whose
> request handlers and background job queue are still writing — it corrupts both the restore and
> the running process. So `serve` takes a PostgreSQL advisory lock at startup and holds it for its
> whole lifetime, and `restore` takes the *same* lock and **refuses with "the server is running;
> stop it before restoring" when a server holds it**, before it touches anything. That is why the
> restore runs in a one-off `docker compose run` container after `docker compose stop app`, rather
> than `docker compose exec` inside the live one. Earlier revisions of this document told you to
> run restore inside the running container; that silently violated the very invariant restore
> depends on, and it is now enforced in the binary rather than left to the reader.

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

There are two ways to give someone an account, and they differ in who chooses
the password. The recommended way is a **sign-in link**: you create the account
and hand over a one-time link, and the person sets their own password when they
open it — you never see or set it. The other is **break-glass**: you set a
password directly. Both exist on the CLI, and the sign-in-link flow also has an
admin UI (Administration → People → **Create user**).

### Create a user with a sign-in link (recommended)

```bash
docker compose exec app /azimuthal admin create-user \
  --email user@example.com \
  --name "Jane Doe" \
  --link
```

This creates the account with a default grant and **no password**, and prints a
one-time sign-in link. Hand it over out of band. It works once and expires
(`AZIMUTHAL_CREDENTIAL_LINK_TTL`, default 60 minutes); the person sets their own
password when they open it. Until then, the account cannot be signed into.

### Create a user with a password (break-glass)

```bash
docker compose exec app /azimuthal admin create-user \
  --email user@example.com \
  --name "Jane Doe" \
  --password secure-password
```

`--password` and `--link` are mutually exclusive; exactly one is required. Use
`--password` only when you genuinely need to set the password yourself — the
sign-in-link flow is preferable because the password is never known to anyone but
the account holder.

### Password resets

There is **no SSO or LDAP integration** — a self-hosted Azimuthal owns its own
passwords — so it ships its own reset mechanism. There are three ways in:

- **Self-service** (`/forgot-password`, or the “Forgot password?” link on the
  sign-in page). The person enters their address. The response is the same
  whether or not the address is known — it is deliberately not an
  account-existence oracle — and the link is **never shown in the browser**. With
  a mail relay configured it is emailed; **without a relay it delivers nothing**,
  and the admin-issued link below is the answer.
- **Admin-issued** (Administration → People → a member’s **Generate reset link**,
  or the CLI). This returns a one-time reset link you hand over — the no-relay
  answer, and the way to reset an account when the person cannot reach their
  inbox.
- **Break-glass CLI**, which sets the password directly:

```bash
docker compose exec app /azimuthal admin reset-password \
  --email user@example.com \
  --password new-secure-password
```

Redeeming a reset link (self-service or admin-issued) **signs the account out of
every device** — a reset is a break-glass event by design. The CLI does the same.

### Changing an email address

A signed-in user changes their own email from **Settings → Profile → Change email
address**. It asks for the current password (reauthentication) and then confirms
through a link, and confirming **signs the account out everywhere**. With a mail
relay the confirmation link goes to the **new** address, which proves the person
controls it. **Without a relay** the link is returned to the requester in the app
instead — this is weaker (it does not prove control of the new address), but the
reauthentication is the security that matters, so the trade is acceptable for a
deployment with no mail. This is the only way to change an email: it does not
travel through the plain profile save.

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
