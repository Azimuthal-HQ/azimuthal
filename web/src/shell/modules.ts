import type { LucideIcon } from 'lucide-react';
import { BookOpen, LifeBuoy, SquareKanban } from 'lucide-react';

/** The three product modules. Values double as spaces.type values (migration 021). */
export type ModuleKey = 'beacon' | 'codex' | 'vector';

export interface ModuleDef {
  key: ModuleKey;
  name: string;
  icon: LucideIcon;
  /** CSS custom property carrying the module hue, e.g. '--module-beacon'. */
  hueVar: string;
  /** Sub-path a space of this module lands on ('' = the space index route). */
  defaultSubpath: string;
}

export const MODULES: Record<ModuleKey, ModuleDef> = {
  beacon: {
    key: 'beacon',
    name: 'Beacon',
    icon: LifeBuoy,
    hueVar: '--module-beacon',
    defaultSubpath: 'tickets',
  },
  codex: {
    key: 'codex',
    name: 'Codex',
    icon: BookOpen,
    hueVar: '--module-codex',
    defaultSubpath: '',
  },
  vector: {
    key: 'vector',
    name: 'Vector',
    icon: SquareKanban,
    hueVar: '--module-vector',
    defaultSubpath: 'backlog',
  },
};

export const MODULE_KEYS = ['beacon', 'codex', 'vector'] as const satisfies readonly ModuleKey[];

/** isModuleKey narrows an arbitrary route param to a ModuleKey. */
export function isModuleKey(v: string | undefined): v is ModuleKey {
  return v === 'beacon' || v === 'codex' || v === 'vector';
}

/** spacePath builds a space-scoped route: spacePath('beacon', id, 'board') → /beacon/{id}/board. */
export function spacePath(module: ModuleKey, spaceId: string, subpath?: string): string {
  const base = `/${module}/${spaceId}`;
  return subpath ? `${base}/${subpath}` : base;
}

/**
 * Maps a notification's entity_kind to the module + detail sub-path of the
 * entity it references. The trailing route segment is the entity UUID for all
 * three (tickets/:ticketId, pages/:pageId, backlog/:itemKey are all UUIDs).
 */
const NOTIFICATION_ENTITY: Record<string, { module: ModuleKey; sub: string }> = {
  ticket: { module: 'beacon', sub: 'tickets' },
  page: { module: 'codex', sub: 'pages' },
  item: { module: 'vector', sub: 'backlog' },
  project_item: { module: 'vector', sub: 'backlog' },
};

/**
 * notificationRoute builds the in-app route for a notification, or null when it
 * cannot be routed (unknown/absent entity kind, or a legacy row without the
 * denormalised space). A null result keeps the bell's mark-read-only behaviour.
 * Navigation lands on the normal space-scoped detail page, which enforces read
 * authz and 404s a deleted / no-longer-accessible entity — it is never a
 * permission oracle.
 */
export function notificationRoute(n: {
  entity_kind?: string;
  entity_id?: string;
  entity_space_id?: string;
}): string | null {
  const target = n.entity_kind ? NOTIFICATION_ENTITY[n.entity_kind] : undefined;
  if (!target || !n.entity_id || !n.entity_space_id) return null;
  return spacePath(target.module, n.entity_space_id, `${target.sub}/${n.entity_id}`);
}
