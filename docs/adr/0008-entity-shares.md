# ADR-0008 — Entity shares

**Status:** Accepted — implemented in P3 (PR #56).
**Origin:** Written as part of `docs/design/v0.3-ia-spec.md` §3; extracted verbatim into this
file in the post-P3 documentation pass. The text below is unchanged.

---

**Context.** A VP needs to share one page or one folder with the whole org without opening
the space it lives in.

**Decision. Shares widen, never narrow.** An entity may be *more* visible than its space.
Never *less*.

This is the whole design. Narrowing would force per-row permission checks on every list,
search, and aggregate query, and produce "appears in search but won't open" defects.
Widening leaves the main query path untouched — `space_id = ANY(readable)` stays exactly as
it is, and a small set of explicitly shared entity IDs is unioned in.

Mandatory rules:

1. **Read-only.** Read on that entity. Never edit, comment, or transition. Org-wide editing
   means a space, not a share.
2. **No container access.** A shared page must not reveal its space, page tree, or sibling
   content. Breadcrumbs degrade to the page itself — never a clickable ancestor chain into a
   space the viewer cannot enter.
3. **Attachments follow the entity.** The object-store read path must honour the share, or
   shared pages render with broken images.
4. **Comments are excluded.** Internal discussion is usually the part you do not want the
   org reading.
5. **Persistent indicator.** Any shared entity carries an always-visible badge. This matters
   most with cascade — a page created under a shared folder next week is org-visible the
   moment it exists, and its author must know before typing.
6. **Only `manage_shares` may create a share** — space admins. A contributor publishing to
   the whole org is a leak vector.
7. Cascade is offered; **single entity is the default**. No size cap, but the UI shows the
   affected page count before confirming.
8. **Shares may expire.** `expires_at` is nullable; null means indefinite. Expiry is
   evaluated in the resolution query, so an expired share stops granting access immediately
   without waiting for a sweeper.
9. **Moving an entity to another space revokes all its shares**, with a UI warning. Without
   this, a page shared org-wide could be dragged into a sensitive space and stay public.
10. **Deleting an entity revokes its shares in the same transaction.** A nightly sweeper
    runs as a backstop, not as the primary mechanism.
11. Revocation is immediate. Readable sets are computed per request and never cached across
    requests.

Cascade is cheap because Codex materialises page paths by ID — a subtree share is a prefix
match, not a recursive walk.

---

## Correction — 2026-07-31 (spec/repo reconciliation)

**No nightly sweeper was built, and the repository deliberately decided against one.** Rule 10's
first sentence is implemented exactly — `ContentTxAdapter` in `internal/db/adapters/content_tx.go`
calls `RevokeSharesByEntity` inside the entity delete's own transaction, and `revokeSubtreeSharesTx`
does the same for the move path. Its second sentence describes machinery that does not exist: no
periodic job of any kind is registered — `NewQueue` in `internal/jobs/queue.go` declares two
workers, `EmailWorker` and `NotificationWorker`, and no `PeriodicJobs` — and there is no ticker or
cron anywhere in `internal/` or `cmd/`.

There is no orphan class for a sweeper to collect. Share rows are never hard-deleted — revocation
stamps `revoked_at` — and expiry is evaluated inside the resolution query, so a share past
`expires_at` stops granting access on the very next request. The reasoning is recorded in the
header comment of `migrations/026_entity_shares.sql`, which states it in terms: "with no sweeper
involved."

Two things make this unarguable rather than a judgement call. **Rule 8, five lines above, already
says the opposite** — expiry denies "without waiting for a sweeper". And there is a passing test
named for the absence: `TestShare14_ExpiredDeniesWithoutSweeper` in
`internal/core/api/entity_shares_integration_test.go`, whose comment reads "with NO sweeper having
run".

**The rule this ADR sets is honoured in full and is regression-tested.** Only the backstop clause
is wrong, and it describes a mechanism, not an obligation. Catalogued as D99.
