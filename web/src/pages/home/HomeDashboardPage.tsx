import { LayoutDashboard } from 'lucide-react';
import { EmptyState } from '../../shell/EmptyState';

/**
 * HomeDashboardPage holds the /home/:dashboardId route. Dashboards and the
 * gadget registry ship in v0.3.4 (spec P5); until then the route renders a
 * branded empty state rather than a blank body.
 */
export function HomeDashboardPage() {
  return (
    <EmptyState
      icon={LayoutDashboard}
      title="Dashboards are on their way"
      description="Custom Home dashboards with gadgets scoped to your teams arrive in a later v0.3 release. Your work overview lives on Home until then."
    />
  );
}
