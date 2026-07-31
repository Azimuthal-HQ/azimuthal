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
| 0005 | Navigation shell | Accepted | `0005-navigation-shell.md` |
| 0006 | Scope model — org, team, space | Accepted | `0006-scope-model.md` |
| 0007 | Access control — subject-side expansion | Accepted | `0007-access-control.md` |
| 0008 | Entity shares — widen, never narrow | Accepted | `0008-entity-shares.md` |
| 0009 | Saved views and dashboards | Accepted | `0009-saved-views-and-dashboards.md` |
| 0010 | Cross-space route family | Accepted | `0010-cross-space-route-family.md` |
| 0011 | Workflow capability boundary | Accepted — tiers 1–3 implemented in v0.4 (migrations 046, 047) | `0011-workflow-capability-boundary.md` |
| 0012 | Content fidelity and unknown nodes | Accepted — implemented in issue #15 (PRs #73, #75) | `0012-content-fidelity-and-unknown-nodes.md` |

Every row points at a file. Nothing is embedded in another document.

The **Status** column abbreviates each ADR's own status line. Where the two disagree the ADR wins
and the index is the defect — row 0011 said "implementation deferred to v0.4" for weeks after
ADR-0011 had been amended to record that tiers 1–3 shipped, which is exactly the failure this note
exists to catch. Corrected 2026-07-31.

## A note on 0005–0010

Those six were originally written inside section 3 of `../design/v0.3-ia-spec.md`, before this
directory existed. They were extracted into individual files in the post-P3 documentation pass —
**verbatim**, with only a status and provenance header added. Section 3 of the specification is
now a pointer to this directory.

No decision changed in the move. If you are comparing an extracted file against the
specification's git history and find a difference in wording, that is a defect in the extraction
and should be raised, not reconciled by editing the ADR.

There are no ADRs numbered 0001 or 0002. Numbering began at 0003 in the v0.1.x line, and the gap
is preserved rather than backfilled so that existing references stay accurate.
