# ADR-0007 — Access control

**Status:** Accepted — implemented in P2 (PR #53).
**Origin:** Written as part of `docs/design/v0.3-ia-spec.md` §3; extracted verbatim into this
file in the post-P3 documentation pass. The text below is unchanged.

---

**Context.** A department head needs visibility across everything beneath them. Implicit
cascade down an org tree makes every check recursive, produces ambiguity when a user sits in
two branches, and makes "why can this person see this?" unanswerable.

**Decision — expansion happens on the subject side, not the grant side.**

```
effective_teams(user) = direct_teams(user) ∪ all descendants of those teams

readable_spaces(user) = spaces where visibility = 'org'
                      ∪ spaces with a grant to the user directly
                      ∪ spaces with a grant to any team in effective_teams(user)
```

A user in a parent team acts with the authority of every team beneath it. A user in a leaf
team gets that leaf's grants and nothing above.

With Engineering as parent of Platform, Design, and Support:

- VP in Engineering → effective teams are all four → sees all four's spaces
- Engineer in Platform → effective teams are Platform only → sees Platform's spaces,
  **not** Engineering's

**Consequences.**

1. There is **no `include_descendants` flag**. Grants do not reach downward. One rule, one
   direction.
2. **`team_members.role` is metadata only** and never affects permissions. Seniority is
   already expressed by position in the tree.
3. A space everyone should read uses `visibility = 'org'`, not a spray of grants.
4. **Team grants are never materialised onto the space.** Effective access resolves per
   request, once, cached on the request context. Materialising creates the "left the team,
   kept the access" defect.
5. Checks test **capabilities**, never role-name string comparison.

**Capability model.**

| Role | Capabilities |
|---|---|
| `viewer` | `read_items`, `read_aggregates` |
| `contributor` | viewer + `create_items`, `edit_own_items`, `comment` |
| `agent` | contributor + `edit_any_item`, `transition_any_item`, `manage_queue` |
| `space_admin` | agent + `manage_space`, `manage_grants`, `manage_shares`, `manage_workflow` |
| org `admin` | all capabilities via middleware bypass, with no grant rows |

Written `can(ctx, CapReadItems, spaceID)`. Never `role == "viewer"`.

**Administrative authority in v0.3.** Team creation, reparenting, and deletion are org admin
only. Space creation is org admin or a `lead` of the owning team. Changing `owner_team_id`
requires `manage_space`.

---

## Correction — 2026-07-31 (spec/repo reconciliation)

**The model as built carries a thirteenth capability, `set_visibility`, which the table above does
not name.** It postdates the verbatim extraction, so the body is left unchanged and the gap is
recorded here instead.

No space role holds it — not even `space_admin`. It governs a space's visibility, both a later
change and a non-default value chosen at creation, and it is granted only by the org-admin bypass
this table's last row already describes. `internal/core/access/capability.go` states the reasoning
where it is declared: visibility changes what the whole organisation sees, which is an org-level
concern rather than a space-level one.

It is structurally distinct, not merely an extra row. Because there is no space to check against
at creation time, it is asked through **`CanOrgWide`** in `internal/core/access/resolver.go`, not
`Can`, and the
codebase carries a second map (`orgLevelCaps`) plus a build-time test —
`TestCapabilityConstants_AreExhaustivelyPartitioned` — asserting that the two maps exhaustively
partition the constant set, so a capability added to neither fails closed.

Space *creation* authority is unchanged by this note; `set_visibility` governs only the visibility
value. One practical consequence for testing, already recorded in `CLAUDE.md` §2: the persona who
must be refused in a `set_visibility` test is a **team lead**, not a viewer, because a viewer is
refused upstream and proves nothing about the gate.

This ADR is the only human-authored statement of the capability model — specification §3 is now a
pointer to this directory — so the omission left the model undocumented outside the generated
OpenAPI. Catalogued as D102.
