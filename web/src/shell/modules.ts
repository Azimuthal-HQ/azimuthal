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
