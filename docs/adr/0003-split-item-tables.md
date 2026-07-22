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