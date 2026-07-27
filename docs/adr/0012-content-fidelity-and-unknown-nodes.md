# ADR-0012 — Content fidelity and unknown nodes

**Status:** Accepted. **Binding on issue #15 (the Codex editor) before it is built.**
**Date:** July 2026

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

**5. Import produces a fidelity report** listing every unrepresented element, its count, and the
pages containing it.

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