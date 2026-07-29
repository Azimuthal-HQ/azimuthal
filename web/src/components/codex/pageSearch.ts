/**
 * The one page-lookup used by every surface that names a page.
 *
 * Three of them now do — the page-include macro, the internal-link button, and
 * the `[[` wikilink autocomplete — and `docs/design/shared-surfaces.md` makes a
 * second implementation of something on that page a defect rather than a
 * convenience. The rules below are not incidental; each of them is a decision
 * that has to hold identically wherever a page is named:
 *
 * **Permission filtering is inherited, not applied here.** The candidate list is
 * the space's page list, which the API already filtered to what this caller can
 * read. There is deliberately no second permission check in the client, because
 * a client-side check is not one — and there is no second server search either,
 * which would be a second place for the filtering to be got wrong.
 *
 * **Scope is the current space.** A tag is org-scoped; a page reference is not.
 * The list comes from `useWikiPages(spaceId)`, so `[[` offers pages in the space
 * being edited. Cross-space linking would need a cross-space page search, which
 * is a route-shape question ADR-0010 governs and a second search path this
 * module exists to avoid — so it is deliberately out of scope here rather than
 * half-built. Candidates still carry their space label, so the shape is already
 * right if that scope ever widens.
 */
import type { WikiPage } from '../../lib/api';

/** How many candidates any picker or autocomplete will show at once. */
export const MAX_PAGE_CANDIDATES = 50;

/**
 * Pages matching a query, excluding the page doing the referring.
 *
 * A page cannot refer to itself: an include would be a cycle, and a link would
 * go nowhere the reader is not already.
 */
export function filterPages(
  pages: WikiPage[],
  query: string,
  excludePageId?: string,
): WikiPage[] {
  const q = query.trim().toLowerCase();
  const candidates = pages.filter((p) => p.id !== excludePageId);
  if (!q) return candidates.slice(0, MAX_PAGE_CANDIDATES);
  return candidates
    .filter((p) => p.title.toLowerCase().includes(q))
    .slice(0, MAX_PAGE_CANDIDATES);
}

/**
 * The page a bare wikilink title resolves to, or null.
 *
 * Case-insensitive and whitespace-trimmed, because `[[design docs]]` and
 * `[[Design Docs]]` are the same intention typed by two people. Exact-match
 * only, though — a prefix match would silently link `[[Runbook]]` to "Runbook
 * archive (2019)", and a link that goes somewhere the author did not name is
 * worse than one that goes nowhere and offers to create the page.
 *
 * Ambiguity resolves to the FIRST match in the space's page order, which is the
 * tree order the sidebar shows. Two pages with the same title in one space is
 * already a situation an author can see and fix; the same title in two
 * different SPACES is legal by design (the slug work in #28) and cannot arise
 * here, because candidates are scoped to one space.
 */
export function findPageByTitle(
  pages: WikiPage[],
  title: string,
  excludePageId?: string,
): WikiPage | null {
  const wanted = title.trim().toLowerCase();
  if (!wanted) return null;
  return (
    pages.find((p) => p.id !== excludePageId && p.title.trim().toLowerCase() === wanted) ?? null
  );
}
