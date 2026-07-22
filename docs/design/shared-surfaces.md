# Shared surfaces

**What this is.** The components, helpers and patterns that P2.5 and P3 built for everyone to
reuse, and the rule attached to each. None of them were documented, so the only way a later
session learned they existed was by tripping over them — or by building a second one.

**How to use it.** Before you build a picker, an error path, a confirmation count, a route guard
or a transactional write with an audit trail, look here first. If something close to what you need
already exists, extend it. A second implementation of anything on this page is a defect, not a
convenience.

Verified against `main` at migration 028.

---

## 1. `PersonTeamPicker` — the only subject picker

**Where:** `web/src/components/PersonTeamPicker.tsx`

```ts
export type PickedSubject =
  | { kind: 'user'; id: string; label: string; email?: string }
  | { kind: 'team'; id: string; label: string };

<PersonTeamPicker
  orgId={orgId}
  subjects="user" | "team" | "both"   // default "both"
  value={picked} onChange={setPicked}
  placeholder? disabled? testId?      // testId default "person-team-picker"
/>
```

Searches people by **name or email** through the member-search endpoint (`useMemberSearch`) and
teams by **name or slug** over the org team list (`useTeams`), and returns a typed subject. It was
built in P2.5 as "the reusable replacement for every free-text UUID field."

> **The rule: there must never be a second picker.** Nobody knows a UUID and nobody should have to
> type one. Any surface that needs to *search* for a person or a team uses this component. If it
> does not fit, extend it; do not fork it.

**Current consumers** — `ShareDialog.tsx` (team audience), `SpaceSettingsPage.tsx` (grant
subject), `TeamsAdminPage.tsx` (team member).

**Not yet using it**, and worth knowing before you assume coverage is complete: three admin
surfaces still select a team through a plain `<select>` over `useTeams()` — the invite's initial
team and a person's primary team (`PeoplePage.tsx`), and a space's owner team
(`SpacesAdminPage.tsx`). Those are bounded single-team dropdowns rather than searches, so they do
not violate the rule as stated, but they are the obvious consolidation candidates and they are
where a fourth pattern would most easily creep in.

---

## 2. `friendlyErrorMessage` — the only route from an error to the UI

**Where:** `web/src/lib/api.ts`

```ts
export function friendlyErrorMessage(err: unknown, fallback: string): string
```

Messages behind `VALIDATION_ERROR`, `CONFLICT` and `GONE` are written for humans on the server and
pass through. **Everything else** — malformed-request internals, server errors, network failures —
collapses to the caller's `fallback`, which must say what failed in the user's terms.

(`GONE` is currently inert: the backend has no `CodeGone`, and every `410` site — expired invites,
expired shares — responds with code `CONFLICT`. Harmless, but do not assume a `GONE` code exists
on the wire because this list names it.)

> **The rule: no raw backend string ever reaches a user.** This is not a style preference; it is a
> regression the E2E suite actively hunts.

`assertNoErrors(page)` in `web/e2e/helpers/setup.ts` is called 20 times across 8 spec files, after
navigation, and fails if any of these are visible:

```
"Something went wrong"   "Failed to load"   "invalid space_id"
"invalid request body"   "UNAUTHORIZED"
```

Rendering an `APIError`'s `.message` directly into a component is the exact defect this catches.
The most recent instance was fixed in the space-create dialog after migration 028 started
returning a constraint-name-driven conflict.

---

## 3. `ContentTxAdapter` — transactional content operations

**Where:** `internal/db/adapters/content_tx.go`

```go
func NewContentTxAdapter(pool *pgxpool.Pool) *ContentTxAdapter

func (a *ContentTxAdapter) MovePageTx(ctx, wiki.MovePageInput) (wiki.MovePageTxResult, error)
func (a *ContentTxAdapter) DeletePageAndRevokeShares(ctx, pageID, actorID) (int64, error)
func (a *ContentTxAdapter) DeleteTicketAndRevokeShares(ctx, ticketID, actorID) error
func (a *ContentTxAdapter) DeleteItemAndRevokeShares(ctx, itemID, actorID) error
```

It owns a pool, opens the transaction itself (`Begin` / `defer Rollback` / `WithTx`), and runs the
entity mutation, the share revocation and the `share.revoked` audit write inside it — tagged with a
reason, `entity_moved` or `entity_deleted`.

Domain services reach it through narrow per-domain interfaces rather than depending on the adapter:
`wiki.ContentTxStore` (two methods), and the equivalent seams on the ticket and project-item
services. All three are wired in `cmd/server/main.go`.

> **The rule: any operation whose correctness depends on shares being revoked with the entity goes
> through this adapter.** ADR-0008 rules 9 and 10 — move revokes shares, delete revokes shares —
> are atomicity contracts, not sequencing preferences. A handler that deletes a page and then
> revokes its shares in a second call has a window in which a deleted page is still org-readable.

---

## 4. Audit write conventions — there are two, and only two

Audit rows go to the append-only `audit_log` (`entity_kind`, `entity_id`, `payload`; never
UPDATE, never DELETE).

**Convention A — handler layer. The default.** The handler performs the mutation, then writes the
audit event. Use this everywhere unless B applies. Most team, grant, membership, visibility and
share events are written this way.

**Convention B — adapter layer, inside the transaction.** Used only where the audit trail is part
of an atomicity contract:

- **P2.5's bulk grants** — one atomic apply of a computed diff, correlated by `batch_id`
  (migration 025) so the viewer can render the batch as a single expandable row. A partially
  audited bulk change is worse than none.
- **P3's revoke-on-move and revoke-on-delete** — the `share.revoked` rows are written in the same
  transaction as the revocation, via `ContentTxAdapter`. If the transaction rolls back, the audit
  claim that access was removed must roll back with it.

> **The rule: two conventions exist; a third should not.** If you are tempted to invent a
> "sometimes-transactional" or deferred-queue variant, the question to answer first is whether the
> trail is part of an atomicity contract. If yes, use B. If no, use A.

---

## 5. Route guard classes, and the test that enforces them

**Where:** the table and the test live in `internal/core/api/route_accounting_test.go`.

Every route carries **exactly one** guard class:

| Class | Meaning |
|---|---|
| `public` | Unauthenticated by design. A reason is required in the table. |
| `user-scoped` | `RequireAuth`; data filtered by caller identity. |
| `org-member` | `RequireAuth` + `ResolveAccess`; 404 for non-members. |
| `org-admin` | org-member + `RequireOrgAdmin` on the mutation. |
| `org-admin-404` | org-member + `RequireOrgAdmin404`. The P2.5 administration surface: non-admins get **404, never 403**, because the surface's existence is itself privileged. |
| `space-read` | org-member + `RequireSpaceInOrg` + `RequireSpaceReadable` (404). |
| `space-write` | space-read + the `create_items` write floor (403), with handler-level refinement above the floor. |
| `space-cap` | space-read + handler-enforced capability (`manage_space`, `manage_grants`). |
| `share-manage` | org-member; the handler resolves the shared entity to its space and enforces `manage_shares` there — read check first, so an unreadable space 404s and a readable-but-uncapable space 403s. Org-scoped because a share names an entity, not a `{spaceID}` in the URL. |
| `share-read` | org-member + `ResolveShares`. Authorised by an active, unexpired, unrevoked share whose audience includes the caller — **not** by space access. The one family that reaches content without space-readability, by design (ADR-0008). 404 for both "no such entity" and "not shared with you", so it leaks neither existence nor shared-ness. |

**The enforcing test:** `TestReadPathSweep_EveryRouteAccounted`. It walks the fully wired chi
router — never a hand-maintained list — and fails **bidirectionally**: on any route present in the
router but missing from the table, and on any table row whose route no longer exists. It also
asserts at least 90 routes were enumerated, so a broken walk cannot pass vacuously. The table
currently holds 142 rows, keyed `"METHOD /path"` — mostly under `/api/v1`, except `/health`,
`/ready`, `/api/docs` and `/api/docs/openapi.yaml`.

Note what the test does and does not do: it proves every route is **classified**, not that each
route's middleware chain matches the class it claims. The classification is a human assertion the
test keeps honest by refusing to let you skip it.

> **The rule: a new route fails this test until you classify it.** That is the point. Classifying
> is the moment you decide whether the route leaks existence, and it is much harder to get wrong
> deliberately than by omission.

---

## 6. Server-side counts for confirmation dialogues

Endpoints exist purely to supply numbers a user is asked to confirm against:

- `GET /api/v1/orgs/{orgID}/spaces/{spaceID}/summary` — `space-cap: manage_space`. Returns three
  contents counts for the delete-confirmation dialogue, each computed with `deleted_at IS NULL`.
- `GET /api/v1/orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/share-impact` — `space-read`. The count
  of active shares a cross-space move would revoke (ADR-0008 rule 9's UI warning).
- `cascade_page_count`, served from the shares handler — the affected page count shown before
  confirming a cascade share (ADR-0008 rule 7).

> **The rule: counts shown before a destructive or widening confirmation come from the API, never
> from counting what the client happens to have loaded.** A client-side count is a count of the
> current page of results. Confirming "this will affect 12 pages" when the real number is 400 is
> the failure mode, and it is silent.

Consumers: `ShareDialog.tsx`, `MovePageDialog.tsx`, `SpacesAdminPage.tsx`.

---

## 7. `TestMatrixAPI23` — the query-constancy tracer

**Where:** `internal/core/api/permission_matrix_integration_test.go`

Two tests, both driving a real `pgxpool` with a tracer attached that counts queries:

- `TestMatrixAPI23_ConstantAuthQueries` — lists tickets at N=3 and N=30 and asserts the query
  count is **identical**, plus a hard ceiling (`≤ 8`) on the per-request budget. It then repeats the
  constancy check on the space directory at 2 spaces vs 12; that half asserts constancy only, with
  no ceiling.
- `TestMatrixAPI23_ShareResolutionConstantQueries` — a shared read by an outsider with no space
  access, before and after adding 25 more org shares. Share resolution must not grow with the
  number of shares.

Both also assert, on every measured request:

```go
require.Equal(t, int64(1), counter.authState.Load()-authBefore,
    "exactly one GetUserAuthState read per authenticated request")
```

> **The rule: this test must not be relaxed, and the two halves of it fail for opposite reasons.**
>
> - **Exactly one** auth-state read. **Zero** would mean the revocation check was optimised away —
>   stateless RS256 tokens would then outlive deactivation, which is precisely the hole
>   `token_generation` exists to close. **More than one** means the single-constant-lookup contract
>   broke.
> - **A constant total.** A list endpoint issuing one authorisation query per row is a defect
>   regardless of output correctness (§2.5 case 23). It passes every functional test and falls over
>   in production.
>
> Each request is measured after a warm request, so connection setup and caches do not pollute the
> count. If a change makes this test fail, the change is wrong far more often than the test is.

Cross-space work in P4 and P6 must extend this tracer to its new endpoints rather than assume the
existing coverage carries over.

---

## 8. `users.token_generation` — session invalidation

**Where:** column added in `024_invites_session_control.sql`; carried in the JWT as the `tgen`
claim; compared by the auth middleware.

RS256 access tokens are stateless, so without this a deactivated user stays authenticated until
their token expires. The middleware reads the column in a single primary-key lookup and rejects any
mismatch. **Incrementing the column instantly kills every token that user holds.**

Incremented on:

- **deactivation**
- **force logout**
- **password change**

Existing users start at `0`, matching the claim on tokens minted after the release; pre-existing
tokens carry no claim and decode as `0`, so the upgrade itself disrupted no session.

> **The rule: deactivation is never optional and never deferred.** "Deactivated but still holding a
> valid token" is not a degraded state, it is an open door. This is also why the one-read-per-request
> assertion in `TestMatrixAPI23` is not a performance nicety — deleting that read would silently
> restore the hole.

---

## Related

- Decisions: [`../adr/`](../adr/) — ADR-0007 for the capability model, ADR-0008 for share rules.
- The specification: [`v0.3-ia-spec.md`](v0.3-ia-spec.md) — §2 testing, §5 resolution, §10
  non-negotiables.
- What the repository actually contains vs what the spec claimed:
  [`spec-repo-reconciliation.md`](spec-repo-reconciliation.md).
