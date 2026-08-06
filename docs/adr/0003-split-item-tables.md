# ADR-0003 — Tickets and project items are separate tables

**Status:** Accepted. Decided during the v0.2 rebuild; documented retroactively July 2026.
**Supersedes:** the unified `items` table proposed in the v1 architecture document.

---

## Context

The v1 architecture document specified a single `items` table covering tickets, tasks, bugs and
stories, on the reasoning that "they differ in metadata, not structure." The v0.1.x line was
built that way. The v0.2 greenfield rebuild reversed it.

The reversal was driven by what the v0.1.x line actually experienced. Service desk tickets and
project items are superficially similar — both have a title, a status, an assignee, a priority —
and structurally divergent everywhere it matters:

**Different lifecycles.** A ticket has SLA clocks, first-response and resolution timers, a queue,
and a requester who is frequently an external customer with no organisation membership at all. A
project item has sprints, story points, backlog rank, epic membership, and a board column.

**Different required fields.** A unified table makes almost every column nullable, because almost
every column is meaningless for one of the two kinds. Constraints then have to be conditional on
type, expressed as CHECK expressions that reference a discriminator — which is a table pretending
to be two tables, with none of the enforcement.

**Different query shapes.** A queue sorts by SLA breach risk. A board sorts by rank within
column. The indexes that serve one are dead weight for the other.

The concrete cost showed up as bugs. v0.1.6 fixed a null constraint violation on item creation
(SQLSTATE 23502) that was precisely this class: a shared table whose nullability depended on a
type the database could not reason about.

---

## Decision

**`tickets` and `project_items` are separate tables. They are never unified.**

What they share is shared deliberately, and built for both from the start:

- The **workflow engine**, with seeded defaults per module
- **Comments**, polymorphic across both
- **Relations**, polymorphic across both
- **Entity shares**, polymorphic across both (ADR-0008)
- The **audit log**

Sharing behaviour is not the same as sharing storage. Each of the above is a genuine
cross-cutting concern; the item rows themselves are not.

---

## Consequences

**Cross-module queries fan out and merge in the API layer.** Saved views, dashboards and search
query each module's table and combine the results in application code (ADR-0009). This is a real
and permanent cost, accepted knowingly.

**Import routing becomes a decision, not a lookup.** A Jira project containing both engineering
bugs and service requests must be split across both tables at import time. This requires a
documented default mapping with per-project override, and it does not go away by adding item
types — types live *within* a table, not across them.

**Item types belong inside each table.** Bug, story, task and epic are types of project item.
Incident, request, change and problem are types of ticket. Adding a `type` column to either is
consistent with this ADR, not a departure from it.

**The temptation to unify will recur.** Every cross-module feature — search, saved views,
dashboards, a unified "assigned to me" list — presents a moment where one table looks simpler.
It is simpler for that feature and worse for everything else. **This decision is not revisited.**
If a future feature appears to require unification, the correct response is to improve the
fan-out and merge layer.

---

## Correction — 2026-07-31 (spec/repo reconciliation)

**SLA clocks and first-response/resolution timers do not exist and never have.** The "Different
lifecycles" contrast above names them as things a ticket has, and "Different query shapes" says a
queue sorts by SLA breach risk. Neither is true of this repository: there is no SLA table, no
timer, target, calendar or pause column, no breach concept, and no queue ordering by breach risk.
A repo-wide search for `sla`, `first_response` and `breach` across Go, SQL and TypeScript returns
only two unrelated false positives. The tickets table's sole time fields are `due_at` and
`resolved_at` — a due date and a resolution timestamp, neither of which is a clock.

Every *other* attribute in both lists is realised: the queue (migration 039, on the saved-view
model), the external requester with no `users` row (migration 044), sprints, backlog rank, epic
membership via `parent_id`/`kind`, a board column (migration 035), and story points expressible
through custom fields (migration 033). SLA is the one that is not.

**The decision itself is untouched and is being honoured** — the tables are separate, and the
fan-out-and-merge consequence is implemented. What is corrected is the rationale: the SLA half of
the lifecycle contrast is *anticipated*, not shipped, and must not be cited as evidence that SLA
machinery exists. It is unbuilt and tracked (item 16 of the recommendations in
`docs/design/parity-review-2026-07.md`), which already records another reader reaching for this
paragraph and concluding "the architecture was shaped around a feature that was never built."

Catalogued as D101 in `docs/design/spec-repo-reconciliation.md`.

---

## Clarification — 2026-08-06 (v0.4.2 A2): the split is of entity tables, not satellites

Nothing here revisits the decision. "This decision is not revisited" stands; so do the split
tables. What follows writes down a principle the decision already contains, because A2 is the
second time a track has had to rediscover it and the next reader should not have to.

**The entity tables stay split; the satellite tables go polymorphic.** The Decision section
above already says it in miniature: comments, relations and shares are "shared deliberately" and
"polymorphic across both," because "sharing behaviour is not the same as sharing storage."
Migration 015 is the executable form of the principle — it took the per-module `item_relations`
and comment columns and made them one polymorphic satellite each, with an `entity_type`
discriminator and the FK dropped on purpose, while the entity tables it hangs off stayed split.

v0.4.2 applies the same move to custom-field values: `item_field_values` (Vector-only by one FK)
became `entity_field_values` (migration 053), polymorphic over the same three-value
`{ticket, project_item, page}` vocabulary, with `custom_field_scopes` attaching definitions to
the forms that want them. The alternative — a parallel `ticket_field_values` — would have
metastasized the two-table split into every satellite: every future cross-cutting feature would
build its storage twice, diverge twice, and pay the migration tax twice. That is not what this
ADR decided. The split exists because ticket ROWS and item ROWS are structurally divergent;
a value row, a comment row, a relation row is the same shape whichever entity it hangs off.

So, for the next satellite (worklogs, watchers, reactions, whatever arrives):

- **One polymorphic table**, `entity_type` + `entity_id`, the shared three-value vocabulary,
  each read/write carrying its per-type space reconciliation — 015's technique, reused by 053.
- **Never a per-module fork** of a satellite. Forking is the unification tax paid in the other
  direction, and it compounds.
- **The entity tables themselves remain split**, exactly as decided above.