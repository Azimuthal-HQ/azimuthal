import type { ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';
import type { DashboardGadget, DashboardModule, GadgetRender } from '../api';

/**
 * The client half of the gadget registry (P5, spec §7, ADR-0009 decision 5).
 *
 * "Every first-party gadget registers through this. A `switch` over gadget key
 * anywhere in the render path is a defect — it closes the extension seam
 * permanently."
 *
 * The server half is internal/core/dashboards/registry.go, which owns what may
 * be WRITTEN — the closed key set, the configuration vocabulary, and the
 * built-in queries. This half owns what is DRAWN. `registry.test.ts` reads the
 * Go file and fails in both directions when the two key sets disagree, the
 * same drift guard the filter vocabulary carries.
 *
 * Deviation from the spec sketch, recorded: `GadgetDefinition` there carries
 * `configSchema: JSONSchema7`. The configuration vocabulary is four optional
 * keys validated server-side, so this carries `configKeys` instead — the same
 * closed list, without a JSON-schema runtime and a second copy of the bounds
 * that would drift from the Go ones.
 */

/** The four configuration keys, mirroring internal/core/dashboards/registry.go. */
export type GadgetConfigKey = 'title' | 'limit' | 'group_by' | 'body';

export interface GadgetProps {
  gadget: DashboardGadget;
  orgId: string;
  /** The reading user, so a row assigned to them can say "You". */
  meId?: string;
}

export interface GadgetDefinition {
  key: string;
  name: string;
  /** A one-line explanation shown in the picker. */
  description: string;
  icon: LucideIcon;
  defaultSpan: 1 | 2 | 4;
  modules: DashboardModule[];
  requiresSavedView: boolean;
  configKeys: GadgetConfigKey[];
  render: GadgetRender;
  /** Draws the gadget's body. The tile chrome is not its concern. */
  Body: (props: GadgetProps) => ReactNode;
}

const registry = new Map<string, GadgetDefinition>();

/**
 * The one way a definition enters the registry.
 *
 * Throws on a duplicate key rather than overwriting: two definitions for one
 * key is a programming error that would otherwise resolve differently
 * depending on module evaluation order.
 */
export function registerGadget(def: GadgetDefinition): void {
  if (registry.has(def.key)) {
    throw new Error(`gadget "${def.key}" registered twice`);
  }
  registry.set(def.key, def);
}

/** Looks a gadget up. Undefined for a key this build does not define — which
 * is a placeholder tile, never an error (ADR-0009 case C5). */
export function getGadget(key: string): GadgetDefinition | undefined {
  return registry.get(key);
}

/** Every registered gadget that may sit on a dashboard of this module. */
export function listGadgets(module: DashboardModule): GadgetDefinition[] {
  return [...registry.values()]
    .filter((d) => d.modules.includes(module))
    .sort((a, b) => a.name.localeCompare(b.name));
}

/** Every registered key, for the drift guard. */
export function registeredKeys(): string[] {
  return [...registry.keys()].sort();
}

/** Bounds, mirroring internal/core/dashboards/registry.go. */
export const GADGET_LIMITS = {
  minLimit: 1,
  maxLimit: 25,
  defaultLimit: 5,
  maxTitle: 80,
  maxNote: 4000,
  maxGadgets: 40,
} as const;

/** The breakdown fields, mirroring internal/core/views/aggregate.go. */
export const BREAKDOWN_FIELDS = ['status', 'priority', 'assignee', 'kind'] as const;
export type BreakdownField = (typeof BREAKDOWN_FIELDS)[number];

/** The column spans migration 042's CHECK admits. */
export const GADGET_SPANS = [1, 2, 4] as const;
