# ADR-0004 — RS256 signing keys are persisted in the database

**Status:** Accepted. Decided after v0.1.11; documented retroactively July 2026.

---

## Context

Through v0.1.10, the RSA signing keypair was generated at process startup. Every container
restart produced a new keypair, which invalidated every JWT the previous process had issued.

The practical effect was that all users were signed out on every deploy and every container
restart. It was fixed in v0.1.11, and it is worth recording *why it survived eleven releases*:
no test restarted the server. The suite covered token issuance and token validation within a
single process lifetime, which is exactly where the bug was not.

The obvious alternative — supplying key material through an environment variable — was
considered and rejected.

---

## Decision

**Signing keys are generated once and persisted in the database.** Not environment variables, not
files on disk, not regenerated at boot. The algorithm is **RS256**.

### Why the database rather than environment configuration

**Self-hosting simplicity.** The single-binary promise is `./azimuthal` plus a PostgreSQL
connection. Requiring an operator to generate key material first, correctly, breaks that promise
and introduces a class of misconfiguration — weak keys, keys pasted into a compose file and
committed to a repository — that the application can avoid entirely by generating its own.

**Horizontal scaling.** Multiple instances behind a load balancer must verify each other's
tokens. With environment configuration that means distributing identical secret material to every
instance and keeping it in sync. With the database, they read the same row. The application layer
stays stateless, which is the property that makes it scalable in the first place.

**Rotation.** Key rotation becomes a database operation with a grace window — the previous key
remains valid for verification while the new key signs — rather than a coordinated restart across
every instance.

### Why RS256 rather than HS256

Asymmetric signing allows verification without the ability to mint. That keeps the door open for
a public JWKS endpoint, or for future services that must validate tokens without being trusted to
issue them. Symmetric signing forecloses both.

---

## Consequences

**The database is the root of trust for authentication.** This follows directly and must be
stated plainly: **database backups contain key material and must be treated as secrets.** A
backup that leaks is a full authentication compromise, not merely a data disclosure.

**Restoring a backup restores the keys**, so tokens issued before that backup was taken become
valid again. Where that is unacceptable, rotation after restore is the remedy, and it should be
part of the documented restore procedure.

**Rotation needs a documented procedure** with an explicit grace window, not an ad-hoc operation.

**Per-user revocation composes on top, not instead.** P2.5 added `users.token_generation`, carried
as a JWT claim and checked on every request, which allows individual sessions to be invalidated
without touching the signing key. The two mechanisms are complementary: key rotation invalidates
everything, generation bumping invalidates one user.

**Restart behaviour must be tested.** The original bug existed because nothing exercised a
restart. Any change touching key handling, token issuance, or token validation must include a
test that issues a token, restarts the server, and asserts the token still validates. This is
non-negotiable and is the direct lesson of v0.1.11.

---

## Correction — 2026-07-31 (spec/repo reconciliation)

**The core decision is implemented correctly.** RS256, generated once, persisted in the database,
never regenerated at boot — that is what migration 018 and `internal/core/auth/keys.go` do, and
the restart test this ADR demands exists.

**Rotation with a grace window does not exist and is not representable without a migration.**
`auth_signing_keys` is a hard singleton: `id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1)`
(`migrations/018_auth_signing_keys.sql:9`), with four columns and no key id, no active/retired
flag and no validity window. Two keys cannot coexist, so "the previous key remains valid for
verification while the new key signs" has nowhere to live. The whole query set is two statements —
read the row at `id = 1`, and insert-if-absent (`internal/db/queries/auth_signing_keys.sql`) —
with no UPDATE, no DELETE and no second-key verification path anywhere in Go or SQL. A search for
`kid`, `jwks`, `rotate` or `rotation` across `internal/core/auth/` returns nothing.

The constraint is load-bearing elsewhere and is acknowledged in code: `internal/core/auth/jwt.go`
notes that "a key per family is not available without changing that decision", which is why the
portal token family is separated by an audience claim instead.

Two consequences follow, and both are flagged for the maintainer rather than settled here:

- **The documented procedure this section requires does not exist.** A search for "key rotation"
  or "grace window" across `docs/` matches only this ADR.
- **The restore remedy above depends on the missing capability.** "Rotation after restore is the
  remedy" for the replayed-token consequence is not available today.

This is catalogued as **D100** and is a **maintainer decision, not a documentation fix**: the
rationale sentence overstates a benefit (a doc correction), while the Consequences obligation is
unmet (a capability to build). The two have different owners, and the capability half belongs
with whoever owns auth and operations.