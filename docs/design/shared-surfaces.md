# Shared surfaces

**What this is.** The components, helpers and patterns that P2.5 and P3 built for everyone to
reuse, and the rule attached to each. None of them were documented, so the only way a later
session learned they existed was by tripping over them — or by building a second one.

**How to use it.** Before you build a picker, an error path, a confirmation count, a route guard
or a transactional write with an audit trail, look here first. If something close to what you need
already exists, extend it. A second implementation of anything on this page is a defect, not a
convenience.

Verified against `main` at migration 028; sections 9 and 10 added at migration 036; section 13
and the section 5 corrections added at migration 038 (P4 saved views); sections 15, 16 and 17
added at migration 048 (P5 dashboards).

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
subject), `TeamsAdminPage.tsx` (team member), `ViewBuilderPage.tsx` and `QueryFilterBuilder.tsx`
(person filter values), `DashboardSettingsDialog.tsx` (team visibility), and
`pages/admin/workflow/TransitionRules.tsx` (a transition's approvers and its `assign_to`
post-function).

**The multi-select pattern.** The picker is SINGLE-select and stops rendering its input once a
value is held, so a list of subjects — an approver set, a filter's value list — drives it with
`value={null}` permanently, appends inside `onChange`, and renders its own chips.
`QueryFilterBuilder` established it and `TransitionRules` follows it. That is reuse, not a fork:
the rule in this section is that a second picker is a defect, not that every consumer must want
exactly one subject.

One thing an approvers list does NOT need from that pattern is `QueryFilterBuilder`'s
`personLabels` cache. That workaround exists because a saved filter stores an id with no name
beside it; `workflow.Approver` already carries a read-time-resolved `subject_name` and
`subject_missing`, so reproducing the cache would be solving a problem the API answered.

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

Messages behind `VALIDATION_ERROR`, `CONFLICT`, `GONE` and `INVALID_TRANSITION` are written for
humans on the server and pass through. **Everything else** — malformed-request internals, server
errors, network failures — collapses to the caller's `fallback`, which must say what failed in the
user's terms.

(`GONE` is currently inert: the backend has no `CodeGone`, and every `410` site — expired invites,
expired shares — responds with code `CONFLICT`. Harmless, but do not assume a `GONE` code exists
on the wire because this list names it.)

(`INVALID_TRANSITION` is the opposite case, and was missing from this list until 2026-07-31. It is
genuinely on the wire: `respond.CodeInvalidTransition` is emitted with a 409 from two handlers,
`projects` and `tickets`. It joined the pass-through list with the workflow admin surface, because
it is the state machine explaining its own refusal — "cannot move from Closed to In Progress" —
and collapsing that into the caller's fallback told the user only that *something* failed, on the
one screen where the reason is the point. Note the tier guards themselves answer 422
`VALIDATION_ERROR` rather than this code. The JSDoc above `friendlyErrorMessage` still names only
three codes and is ledgered for correction.)

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
| `org-read` | org-member; a member-visible read of org-wide metadata (item types, tags, custom-field definitions) that is not a space-scoped resource. |
| `org-admin` | org-member + `RequireOrgAdmin` on the mutation. |
| `org-admin-404` | org-member + `RequireOrgAdmin404`. The P2.5 administration surface: non-admins get **404, never 403**, because the surface's existence is itself privileged. |
| `space-read` | org-member + `RequireSpaceInOrg` + `RequireSpaceReadable` (404). |
| `space-write` | space-read + the `create_items` write floor (403), with handler-level refinement above the floor. |
| `space-cap` | space-read + handler-enforced capability (`manage_space`, `manage_grants`). |
| `share-manage` | org-member; the handler resolves the shared entity to its space and enforces `manage_shares` there — read check first, so an unreadable space 404s and a readable-but-uncapable space 403s. Org-scoped because a share names an entity, not a `{spaceID}` in the URL. |
| `share-read` | org-member + `ResolveShares`. Authorised by an active, unexpired, unrevoked share whose audience includes the caller — **not** by space access. The one family that reaches content without space-readability, by design (ADR-0008). 404 for both "no such entity" and "not shared with you", so it leaks neither existence nor shared-ness. |
| `portal-session` | **Not** org-member: the caller is an external requester outside the capability model entirely, so `access.Can` is never consulted. `RequirePortalSession` (audience-verified token plus a live `session_generation`) plus queries scoped to the requester's own rows. The only route family reachable from the public internet by someone with no account. |

*`org-read` and `portal-session` were added to this table on 2026-07-31; the vocabulary the sweep
enforces has twelve classes and this table listed ten. `portal-session` was absent from the whole
document, not just the table. Note the same two are missing from the doc comment above
`guardClasses` in `route_accounting_test.go`, which is very likely where this table was copied
from — that in-code copy is the more load-bearing of the two and is ledgered for correction.*

**The enforcing test:** `TestReadPathSweep_EveryRouteAccounted`. It walks the fully wired chi
router — never a hand-maintained list — and fails **bidirectionally**: on any route present in the
router but missing from the table, and on any table row whose route no longer exists. It also
asserts at least 90 routes were enumerated, so a broken walk cannot pass vacuously. The table
holds **217 rows** as of P6 cross-module search, keyed `"METHOD /path"` —
mostly under `/api/v1`, except `/health`, `/ready`, `/api/docs` and `/api/docs/openapi.yaml`.

**Count the map; do not quote that number.** It has now been wrong four times: 142 until P4 counted
them, 172 until the pre-cutover pass did, 185 until P6 did — 185 was already stale before P6 started,
by the whole #85-#92 wave — and 172 was already stale before the pre-cutover pass started — P4 PR-B's queue
rows, the Codex UX pass, and this one all landed after the sentence was last written. A figure three
consecutive phases have had to correct does not belong in prose, and it is recorded here only
because deleting it outright would leave a reader wondering whether the table is ten rows or two
hundred. The repository wins; `routeAccounting` is the repository.

Note what the test does and does not do: it proves every route is **classified**, not that each
route's middleware chain matches the class it claims. The classification is a human assertion the
test keeps honest by refusing to let you skip it.

A second test, `TestReadPathSweep_GuardClassMatchesMiddleware`, *does* read the real chain — but
only for **three** classes *(this said two until 2026-07-31)*.

- **`org-admin-404`** and **`portal-session`** are checked in **both** directions: a row claiming
  the class whose chain lacks the guard fails, and a chain carrying the guard under any other
  claim fails.
- **`org-admin`** is checked in **one** direction only — a row claiming it without
  `RequireOrgAdmin` fails, but nothing catches a chain carrying `RequireOrgAdmin` under a weaker
  claim. (The two cannot alias: `carries` matches `"."+guard+"."`, which is what keeps
  `RequireOrgAdmin` from matching `RequireOrgAdmin404` frames.)

Two subtrees additionally carry a **prefix rule**, reached through the chain rather than the row,
so an honest-looking row cannot satisfy it. Every route under
`/orgs/{orgID}/users|invites|grants|audit-log/` must carry `RequireOrgAdmin404` unless listed in
`deliberateNonAdminRoutes` with a reason; every route under `/portal/{portalKey}/my/` must carry
`RequirePortalSession` unless listed in `deliberatePublicPortalRoutes` with a reason. That second
map is **deliberately empty** — a new public portal route belongs outside `/my/`, not inside it
with an exemption.

Every other class — `space-read`, `space-write`, `space-cap`, `org-member`, `org-read`,
`share-manage`, `share-read`, and also `public` and `user-scoped` — is accepted on the strength of
the row alone, and its middleware is never inspected. For those, the guard is proved by an
explicit permission-matrix test or it is not proved at all.
`views_endpoint_matrix_integration_test.go` (P4) is the pattern to copy.

> **The sweep and the dark-harness test had a seam between them, and P4 closed it.** Both walk the
> router built by `newTestServerOn`. `TestHarness_NoDarkDependencies` deliberately *skips* a nil
> handler, on the stated grounds that "the route-accounting sweep already covers" it — but a
> handler left nil in the harness contributes no routes to that walk, so it needs no accounting
> rows and is invisible to **both** tests at once. That is the dark-harness failure one level up:
> not a mounted handler missing a collaborator, but an entire feature that exists in production and
> in no test. P4 walked into it — the saved-view routes were added, the sweep stayed green, and
> nothing said the routes simply were not mounted.
>
> `TestHarness_NoUnmountedSurfaces` closes it: every `RouterConfig` handler field is mounted in the
> harness, or named in `unmountedInHarness` **with its reason**.

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
codexExtensions(opts?): AnyExtension[]   // extensions/index.ts — the registered vocabulary
registeredTypes(exts): {nodes, marks}    // derived from a real ProseMirror schema
<CodexEditor …>                          // the editing surface
<CodexDocRenderer …>                     // the reading surface: the same thing, not editable
<PagePicker …>                           // the only "choose a page in this space" control
filterPages / findPageByTitle            // pageSearch.ts — the one page lookup
codexMeasureClasses                      // editorStyles.ts — the one document measure
markdownPasteContent(text)               // lib/codex/markdownPaste.ts — the one paste converter
tagBrowsePath(module, spaceId, label)    // components/tags/tagLinks.ts — where a tag chip goes
<EntityTags …>                           // the only tag chip and tag editor (pages, tickets, items)
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

The vocabulary guard has a **fourth link**, added with the Obsidian affordances: `projectedAttrs`
in `schema.json` names the ATTRIBUTES `doc.ToMarkdown` reads. An attribute that drifts fails
differently from a type — nothing breaks, the page publishes, the document stores correctly, and
the content quietly stops being findable. `schema.test.ts` holds the TypeScript mirror equal to
that manifest, `extensions.test.ts` asks the real ProseMirror schema whether it declares every
attribute the manifest names, and `projection_attrs_test.go` projects a document carrying each one
and looks for its effect — with a mutation pass that removes the attribute and requires the
expectation to break, so a case that asserts nothing about its attribute fails as a test rather
than passing as coverage.

Nine things about it that are easy to get wrong:

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
- **There is one page lookup, in `pageSearch.ts`.** `filterPages` and `findPageByTitle` back the
  picker, the `[[` autocomplete and the create-on-click dialogue. Permission filtering is
  *inherited* — the candidate list is the space's page list, which the API already filtered — so
  there is no second client-side check and, deliberately, no second server search. Resolution is
  exact-match and case-insensitive: a prefix match would silently link `[[Runbook]]` to "Runbook
  archive (2019)", and a link that goes somewhere the author did not name is worse than one that
  goes nowhere and offers to create the page.
- **There is one document measure, `codexMeasureClasses`,** and it wraps the reader, the editor and
  the drafts list alike. The reader used to be pinned to a fixed 76ch while the editor had no
  constraint at all, so the same paragraph broke in different places on either side of one click.
  The clamp itself is the `--codex-measure` token; below it the surface is fluid.
- **A pasted markdown block is converted by `markdownPasteContent`, and its dialect is TESTED, not
  asserted.** `internal/core/wiki/doc/markdown_corpus.json` holds one sample of each construct with
  the document it must produce, generated from the server's own goldmark converter; the Go and the
  TypeScript converters are each checked against those bytes. Anything outside the corpus stays
  plain text — never a preservation placeholder, because an id minted in the browser resolves to
  nothing and publish refuses the entire write.
- **A link mark has three states, not two.** `href` leaves Azimuthal, `page_id` resolves to a page,
  and `target_title` names a page nobody has written yet. Never invent an href for the third: it
  would render as a link that goes nowhere.

**Current consumers** — `web/src/pages/codex/WikiPage.tsx`, `web/src/pages/codex/DraftsPage.tsx`,
`web/src/pages/codex/TagPage.tsx`.
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

## 13. Cross-container reads, and the one sanctioned share-union exception

**Where:** `internal/db/queries/saved_views.sql` (`ListViewTickets`, `ListViewProjectItems`),
reached through `internal/core/views.Resolve`.

**The rule this excepts.** A space-scoped listing filters on `space_id = ANY(readable)` and
**never unions shares**. That is what keeps ADR-0008 cheap: shares widen visibility for one
entity, the main query path is untouched, and no list has to do per-row permission work
(spec §5, and §2.5 case 23 forbids per-item authorisation inside a list handler).

**The exception.** A saved view is cross-container by nature — that is the entire feature — so
its two result fan-outs are the sanctioned place where the readable-space set is unioned with the
caller's shared entities:

```sql
WHERE tk.space_id = ANY(@readable_space_ids) OR tk.id = ANY(@shared_ticket_ids)
```

It is recorded here for the same reason the Codex publish-409 exception is recorded in section 2:
so the next person who finds a share union in a list query can tell a sanctioned exception from a
mistake, and so a *second* one has to be argued for rather than copied.

Four things about it that are load-bearing:

- **Per viewer, always.** A shared view shares the definition, never the results. Two people
  opening one view legitimately see different rows, and a viewer with less access silently sees
  fewer. That is correct behaviour, not a bug to smooth over, and the UI must never present it as
  a sync failure. Nothing in the resolution consults the view's *owner* — the owner's access is
  irrelevant to what a viewer may read.

- **There is no subtree term, and its absence is correct.** Migration 026 constrains cascade to
  pages (`entity_shares_cascade_pages_only`), so a ticket or project-item share is always exactly
  one entity and `SharedEntities.DirectIDs(type)` is the complete story for both tables. The
  `(space_id, pattern)` pair problem spec §5 warns about — and the accessor it says "P4 must build
  first" — is a **page** problem. P6 search reads pages and still has to solve it; **D46 stays
  open**. Do not copy this query's shape onto pages.

- **Share resolution is mounted per route, not per family.** `ResolveShares` is applied to
  `GET /views/{viewID}/results` and `POST /views/preview` and to nothing else under `/views`:
  listing or editing a view has no use for share coverage and must not pay a query for it. This is
  the same discipline §5 states for P3's `/shared` subtree — mount it where it is needed and
  re-run the case-23 constancy tracer, rather than promoting it to the org-wide middleware where
  every route starts paying.

- **The readable set and the shared set ARE the access control**, not a hint. Neither array may be
  widened by a caller, and the service short-circuits to an empty page when both are empty rather
  than relying on `= ANY('{}')`. `TestViewResults_TicketInUnreadableSpaceDoesNotLeak` fails if the
  space predicate is weakened; `TestViewResults_ShareUnionReachesAnUnreadableSpace` fails if the
  share term is dropped.

**Two schema functions belong to this surface** (migration 038), and both exist so a rule is
written once:

- `saved_view_sort_key(...)` collapses whichever of the six sortable fields a view uses into one
  comparable text value, so one static query serves every field in both directions and the API
  layer can merge the two modules by comparing the same key type (ADR-0009: fan out per module,
  merge in the API layer). Callers apply `COLLATE "C"` so PostgreSQL's ordering and Go's bytewise
  string comparison are the **same** ordering — on a title sort a linguistic collation would
  interleave two correctly-sorted halves incorrectly, and only across a page boundary.
- `effective_team_ids(org, user)` is ADR-0007's subject-side team expansion, named. The same rule
  is still written inline inside `ResolveAccessRows`; that copy was deliberately left alone
  because it is the product's hottest query and the subject of the case-23 tracer.
  `TestEffectiveTeamIDs_AgreesWithGrantResolution` pins the two together.

---

## 14. The Swagger UI assets are vendored

**Where:** `internal/core/api/swaggerui/`, served from `/api/docs/assets/`.

swagger-ui-dist is embedded in the binary (Apache 2.0, licence and third-party notices alongside
it), not loaded from a CDN.

> **The rule: a self-hosted product fetches nothing from the internet to render its own pages.** An
> isolated network is the normal deployment, not the exception, and there a CDN reference is a blank
> screen with no explanation. On a connected one it is third-party code, resolved from a floating
> version tag, executing in an authenticated administrator's browser on the origin that holds their
> session. `TestDocsEndpoint_LoadsNoExternalAssets` fails on any absolute URL in the page.

---

## 15. `ticketref.Policy` — the operator ticket reference, in one place

**Where:** `internal/core/api/ticketref/` (Go), `ticketRefQuery` and `IdWithTicketRef` in
`web/src/lib/api.ts`, `TicketRefField` in `web/src/components/` (browser).

Six handler packages record an operator-supplied reference on their audit events — `admin`,
`teams`, `spaces`, `invites`, and, since the pre-cutover pass, `grants` and `shares`. They all
share one `ticketref.Policy` value, built once in `cmd/server/main.go` from
`AZIMUTHAL_TICKET_REF_REQUIRED` and threaded in with `WithTicketRefPolicy`.

**Do not write a second length cap, a second required check, or a second transport.** The
reference travels as the `ticket_ref` query parameter on every mutation, because the ones that
most need it carry no body at all (team delete, space delete, member removal, grant revoke, share
revoke are all DELETE). Bulk-apply is the single exception and keeps its shipped body field; both
paths go through the same `Policy`, which is what stops them drifting.

**Two invariants, both with tests, both easy to break by accident:**

| Invariant | Why | The test |
|---|---|---|
| Resolve **after** the authorisation gate | Turning the flag on must not change a single authorisation outcome. Resolve-first turns every 403 and 404 into a 400 for reference-less requests, which silently rewrites the endpoint matrix. It is worst on `/shares`, which carries no space guard — the handler does the whole 404-then-403 split itself, so nothing upstream would still answer correctly. | `TestTicketRefRequired_AuthorisationAnswersBeforeTheReference` |
| Resolve **before** the write | Under a required policy a missing reference has to mean *nothing happened*; a 400 after the service call leaves an unreferenced change committed, which is the outcome the requirement exists to prevent. Assert it by re-reading the resource, never by the status code alone. | `TestTicketRefRequired_MissingReference_RejectsAndWritesNothing`, `TestTicketRefRequired_GrantsAndShares_RejectAndWriteNothing` |

`invites` resolves *before* its 404 lookup. That is safe only because the whole `/invites` subtree
sits behind `RequireOrgAdmin404`, and it is not the pattern to copy.

**The structural guard.** A `ticketref.Policy` is a value field, so `TestHarness_NoDarkDependencies`
cannot see it — a handler can gain a reference gate that no test ever exercises under the required
posture, which is exactly how `grants` and `shares` went uncovered. `TestHarness_EveryTicketRefHandlerIsUnderTheRequiredPolicy`
closes it: every handler carrying a Policy must also be mounted in `newTicketRefRequiredServer`
with `Required` true, and it fails by field name.

**Not everything that writes an audit event takes one.** `writeShareRevokedTx` in
`internal/db/adapters/content_tx.go` writes `share.revoked` from inside a page move or delete —
the system enforcing ADR-0008, not an operator administrative change — and deliberately stores SQL
NULL. `TestShare_RevokeOnDelete_WritesNoTicketRef` pins both halves.

---

## 16. `GET /orgs/{orgID}/config` — the only boot-flag surface

**Where:** `internal/core/api/spaces/config.go` (`BootConfig`), `useTicketRefRequired` in
`web/src/lib/api.ts`.

One endpoint publishes the boot-time flags the browser needs. **The `BootConfig` struct is the
allowlist** — there is no filtering step between it and the wire, deliberately, because a filter
is a thing to get wrong whereas a hand-written struct is a decision someone had to type.

`config.Config` holds `DatabaseURL`, `StorageSecretKey` and `JWTPrivateKeyPath` a few fields away
from the flags that belong here. So: never populate this by reflecting over `config.Config`
(reflection turns an allowlist into a deny-list that publishes whatever the next person adds), and
never give a field `omitempty` (a zero value would vanish from the wire in tests while shipping in
production). Both dodges are failures in `TestBootConfig_JSONTagsAreTheAllowlist`, which reads the
type rather than a response — the integration key-set test passes for both, which is why the pair
exists.

The handler reads the same `ticketref.Policy` the mutating handlers enforce, not a second copy of
the flag: an endpoint reporting "not required" while the mutations refuse is worse than no
endpoint. The `orgID` in the path authorises the read and does not scope the values; they are
process-wide. If they ever become per-org, that is a migration and a different endpoint.

On the client, `useTicketRefRequired` fails safe to **false**. The server enforces the requirement
either way and is the authority; guessing `true` on a failed fetch would lock every administrative
dialog on the instance behind a field the operator may not need.
## 17. The gadget registry — two halves, one vocabulary

**Where:** `internal/core/dashboards/registry.go` (server) and
`web/src/lib/dashboards/registry.ts` (client), with the definitions in
`web/src/components/dashboards/gadgets.tsx`.

The server half owns what may be WRITTEN: the closed key set, the four configuration keys
(`title`, `limit`, `group_by`, `body`) and every bound. The client half owns what is DRAWN.
Neither is complete without the other, and they are pinned together by
`web/src/lib/dashboards/registry.test.ts`, which reads the Go file and fails in **both**
directions — a key the server accepts and the client cannot draw renders an "unknown gadget"
placeholder for something that is not unknown, and a key the client offers and the server refuses
is a picker entry whose Add button always 422s.

> **The rule: no `switch` over a gadget key anywhere in the render path.** ADR-0009 decision 5
> calls one a defect because it closes the extension seam permanently; more immediately, it
> scatters "what may this gadget carry" across every function that has to ask. Dispatch is a map
> read on both sides. `TestRegistry_NoSwitchOverGadgetKey` parses the Go package's own AST and
> fails on any switch whose subject is a gadget key.

**Strict on write, tolerant on read.** A key this build does not know is refused at the API
boundary; a key this build does not know that is ALREADY STORED must still load, as an inert
labelled placeholder (decision log C5). That is why migration 048 puts no CHECK on `gadget_key`
and why `dashboards.Gadget.Key` is a `string` rather than a `GadgetKey`.

**Adding a gadget** is one `registerGadget` call on each side plus one line in the drift test's
expectations. It is deliberately not less than that.

## 18. Aggregates go through the saved-view fan-out, never through the client

**Where:** `internal/core/views/aggregate.go`, backed by `CountViewTickets`,
`CountViewProjectItems`, `BreakdownViewTickets` and `BreakdownViewProjectItems` in
`internal/db/queries/saved_views.sql`.

Counts and breakdowns are the same read a results page performs, answered with `COUNT` and
`GROUP BY` instead of a row set — the same filter vocabulary, the same fan-out, the same
per-viewer access union, the same ADR-0008 exception (§13).

> **A filter field must be added to SIX predicate blocks, not two.** `ListViewTickets` and
> `ListViewProjectItems` are joined by the `Count*` and `Breakdown*` pairs above, and all six carry
> a hand-maintained copy of the same `WHERE` terms with nothing mechanical linking them. A field
> added to some of them compiles, generates and passes every existing test, while a gadget quietly
> reports a number for a query nobody ran. Filter v2 hit this immediately: a replace-all matched
> five of the six, because `ListViewTickets` carries a comment the others do not.
>
> `TestSavedViewFanouts_CarryIdenticalFilterPredicates` (in `internal/db/queries/`) now fails the
> moment the six disagree, and `TestViewAggregate_CountAgreesWithTheListItCounts` proves the same
> thing behaviourally against a real database. The two halves may differ only by `kinds` and
> `sprint_ids`, which are Vector-only columns.

> **The rule: never fetch pages and count them.** That form is bounded by `MaxPageSize` and would
> silently under-report any view with more than two hundred results, which is precisely the view
> somebody puts a count gadget on. `TestViewAggregate_CountIsNotBoundedByThePageSize` fails if it
> is ever done that way.

Two more properties the callers depend on: a breakdown's buckets always sum to its total (anything
past the bucket cap is rolled into one explicit `other` bucket, never dropped), and the empty
bucket key is a REAL bucket — unassigned work is what a breakdown is for.

**A bucket key is a disclosure.** A status that exists only in a space the caller cannot read must
not appear as a bucket, and `TestViewAggregate_BreakdownLeaksNoBucketFromAnUnreadableSpace` is
written so that widening either access array fails it.

## 19. `Markdown` — the only read-only markdown renderer

**Where:** `web/src/components/Markdown.tsx`.

Four pages had each hand-rolled the same `<ReactMarkdown>` plus the same six-line `prose` class
list before a fifth was about to ship with the P5 note gadget. The three non-Codex call sites
(`TicketDetailPage`, `ItemDetailPage`, `SharedEntityPage`) now use this one.

> **The rule: raw HTML stays off.** react-markdown v10 escapes embedded HTML by default; turning it
> back on means `rehype-raw`. A note gadget's body lands on somebody else's dashboard the moment
> the dashboard is shared, and this surface has no need of markup at all.

`pages/codex/WikiPage.tsx` keeps its own call site because it DOES pass `rehype-raw`, for legacy
wiki content.

**That decision has now been made, and not by turning it off.** The rule above used to end "and
there is no sanitiser behind it anywhere in this codebase", and this section used to say somebody
eventually had to decide about the Codex call site. The v0.4.1 trust patch decided it: raw HTML
stays a Codex feature, and `rehype-sanitize` runs immediately behind `rehype-raw` there.

Plugin order is the whole security property — sanitising *before* `rehype-raw` sanitises escaped
text and then re-inflates the markup, which is the same as not sanitising — so the pair lives in
one named constant, `WIKI_REHYPE_PLUGINS` in `WikiPage`, with the reasoning beside it.

The schema is `rehype-sanitize`'s default (GitHub's) with **nothing widened**. It already permits
what this surface needs: `className` matching `/^language-./` on `<code>`, which is what the `code`
component override reads to pick a highlighter language, and relative `src`/`href`. The one
deviation is a tightening — `<style>` joins `<script>` in `strip`, because anything outside
`tagNames` is otherwise *unwrapped*, which left a page printing its own stylesheet into the body as
visible text.

**Still do not copy that block into a new surface.** Owning a sanitiser schema is a cost, and
every surface in this section renders text that has no business being markup.

**Every prose colour is pinned to a token.** The app's theme is the `.dark` class while
`prose-invert` keys off the OS media query, so a body styled with `dark:prose-invert` alone renders
light-on-light for anybody whose system theme disagrees with their app theme.

---

## 20. `TierService` — the only thing that decides whether a status may change

Five routes can change an entity's status or workflow state:

```
POST .../tickets/{ticketID}/status
POST .../projects/items/{itemID}/status
POST .../tickets/{ticketID}/workflow-state
POST .../projects/items/{itemID}/workflow-state
POST .../workflow/approvals/{approvalID}/decide
```

**All five reach `TierService`, and none of them decides anything itself.** That is the whole rule.
A route that answered the legality question on its own would be a way around every configured guard
— which is exactly what shipped: two of these ran a hardcoded Go map, one ran the database engine,
and one validated nothing at all.

**`TierService.Gate` is the write side.** It answers where the entity is, whether the target names a
state, whether the workflow defines an edge, and then the ADR-0011 tiers in order — conditions,
validators, approvers, post-functions. A caller's only job is to render the answer and, when it says
proceed, write it.

**`TierService.OfferedTransitions` is the read side**, served at
`GET .../workflow/entities/{entityType}/{entityID}/transitions`, and both status pickers derive
their options from it. The two halves share `TierService.ResolveFromState`, and that sharing is
load-bearing: if the picker and the mutation placed the entity differently, the picker would offer
moves the server refuses and nothing would point at the disagreement.

The read side is deliberately NOT built on the gate. `tiergate.Gate.Evaluate` **writes** — it
creates the pending approval row and notifies its approvers — so a picker built on it would file an
approval request every time a page loaded.

**Three things that look like exceptions and are not.**

*A space with no workflow.* `TransitionDecision.NoWorkflow` tells the caller this package has no
opinion, and the caller applies whatever rule it had before workflows existed. That is the only
surviving "nothing applies", and it is what keeps an unassigned space behaving exactly as it did.

*Conditions are evaluated on both sides.* A condition hides at offer time AND refuses at commit
time. Not redundancy: the mutation route is reachable with curl, and a server that assumes the
client filtered is not enforcing anything.

*Creation is not a transition.* A new entity is PLACED in the machine rather than moved through it,
so it has no from-state and no edge. `tiergate.Gate.InitialPosition` is that seam, and both create
routes call it.

**On the frontend, `statusOptionsFor` in `web/src/lib/workflow/statusOptions.ts` is the only
derivation of a status picker's options.** Vector and Beacon each had their own hardcoded list, and
the two disagreed with each other, with the board's columns, and with the server — one offered a
status naming no state and omitted one that did, so an item in that state rendered a `<select>` with
no matching option. A third copy is a defect.

---

## Related

- Decisions: [`../adr/`](../adr/) — ADR-0007 for the capability model, ADR-0008 for share rules.
- The specification: [`v0.3-ia-spec.md`](v0.3-ia-spec.md) — §2 testing, §5 resolution, §10
  non-negotiables.
- What the repository actually contains vs what the spec claimed:
  [`spec-repo-reconciliation.md`](spec-repo-reconciliation.md).
