import { LayoutGrid } from 'lucide-react';
import { GadgetTile } from './GadgetTile';
import { EmptyState } from '../../shell/EmptyState';
import type { DashboardGadget } from '../../lib/api';

interface DashboardGridProps {
  gadgets: DashboardGadget[];
  orgId: string;
  meId?: string;
  /** Present only when the reader owns the dashboard. */
  onRemove?: (gadgetId: string) => void;
  onConfigure?: (gadgetId: string) => void;
  /** Rendered inside the empty state, for a dashboard the reader may edit. */
  emptyAction?: React.ReactNode;
}

/**
 * The four-column gadget grid.
 *
 * Four columns above `md` and two below, so a two-span tile stays two-span on
 * a narrow screen rather than collapsing to a strip. Mobile is a non-goal
 * beyond not breaking (decision log D5) — this keeps it from breaking.
 */
export function DashboardGrid({
  gadgets,
  orgId,
  meId,
  onRemove,
  onConfigure,
  emptyAction,
}: DashboardGridProps) {
  if (gadgets.length === 0) {
    return (
      <EmptyState
        icon={LayoutGrid}
        title="Nothing on this dashboard yet"
        description="Add a gadget to start tracking what matters to you."
        action={emptyAction}
        className="mt-[var(--space-2)]"
      />
    );
  }
  return (
    <div
      data-testid="dashboard-grid"
      className="grid grid-cols-2 gap-[var(--space-3)] md:grid-cols-4"
    >
      {gadgets.map((g) => (
        <GadgetTile
          key={g.id}
          gadget={g}
          orgId={orgId}
          meId={meId}
          onRemove={onRemove ? () => onRemove(g.id) : undefined}
          onConfigure={onConfigure ? () => onConfigure(g.id) : undefined}
        />
      ))}
    </div>
  );
}
