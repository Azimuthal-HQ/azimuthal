# Architecture decision records

Each ADR records one significant decision, why it was made, and what it commits us to. Code shows
*what* was built; these record *why*, which is the part that gets lost.

A decision is never reversed by editing its ADR. Write a new one that supersedes it, and mark the
old one superseded. That leaves an auditable trail.

## Index

| ADR | Decision | Status | Location |
|---|---|---|---|
| 0003 | Tickets and project items are separate tables | Accepted | `0003-split-item-tables.md` |
| 0004 | RS256 signing keys persisted in the database | Accepted | `0004-signing-keys-in-database.md` |
| 0005 | Navigation shell | Accepted | `../design/v0.3-ia-spec.md` §3 |
| 0006 | Scope model — org, team, space | Accepted | `../design/v0.3-ia-spec.md` §3 |
| 0007 | Access control — subject-side expansion | Accepted | `../design/v0.3-ia-spec.md` §3 |
| 0008 | Entity shares — widen, never narrow | Accepted | `../design/v0.3-ia-spec.md` §3 |
| 0009 | Saved views and dashboards | Accepted | `../design/v0.3-ia-spec.md` §3 |
| 0010 | Cross-space route family | Accepted | `../design/v0.3-ia-spec.md` §3 |
| 0011 | Workflow capability boundary | Accepted (implementation deferred to v0.4) | `0011-workflow-capability-boundary.md` |
| 0012 | Content fidelity and unknown nodes | Accepted — binding on issue #15 | `0012-content-fidelity-and-unknown-nodes.md` |

## A note on 0005–0010

Those six are embedded in section 3 of the v0.3 information architecture specification rather
than living here. They were written as part of that document before this directory existed.

They should be extracted into individual files in a later documentation pass. Until then, **this
index is the single place to look** — do not assume a decision is missing because there is no file
for it.

There are no ADRs numbered 0001 or 0002. Numbering began at 0003 in the v0.1.x line, and the gap
is preserved rather than backfilled so that existing references stay accurate.