# ADR-0006 — Scope model

**Status:** Accepted — implemented in P2 (PR #53).
**Origin:** Written as part of `docs/design/v0.3-ia-spec.md` §3; extracted verbatim into this
file in the post-P3 documentation pass. The text below is unchanged.

---

**Decision.**

1. **Org** is a tenant boundary. Avatar menu.
2. **Team** is a grouping and filtering concept. Users never navigate "into" a team.
3. **Space** is the unit of work and the unit of access.
4. **Every user belongs to at least one team.** A default team is seeded at org creation;
   new users join it unless assigned otherwise. A user removed from their last team is
   automatically added back to the default team — never teamless.
5. A team and a space may correspond one-to-one. Normal, not a special case.
6. The space picker shows the **union of everything the user can read**, grouped by owning
   team, searchable, with recents and starred pinned above the groups.
7. Team focus is an **opt-in filter**, visibly indicated in the top bar with one-click clear.

**The governing rule: union by default, narrow by choice.** The default state must never
hide work from a user who has access to it.
