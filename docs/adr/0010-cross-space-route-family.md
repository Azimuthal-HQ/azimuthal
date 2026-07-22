# ADR-0010 — Cross-space route family

**Status:** Accepted — teams, grants and shares route families implemented in P2 and P3;
views, dashboards and search route families planned for P4–P6.
**Origin:** Written as part of `docs/design/v0.3-ia-spec.md` §3; extracted verbatim into this
file in the post-P3 documentation pass. The text below is unchanged.

---

**Decision.** Add an **org-scoped route family** as a deliberate extension of the single
scoping convention, not an exception:

```
/api/v1/orgs/{org_id}/teams/...
/api/v1/orgs/{org_id}/views/...
/api/v1/orgs/{org_id}/dashboards/...
/api/v1/orgs/{org_id}/shares/...
/api/v1/orgs/{org_id}/search
/api/v1/orgs/{org_id}/spaces          (directory)
```

Space-scoped resources keep `/api/v1/orgs/{org_id}/spaces/{space_id}/...` unchanged. Every
cross-space endpoint filters against the caller's resolved readable set.
