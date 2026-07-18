import { BarChart3 } from 'lucide-react';
import { EmptyState } from '../../shell/EmptyState';

/**
 * Reports view for a service desk space.
 *
 * Reporting is not built yet; this route renders a branded empty state
 * describing what the view will do — never a blank body.
 */
export function ReportsPage() {
  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">Reports</h1>
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Insights for this service desk
        </p>
      </div>
      <EmptyState
        icon={BarChart3}
        title="Reports are coming soon"
        description="This is where you'll see ticket volume, resolution times, and workload across your team — so you know exactly where your queue is headed."
      />
    </div>
  );
}
