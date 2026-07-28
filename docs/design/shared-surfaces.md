# Shared surfaces

**What this is.** The components, helpers and patterns that P2.5 and P3 built for everyone to
reuse, and the rule attached to each. None of them were documented, so the only way a later
session learned they existed was by tripping over them — or by building a second one.

**How to use it.** Before you build a picker, an error path, a confirmation count, a route guard
or a transactional write with an audit trail, look here first. If something close to what you need
already exists, extend it. A second implementation of anything on this page is a defect, not a
convenience.

Verified against `main` at migration 028; sections 9 and 10 added at migration 036.

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

`assertNoErrors(page)` in `web/e2e/helpers/setup.ts` is called after navigation across the E2E
suite, and fails if any of these **six** strings is visible:

```
"Something went wrong"   "Failed to load"        "could not be loaded"
"invalid space_id"       "invalid request body"  "UNAUTHORIZED"
```

`"could not be loaded"` is the one most easily tripped by accident, because it is also the shape
most `friendlyErrorMessage` fallbacks take — the interior restyle routed every load failure
through copy reading "… could not be loaded." New fallback copy on any path an E2E spec navigates
through must avoid all six. (**D53**: this list said five until issue #15; the helper has asserted
six since the interior restyle.)

Rendering an `APIError`'s `.message` directly into a component is the exact defect this catches.
The most recent instance was fixed in the space-create dialog after migration 028 started
returning a constraint-name-driven conflict.

**One sanctioned exception, and only one.** The Codex publish route answers its two 409s with a
bare `ConflictDetail` or `LostContentDetail` object rather than an error envelope, so they carry
no code at all and `friendlyErrorMessage` would collapse them into a fallback. Those `message`
fields are prose written server-side for exactly one dialog — "this text IS the dialogue the
author reads", `internal/core/api/wiki/document_handler.go` — and they name the versions and
counts involved, so restating them client-side would mean maintaining the same sentence in two
languages and letting them drift. They are shown verbatim, from the two typed errors
(`PublishConflictError`, `PublishLostContentError`) only. Everything that arrives as an `APIError`
still goes through `friendlyErrorMessage`, including this route's 422s — which do carry
`VALIDATION_ERROR`, so they pass through it anyway.

---

## 3. `ContentTxAdapter` — transactional content operations

**Where:** `internal/db/adapters/content_tx.go`

```go
func NewContentTxAdapter(pool *pgxpool.Pool) *ContentTxAdapter

func (a *ContentTxAdapter) MovePageTx(ctx, wiki.MovePageInput) (wiki.MovePageTxResult, error)
func (a *ContentTxAdapter) DeletePageAndRevokeShares(ctx, pageID, actorID) (int64, error)
func (a *ContentTxAdapter) DeleteTicketAndRevokeShares(ctx, ticketID, actorID) error
func (a *ContentTxAdapter) DeleteItemAndRevokeShares(ctx, itemID, actorID) error
func (a *ContentTxAdapter) UpdatePageContentTx(ctx, wiki.UpdatePageInput) (generated.Page, error)
func (a *ContentTxAdapter) PublishPageTx(ctx, wiki.PublishPageTxInput) (generated.Page, error)
```

The last two carry no share invariant. They are here because they are the other kind of thing this
adapter owns: a content write whose parts have to land together. See sections 10 and 12.

Space creation has the same shape and its own adapter, `SpaceCreateAdapter`
(`internal/db/adapters/space_create_tx.go`), reached through `spaces.CreateTxStore`: the space row,
the creator's `space_members` row and — for a non-org-admin creator — the creator's `space_admin`
grant commit together, so a failure cannot leave an orphaned space holding the slug and key. The
grant inside that transaction goes through the real `access.GrantService`, bound to the
transaction's queries by `AccessAdapter.withTx`, rather than a second hand-written INSERT.

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

---

## 9. `wiki/doc` — the only rich-text document model

**Where:** `internal/core/wiki/doc/`, with its vocabulary in `internal/core/wiki/doc/schema.json`.

```go
func Shield(document json.RawMessage) (Shielded, error)
func Restore(document json.RawMessage, base Shielded) (Restored, error)
func FromMarkdown(markdown string) (json.RawMessage, error)  // the legacy on-ramp
func ToMarkdown(document json.RawMessage) (string, error)    // the search projection
func Validate(document json.RawMessage) error
```

Built for issue #15 under ADR-0012. `Shield` rewrites every node and mark type outside
`schema.json` into a placeholder the editor's schema *does* define, and hands back the verbatim
originals keyed by placeholder id; `Restore` splices those exact byte slices back. The guarantee is
one sentence: **the bytes written back are the bytes that were read, and they never pass through the
client.** A placeholder carries a display copy of its original in `az_raw` so the editor can label
the block; `Restore` resolves from the captured map and ignores it.

> **The rule: there is one document model, and content outside its schema is preserved, never
> dropped.** ProseMirror silently discards content that does not match its schema, so a rich-text
> surface built without this loses data quietly — one page at a time, months after the import that
> introduced it. ADR-0012 exists specifically to reach issue #15 before that was built. Any later
> surface that wants rich text — a Beacon ticket description, a Vector item body — extends this
> package and this schema. A second document model, or a schema that "just" drops what it does not
> recognise, is the defect ADR-0012 was written to prevent.

Three things about it that are easy to get wrong twice:

- **`encoding/json` must not serialise a document node.** `json.Marshal` compacts a
  `json.RawMessage` and escapes `<`, `>` and `&` inside it. Both are value-preserving and
  byte-mangling, and a preserved Confluence macro is mostly angle brackets. The package assembles
  nodes by splicing member bytes itself, and `marshalPlain` is its non-escaping encoder.
- **Placeholder ids are positional and therefore stable**, which is what lets a read and a later
  write agree on them with no server-side session state: publish re-derives the base document at
  `base_version` and re-shields it. `FromMarkdown` is deterministic for the same reason — a legacy
  page's ids come from converting the same markdown twice.
- **The storage column is `json`, not `jsonb`.** See migration 036, and the test that fails if
  anybody changes it.

**Current consumers** — `internal/core/wiki` (`DocumentService`), `internal/core/attachments`
(image sniffing, via `doc.SniffImageType` / `doc.SupportedImageType`), and the editor
(section 11).

---

## 10. `PublishPageTx` — content, history and draft in one transaction

**Where:** `internal/db/adapters/publish_tx.go`, on the existing `ContentTxAdapter` (section 3).

Publishing a Codex page is three writes that have to be one: the page row, the `page_revisions` row
for the version just created, and the removal of the draft that was published. It is reached through
`wiki.DocumentTxStore`, a one-method per-domain interface, exactly as `wiki.ContentTxStore` reaches
the move and delete transactions.

> **The rule: the version guard belongs in the UPDATE, not in a SELECT before it.** Two simultaneous
> publishes can both pass a pre-check and then both write. `PublishPageDocument` carries
> `WHERE version = $2`, so the loser gets zero rows and is told it conflicted.
> `OverwritePageDocument` is the guard-free counterpart, and is reachable only from a caller that has
> already reported the conflict and been told, in those words, to overwrite it.

The same reasoning that put share revocation in a transaction (ADR-0008 rules 9 and 10) applies
here: a page whose history skips a version, or a draft that reappears as unpublished work the author
already published, is not fixable by retrying.

**The asymmetry with the older path is gone.** `wiki.Service.UpdatePage` — the markdown save —
used to update the page and insert its revision as two separate pool calls, and to leave `doc`
stale on a document-backed page. Both were recorded as defects (D54 and the section 3 entry) and
both are closed: the save now runs through `ContentTxStore.UpdatePageContentTx`, one transaction,
and refuses a page where `doc IS NOT NULL` with 409 rather than writing half a representation.

> **The rule: three refusals, decided under one row lock.** The transaction takes
> `GetPageForUpdate` first, so "zero rows affected" never has to be guessed at afterwards by a
> second read — gone is `ErrPageNotFound`, holds a document is `ErrPageIsDocumentBacked`, version
> moved on is `ErrVersionConflict`. The document test is strictly `doc IS NOT NULL`: a page that
> has only ever held markdown, including one open in the editor but never published, keeps taking
> markdown saves.

---

## 11. The Codex editor — one extension set, one renderer, one page picker

**Where:** `web/src/components/codex/`.

```ts
codexExtensions(): AnyExtension[]        // extensions/index.ts — the registered vocabulary
registeredTypes(exts): {nodes, marks}    // derived from a real ProseMirror schema
<CodexEditor …>                          // the editing surface
<CodexDocRenderer …>                     // the reading surface: the same thing, not editable
<PagePicker …>                           // the only "choose a page in this space" control
```

Built for issue #15 PR-B on the model in section 9.

> **The rule: the editor's registered vocabulary equals `schema.json`, in both directions, and a
> test proves it against a real schema.** A type the editor registers but the manifest omits is
> merely preserved when it need not have been — annoying, safe. A type the manifest names and the
> editor does **not** register is silent data loss: the server stops capturing it because the
> schema says the editor handles it, and ProseMirror drops it on load before anything server-side
> can notice. `web/src/lib/codex/schema.test.ts` compares the TypeScript mirror to the Go
> manifest; `extensions/extensions.test.ts` compares a schema built from the real extension list
> to that mirror. Both fail in both directions.
>
> The concrete case is not hypothetical: StarterKit ships `underline`, which `schema.json` does
> not name, so it is explicitly disabled. Re-enabling it is a one-word change.

Four things about it that are easy to get wrong:

- **The reading surface is the editor with `editable: false`.** Not a second renderer. A separate
  read path drifts, and the first thing to drift is the labelling of preserved blocks — which is
  precisely what ADR-0012 section 2 requires a reader to see.
- **The reader's document comes from `GET …/{pageID}/document`, never from `WikiPage.doc`.** The
  stored document still contains types outside the editor's schema; only the `/document` route
  shields them into labelled placeholders. Reading it needs space-read, not edit.
- **Macro attribute names are a contract with `doc.ToMarkdown`,** which reads `kind`, `title`,
  `text`, `page_id`, `language` and `attachment_id` by name to build the markdown that feeds the
  generated `search_vector`. Renaming one breaks nothing at runtime — the page publishes, the
  document stores correctly, and the content quietly stops being findable.
- **`base_version` is fixed for the life of an editing session,** including through an overwrite.
  The preservation ids were minted against that version and publish re-derives it to resolve them.

**Current consumers** — `web/src/pages/codex/WikiPage.tsx`, `web/src/pages/codex/DraftsPage.tsx`.
Any later rich-text surface — a Beacon ticket description, a Vector item body — extends this set
and this schema rather than starting a second one.

## 12. `fetchObjectURL` — the only way server bytes reach the browser

**Where:** `web/src/lib/api.ts`.

```ts
async function fetchObjectURL(path, unavailable): Promise<string>   // internal
export async function fetchPageImageObjectURL(spaceId, attachmentId): Promise<string>
export async function fetchSharedAttachmentObjectURL(orgId, entityType, entityId, attachmentId): Promise<string>
```

Every binary route authenticates from the `Authorization` header or a `session` cookie, and this
frontend holds a bearer token in localStorage and sets **no cookie** — nothing in
`internal/core/api/auth` calls `http.SetCookie`. So a URL handed straight to the browser is fetched
with no credential at all and answered 401.

> **The rule: a route that streams bytes gets a fetch-and-object-URL helper, never a URL builder.**
> The failure is silent in both places it can happen. In an `<img src>` it is a broken-image icon
> and no error anywhere; in an `<a href download>` it is a saved file whose contents are a JSON
> error. That is how the shared entity page shipped with neither its images nor its downloads
> working, behind a URL builder whose own comment claimed "the browser fetches it with the session
> cookie" (S8). `sharedAttachmentURL` is gone; the shape that produced the bug is no longer
> available.

Callers own the lifetime: resolve, use, `URL.revokeObjectURL` on teardown, and revoke immediately
if the component unmounted while the request was in flight. `web/src/components/codex/nodeviews/ImageView.tsx`
and `web/src/pages/shared/SharedAttachment.tsx` are the two examples.

**Current consumers** — the Codex image node view, and the shared entity page's previews and
download links.

---

## 13. The Swagger UI assets are vendored

**Where:** `internal/core/api/swaggerui/`, served from `/api/docs/assets/`.

swagger-ui-dist is embedded in the binary (Apache 2.0, licence and third-party notices alongside
it), not loaded from a CDN.

> **The rule: a self-hosted product fetches nothing from the internet to render its own pages.** An
> isolated network is the normal deployment, not the exception, and there a CDN reference is a blank
> screen with no explanation. On a connected one it is third-party code, resolved from a floating
> version tag, executing in an authenticated administrator's browser on the origin that holds their
> session. `TestDocsEndpoint_LoadsNoExternalAssets` fails on any absolute URL in the page.

---

## Related

- Decisions: [`../adr/`](../adr/) — ADR-0007 for the capability model, ADR-0008 for share rules.
- The specification: [`v0.3-ia-spec.md`](v0.3-ia-spec.md) — §2 testing, §5 resolution, §10
  non-negotiables.
- What the repository actually contains vs what the spec claimed:
  [`spec-repo-reconciliation.md`](spec-repo-reconciliation.md).
