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
