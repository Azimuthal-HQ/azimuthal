import type { SearchHit } from '../../lib/api';

/**
 * Where a search hit points, and what it is called.
 *
 * Separate from the presentation components because these are plain functions:
 * a module that exports both breaks fast refresh, which is what
 * react-refresh/only-export-components is protecting.
 */
/**
 * Where a hit links.
 *
 * A share-only hit routes through /shared, NOT into its space: the viewer
 * cannot enter that space, and the server did not tell us which one it is — a
 * share-only result carries no space id by design (matrix case 16). Building a
 * space-scoped URL here would produce a link to a 404 at best, and would mean
 * this component had invented a container the API deliberately withheld.
 */
export function hitHref(hit: SearchHit): string {
  if (hit.origin === 'share') {
    const kind = hit.module === 'codex' ? 'page' : hit.module === 'beacon' ? 'ticket' : 'project_item';
    return `/shared/${kind}/${hit.id}`;
  }
  // The space-scoped shapes the rest of the app already uses:
  // /codex/{space}/pages/{id}, /beacon/{space}/tickets/{id},
  // /vector/{space}/backlog/{id}. Taken from the existing call sites rather
  // than from the route table, because the route table's ":module/:spaceId"
  // does not say which module name each entity type maps to.
  switch (hit.module) {
    case 'codex':
      return `/codex/${hit.space_id}/pages/${hit.id}`;
    case 'beacon':
      return `/beacon/${hit.space_id}/tickets/${hit.id}`;
    case 'vector':
      return `/vector/${hit.space_id}/backlog/${hit.id}`;
  }
}

/** The human reference for a hit, where one exists. */
export function hitReference(hit: SearchHit): string | null {
  if (hit.module === 'vector' && hit.item_key) return hit.item_key;
  if (hit.module === 'beacon' && hit.space_key && hit.number) return `${hit.space_key}-${hit.number}`;
  return null;
}
