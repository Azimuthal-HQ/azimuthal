# ADR-0011 — Workflow capability boundary

**Status:** Accepted — boundary decision. Tiers 1–3 implemented in v0.4 (migrations 046, 047);
the amendments recording exactly what shipped are inline below.
**Date:** July 2026 · **Amended:** July 2026 (v0.4 workflow tiers)

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

> **Amendment — v0.4 implementation, migration 046.** The examples above were illustrative, not
> exhaustive. The shipped vocabulary is four kinds:
>
> | kind | what it implements |
> |---|---|
> | `actor_is_assignee` | "only the assignee may move an item to In Review" |
> | `actor_in_team` | "Only members of a given team may reopen" |
> | `field_required` | "A required field must be non-empty" |
> | `actor_has_capability` | **an extension beyond the examples** — see below |
>
> `actor_has_capability` implements the *restricted transitions* this ADR's Context names as a
> compliance requirement, expressed against the ADR-0007 capability model rather than against role
> names, over a deliberately narrow subset (`edit_any_item`, `transition_any_item`, `manage_queue`,
> `manage_space`). It is written into this ADR rather than left implicit because a vocabulary that
> grows without amending its own ADR is how the boundary this document draws stops meaning
> anything.
>
> **"Resolution must be set before Closed" is not implemented, and is not approximated.** This
> product has no resolution field — only `resolved_at`, a timestamp — so the example has nothing to
> bind to. Introducing one is a product decision rather than a workflow-engine decision, and is
> deferred.
>
> The vocabulary is defined in exactly one place, `internal/core/workflow/guard.go`, and mirrored
> by CHECK constraints in migration 046. A kind written by a newer build and read by an older one
> is **refused**, never skipped: a skipped condition would offer a transition an administrator
> restricted, and a skipped validator would commit a write an administrator forbade.

### Tier 2 — Approvals. In scope, and required independently of migration.

A transition that blocks pending approval from a named user, team, or role, with the pending
state visible and the decision recorded.

Beacon cannot credibly replace JSM without this. Change management and service request workflows
are not ITSM in any meaningful sense without an approval gate. This tier is justified on its own
merits; migration only raises its priority.

> **Amendment — v0.4 implementation, migration 047.** Approvers may be a **user** or a **team**.
> The third subject kind this section names, *role*, is **not implemented and not approximated**:
> a space role (`access.Role`) has no user-set resolution query — the access model resolves
> subject-side, from a user to their capabilities, with no inverse — and `team_members.role` is
> explicitly metadata that the capability model never reads. Either would change the access model,
> which is a decision this phase raises rather than takes.
>
> Authority to decide is **data, not a capability**: a person may decide because an administrator
> named them, or a team they are an ADR-0007 effective member of, on that transition. No new
> `Capability` constant was introduced, and none should be — "who approves change requests" is
> per-gate, not per-role.
>
> One approval step per transition. Several approver subjects on one step is supported and means
> any one of them may decide; multi-step chains are deliberately absent rather than approximated by
> letting an administrator attach several steps that happen to run in order.
>
> **A pending approval does not move the item.** It stays in its source status and the transition
> commits only when approval is granted, so "decline returns the item to the source status" holds
> because it never left. The rejected alternative — move to the target and move back on decline —
> produces an item that is *closed pending approval*, which defeats the gate, because every board,
> queue and saved view reads status.

### Tier 3 — Post-functions. Restricted to a fixed, closed set.

Permitted actions: set a field, assign to a user or team, add a comment, transition a linked
item.

That set is defined in code. It is extended only by a deliberate release decision, never by
configuration, and never by anything a user supplies.

> **Amendment — v0.4 implementation, migration 046.** Two of the four ship; two are deferred, and
> every disposition is recorded because "the set is fixed in code" only means something if the
> reason each member is in or out is written down.
>
> | action | disposition |
> |---|---|
> | set a field | **ships**, over `due_at` and `labels` only |
> | assign to a user | **ships** |
> | …or a team | **not representable** — `assignee_id REFERENCES users(id)` on both entity tables |
> | add a comment | **deferred** — the comment surface is owned by another track in flight |
> | transition a linked item | **deferred** — no link table exists; the only structural relation is `project_items.parent_id`, a hierarchy, whose traversal would need a cycle guard this ADR does not describe |
>
> `description` is readable by a guard and deliberately **not** writable by a post-function: one
> that set it would overwrite author-written prose on every transition, which is silent data loss
> dressed as automation. `assignee_id` is absent from `set_field` because `assign_to` owns it, and
> two ways to write one column is how they come to disagree.
>
> Post-functions run **inside the transaction that writes the status**, with the audit row. A
> post-function that landed when the transition rolled back would have invented an effect with no
> cause; one lost when it committed would have silently not run. An action a build cannot perform
> **aborts the transition** rather than being skipped.
>
> Each deferred action widens migration 046's `CHECK` when it lands — which is exactly the
> "extended only by a deliberate release decision" this section requires.

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