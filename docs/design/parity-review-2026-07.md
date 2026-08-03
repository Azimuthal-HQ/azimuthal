# Parity review — Azimuthal vs. the Atlassian suite

**Date:** 2026-07-30 · **Reviewed commit:** `532d387` (`origin/main`, P6 PR-B / #94)
**Method:** a throwaway instance of this exact commit, built from source and driven as a migrating
team would drive it; then the routes, the route-accounting table and the components read to
separate what is real from what is rendered; then the Atlassian side checked against Atlassian's
own current documentation.

This document is adversarial by construction. It is a list of what is missing, thin, or pretend.
It is deliberately not a summary of what works, except in §6, where the wins are ones I verified
rather than ones the documentation asserts.

---

## 0. Base verification

A prior audit ran eight commits stale and produced confident, well-cited, wrong findings. So,
first:

```
local HEAD  532d387516216647d20db5f005a6e60853051f69
origin/main 532d387516216647d20db5f005a6e60853051f69
git diff --stat origin/main  → empty
```

Thirteen merges beyond v0.3.6 are present and were graded as shipped code, not as plans: #82
(eslint gate), #87 (Codex Obsidian affordances, migration 040), #83 (assessor CLI), #84 (backend
residuals), #85 (portal API, 044/045), #86 (workflow tiers, 046/047), #88/#89 (dashboards, 048),
#90 (cleanup), #91 (maintenance), #92 (filter v2), #93/#94 (search, 049).

The live instance ran as an isolated compose project (`azimreview`, app on `:8099`) built from
this commit. It migrated cleanly to version 49. The shared test stack on `:5433` was not touched.

**Two items were pre-graded by the brief and are recorded without discovery effort:** the customer
portal has no requester surface (API only), and workflow tiers have no admin editor and no
approval UI (engine + API only). Both are **Absent-but-commissioned**; PR-Bs are in flight.
Findings below that touch those areas are about things *other* than the missing UI — and in two
places the pre-grade turns out to understate the gap, which is noted where it arises.

**A methodological warning for the next reviewer.** The brief suggested distinguishing a real
route from a missing one by an unauthenticated probe (401 = exists, 404 = does not). **That does
not work on this build.** `RequireAuth` is applied to the whole `/api/v1` group at
`router.go:188`, before routing resolves, so `GET /api/v1/zzz-nonsense-xyz` returns 401 exactly
like a real route. Invented paths such as `…/slas` and `…/webhooks` return 401 and read as
present. Every existence claim in this report is grounded in `router.go`,
`route_accounting_test.go`, or an authenticated live call — never in a bare 401.

---

## 1. Verdict

**A 20-person team cannot leave Atlassian for this today.** The blocker is not breadth — the
breadth is genuinely impressive for the elapsed time — it is that several things a person touches
on their first morning either do not exist or exist as controls that do nothing. You cannot
attach a screenshot to a bug. `@mentioning` a colleague does nothing at all: no link, no
notification, no email. Nobody is ever emailed about anything, for any event. Assigning work in
Vector notifies no one. There is no epic → story → subtask hierarchy reachable by any client. Any
one of these is survivable; together they mean the tool cannot carry a team's daily
communication, and a team that cannot rely on it for that keeps Jira open in the next tab.

**A 200-person org cannot adopt it at all,** for different reasons. SSO does not exist —
`internal/core/sso` is an interface and a no-op returning `ErrNotConfigured`, not wired into the
router at all — while `README.md:13` lists "**SSO** — SAML/OIDC single sign-on" as a feature.
There is no SCIM, no LDAP, no rate limiting (a stub interface whose comment still says the
implementation "will be added in Phase 2"), no metrics endpoint, no API tokens or service
accounts, and no audit-log export. Provisioning 200 people means 200 hand-delivered invite links,
because invite email is off by default and the SMTP sender supports no authentication, so it
cannot talk to SES, SendGrid, Google or O365.

**And before either of those: the documented backup does not run.** `docker compose exec app
/azimuthal backup` — the exact command in `docs/self-hosting.md:173`, and in the daily cron job at
`:197` — fails in the shipped container, because the image is `distroless/static` and the command
forks `pg_dump`. I ran it; it exits 1. An operator following the documentation believes they have
nightly backups and has none. This is the most operationally dangerous finding in the review and
it is a one-line fix.

**And one finding outranks the parity question entirely:** a cross-space read-authorization gap in
the item-relations read path. It is tracked privately rather than described here (§1a), and it
should be closed before this document is circulated.

The honest summary: **Azimuthal today is a credible internal tool for a small team that already
communicates in Slack, and is not yet a replacement for the Atlassian suite for anyone.** The
distance to the first claim is short. The distance to the second is a phase or two, and most of it
is UI work over engines that already exist and are already tested.

One thing cuts the other way and deserves saying plainly: the *architecture* here is better than
the product. The access model, the audit log, the route-accounting discipline, the preservation
carriers and the workflow engine are real, and several are genuinely ahead of what Atlassian
ships. The gap is overwhelmingly at the last mile — routes without components, engines without
editors, functions without callers. That is a much better problem to have than the reverse, and
it is why the recommended list in §7 is mostly small.

---

## 1a. One security finding

A cross-space read-authorization gap exists in the item-relations read path; tracked privately and
fixed ahead of the backup repair — see item 0 in the recommendations.

Details are deliberately omitted from this document: the repository is public and the gap is
unpatched at the time of writing. They have been passed to the maintainer separately.

---

## 2. The T1 kill list

Ranked by how early a user hits it and how badly it hurts. Each line was reproduced live or
proved in code; the evidence says which.

| # | What breaks | Evidence |
|---|---|---|
| 0 | **The documented backup command fails in the shipped container, silently and forever.** The image is `gcr.io/distroless/static:nonroot`; `backup.go` forks `pg_dump` and `psql`, `restore.go` forks `psql`. None exists in the image. The docs prescribe running it *inside* the container, including as a nightly cron. | Live: `docker exec azimreview-app-1 /azimuthal backup --output /tmp/b.tar.gz` → `Error: dumping postgres: pg_dump failed: exec: "pg_dump": executable file not found in $PATH`. `build/Dockerfile:51`; `cmd/server/backup.go:114,119`; `cmd/server/restore.go:169`; `docs/self-hosting.md:173,197`. |
| 1 | **You cannot attach a file to a ticket or an item.** The backend accepts multipart uploads at `POST /spaces/{spaceID}/attachments`; no component ever calls it. The only three uploads in the whole frontend are two avatars and a Codex page image. | `web/src/lib/api.ts` — `FormData` at 1150 (`uploadOwnAvatar`), 1156 (`uploadUserAvatar`), 1560 (`uploadPageImage`), nowhere else. No attachment markup in `ItemDetailPage.tsx`. |
| 2 | **`@mention` does nothing.** The comment stores the literal text. Not parsed, not linked, not resolved, fires no notification. | Live: posted `"Hey @Ada Founder … Ref PLAT-3 and [[Onboarding]]"`; round-tripped verbatim, `unread_count` stayed 0, rendered as plain text. |
| 3 | **Nothing is ever emailed about anything.** `email.Sender.Send` has four call sites: invites, portal sign-in links, a job worker nothing feeds, and a dead ticket-reply function. No notification path reaches it. `internal/core/notifications/` is a two-line `doc.go`. | `invite_sender.go:39`, `portal_sender.go:42`, `jobs/email.go:45`. `EnqueueEmail` is called only from `queue_test.go:65`, so the registered `EmailWorker` processes a job kind nothing produces. |
| 4 | **In-app notification fires for exactly two events** — Beacon ticket assignment and portal reply. Assigning a Vector item notifies nobody; commenting notifies nobody; Codex notifies nobody. The comments handler is wired with a live enqueuer and never calls it. | Live: assigned a ticket *and* an item → `unread_count` = **1**, ticket only. `grep "EventKind" internal/ cmd/ | grep -v _test` → two producers repo-wide. `main.go:436` wires the enqueuer; `grep -i enqueu comments/handler.go` → declaration and setter, no call. |
| 5 | **Epic → story → subtask hierarchy is unreachable.** `project_items.parent_id` exists and is plumbed through sqlc and the adapter both ways, but is absent from both `createItemRequest` and `updateItemRequest`, and no UI sets it. | `projects/handler.go:159-168`, `194-202`; plumbing at `db/queries/project_items.sql:18`, `adapters/projects.go:35,212`. Live: `PATCH {"parent_id": …}` → 400. |
| 6 | **Condition-class workflow guards are accepted and silently do nothing.** §5.1 — the sharpest finding here. | Live, same guard kind, same item: `condition` → **200, transition proceeds**; `validator` → **422 "only the assignee may make this transition"**. |
| 7 | **Status is free text.** Any string writes through, including one matching no workflow state. | Live: `{"status":"banana"}` → **200**, stored. Deliberate — `tier_service.go:139-143`, *"Absence is not refusal."* |
| 8 | **The item-detail status dropdown is hardcoded and matches neither module.** Offers `open`/`closed` (not Vector states); omits `backlog`/`todo` (which are). | `ItemDetailPage.tsx:54`. Live: Vector is `backlog, todo, in_progress, in_review, done`; Beacon is `open, in_progress, resolved, closed`. |
| 9 | **Labels are unusable and the stub page instructs the impossible.** There is no label admin UI at all — `LabelsPage.tsx` is a "coming soon" empty state — and no saved view can filter by label. Its own copy says *"items keep the labels assigned on their detail view"*; the detail view has no label control. | `web/src/pages/vector/LabelsPage.tsx:22-26`. Filter vocabulary has no `labels` field (`views/filter.go:445-487`). This makes `known-issues.md` §14 stale in the *worse* direction: it says admin-created labels have no effect; you cannot create them. |
| 10 | **Sorting a saved view by priority is backwards.** `priority DESC` returns *least* urgent first. Every other sort field is chronological, where DESC means newest first, so the inconsistency is silent and returns plausible data. | Live: `sort {priority, desc}` → `low, medium, urgent`; `asc` → `urgent, medium, low`. Cause: `migrations/038:222-226` maps `urgent→'0' … low→'3'`. Jira's `ORDER BY priority DESC` puts Highest first. |
| 11 | **No bulk edit, transition, or move.** The only bulk routes are for *grants*. | Live: `…/items/bulk`, `…/items/bulk-edit` → 405. |
| 12 | **No CSV import or export**, in either direction — and no importer of any kind (§5.7). | `grep -ri csv` over `internal/ migrations/ web/src/` (excluding the assessor) → **0 non-test hits**. |
| 13 | **No issue history.** The item page's "Activity" panel is the comment list. Field changes reach the audit log, which has no per-item view and, per `known-issues.md` §22, no `space_id` to scope one. | `ItemDetailPage.tsx:382`. |
| 14 | **No keyboard shortcuts.** No app-level shortcut layer; the only `onKeyDown` handlers are inside the Codex editor. | `grep -rn "keydown\|hotkey\|shortcut" web/src/`. |
| 15 | **The search box advertises a query that returns nothing.** The placeholder reads *"try type:ticket or tag:runbooks"* and the empty state repeats it. An operator-only query strips to empty text and returns `state: no_searchable_terms`. | `SearchPage.tsx:61,117`. Live: `tag:ops` → 0 results, `"state":"no_searchable_terms"`. |
| 16 | **Field-scoped search silently returns nothing.** `title:Login` → 0 results with `"state":"ok"`, while bare `Login` → 1. Deliberate, but indistinguishable from "no such issue". | Live; `internal/core/search/query.go:7-26` documents the choice. |
| 17 | **The login page ships a dead "Sign up" link** in every deployment. No `/signup` route is registered at all. | `LoginPage.tsx:110-115`; no signup route in `App.tsx`. Live: `/signup` → `/login?redirect=%2Fsignup`. |
| 18 | **Watchers, time tracking, estimates, worklogs, components, fix versions/releases, burndown and velocity do not exist.** | `grep -riE` over `internal/ migrations/ web/src/`, excluding tests and the assessor: each **0 hits**. The repo's own JQL classifier agrees — `assess/jql/classify_test.go:401` classifies `watcher` as NotExpressible. |
| 19 | **Beacon tickets cannot be linked at all.** Relations exist for `project_items` only; the schema has supported tickets since migration 015. | `known-issues.md` §15, which corrects its own recorded blocker. |

### The correction that matters most

**ADR-0011 says "no link table exists". That is wrong, and wrong in the direction that costs
users.** `entity_relations` has existed since migration 004, was made polymorphic by 015, and
`POST /projects/items/{itemID}/relations` works today — I created `blocks`, `is_blocked_by`,
`duplicates`, `relates_to` and `wiki_link` links live, and they render on the item page
(`ItemDetailPage.tsx:314-378`).

The cost of the stale premise is concrete: **the shipped migration assessor tells a migrating team
their Jira links have nowhere to go.**

> `internal/assess/jira_assess.go:378` — *"Azimuthal models a parent/child hierarchy on
> project_items but has no typed link graph, so blocks/relates-to/duplicates links have nowhere
> to go"*

Both clauses are backwards. The hierarchy is the part with nowhere to go (#5); the typed link
graph is the part that works. A team running `azimuthal assess` will be told to abandon data that
would import, and told nothing about the data that would not.

---

## 3. Graded matrix

`Present` = real backend, a UI that reaches it, enough depth for week-one use. `Partial` = exists
but shallow, or backend-only. `Absent` = no route, no schema, no UI. `Dead-control` = a control
that does nothing, or a capability whose only caller is a test.

### 3.1 Jira side — Vector and shared

| Tier | Capability | Grade | Evidence |
|---|---|---|---|
| T1 | Issue links | **Partial** | API + UI, 4 kinds, fixed — and shallow in ways that compound. **No dedup or contradiction check:** I hold `blocks` *and* `is_blocked_by` *and* `duplicates` on one pair, all rendered side by side. Two further depth defects in this path are covered by the §1a private item and are not described here. `GetBlockers` (`relations.go:93`) exists with no route and no non-test caller. Jira ships configurable link types and auto-creates the inverse ([Atlassian](https://support.atlassian.com/jira-cloud-administration/docs/configure-issue-linking/)). Beacon excluded entirely. |
| T1 | Issue hierarchy | **Absent** | §2 #5. |
| T1 | Attachments on items/tickets | **Dead-control** | §2 #1. The E2E suite that appears to cover it drives the API directly (`web/e2e/attachments.spec.ts:45` uses `page.request.post`), so it reads as covered while no UI exists. |
| T1 | @mentions | **Absent** | §2 #2. |
| T1 | Watchers | **Absent** | 0 hits. |
| T1 | Notification on change | **Partial** | Two events, in-app only. §2 #4. |
| T1 | Notification email | **Absent** | §2 #3. |
| T1 | Rich text in descriptions/comments | **Absent** | Codex got TipTap; Vector did not. `description` is a plain string on create and PATCH; comments are a plain `content` string. |
| T1 | Issue history / activity | **Absent** | §2 #13. |
| T1 | Board and backlog | **Present** | Backlog, sprint board with drag-and-drop, board config with columns and reset. `SprintBoardPage` genuinely derives columns from workflow states — the one place the workflow drives the UI. |
| T1 | Board quick-filters | **Absent** | No quick-filter concept. |
| T1 | Keyboard shortcuts | **Absent** | §2 #14. |
| T2 | Bulk operations | **Absent** | §2 #11. |
| T2 | Labels | **Dead-control** | §2 #9. |
| T2 | Components / fix versions / time tracking | **Absent** | 0 hits each. The assessor is honest here: *"there is no time-tracking model, so worklogs are lost entirely"*. |
| T2 | Sprints | **Present** | Create, start, complete, active, item assignment, backlog↔sprint moves, roadmap. |
| T2 | Sprint reports / velocity / burndown | **Absent** | 0 hits. Sprints can be run but not measured. |
| T2 | Clone an issue | **Absent** | The only `clone` hits are test-database template cloning. |
| T2 | Move an issue between spaces | **Absent** | No route. (Wiki pages *can* move, carefully — `POST /wiki/{pageID}/move`.) |
| T2 | Saved views | **Partial** | Real, scoped, per-viewer, degradation rules honoured — but see §5.4 and the priority-sort defect at §2 #10. |
| T2 | Saved-view sharing | **Partial** | Org/team/private visibility. No subscriptions or scheduled delivery (which would need email). |
| T2 | Archiving | **Absent** | Soft-delete on some tables (`known-issues.md` §4); no user-facing archive. |
| T3 | REST API completeness | **Partial** | 152 documented paths, regenerated and diffed in CI — genuinely good, but "complete" relative to a smaller product. |
| T3 | Webhooks / automation | **Absent** | §4.1. |

### 3.2 Confluence side — Codex

| Tier | Capability | Grade | Evidence |
|---|---|---|---|
| T1 | Page tree, create, edit, move | **Present** | Real `parent_id` + `position`, tree endpoint, move with cross-space share revocation. |
| T1 | Page history, diff, restore | **Present** | `/revisions`, `/revisions/{version}`, `/restore`, and a real `/diff`. Better than expected; restore has its own integration test. |
| T1 | Preservation carriers | **Present** | All three ADR-0012 carriers exist — `unknownContent`, `unknownInline`, `unknownMark` (`web/src/lib/codex/schema.ts:101-103`), drift-guarded in both directions. The best-engineered thing in the repository — but see §5.7 for what it is missing. |
| T1 | Page comments | **Partial** | `GET/POST /wiki/{pageID}/comments`. No mentions, no notifications, no resolve — **and threaded replies are written and then permanently invisible**: `createCommentRequest.ParentID` is accepted and persisted, and the list query never returns children. |
| T1 | Inline comments | **Absent** | Deliberately deferred. §4.3. |
| T1 | @mentions / watch a page or space | **Absent** | No watch concept anywhere. |
| T2 | Wikilinks and tags | **Partial** | Real in the editor: `[[…]]` autocomplete, tag chips, tag index, `/tags/{slug}/pages`. **Editor-only.** Content written through the API's `content` field is never parsed — I created a page containing `[[Runbook]]` and `#ops`; both rendered as literal text and `GET /tags` returned `[]`. That is the path an importer or script uses. |
| T2 | Backlinks / unlinked mentions | **Absent** | No backlinks panel. The `wiki_link` relation kind exists but nothing surfaces an inbound-link list. Both Obsidian and Confluence have this. |
| T2 | Macros | **Partial** | Exactly 8: panel, expand, statusLozenge, tableOfContents, layout(+column), childrenDisplay, pageInclude, plus code blocks. See §4.4 — the interesting gap is against ADR-0012's own promises, not against Confluence's count. |
| T2 | Page-level restrictions | **Absent** | Space-level only — and per ADR-0008 ("shares widen, never narrow") this is architecturally closed, not merely unbuilt. A Confluence space carrying page restrictions cannot be represented. Restrictions are a paid-plan Confluence feature ([Atlassian](https://support.atlassian.com/confluence-cloud/docs/add-or-remove-page-restrictions/)) and the mechanism most wiki migrations depend on. |
| T2 | Page templates / blueprints | **Absent** | No template concept; every page starts empty. |
| T2 | Export (PDF / Word / space) | **Absent** | No export route. `GET /wiki/{pageID}/render` serialises one page's HTML and is the nearest thing — a plausible foundation, not an export. |
| T2 | Attachments on pages | **Partial** | Editor images only (`/wiki/{pageID}/images`); no general file-attachment surface. |
| T2 | Copy / move page trees | **Partial** | Move exists; copy does not. |
| T3 | Orphaned pages | **Absent — and mis-presented** | No orphan report, and `CodexSidebar.tsx:35-54` reparents any page whose `parent_id` is missing from the returned set to the tree root, so orphans silently appear as top-level pages rather than being flagged. |

### 3.3 JSM side — Beacon and the portal

| Tier | Capability | Grade | Evidence |
|---|---|---|---|
| T1 | Queues | **Partial** | Real, ordered, per-space. **Not seeded** — a new Beacon space has zero queues until someone calls `/queues/defaults` (which creates 4). Config depth is exactly the saved-view vocabulary: 8 membership fields, AND-only, one sort key. JSM queues filter on any field with JQL. One default queue, "Recently resolved", sorts by `resolved_at` — a column nothing ever writes (§5.4). |
| T1 | Internal notes vs public replies | **Partial** | The *backend* is excellent — migration 045 is a real boundary, enforced server-side, defaulting to `internal` when absent, with the reasoning recorded. But it is unreachable from the product: `api.ts`'s `CreateCommentRequest` carries only `{content}` and the `Comment` interface has no `visibility` field, so an agent cannot post a public reply from the UI. |
| T1 | Request types / forms with custom fields | **Absent** | Custom fields exist (migration 033) but there is no request-type concept and no intake form. A requester submits a title and a description. |
| T1 | SLAs | **Absent** | No timers, targets, calendars, pause conditions or breach visibility. The largest single JSM gap — SLAs are most of why teams buy JSM ([Atlassian](https://support.atlassian.com/jira-service-management-cloud/docs/create-service-level-agreements-slas/)). Pointedly, ADR-0003 justifies the tickets/project_items split partly on *"a ticket has SLA clocks, first-response and resolution timers"* — the architecture was shaped around a feature that was never built. |
| T1 | Email channel | **Dead-control** | §5.2. Parser and reply function both complete, both tested, both unreachable — while `README.md:9` advertises "email ingestion". |
| T2 | Approvals | **Partial, with a live defect** | Engine is real (047) and enforced on the route the UI uses. But the `202 pending_approval` response (`tiergate.go:153-167`) is typed as a `Ticket`, and `api.ts`'s `apiFetch` treats any `response.ok` — which includes 202 — as success. **Configuring an approver therefore makes the UI report a transition as succeeded when it is actually pending.** Separately, approval state is not surfaced on any ticket read model or queue column, so even the commissioned UI has nothing to render against. |
| T2 | Portal | **Partial** | Config, public descriptor, magic-link auth, request list and replies work at the API. The pre-grade understates it: `grep -i portal web/src/lib/api.ts` returns **zero matches**, so the *agent-side* admin routes have no caller either — there is no portal UI of any kind. Two further problems in §5.3. |
| T2 | Customer organizations | **Absent** | Migration 044 models individual requesters only. |
| T2 | KB deflection / KB linkage | **Absent** | No portal search, no article suggestion, no ticket→page link surface. (`TicketRefField.tsx` is a name-shaped false lead — its own doc comment says it is a free-text audit note, "not a foreign key".) |
| T2 | CSAT / request participants / CC | **Absent** | No models. |
| T3 | On-call / alerting | **Absent** | Not implemented and not declared a non-goal anywhere in `docs/`. Worth an explicit decision either way. |

### 3.4 Platform and operations

| Tier | Capability | Grade | Evidence |
|---|---|---|---|
| T1 | Single-binary deployment | **Present** | Verified: `web/embed.go` carries `//go:embed all:dist`; `migrations/migrations.go` embeds the SQL. One artifact, migrations at boot. |
| T1 | **Backup / restore** | **Dead-control** | §2 #0 and §5.6. Cannot run in the shipped image; and `restore.go:169-172` runs `psql` without `-v ON_ERROR_STOP=1` and discards stdout, so a dump whose statements failed still exits 0 and prints "Database restored." |
| T1 | Responsive / mobile | **Partial** | No horizontal document overflow at 375 px, but the top-bar action cluster — including Notifications — overflows the viewport edge and is clipped. Nine breakpoint usages across the whole shell. No mobile app. |
| T2 | SSO (SAML/OIDC) | **Dead-control** | `internal/core/sso/provider.go` is an interface plus a no-op returning `ErrNotConfigured`, not referenced by `main.go` or `router.go`. Its package comment asserts *"SSO is a standard feature available to all Azimuthal users"* directly above the no-op. `README.md:13` lists it. Atlassian gates the equivalent behind Atlassian Guard ([Atlassian](https://www.atlassian.com/software/guard/pricing)) — a place Azimuthal could be genuinely ahead, and isn't. |
| T2 | SCIM / LDAP | **Absent** | `manual/scim/oidc` appears only as a `source` enum on team membership (migration 022). |
| T2 | SMTP end-to-end | **Partial** | A real `SMTPSender` exists and invites/portal links use it. **No authentication and no TLS** (`sender.go:41-52`, `smtp.SendMail(addr, nil, …)`), so it cannot reach SES, SendGrid, Google Workspace or O365 — only an unauthenticated internal relay. |
| T2 | Invites | **Present** | Bulk invite with per-address outcomes, expiry, resend, returned `invite_url`. Default delivery is `link`; honest and documented. |
| T2 | Permissions administrability | **Partial** | The capability model (ADR-0007) is sound and better-designed than Jira's permission schemes. Admin surface exists: `/access-matrix`, `/spaces/{id}/effective-access`, grants CRUD, bulk preview/apply. Missing is "why can *this person* see *this object*?" — the matrix is a grid, not an explanation. |
| T2 | Workflow administration | **Partial** | Worth separating from the pre-grade: `App.tsx:113` **does** route `/admin/workflows` to `WorkflowAdminPage.tsx`. But `AdminLayout.tsx:54-61` lists eight tabs and Workflows is not among them, so the page exists and is unreachable by navigation. |
| T2 | Audit log | **Present** | Genuinely good: append-only, rich, real. 28 entries from a short session including `workflow.guard_created`, `workflow.guard_deleted`, `item.status_changed`, `share.created`. |
| T2 | Audit-log export | **Absent** | No export route. |
| T2 | Upgrade path | **Partial** | `docs/upgrade.md` exists and migrations are embedded — but its rollback section leads with `psql … < backup-pre-upgrade.sql`, a `.sql` file its own backup step never produces (that step emits `backup-pre-upgrade.tar.gz`). A correct `docker cp` + restore alternative follows two lines below, so the section is recoverable — but the first command an operator runs under pressure fails, and the whole procedure depends on a backup that does not run at all (§2 #0). |
| T3 | API tokens / service accounts | **Absent** | An integration must log in as a human, with that human's password, holding a 24-hour bearer token carrying that human's full authority, revocable only by `force-logout`. This blocks every CI job, bot and script. |
| T3 | Rate limiting | **Dead-control** | `auth/middleware.go:190` — *"a stub interface … The concrete implementation will be added in Phase 2."* The project is at P6. |
| T3 | Observability | **Absent** | Structured slog with request IDs is present and good. No `/metrics`, no Prometheus, no OpenTelemetry. `/health` and `/ready` only — and `HandleReady` (`health.go:49`) takes no arguments and touches no dependency, so it is structurally incapable of reporting unreadiness. `LOG_LEVEL` is parsed, passed by compose, and never read: `serve.go:26` hardcodes `slog.LevelInfo`. |
| T3 | i18n / l10n | **Absent** | No translation layer; every string is hardcoded English. Compounded by §5.5. |
| T3 | Accessibility | **Partial** | Reasonable semantics and `aria-label`s on icon buttons — the accessibility tree read cleanly. No skip link, no `aria-live` in the shell, no focus-management utilities, no automated a11y gate. |
| T3 | Session hygiene | **Partial** | `SessionService.PurgeExpiredSessions` exists and is tested; nothing calls it, and there is no scheduler to call it from, so the sessions table grows monotonically. |
| T3 | Multi-org tenancy | **Partial** | Org is a first-class scope, but a deployment provisions one org via CLI and a JWT carries a single `org_id`. |

---

## 4. Deliberate exclusions and what they cost

Locked decisions are not relitigated. Their user-facing consequences are.

### 4.1 "No workflow scripting, ever" — and the mitigation that does not exist

ADR-0011 is right, and it is the best-argued document in the repository. But it names its own
mitigation:

> *"Genuine automation belongs at the integration boundary — webhooks and the job queue — rather
> than inside the workflow engine."* (ADR-0011:154)

**The word "webhook" appears exactly once in the entire repository: in that sentence.** No route,
no table, no config key, no handler, no test, no tracking issue.

So the full story for a migrating team: their Jira automation cannot be scripted (locked, fine),
cannot be replaced by post-functions (two ship — set `due_at`/`labels`, assign to a user), and
cannot move to the integration boundary either, because that boundary does not exist. With no API
tokens (§3.4), even an external automation service has nothing to authenticate as.

For calibration: Jira Automation ships on every Cloud edition, including Free at 100 rule runs per
month, Standard at 1,700, and Premium at 1,000 per user per month
([Atlassian](https://support.atlassian.com/automation/kb/difference-between-automation-service-limits-and-automation-usage-limits/)).
A team with 40 rules is an ordinary Standard customer, not a power user.

**How far do workflow tiers stretch?** About as far as "restrict who may transition, and require a
field first". Not to "when a bug is closed, comment on the linked support ticket and notify the
reporter" — post-function comments are deferred, transitioning a linked item is deferred, and
notification does not exist. That gap between the ADR's framing and an admin's experience belongs
in the migration docs rather than being discovered.

### 4.2 "No query language, ever"

Correct as a decision, and the closed vocabularies are well executed — `filter.go` is one of the
clearest files here, and search's "unknown operators are literal text" reasoning is sound.

The cost is that muscle memory fails silently rather than loudly. `title:Login`, `assignee:ada`,
`status:open`, `created:-7d` each return zero results with `"state":"ok"`. Every one is a reflex
for someone arriving from JQL or CQL — and the product's *own* placeholder text advertises a query
shape that returns nothing (§2 #15). **The mitigation is cheap and does not touch the decision:**
when a stripped token looks like `field:value` and the field is not in the closed set, say so in
the response. The parser already identifies these tokens; it discards the fact.

### 4.3 Inline comments deferred

Review workflows move to a different tool. Confluence's inline comment is how a reviewer says
"this sentence is wrong" without editing someone's prose. Without it, and without page mentions or
notifications, a Codex page is a publishing surface rather than a collaboration surface. That may
be right sequencing, but "replaces Confluence" is not yet true for teams whose Confluence use is
mostly review.

### 4.4 ADR-0012's unshipped promises

ADR-0012 §4 commits to more than the 8 macros that shipped:

- **Cross-reference macros** — "Jira issue, page include, children display". Page include and
  children display shipped. **The item embed did not** — no `itemEmbed`, `jiraIssue`,
  `vectorEmbed` or `ticketEmbed` node exists in `web/src/components/codex/`. The ADR called this
  "Codex↔Vector embedding, a feature worth having anyway"; it is also the mechanism JSM
  knowledge-base linkage would need.
- **Dynamic content macros** — "content by label, page properties reports map onto the saved-views
  layer". **Nothing.** Notably `extensions/macros.ts:26-30` defers them on the grounds that they
  map onto saved views "and that is P4" — P4 merged in #79/#81, so the stated reason has expired.

Recorded as ADR drift rather than new gaps: the decision was made, the implementation is partial,
and the ADR has not been amended to say so — the exact pattern ADR-0011 warned about when it
amended itself for the shipped guard vocabulary.

---

## 5. Pretend-parity findings

Things that demo well and collapse under real use, ordered by severity.

### 5.1 Condition-class workflow guards are inert

The most serious finding here, because it is a compliance control that reports success and does
nothing.

ADR-0011 Tier 1 defines two guard classes: a **condition** determines whether a transition is
*offered*; a **validator** determines whether it *succeeds*. Both are accepted by
`POST /workflows/{id}/transitions/{id}/guards`, both validated, both persisted, both audited.

Only validators are ever evaluated.

Each link verified:

1. In non-test code, `GuardConditionClass` is evaluated in exactly one place —
   `internal/core/workflow/tier_service.go:228`, inside `TierService.AvailableTransitions`.
2. `AvailableTransitions` has **no HTTP route**. `registerTierSpaceRoutes` (`tiers.go:71-74`)
   registers only `/approvals` and `/approvals/{approvalID}/decide`. Nothing in
   `internal/core/api/` calls it.
3. `TierService.Gate` — the path every real transition takes — deliberately does *not* evaluate
   conditions, and says why: *"Conditions were already applied when the transition was offered;
   re-running them here would refuse a transition the user was never shown"*
   (`tier_service.go:152-155`).

Nothing offers. The assumption the gate relies on is never established.

Reproduced live on an unassigned item, same guard kind each way:

```
condition + actor_is_assignee  → POST …/status {"status":"in_progress"} → 200, status=in_progress
validator + actor_is_assignee  → POST …/status {"status":"in_progress"} → 422
                                  "only the assignee may make this transition"
```

An administrator configuring "only the assignee may move this to In Review" as a condition — which
is exactly how ADR-0011's own example reads — gets a `201 Created`, an audit entry, and no
enforcement.

This is *not* the "no admin UI" gap; the engine is reachable and half of it works correctly. Two
things close it: route `AvailableTransitions`, and have `Gate` evaluate conditions as well, so
that a transition attempted directly still refuses.

### 5.2 The JSM email channel is fully-built dead code

`internal/core/tickets/email_ingest.go` parses RFC 2822 and `CreateFromEmail` turns it into a
ticket. `internal/core/tickets/email_reply.go` sends an outbound reply. Both have tests. Neither
has a single production caller:

```
grep -rn "CreateFromEmail\|ParseInboundEmail" internal/ cmd/  → definitions + email_ingest_test.go
grep -rn "SendReply"                          internal/       → its own definition only
```

No IMAP client, no POP client, no inbound webhook, no mail-drop poller. Meanwhile `README.md:9`
advertises **"Service Desk — ticket lifecycle, email ingestion, kanban boards"**.

This is the failure mode CLAUDE.md warns about under "no dark harness", arriving from the other
direction: not a test that never reaches a handler, but a handler-shaped thing nothing ever
reaches, with passing tests that make the feature list look complete.

### 5.3 The customer portal cannot authenticate anyone as shipped

Beyond the known missing UI, the only authentication path is broken under the project's own
`docker-compose.yml`:

- Disclosure of the sign-in URL is refused in production by design — a sound decision, since the
  endpoint is necessarily unauthenticated and disclosing the URL would be a total bypass.
- `build/docker-compose.yml` sets `APP_ENV: production`, so `DiscloseLink` is false
  (`Config.PortalLinkDisclosureAllowed`, `internal/config/config.go`).
- Delivery defaults to `link`, so no email is attempted — and `SMTP_HOST` defaults to `localhost`
  with no auth support anyway.

> **Corrected 2026-08-02.** The first bullet read: "`PortalLinkDeliveryLink` is refused in
> production by design". That was false in both halves. `link` delivery has never been refused —
> `Config.validate` deliberately does not reject it in production, and says so in a comment — and
> since #108 the delivery mode does not influence disclosure at all: disclosure is
> `PortalDiscloseLink && !IsProduction()`, stated once in `Config.PortalLinkDisclosureAllowed` and
> nowhere else. What is refused is the *disclosure*, not the *mode*, and the conclusion this
> section draws survives the correction unchanged — the requester still receives nothing.
>
> The citation was `main.go:405`, which violates the symbol-and-file rule (CLAUDE.md §6) and had
> already rotted: `main.go` calls `cfg.PortalLinkDisclosureAllowed()` and does no arithmetic of its
> own, so there is no rule at that line to cite. Converted rather than renumbered.
>
> #108 fixed the code and touched no documentation, which is why this survived it. Three copies of
> the claim existed in prose at `abedbf85` — this one, the `AZIMUTHAL_PORTAL_LINK_DELIVERY` row in
> `docs/self-hosting.md`, and the same row in `README.md` — and all three are corrected in the same
> pass as this note.

Live, on the shipped compose file:

```
POST /portal/{key}/auth/request-link  →  202  {"status":"sent","delivered":false}
```

A `202 "sent"` with `delivered: false` and no link anywhere. The requester waits forever. The
payload is at least honest; nothing surfaces the misconfiguration to the admin who enabled the
portal.

Smaller: the portal key is a random 20-character string (`3n42xwvyazl2zqworng6`) with no way to
set it — `createPortalRequest` takes only `name` and `intro`. JSM portals have readable URLs.

### 5.4 Saved views and queues are thinner than the surface suggests

The filter vocabulary is well-designed and honest about its limits. Because queues reuse it, its
limits are JSM's limits too. The closed field set (`filter.go:445-487`): `modules`, `space_ids`,
`statuses`, `priorities`, `assignees`, `kinds`, `sprint_ids`, `text`, four date ranges, per-field
`not`.

Three ordinary Jira filters:

| JQL | Expressible? |
|---|---|
| `assignee = currentUser() AND status not in (Done, Closed) ORDER BY priority DESC` | **Yes** — but the sort comes back inverted (§2 #10). |
| `project = X AND labels in (urgent) AND created >= -7d` | **No** — there is no `labels` field. |
| `(status = Blocked OR priority = Highest) AND assignee is EMPTY` | **No** — cross-field OR has no representation, by design. |

Also absent as filterable: **labels, reporter, custom fields**, and description text (`text`
matches the title only — `filter.go:464-466`). Custom fields shipped in migration 033 and cannot
be filtered on, making them display-only metadata.

**`resolved_at` is a dead field exposed twice.** No INSERT or UPDATE in `internal/db/queries/`
ever writes it, yet it is offered as a filter *and* as a sort key, and the shipped default queue
"Recently resolved" (`views/queue.go:100-105`) sorts by it. Every Beacon space gets a queue that
is permanently empty.

One sort key, one direction. Jira sorts on several.

### 5.5 Search: real operators, English-only index, no pagination in the UI

Verified live: `onboard` matches `Onboarding` (stemming works), `boarding` matches nothing (no
substring or trigram fallback), a typo returns nothing, `OR` and `-negation` work via
`websearch_to_tsquery`, and `type:`/`module:`/`tag:` narrow correctly.

Two consequences worth stating:

**English-only.** Migration 049 and the per-module queries use the `english` configuration
throughout. For non-English content, English stemming and an English stopword list are applied to,
say, German or Japanese text. Practically: Japanese and Chinese do not tokenise into words at all
and become effectively unsearchable; European languages match only exact word forms. With no i18n
(§3.4), a non-English team gets an English UI *and* a search that quietly under-recalls their
content. Defensible for v1 — it should be documented rather than discovered.

**No pagination in the UI.** The backend mints cursors and puts `next_cursor` on the wire; the
frontend already carries the plumbing — `SearchOptions.cursor` exists (`api.ts:5135`) and
`fetchSearch` forwards it (`:5143`) — and **no caller passes it**. Results silently stop at the
first page.

Genuinely good and worth keeping: the `state` field distinguishing `ok` from
`no_searchable_terms`, the permission fan-out, and stripping operators before building the
tsquery rather than letting `type:beacon` become the lexemes `type` and `beacon`. Note also that
the three older per-module search endpoints still use `plainto_tsquery` — no phrases, no OR, no
negation — so search quality depends on which entry point you reach.

### 5.6 Backup and restore

Covered at §2 #0. Two failure modes, both silent:

1. **It cannot run where the docs say to run it.** Distroless image, forked `pg_dump`/`psql`.
   `docs/self-hosting.md:197` is a nightly cron that has never produced a file. (The binary run
   *outside* the container, on a host with the Postgres client tools installed, works — which is
   presumably how it was tested.)
2. **A partial restore reports success.** `restore.go:169-172` runs `psql` with no
   `-v ON_ERROR_STOP=1` and `Stdout = io.Discard`; psql exits 0 when individual statements fail,
   and the CLI prints "  Database restored."

The second is the more dangerous of the two, because it only manifests on the day someone is
recovering from an incident. Fixing #1 without #2 would produce backups that restore wrongly and
say they worked.

### 5.7 The preservation machinery has no producer

ADR-0012 §5 requires that "import produces a fidelity report". **There is no importer.** `cmd/`
contains `migrate` and `server`; `internal/assess` is a read-only *assessor* that classifies a
Jira or Confluence export and prints a verdict ledger. No route in `route_accounting_test.go`
ingests content.

So the three carriers — the best-engineered thing in the codebase — are correct, tested,
drift-guarded, and currently unreachable by any real content. That is the right build order (the
ADR exists precisely to reach the editor before it shipped) but it should be stated plainly: the
migration story today is *assessment*, not migration. A team can be told what will not import.
They cannot yet import.

### 5.8 Smaller things that will bite

- **`search_vector` leaks into API responses.** The page-create response includes the full
  PostgreSQL tsvector as a `Lexemes` array. Internal index state should not be on the wire.
- **Generic 400s.** The strict decoder answers `"invalid request body"` with no field name. Every
  wrong-field guess in this review produced an identical, unhelpful error. Good for security, poor
  for anyone writing an integration.
- **500s are near-silent.** ~149 `StatusInternalServerError` sites in `internal/core/api` against
  ~12 `slog` calls in that tree, and `respond.Error` never logs the cause. Most 500s leave an
  access-log line and nothing else to debug from.
- **`internal/core/analytics` is an entire package** — full type surface, its own test file —
  returning `ErrNotImplemented` and referenced by nothing outside itself.
- **`azimuthal admin create-user` names the org after the user.** Mine became "Ada Founder", slug
  `ada-founder`, with no `--org-name` flag and no rename I could find.
- **A new Beacon space has no queues** until someone knows to call `/queues/defaults`.
- **`/auth/me` reports `"role": "member"` for the CLI-created owner**, which the CLI announced as
  "added as owner". Org admin is a middleware bypass by design (ADR-0007), so this may be
  correct-but-confusing; flagged rather than graded, because resolving it needs a maintainer's
  read of intent.

---

## 6. Genuinely ahead of Atlassian

Verified, not taken on trust. Each I either reproduced live or read to the bottom. **One item I
had drafted as a win — backup and restore — was removed after testing it; see §5.6.** That is the
whole reason this section is short.

1. **Zero-silent-data-loss preservation is real and unusually rigorous.** All three ADR-0012
   carriers exist — `unknownContent`, `unknownInline`, `unknownMark` — not just the block one,
   with the reasoning written down for why a mark and an inline node need separate carriers, and
   equality tests that fail in both directions. Nothing in Atlassian's own migration tooling makes
   a comparable promise. (Caveat: no importer yet feeds it — §5.7.)
2. **Per-viewer resolution as an architectural default.** Verified live: the Home dashboard
   resolved "My work" against my own access across Vector *and* Beacon in one view. ADR-0009's
   degradation rules — scope unavailable, not-available-to-you, unknown gadget key, deleted view —
   are specified as mandatory rather than as error states. Jira dashboards do not degrade this
   gracefully.
3. **Single-binary, single-compose self-hosting.** Verified: `//go:embed all:dist` plus embedded
   migrations, applied at boot. Atlassian ended Server in February 2024; Data Center is a
   multi-node licence at enterprise minimums. "One binary and a compose file" has no Atlassian
   equivalent at any price.
4. **Route-guard accounting enforced by a test.** Every route carries an explicit guard class,
   checked against the router's real middleware chain rather than against a comment
   (`route_accounting_test.go`). I know of no comparable discipline in this product category.
5. **The comment-visibility default.** `Visibility *string` where absent means *internal*, with
   the reasoning recorded: had the zero value meant public, shipping the portal would have
   retroactively published every comment written by a stale tab. A real bullet dodged, and the
   kind of thing usually only written down after an incident. (It is not reachable from the UI
   yet — §3.3 — but the decision is the win.)
6. **The audit log.** Append-only, genuinely comprehensive, capturing workflow-guard lifecycle and
   share creation, not just logins.
7. **The documentation's own honesty, where it exists.** `docs/self-hosting.md:116-136` volunteers
   that `build/docker-compose.yml` declares no `env_file`, and the config comments explain their
   own trade-offs. Several findings above were *easier* to make because the code says what it is
   doing. That culture is an asset; the failures in §5 are places it lapsed, not the norm.

**One claimed win I am not crediting:** "all features for all users, no enterprise tier" is a
strong position against Atlassian Guard gating SAML/SCIM behind a separate paid product. But
Azimuthal has no SSO to give away (§3.4). The position is worth what it delivers, and today the
free-for-everyone item does not exist.

---

## 7. Recommended next phase, ordered by adoption impact

Sized S (days) / M (a week or two) / L (a phase).

### Pre-announcement table stakes

Shipping without these makes the announcement itself a liability, because the gap is between what
the product *says* and what it *does*.

| # | Work | Size | Why first |
|---|---|---|---|
| 0 | **Close the cross-space read-authorization gap in the item-relations read path,** with a permission-matrix case so it cannot regress. Two related depth defects in the same path fix in the same change. Tracked privately; see §1a. | **S** | A read-authorization gap around the guard the whole access model rests on. Ahead of everything else, including backup. |
| 1 | **Make backup work, and make restore fail loudly.** Add the Postgres client to the image (or replace the fork with an in-process dump); add `-v ON_ERROR_STOP=1` and stop discarding psql's output; fix `docs/upgrade.md`'s rollback to name the file the backup actually produces. | **S** | §2 #0, §5.6. Operators believe they have nightly backups and have none, and the restore path would report success on a partial recovery. Nothing else on this list can lose a customer's data. |
| 2 | **Correct `README.md` and the assessor.** Remove or qualify "SSO — SAML/OIDC", "email ingestion", and "Backup and restore"; fix `jira_assess.go:378` so it stops telling migrators their links are unmappable and starts telling them their *hierarchy* is. | **S** | Costs a day. Until then the front page and the migration tool both misstate the product. |
| 3 | **Fix condition-class guards** — route `AvailableTransitions`, and have `Gate` evaluate conditions too. | **S** | §5.1. A compliance control that returns 201 and does nothing is worse than one that is absent. |
| 4 | **Attachments on tickets and items.** The backend is done; this is a component and an `api.ts` function. | **S** | §2 #1. Best ratio of user-visible value to work on the list. |
| 5 | **Fix the priority sort direction.** | **S** | §2 #10. Silent, returns plausible data, and inverts the single most common sort in any tracker. |
| 6 | **@mentions: parse, link, and notify.** Plus the notification the comments handler is already wired to enqueue and never does. | **M** | §2 #2, #4. This is what makes the tool a place people talk rather than a place records live. |
| 7 | **Notification coverage + email delivery.** Notify on item assignment, comment, mention and status change; wire notifications to the existing `email` package; add SMTP AUTH and TLS. | **M** | §2 #3. Without SMTP AUTH no self-hoster can use a managed mail provider. |
| 8 | **Surface the 202.** Stop typing `pending_approval` as a success; render "pending approval" in the UI. | **S** | §3.3. Today enabling an approver makes the UI lie about whether work moved. |
| 9 | **Bind the status control to the workflow,** and reject statuses matching no configured state. | **S** | §2 #7, #8. Removes a hardcoded list that already disagrees with both shipped workflows. |
| 10 | **Remove the dead "Sign up" link**; fix the search placeholder to advertise a query that returns results; add the Workflows tab to `AdminLayout`. | **S** | §2 #15, #17; §3.4. Three one-line fixes to controls that currently mislead on first contact. |
| 11 | **API tokens / service accounts.** | **M** | §3.4. Blocks every CI job, bot and integration, and is the stated home of automation per ADR-0011. |

### Post-launch fast-follows

| # | Work | Size | Why |
|---|---|---|---|
| 12 | **Issue hierarchy** — `parent_id` on create and PATCH with a cycle guard, plus UI. | **M** | §2 #5. Schema and plumbing are ready. Unblocks epics and ADR-0011's deferred "transition a linked item". |
| 13 | **Labels: one store, one surface.** Join table, filter field, autocomplete, bulk apply, and a real admin page. | **M** | §2 #9. Closes `known-issues.md` §14 and makes filter v2 usably complete. |
| 14 | **Bulk edit / transition, and CSV import/export.** | **M** | Migration day is a bulk operation; import is how data gets in at all. |
| 15 | **Webhooks.** | **M** | §4.1. Delivers ADR-0011's own stated mitigation. |
| 16 | **SLAs for Beacon.** | **L** | §3.3. The largest single JSM gap, and the one ADR-0003 already assumed. |
| 17 | **Email channel — wire the parser that already exists.** | **M** | §5.2. Most of the work is done and unreachable. |
| 18 | **Search: paginate (the plumbing exists), and name the ignored operator.** | **S** | §5.5, §4.2. Both are small and remove silent-wrong-answer traps. |
| 19 | **Ticket relations**, plus contradiction refusal, layered on top of the item-0 change. | **S** | §2 #19, §3.1. Schema shipped in migration 015; handler work only. |
| 20 | **Issue history / activity feed.** Needs `audit_log.space_id` first (`known-issues.md` §22). | **M** | §2 #13. |
| 21 | **Parse wikilinks and tags on the API content path,** not only in the editor; surface backlinks. | **S** | §3.2. Otherwise every importer and script silently produces unlinked pages. |
| 22 | **Comment visibility in the UI; threaded replies visible.** | **S** | §3.3, §3.2. Two good backends with no surface; replies are currently written and lost. |
| 23 | **An importer**, so the preservation carriers have a producer and ADR-0012's fidelity report has something to report on. | **L** | §5.7. This is the difference between "assessment" and "migration". |
| 24 | **Page-level restrictions; page templates; export.** | **L** | §3.2. The three Confluence features migrations most depend on — and restrictions need an ADR-0008 amendment, not just code. |
| 25 | **SSO (SAML/OIDC) and SCIM.** | **L** | §3.4. The 200-person adoption gate, and where the no-paid-tier position would actually mean something. |
| 26 | **Rate limiting, `/metrics`, a `/ready` that can fail, audit export, session purge scheduling.** | **M** | §3.4. Operations and compliance table stakes at scale. |
| 27 | **i18n scaffolding and a non-`english` text-search configuration.** | **L** | §5.5. Do the scaffolding before the string count grows further. |

---

## Appendix — reproduction

```bash
```bash
# Isolated throwaway stack from this commit (never the shared :5433 test stack)
docker compose -p azimreview \
  -f build/docker-compose.yml -f build/docker-compose.build.yml \
  --env-file .env up -d --build          # APP_PORT=8099

docker exec azimreview-app-1 /azimuthal admin create-user \
  --email admin@northwind.test --name "Ada Founder" --password '…'

# The backup failure reproduces in one command:
docker exec azimreview-app-1 /azimuthal backup --output /tmp/b.tar.gz

# Teardown
docker compose -p azimreview -f build/docker-compose.yml down -v
```

Findings marked "live" were reproduced against that instance at `532d387`. Findings with a
`file:line` were read in the tree at that commit. Where a documented claim and the code disagreed,
the code was treated as authoritative and the disagreement is reported rather than resolved —
`docs/design/spec-repo-reconciliation.md` is the place for resolutions, and §2's ADR-0011 link
correction, §4.4's ADR-0012 drift and §5.7's missing importer are all candidates.

**Not verifiable in this environment**, and deliberately ungraded rather than guessed: actual SMTP
delivery to a real relay (no mail server was configured); behaviour at data volumes large enough
to expose pagination or fan-out cliffs; and the two pre-graded in-flight PR-Bs.
