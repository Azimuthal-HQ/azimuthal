# ADR-0011 — Workflow capability boundary

**Status:** Accepted — boundary decision. Implementation deferred to v0.4.
**Date:** July 2026

## Context

Vector and Beacon ship a deliberately minimal workflow engine: named states, reorder, add,
remove, seeded defaults per module. No conditions, no validators, no side effects. That was the
right choice for shipping, and it remains right for teams starting fresh.

Migration from Jira and Jira Service Management changes the calculus. Data migration without
process migration means every team changes how they work on the day they switch, which is the
most common reason platform migrations fail — not missing data, missing habits. Jira workflows
also encode compliance requirements: approval gates on change requests, mandatory fields before
closure, restricted transitions. Some organisations cannot abandon those and remain compliant.

But Jira's workflow surface is not one thing. It divides into tiers with very different costs,
and treating it as a single capability leads either to refusing all of it or adopting all of it.
Both are wrong.

## Decision

Three tiers. Two are in scope. One is permanently out.

### Tier 1 — Conditions and validators. In scope.

Declarative predicates evaluated at transition time, with no side effects. A condition
determines whether a transition is offered; a validator determines whether it succeeds.

Examples: only the assignee may move an item to In Review. Resolution must be set before Closed.
A required field must be non-empty. Only members of a given team may reopen.

Cheap to implement safely, they cover a large share of real workflows, and they are fully
inspectable — the engine can always explain why a transition was refused.

### Tier 2 — Approvals. In scope, and required independently of migration.

A transition that blocks pending approval from a named user, team, or role, with the pending
state visible and the decision recorded.

Beacon cannot credibly replace JSM without this. Change management and service request workflows
are not ITSM in any meaningful sense without an approval gate. This tier is justified on its own
merits; migration only raises its priority.

### Tier 3 — Post-functions. Restricted to a fixed, closed set.

Permitted actions: set a field, assign to a user or team, add a comment, transition a linked
item.

That set is defined in code. It is extended only by a deliberate release decision, never by
configuration, and never by anything a user supplies.

### Arbitrary scripting is permanently out of scope.

No Groovy, no JavaScript hooks, no user-supplied code, no plugin execution — at any tier, under
any framing, in any edition, now or later.

## Rationale for the exclusion

Adopting scripting means adopting an execution sandbox, a security boundary, a resource-limit
story, and a support surface where every deployment's workflows are bespoke code that only that
deployment understands.

It is also, empirically, the single largest source of upgrade breakage and unmigratable
configuration in Jira estates — and precisely the thing that makes those estates hard to leave.
Reproducing it would reproduce the trap.

Refusing it is a product position, not a limitation. Bounding what a workflow can do to what the
engine can reason about keeps workflows inspectable, testable, diffable, and migratable. A
workflow you can read is a workflow you can move.

## Consequences

- **Some Jira workflows cannot be represented.** The migration assessor must report them
  explicitly rather than approximating them silently. See ADR-0012.
- **The workflow admin UI becomes materially more complex** than the v0.3 minimal version.
  Budget for it as its own phase, not a rider.
- **Genuine automation belongs at the integration boundary** — webhooks and the job queue —
  rather than inside the workflow engine.
- **If a future requirement appears to need scripting**, the correct response is to extend the
  closed post-function set by a release decision. Never to open the boundary. This clause exists
  so the question does not get relitigated under pressure from a single large prospect.