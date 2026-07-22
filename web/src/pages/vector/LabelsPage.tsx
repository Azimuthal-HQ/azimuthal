import { Tags } from 'lucide-react';
import { EmptyState } from '../../shell/EmptyState';

/**
 * Labels view for a project space.
 *
 * Label management is not built yet: the labels admin table exists but is not
 * linked to project items (known issue #14, resolved by the items table split
 * in a later phase). Until then this route renders a branded empty state —
 * never a blank body.
 */
export function LabelsPage() {
  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">Labels</h1>
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Organise work with shared labels
        </p>
      </div>
      <EmptyState
        icon={Tags}
        title="Label management is coming soon"
        description="This is where you'll create and manage labels for this project — name them, colour them, and group related work. Until then, items keep the labels assigned on their detail view."
      />
    </div>
  );
}
