# ADR-0012 — Content fidelity and unknown nodes

**Status:** Accepted — implemented in issue #15 (PR #73 the document model, PR #75 the editor
surface); binding on it from before it was built.
**Date:** July 2026 · **Amended:** July 2026 (security & integrity pass S1 — the three
preservation carriers) · **Corrected:** 2026-07-31 (status, and the §4 macro dispositions)

> **Amendment — 2026-07-27, PR #76 (S1).** The Decision section below was materially rewritten and
> the document recorded nothing. §1 originally named **one** carrier, `unknownContent`, and
> described it as a node; it was broadened to the three carriers in the table, with the paragraph
> explaining why a mark and an inline node need their own. §3 was extended from "It round-trips" to
> "They round-trip", requiring the test to cover all three, and the final Consequences bullet
> gained "**and marks** byte-for-byte — all three carriers, not just the block one". Nothing that
> had shipped was removed. The broad reading was confirmed by the maintainer on 2026-07-25; see
> D51 and D55 in `docs/design/spec-repo-reconciliation.md`.
>
> This amendment block is added on 2026-07-31, not on the day of the change. Until then the ADR
> gave a reader no way to know its Decision text had moved — while a code comment
> (`web/src/components/codex/extensions/preservation.ts`) already cited "the S1 amendment" as an
> established fact. That asymmetry is the reason ADRs carry an `Amended:` field at all.

## Context

Codex will eventually import Confluence content. Confluence's storage format carries hundreds of
macro types: panels, expands, code blocks, status lozenges, tables of contents, layout macros,
page includes, Jira issue embeds, dynamic content queries, and third-party app macros — Gliffy,
draw.io — that no importer can represent at all.

The editor that will render this content has not been built. Issue #15 is open. Its node
vocabulary is therefore still a design decision, and it stops being one the moment the editor
ships.

This matters more than it sounds. TipTap is built on ProseMirror, and **ProseMirror silently
drops content that does not match its schema.** An editor built without accommodation for
unrepresentable content will destroy that content the first time a user opens and saves an
imported page. Not at import time, where it would be caught — later, quietly, one page at a time.

## Decision

**Zero silent data loss.** Content that cannot be represented natively is preserved visibly. It
is never dropped, never approximated without saying so, and never mangled quietly.

Concretely:

**1. The editor schema includes preservation carriers from day one.** Each stores the source
system, the original element or macro name, its attributes, and its raw body verbatim.

There are **three** of them, because ProseMirror content occupies three positions and a single
node type cannot cover them all — a node is either block or inline, and a mark is neither:

| Carrier          | Position     | Preserves                                              |
|------------------|--------------|--------------------------------------------------------|
| `unknownContent` | block node   | an unrepresentable block: a macro, a panel, a layout    |
| `unknownInline`  | inline node  | an unrepresentable inline element inside a paragraph    |
| `unknownMark`    | mark         | unrepresentable formatting applied to a run of text     |

**This is the substance of the guarantee, not an implementation detail.** ProseMirror discards an
unrecognised mark and an unrecognised inline node exactly as silently as it discards a block. A
reading of "zero silent data loss" that covered only blocks would contradict the heading above it,
and would not be hypothetical: Codex's markdown editor serialised text colour and highlight as
inline `<span>` HTML, so pages stored in this repository already contain inline content that the
narrow reading would destroy on first edit. That editor has since been removed, but the content it
wrote has not.

**2. They render as a visible, labelled placeholder** — not an error, not a blank space. A reader
can see that something exists here, what it was, and that it has been preserved.

**3. They round-trip.** Editing and saving a page containing unknown nodes *or marks* leaves them
byte-identical. This is the requirement most likely to be missed and the most damaging when it
is: a user opens an imported page, fixes a typo, and silently destroys forty macros. This must
have an explicit test, and the test must cover all three carriers — a round-trip test that
exercises only blocks leaves the two positions most easily lost unguarded.

**4. Known macros become first-class custom nodes**, mapped deliberately:

- Panels, expands, code blocks, status lozenges, tables of contents, and layout macros render
  natively — these are the majority of macro *instances* in a typical wiki
- Cross-reference macros (Jira issue, page include, children display) render against Azimuthal's
  own data, which incidentally delivers Codex↔Vector embedding, a feature worth having anyway
- Dynamic content macros (content by label, page properties reports) map onto the saved-views
  layer, since they are queries

> **Amendment — 2026-07-31: what shipped of §4, and what did not.** Eight first-class macros
> exist: `panel`, `expand`, `statusLozenge`, `tableOfContents`, `layout`, `layoutColumn`,
> `childrenDisplay` and `pageInclude`. The two bullets above are each partly unfulfilled, and the
> reasons differ enough to record separately. The bullets stand as intent; neither is withdrawn.
>
> **The item embed did not ship.** Of the cross-reference bullet, page include and children
> display exist; the Jira-issue side has no counterpart — no `itemEmbed`, `jiraIssue`,
> `vectorEmbed` or `ticketEmbed` node exists anywhere. So the "Codex↔Vector embedding" this bullet
> anticipated does not exist as an editor node. (Page↔item *linking* does exist, through the
> `wiki_link` relation kind, but that is a stored relation in a side panel, not a macro rendering
> item data inside the document.) The absence is deliberate and reasoned in code — the editor's
> macro module records it as "a cross-space route-shape question ADR-0010 governs" — but the
> reason lived only in code until now. An imported Jira-issue macro is preserved as
> `unknownContent`, so the zero-silent-data-loss guarantee is met by this ADR's own designed
> fallback; what is not delivered is the native rendering.
>
> **No dynamic-content macro shipped, and the substrate this bullet names has since ruled itself
> out.** Saved views (ADR-0009, migration 038) read Beacon and Vector only: the module enum has
> exactly two values and the validator refuses anything else, with the exclusion stated as a
> decision — "Codex is deliberately absent: pages are found through P6 search, which owns the page
> read path and its cascade share semantics" — the comment above the `Module` constants in
> `internal/core/views/filter.go`.
> Content-by-label and page-properties reports are *page* queries, so under the shipped design
> they cannot map onto saved views at all. **Where these macros should map is therefore reopened,
> not settled** — a live conflict between two accepted decisions, weighed on a cascade-share
> argument this ADR never considered. That is a maintainer decision, catalogued as **D96**.
>
> One stale reason to retire in code: the macro module defers these on the grounds that saved
> views are "P4". P4 merged in #79/#81, so that reason has expired; the live reason is the one
> above. The item-embed disposition is catalogued as **D95**.

**5. Import produces a fidelity report** listing every unrepresented element, its count, and the
pages containing it.

> **Note — 2026-07-31: there is no importer.** This clause sets a condition on any import, and no
> import exists to meet it. `cmd/` holds exactly two binaries, `migrate` and `server`; no route
> ingests content. `internal/assess` is a read-only **assessor** — it classifies a Jira or
> Confluence export and prints a verdict ledger, and a test walks its import graph to keep it
> unable to reach a database. So the migration story today is *assessment*, not migration: a team
> can be told what will not import, and cannot yet import.
>
> This is the right build order and the ADR says so — it exists precisely to reach the editor
> before it shipped — but it should be stated rather than discovered. The consequence worth
> knowing is that **the three preservation carriers have no producer**: they are correct, tested
> and drift-guarded in both directions, and no real imported content has ever reached them. The
> importer is a planned phase, not a shipped one. Catalogued as **D103**.

## Rationale

"We support twelve macros" is a feature list. "Everything imports, most renders natively, the
rest is visibly preserved and itemised in a report" is a migration guarantee. The second is
enormously more credible and it costs one node type.

A migration that says "these 40 pages use macros we cannot render, here they are" earns trust. A
migration that silently approximates loses it permanently, and the loss is discovered months
later when someone notices a diagram is gone.

## Consequences

- **Issue #15 cannot be implemented as a plain rich-text editor.** Its node architecture is
  load-bearing and must accommodate custom and unknown nodes from the start, or the editor gets
  rebuilt to add them later. This ADR exists specifically to reach #15 before it is built.
- **The principle generalises.** It applies to any importer, any source format, any future
  migration — not only Confluence.
- **A fidelity report is a required output of any import**, not an optional extra.
- **Unknown content is not searchable as rich text.** Indexing its raw body as plain text is
  acceptable; pretending it renders is not.
- Round-trip preservation constrains how the editor serialises documents. Verify it with a test
  that opens, edits, saves, and compares the unknown nodes **and marks** byte-for-byte — all three
  carriers, not just the block one.