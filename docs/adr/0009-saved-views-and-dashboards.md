# ADR-0009 — Saved views and dashboards

**Status:** Accepted — implementation planned for P4 (saved views) and P5 (dashboards).
**Origin:** Written as part of `docs/design/v0.3-ia-spec.md` §3; extracted verbatim into this
file in the post-P3 documentation pass. The text below is unchanged.

---

**Decision.**

1. A **saved view** stores a scoped query, not results. It is the substrate.
2. A **gadget** references a saved view plus a render mode. It never embeds a query.
3. A **dashboard** arranges gadgets and owns layout only.
4. **Scope lives on the saved view**, so one dashboard can mix a team-scoped gadget with an
   everything-scoped one.
5. **First-party gadgets ship through the same registry a third-party gadget would use.** No
   switch statement over gadget type anywhere in the render path.
6. Saved views ship and stand alone before any gadget exists.

**Degradation rules — all mandatory.**

- A saved view whose scope team or space was deleted is marked invalid, renders "scope
  unavailable", and prompts its owner to re-scope. It never errors.
- A gadget on a shared dashboard whose data the viewer cannot read renders "not available to
  you". The dashboard still loads.
- An unknown `gadget_key` renders a placeholder tile. It never crashes the dashboard.
- A gadget whose `saved_view_id` was deleted renders a recoverable empty state offering to
  pick another view.

Users may hold many Home dashboards, one marked default.

**Consequence.** Cross-module views query `tickets` and `project_items` together. Per
ADR-0003 those tables stay split; the view layer **fans out per module and merges in the API
layer**. Unifying the tables to simplify this is prohibited.
