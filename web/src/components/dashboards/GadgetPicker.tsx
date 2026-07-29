import { listGadgets, type GadgetDefinition } from '../../lib/dashboards/registry';
import type { DashboardModule } from '../../lib/api';
// Registration side effect: the picker lists whatever registerGadget was
// called with, so it must have been called.
import './gadgets';

interface GadgetPickerProps {
  module: DashboardModule;
  onPick: (def: GadgetDefinition) => void;
}

/**
 * The "add a gadget" panel, per the dashboards prototype: a dashed tile
 * spanning the grid, holding one chip per gadget kind.
 *
 * It lists the REGISTRY rather than a hand-written array, so a gadget added to
 * the registry appears here with no second edit — which is what makes the
 * registry a seam rather than a lookup table.
 */
export function GadgetPicker({ module, onPick }: GadgetPickerProps) {
  const gadgets = listGadgets(module);
  return (
    <div
      data-testid="gadget-picker"
      className="col-span-2 rounded-[var(--radius-xl)] border border-dashed border-[var(--color-border)] p-[var(--space-4)] md:col-span-4"
    >
      <p className="mb-3 text-[11.5px] font-medium text-[var(--color-text-muted)]">Add a gadget</p>
      <div className="flex flex-wrap gap-2">
        {gadgets.map((def) => (
          <button
            key={def.key}
            type="button"
            data-testid={`gadget-option-${def.key}`}
            onClick={() => onPick(def)}
            title={def.description}
            className="flex items-center gap-2 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-[var(--text-xs)] text-[var(--color-text-muted)] transition-colors hover:border-[var(--color-text-muted)] hover:text-[var(--color-text)]"
          >
            <def.icon className="h-3.5 w-3.5 shrink-0" />
            {def.name}
          </button>
        ))}
      </div>
    </div>
  );
}
