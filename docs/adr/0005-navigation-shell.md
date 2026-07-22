# ADR-0005 — Navigation shell

**Status:** Accepted — implemented in P1 (PR #51).
**Origin:** Written as part of `docs/design/v0.3-ia-spec.md` §3; extracted verbatim into this
file in the post-P3 documentation pass. The text below is unchanged.

---

**Context.** Module sub-routes render outside the space layout, so the sidebar loses context
and collapses to Home and Settings inside Sprints, Roadmap, and Labels. The UI does not
communicate which module the user is in and does not feel like one suite.

**Decision.**

1. The product switcher lives in the **top bar**: logomark, Home, Beacon, Codex, Vector.
2. The **left panel belongs entirely to the current space** and collapses to an icon rail.
3. All module sub-routes render **inside** the space layout. The sidebar is a layout
   component, never a per-page component.
4. Org moves to the avatar menu. It is a tenant boundary, not a navigation control.
5. Every unimplemented route renders a **branded empty state**. A blank screen is a defect.
6. Codex's sidebar has a fixed region — space picker, search, Recent, Starred, Drafts —
   above a page tree in its own scroll container.
7. Routes are `/:module/:spaceId/...` using rebranded module names. Existing bookmarks
   break; acceptable pre-1.0.
8. Post-login landing is the Home overview.
9. Home's left panel header is a static "Your work" label — the space picker is inert there
   because Home is scoped to the user, not a space.
